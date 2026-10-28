package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/UfukUstali/dev-reverse-proxy/internal/publication"
)

const (
	routingTimeout  = 5 * time.Second
	routingMaxBytes = 1 << 20
)

func configureRouting(cfg *Config) error {
	if (cfg.RoutingSubdomain == "") != (cfg.RoutingSecret == "") {
		return errors.New("Authcenter routing subdomain and secret must be configured together")
	}
	if cfg.RoutingInterval == 0 {
		cfg.RoutingInterval = 30 * time.Second
	}
	if cfg.RoutingInterval < 0 {
		return errors.New("Authcenter routing interval must be positive")
	}
	if cfg.RoutingSubdomain == "" {
		return nil
	}
	if !publication.ValidName(cfg.RoutingSubdomain) {
		return errors.New("Authcenter routing subdomain must be a lowercase single DNS label")
	}
	for _, c := range cfg.RoutingSecret {
		if c < 33 || c > 126 {
			return errors.New("Authcenter routing secret must contain only visible ASCII without spaces")
		}
	}
	return nil
}

func routingClient() *http.Client {
	return &http.Client{
		Timeout: routingTimeout,
		// A redirect must never forward the credential or replace the configured endpoint.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (g *Gateway) pollRouting(ctx context.Context, client *http.Client) {
	for {
		if err := g.refreshRouting(ctx, client); err != nil && ctx.Err() == nil {
			log.Printf("Authcenter routing: %v", err)
		}
		timer := time.NewTimer(g.cfg.RoutingInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-g.routingWake:
			timer.Stop()
		}
	}
}

func (g *Gateway) refreshRouting(ctx context.Context, client *http.Client) error {
	g.mu.Lock()
	source, live := g.publications[g.cfg.RoutingSubdomain]
	live = live && time.Now().Before(source.Expires)
	g.mu.Unlock()
	if !live {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, routingTimeout)
	defer cancel()
	endpoint := "https://" + g.cfg.RoutingSubdomain + "." + g.cfg.Domain + "/routing"
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("cannot create routing request")
	}
	r.Header.Set("Authorization", "Bearer "+g.cfg.RoutingSecret)
	r.Header.Set("Accept", "application/json")
	resp, err := client.Do(r)
	if err != nil {
		// Do not log configured URLs, credentials, or response bodies.
		return errors.New("routing request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("routing HTTP status %d", resp.StatusCode)
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return errors.New("routing response must be application/json")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, routingMaxBytes+1))
	if err != nil {
		return errors.New("cannot read routing response")
	}
	if len(data) > routingMaxBytes {
		return errors.New("routing response exceeds 1 MiB")
	}
	aliases, err := parseRouting(data, g.cfg.Domain)
	if err != nil {
		return fmt.Errorf("invalid routing snapshot: %w", err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, live := g.publications[g.cfg.RoutingSubdomain]
	if !live || !time.Now().Before(current.Expires) || current.session != source.session || current.Owner != source.Owner || current.IP != source.IP || current.Port != source.Port {
		// A departing Authcenter publication cannot replace the last good snapshot.
		return nil
	}
	for domain := range aliases {
		if p, ok := g.publications[strings.TrimSuffix(domain, "."+g.cfg.Domain)]; ok && time.Now().Before(p.Expires) {
			return fmt.Errorf("alias %s conflicts with a publication hostname", domain)
		}
	}
	if maps.Equal(g.aliases, aliases) {
		return nil
	}
	old := g.aliases
	g.aliases = aliases
	if err := g.writeConfig(); err != nil {
		g.aliases = old
		return fmt.Errorf("write routing snapshot: %w", err)
	}
	return nil
}

// aliasRouted reports whether an imported alias currently routes to its target.
// The caller holds g.mu.
func (g *Gateway) aliasRouted(domain, id string, now time.Time) bool {
	p, ok := g.publications[id]
	if !ok || !now.Before(p.Expires) {
		return false
	}
	// Publication registrations always retain their own hostname, even if
	// Authcenter previously imported that name as an alias.
	other, ok := g.publications[strings.TrimSuffix(domain, "."+g.cfg.Domain)]
	return !ok || !now.Before(other.Expires)
}

// aliasURLs lists the routed alias URLs of one publication in sorted order.
// The caller holds g.mu.
func (g *Gateway) aliasURLs(id string) []string {
	now := time.Now()
	var urls []string
	for domain, target := range g.aliases {
		if target == id && g.aliasRouted(domain, id, now) {
			urls = append(urls, "https://"+domain)
		}
	}
	slices.Sort(urls)
	return urls
}

// routingObject rejects missing, unknown, case-mismatched and duplicate keys,
// including duplicate keys that encoding/json would otherwise silently replace.
func routingObject(data []byte, keys ...string) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("expected an object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("expected an object key")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		allowed := false
		for _, want := range keys {
			allowed = allowed || key == want
		}
		if !allowed {
			return nil, fmt.Errorf("unknown key %q", key)
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("expected one JSON object")
	}
	if len(fields) != len(keys) {
		return nil, errors.New("missing required key")
	}
	return fields, nil
}

func parseRouting(data []byte, publicDomain string) (map[string]string, error) {
	fields, err := routingObject(data, "routes")
	if err != nil {
		return nil, err
	}
	var routes []json.RawMessage
	if err := json.Unmarshal(fields["routes"], &routes); err != nil || routes == nil {
		return nil, errors.New("routes must be an array")
	}
	aliases := make(map[string]string, len(routes))
	targets := make(map[string]bool, len(routes))
	for _, route := range routes {
		fields, err := routingObject(route, "domain", "appDomain")
		if err != nil {
			return nil, err
		}
		var domain, appDomain string
		if err := json.Unmarshal(fields["domain"], &domain); err != nil {
			return nil, errors.New("domain must be a hostname string")
		}
		if err := json.Unmarshal(fields["appDomain"], &appDomain); err != nil {
			return nil, errors.New("appDomain must be a hostname string")
		}
		id := strings.TrimSuffix(appDomain, "."+publicDomain)
		if appDomain != id+"."+publicDomain || !publication.ValidName(id) {
			return nil, fmt.Errorf("appDomain %q must be an exact publication hostname", appDomain)
		}
		// All aliases use the centrally provisioned single-label wildcard.
		label := strings.TrimSuffix(domain, "."+publicDomain)
		if len(domain) > 253 || domain == appDomain || domain != label+"."+publicDomain || !publication.ValidName(label) {
			return nil, fmt.Errorf("unsupported alias hostname %q", domain)
		}
		if _, exists := aliases[domain]; exists {
			return nil, fmt.Errorf("duplicate alias ownership for %s", domain)
		}
		aliases[domain] = id
		targets[appDomain] = true
	}
	for domain := range aliases {
		if targets[domain] {
			return nil, fmt.Errorf("alias %s conflicts with an application hostname", domain)
		}
	}
	return aliases, nil
}
