// Package gateway manages leased publications and Traefik's dedicated route file.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/UfukUstali/dev-reverse-proxy/internal/publication"
	"github.com/UfukUstali/dev-reverse-proxy/internal/tailnet"
)

type Config struct {
	DNS                                               *DNS
	ConfigDir, Domain, PublicIP, EntryPoint, Resolver string
	Lease                                             time.Duration
	Identify                                          tailnet.Identify
	RoutingSubdomain, RoutingSecret                   string
	RoutingInterval                                   time.Duration
}

type Publication struct {
	ID       string    `json:"id"`
	Owner    string    `json:"owner"`
	IP       string    `json:"ip"`
	Port     int       `json:"port"`
	Wildcard bool      `json:"wildcard"`
	URL      string    `json:"url"`
	Expires  time.Time `json:"expires"`
	session  string
}

type Gateway struct {
	pending      map[string]bool
	cfg          Config
	mu           sync.Mutex
	publications map[string]Publication
	retired      map[string]bool
	aliases      map[string]string // Authcenter hostname -> exact publication ID, including dormant targets.
	routingWake  chan struct{}
}

func New(ctx context.Context, cfg Config) (*Gateway, error) {
	if err := configureRouting(&cfg); err != nil {
		return nil, err
	}
	if cfg.Lease == 0 {
		cfg.Lease = 30 * time.Second
	}
	if cfg.Lease < 0 || cfg.Identify == nil || cfg.EntryPoint == "" || cfg.Resolver == "" {
		return nil, errors.New("identity, entrypoint, resolver and positive lease are required")
	}
	if len(cfg.Domain) > 180 || !strings.Contains(cfg.Domain, ".") {
		return nil, errors.New("invalid public domain")
	}
	for part := range strings.SplitSeq(cfg.Domain, ".") {
		if !publication.ValidName(part) {
			return nil, errors.New("invalid public domain")
		}
	}
	if _, err := netip.ParseAddr(cfg.PublicIP); err != nil {
		return nil, fmt.Errorf("invalid public gateway IP: %w", err)
	}
	if err := os.MkdirAll(cfg.ConfigDir, 0755); err != nil {
		return nil, err
	}
	g := &Gateway{pending: map[string]bool{}, cfg: cfg, publications: map[string]Publication{}, retired: map[string]bool{}, routingWake: make(chan struct{}, 1)}
	if err := g.writeConfig(); err != nil {
		return nil, err
	}
	go g.expire(ctx)
	if cfg.RoutingSubdomain != "" {
		go g.pollRouting(ctx, routingClient())
	}
	return g, nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	local := ""
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(interface{ String() string }); ok {
		local = a.String()
	}
	owner, err := g.cfg.Identify(ctx, r.RemoteAddr, local)
	if err != nil {
		reply(w, 403, publication.Response{Message: "publishing not authorized: " + err.Error()})
		return
	}
	switch r.URL.Path {
	case "/register":
		if !method(w, r, "POST") {
			return
		}
		g.register(ctx, w, r, owner)
	case "/heartbeat", "/unregister":
		if !method(w, r, "POST") {
			return
		}
		g.update(w, r, owner)
	case "/status", "/clients":
		if !method(w, r, "GET") {
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if r.URL.Path == "/status" {
			reply(w, 200, publication.Response{Status: "ok", Domain: g.cfg.Domain, PublicIP: g.cfg.PublicIP})
			return
		}
		active := []Publication{}
		for _, p := range g.publications {
			if time.Now().Before(p.Expires) {
				active = append(active, p)
			}
		}
		reply(w, 200, map[string]any{"clients": active})
	default:
		http.NotFound(w, r)
	}
}

func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	w.Header().Set("Allow", want)
	reply(w, 405, publication.Response{Message: "method not allowed"})
	return false
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (g *Gateway) response(p Publication) publication.Response {
	return publication.Response{Status: "registered", URL: p.URL, Domain: g.cfg.Domain, PublicIP: g.cfg.PublicIP, Aliases: g.aliasURLs(p.ID)}
}
func sessionKey(owner, session string) string { return owner + ":" + session }

func (g *Gateway) register(ctx context.Context, w http.ResponseWriter, r *http.Request, owner tailnet.Identity) {
	var req publication.RegisterRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		reply(w, 400, publication.Response{Message: "invalid registration JSON: " + err.Error()})
		return
	}
	if err := d.Decode(new(any)); err != io.EOF {
		reply(w, 400, publication.Response{Message: "expected one JSON object"})
		return
	}
	if !publication.ValidName(req.ID) || !publication.ValidSession(req.Session) || req.Port < publication.ForwardMin || req.Port > publication.ForwardMax {
		reply(w, 400, publication.Response{Message: "id must be a lowercase DNS label, session must be 64 hex characters, forwarding port must be 20000-29999"})
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending[req.ID] {
		reply(w, 409, publication.Response{Message: "publication name registration in progress"})
		return
	}
	if g.retired[sessionKey(owner.ID, req.Session)] {
		reply(w, 410, publication.Response{Message: "session retired; register a new session"})
		return
	}
	if old, ok := g.publications[req.ID]; ok {
		if time.Now().Before(old.Expires) {
			if old.Owner == owner.ID && old.session == req.Session && old.Port == req.Port && old.IP == owner.IP && old.Wildcard == req.Wildcard {
				reply(w, 200, g.response(old))
				return
			}
			reply(w, 409, publication.Response{Message: "publication name already in use"})
			return
		}
		g.retired[sessionKey(old.Owner, old.session)] = true
		if old.session == req.Session && old.Owner == owner.ID {
			reply(w, 410, publication.Response{Message: "session expired; register a new session"})
			return
		}
	}
	if req.Wildcard {
		g.pending[req.ID] = true
		g.mu.Unlock()
		err := g.cfg.DNS.ensure(ctx, req.ID+"."+g.cfg.Domain, g.cfg.PublicIP)
		g.mu.Lock()
		delete(g.pending, req.ID)
		if err != nil {
			status := http.StatusServiceUnavailable
			var configError *dnsConfigError
			if errors.As(err, &configError) {
				status = http.StatusUnprocessableEntity
			}
			reply(w, status, publication.Response{Message: "wildcard DNS: " + err.Error()})
			return
		}
	}
	if g.retired[sessionKey(owner.ID, req.Session)] {
		reply(w, 410, publication.Response{Message: "session retired"})
		return
	}
	if err := ctx.Err(); err != nil {
		reply(w, 503, publication.Response{Message: err.Error()})
		return
	}
	p := Publication{ID: req.ID, Owner: owner.ID, IP: owner.IP, Port: req.Port, Wildcard: req.Wildcard, URL: "https://" + req.ID + "." + g.cfg.Domain, Expires: time.Now().Add(g.cfg.Lease), session: req.Session}
	old, exists := g.publications[req.ID]
	g.publications[req.ID] = p
	if err := g.writeConfig(); err != nil {
		if exists {
			g.publications[req.ID] = old
		} else {
			delete(g.publications, req.ID)
		}
		reply(w, 503, publication.Response{Message: "write Traefik configuration: " + err.Error()})
		return
	}
	if p.ID == g.cfg.RoutingSubdomain {
		select {
		case g.routingWake <- struct{}{}:
		default:
		}
	}
	reply(w, 200, g.response(p))
}

func (g *Gateway) update(w http.ResponseWriter, r *http.Request, owner tailnet.Identity) {
	g.mu.Lock()
	defer g.mu.Unlock()
	id, session := r.URL.Query().Get("id"), r.URL.Query().Get("session")
	if r.URL.Path == "/unregister" && publication.ValidSession(session) {
		g.retired[sessionKey(owner.ID, session)] = true
	}
	p, ok := g.publications[id]
	if !ok || !time.Now().Before(p.Expires) {
		reply(w, 404, publication.Response{Message: "publication expired or absent"})
		return
	}
	if p.Owner != owner.ID || p.session != session {
		reply(w, 403, publication.Response{Message: "publication belongs to another active session"})
		return
	}
	if r.URL.Path == "/heartbeat" {
		p.Expires = time.Now().Add(g.cfg.Lease)
		g.publications[id] = p
		reply(w, 200, publication.Response{Status: "ok", Aliases: g.aliasURLs(id)})
		return
	}
	// Retire before removing routes. A failed disk write must not allow stale heartbeats.
	g.retired[sessionKey(p.Owner, p.session)] = true
	p.Expires = time.Now()
	g.publications[id] = p
	if err := g.writeConfig(); err != nil {
		reply(w, 503, publication.Response{Message: err.Error()})
		return
	}
	delete(g.publications, id)
	reply(w, 200, publication.Response{Status: "unregistered"})
}

func (g *Gateway) expire(ctx context.Context) {
	interval := min(g.cfg.Lease/10, time.Second)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.mu.Lock()
			expired := []string{}
			cutoff := time.Now()
			for id, p := range g.publications {
				if !cutoff.Before(p.Expires) {
					g.retired[sessionKey(p.Owner, p.session)] = true
					expired = append(expired, id)
				}
			}
			if len(expired) > 0 {
				if err := g.writeConfig(); err != nil {
					log.Printf("expire routes: %v", err)
				} else {
					for _, id := range expired {
						delete(g.publications, id)
					}
				}
			}
			g.mu.Unlock()
		}
	}
}

// replaceFile keeps Traefik from reading a partially written configuration.
func replaceFile(dir string, data []byte) error {
	f, err := os.CreateTemp(dir, ".devrp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0644); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "devrp.yml"))
}

// Close removes active routes during an orderly gateway shutdown.
func (g *Gateway) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, p := range g.publications {
		g.retired[sessionKey(p.Owner, p.session)] = true
		p.Expires = time.Now()
		g.publications[id] = p
	}
	return g.writeConfig()
}
