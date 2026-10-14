package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UfukUstali/dev-reverse-proxy/internal/tailnet"
	"gopkg.in/yaml.v3"
)

const testDomain = "devrp.example.com"
const testAlias = "tenant." + testDomain
const testSnapshot = `{"routes":[{"domain":"` + testAlias + `","appDomain":"app.` + testDomain + `"}]}`

func TestParseRouting(t *testing.T) {
	for _, body := range []string{
		testSnapshot,
		`{"routes":[]}`,
	} {
		if _, err := parseRouting([]byte(body), testDomain); err != nil {
			t.Fatalf("rejected %s: %v", body, err)
		}
	}
	invalid := []string{
		`null`, `[]`, `{}`, `{"routes":null}`, `{"routes":{}}`, `{"routes":[null]}`,
		`{"routes":[],"extra":true}`, `{"routes":[],"routes":[]}`, `{"Routes":[]}`,
		testSnapshot + `{}`, strings.TrimSuffix(testSnapshot, "}"),
		strings.Replace(testSnapshot, `"appDomain"`, `"AppDomain"`, 1),
		strings.Replace(testSnapshot, `"appDomain"`, `"domain":"other.`+testDomain+`","appDomain"`, 1),
		strings.Replace(testSnapshot, `"appDomain"`, `"ip":"100.64.0.99","appDomain"`, 1),
		strings.Replace(testSnapshot, `"appDomain"`, `"port":20001,"appDomain"`, 1),
		`{"routes":[{"domain":"tenant.` + testDomain + `"}]}`,
		`{"routes":[{"domain":"other.` + testDomain + `","appDomain":"app.` + testDomain + `"},{"domain":"tenant.` + testDomain + `","appDomain":"other.` + testDomain + `"}]}`,
	}
	for _, domain := range []string{
		"app." + testDomain, testDomain, "tenant.external.example", "UPPER." + testDomain,
		"tenant." + testDomain + ".", "tenant." + testDomain + ":443", "*." + testDomain,
		"https://tenant." + testDomain, "-tenant." + testDomain, strings.Repeat("a", 64) + "." + testDomain,
		"tenant.app." + testDomain, "tenant.other." + testDomain, "deep.tenant.app." + testDomain,
	} {
		invalid = append(invalid, strings.Replace(testSnapshot, testAlias, domain, 1))
	}
	for _, target := range []string{"app.external.example", "app." + testDomain + ".", "APP." + testDomain, "app." + testDomain + ":20001", "child.app." + testDomain, "100.64.0.2", "http://app." + testDomain} {
		invalid = append(invalid, strings.Replace(testSnapshot, "app."+testDomain, target, 1))
	}
	for _, target := range []string{"app", "other"} {
		invalid = append(invalid, `{"routes":[{"domain":"`+testAlias+`","appDomain":"app.`+testDomain+`"},{"domain":"`+testAlias+`","appDomain":"`+target+`.`+testDomain+`"}]}`)
	}
	for i, body := range invalid {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if _, err := parseRouting([]byte(body), testDomain); err == nil {
				t.Fatalf("accepted malformed snapshot: %s", body)
			}
		})
	}
}

func routingGateway(t *testing.T, lease time.Duration) *Gateway {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	g, err := New(ctx, Config{ConfigDir: t.TempDir(), Domain: testDomain, PublicIP: "203.0.113.10", EntryPoint: "websecure", Resolver: "dns", Lease: lease,
		Identify: func(context.Context, string, string) (tailnet.Identity, error) {
			return tailnet.Identity{ID: "node", IP: "100.64.0.2"}, nil
		},
	})
	t.Cleanup(cancel)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func registerRoutingPublication(t *testing.T, g *Gateway, id, session string, port int) {
	t.Helper()
	controlCall(t, g, "/register", fmt.Sprintf(`{"id":%q,"session":%q,"port":%d}`, id, strings.Repeat(session, 64), port))
}

func controlCall(t *testing.T, g *Gateway, path, body string) {
	t.Helper()
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
	}
}

func routeConfig(t *testing.T, g *Gateway) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(g.cfg.ConfigDir, "devrp.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg["http"].(map[string]any)
}

func aliasPresent(t *testing.T, g *Gateway) bool {
	t.Helper()
	routers, _ := routeConfig(t, g)["routers"].(map[string]any)
	return routers["devrp-alias-"+testAlias] != nil
}

// Updates and reads are synchronized so the real HTTP poller can run under -race.
type routingFixture struct {
	mu                sync.Mutex
	body, contentType string
	status            int
	calls             int
}

func (f *routingFixture) set(status int, contentType, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.contentType, f.body = status, contentType, body
}

func (f *routingFixture) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *routingFixture) server(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Host != "authcenter."+testDomain || r.URL.Path != "/routing" || r.Header.Get("Authorization") != "Bearer test-secret" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected routing request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		w.Header().Set("Content-Type", f.contentType)
		w.Header().Set("Location", "/redirect-target")
		w.WriteHeader(f.status)
		_, _ = fmt.Fprint(w, f.body)
	}))
	t.Cleanup(s.Close)
	return s
}

// Keep the derived public URL and Host, but dial the isolated TLS fixture.
func routingFixtureClient(t *testing.T, s *httptest.Server) *http.Client {
	t.Helper()
	client := routingClient()
	transport := s.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = s.Certificate().DNSNames[0]
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
	}
	client.Transport = transport
	t.Cleanup(transport.CloseIdleConnections)
	return client
}

func prepareRoutingPoll(t *testing.T, g *Gateway, s *httptest.Server) *http.Client {
	t.Helper()
	g.cfg.RoutingSubdomain, g.cfg.RoutingSecret = "authcenter", "test-secret"
	registerRoutingPublication(t, g, "authcenter", "d", 20004)
	return routingFixtureClient(t, s)
}

func TestRoutingPollAuthReplacementAndFailures(t *testing.T) {
	g := routingGateway(t, time.Minute)
	registerRoutingPublication(t, g, "app", "a", 20001)
	f := &routingFixture{}
	f.set(200, "application/json; charset=utf-8", testSnapshot)
	s := f.server(t)
	client := prepareRoutingPoll(t, g, s)
	refresh := func() error { return g.refreshRouting(context.Background(), client) }
	if err := refresh(); err != nil || !aliasPresent(t, g) {
		t.Fatalf("valid poll not applied: %v", err)
	}
	cfg := routeConfig(t, g)
	router := cfg["routers"].(map[string]any)["devrp-alias-"+testAlias].(map[string]any)
	if router["service"] != "devrp-app" || router["rule"] != "Host(`"+testAlias+"`)" || len(router["tls"].(map[string]any)) != 0 {
		t.Fatalf("alias must reuse service and shared TLS: %v", router)
	}
	if middleware := router["middlewares"].([]any); len(middleware) != 1 || middleware[0] != "devrp-forwarding" {
		t.Fatalf("alias bypassed forwarding middleware: %v", middleware)
	}
	service := cfg["services"].(map[string]any)["devrp-app"].(map[string]any)["loadBalancer"].(map[string]any)
	if service["passHostHeader"] != true || service["servers"].([]any)[0].(map[string]any)["url"] != "http://100.64.0.2:20001" {
		t.Fatalf("alias changed host or trusted destination: %v", service)
	}
	for _, tc := range []struct {
		status      int
		media, body string
	}{
		{503, "application/json", `{"routes":[]}`},
		{401, "application/json", `{"routes":[]}`},
		{302, "application/json", `{"routes":[]}`},
		{200, "text/plain", `{"routes":[]}`},
		{200, "", `{"routes":[]}`},
		{200, "application/json", `{"routes":null}`},
		{200, "application/json", strings.Replace(testSnapshot, testAlias, "tenant.app."+testDomain, 1)},
		{200, "application/json", strings.Repeat(" ", routingMaxBytes+1)},
		// The first route is valid, but the whole replacement must be rejected.
		{200, "application/json", `{"routes":[{"domain":"replacement.` + testDomain + `","appDomain":"app.` + testDomain + `"},{"domain":"external.example","appDomain":"app.` + testDomain + `"}]}`},
	} {
		f.set(tc.status, tc.media, tc.body)
		if err := refresh(); err == nil || !aliasPresent(t, g) {
			t.Fatalf("failure replaced last good routes: status=%d media=%s err=%v", tc.status, tc.media, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.refreshRouting(ctx, client); err == nil || !aliasPresent(t, g) {
		t.Fatal("cancelled poll changed routes")
	}
	f.set(200, "application/json", strings.Replace(testSnapshot, testAlias, "replacement."+testDomain, 1))
	if err := refresh(); err != nil || aliasPresent(t, g) {
		t.Fatalf("nonempty snapshot did not replace old aliases: %v", err)
	}
	if routeConfig(t, g)["routers"].(map[string]any)["devrp-alias-replacement."+testDomain] == nil {
		t.Fatal("replacement alias not installed")
	}
	f.set(200, "application/json", testSnapshot)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	f.set(200, "application/json", `{"routes":[]}`)
	if err := refresh(); err != nil || aliasPresent(t, g) {
		t.Fatalf("empty snapshot did not clear aliases: %v", err)
	}
	f.set(200, "application/json", testSnapshot)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := refresh(); err == nil || !aliasPresent(t, g) {
		t.Fatal("unreachable Authcenter changed routes")
	}
}

func TestRoutingLeaseRemovalAndReregistration(t *testing.T) {
	g := routingGateway(t, 120*time.Millisecond)
	f := &routingFixture{}
	f.set(200, "application/json", testSnapshot)
	s := f.server(t)
	client := prepareRoutingPoll(t, g, s)
	if err := g.refreshRouting(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if aliasPresent(t, g) {
		t.Fatal("dormant destination got a route")
	}
	registerRoutingPublication(t, g, "app", "a", 20001)
	if !aliasPresent(t, g) {
		t.Fatal("register did not activate retained alias")
	}
	// No successful polls follow. Lease expiry must still remove the route.
	s.Close()
	deadline := time.Now().Add(time.Second)
	for aliasPresent(t, g) {
		if time.Now().After(deadline) {
			t.Fatal("alias survived lease expiry")
		}
		time.Sleep(5 * time.Millisecond)
	}
	registerRoutingPublication(t, g, "app", "b", 20002)
	if !aliasPresent(t, g) {
		t.Fatal("re-registration did not restore retained alias")
	}
	services := routeConfig(t, g)["services"].(map[string]any)
	servers := services["devrp-app"].(map[string]any)["loadBalancer"].(map[string]any)["servers"].([]any)
	if servers[0].(map[string]any)["url"] != "http://100.64.0.2:20002" {
		t.Fatal("alias reused expired destination")
	}
	controlCall(t, g, "/unregister?id=app&session="+strings.Repeat("b", 64), "")
	if aliasPresent(t, g) {
		t.Fatal("alias survived unregister")
	}
	registerRoutingPublication(t, g, "app", "c", 20003)
	if err := g.Close(); err != nil || aliasPresent(t, g) {
		t.Fatalf("alias survived shutdown: %v", err)
	}
}

func TestRoutingPublicationConflicts(t *testing.T) {
	g := routingGateway(t, time.Minute)
	registerRoutingPublication(t, g, "app", "a", 20001)
	f := &routingFixture{}
	f.set(200, "application/json", testSnapshot)
	s := f.server(t)
	client := prepareRoutingPoll(t, g, s)
	if err := g.refreshRouting(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	registerRoutingPublication(t, g, "tenant", "b", 20002)
	if aliasPresent(t, g) {
		t.Fatal("imported alias overrode publication")
	}
	router := routeConfig(t, g)["routers"].(map[string]any)["devrp-tenant"].(map[string]any)
	if router["service"] != "devrp-tenant" {
		t.Fatal("publication destination changed")
	}
	if err := g.refreshRouting(context.Background(), client); err == nil {
		t.Fatal("accepted conflicting snapshot")
	}
	controlCall(t, g, "/unregister?id=tenant&session="+strings.Repeat("b", 64), "")
	if !aliasPresent(t, g) {
		t.Fatal("alias did not resume when hostname became free")
	}
	// Failed atomic writes also retain the accepted snapshot.
	originalDir := g.cfg.ConfigDir
	g.cfg.ConfigDir = filepath.Join(t.TempDir(), "missing")
	f.set(200, "application/json", `{"routes":[]}`)
	if err := g.refreshRouting(context.Background(), client); err == nil {
		t.Fatal("missing write failure")
	}
	g.cfg.ConfigDir = originalDir
	if g.aliases[testAlias] != "app" || !aliasPresent(t, g) {
		t.Fatal("write failure lost last good snapshot")
	}
}

func TestRoutingConfiguration(t *testing.T) {
	for _, cfg := range []Config{
		{RoutingSubdomain: "authcenter"}, {RoutingSecret: "secret"},
		{RoutingSubdomain: "https://authcenter.devrp.example.com/routing", RoutingSecret: "secret"},
		{RoutingSubdomain: "authcenter.devrp.example.com", RoutingSecret: "secret"},
		{RoutingSubdomain: "authcenter:443", RoutingSecret: "secret"},
		{RoutingSubdomain: "authcenter/routing", RoutingSecret: "secret"},
		{RoutingSubdomain: "UPPER", RoutingSecret: "secret"},
		{RoutingSubdomain: "-bad", RoutingSecret: "secret"},
		{RoutingSubdomain: strings.Repeat("a", 64), RoutingSecret: "secret"},
		{RoutingSubdomain: "authcenter", RoutingSecret: "bad\r\nsecret"},
		{RoutingSubdomain: "authcenter", RoutingSecret: "bad secret"},
		{RoutingInterval: -time.Second},
	} {
		if err := configureRouting(&cfg); err == nil {
			t.Fatalf("accepted invalid config: %+v", cfg)
		}
	}
	for _, cfg := range []Config{{}, {RoutingSubdomain: "authcenter", RoutingSecret: "secret"}} {
		if err := configureRouting(&cfg); err != nil || cfg.RoutingInterval != 30*time.Second {
			t.Fatalf("bad defaults: %v", err)
		}
	}
}

func TestRoutingBackgroundPoller(t *testing.T) {
	f := &routingFixture{}
	f.set(200, "application/json", testSnapshot)
	s := f.server(t)
	g := routingGateway(t, time.Minute)
	g.cfg.RoutingSubdomain, g.cfg.RoutingSecret, g.cfg.RoutingInterval = "authcenter", "test-secret", 10*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go g.pollRouting(ctx, routingFixtureClient(t, s))
	registerRoutingPublication(t, g, "app", "a", 20001)
	registerRoutingPublication(t, g, "authcenter", "d", 20004)
	wait := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for aliasPresent(t, g) != want {
			if time.Now().After(deadline) {
				t.Fatalf("background poller: alias presence did not become %v", want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	wait(true)
	f.set(200, "application/json", `{"routes":[]}`)
	wait(false)
}

func TestRoutingRequiresLiveAuthcenterPublication(t *testing.T) {
	g := routingGateway(t, time.Minute)
	g.cfg.RoutingSubdomain, g.cfg.RoutingSecret = "authcenter", "test-secret"
	f := &routingFixture{}
	f.set(200, "application/json", testSnapshot)
	s := f.server(t)
	client := routingFixtureClient(t, s)
	refresh := func() {
		t.Helper()
		if err := g.refreshRouting(context.Background(), client); err != nil {
			t.Fatal(err)
		}
	}
	refresh()
	registerRoutingPublication(t, g, "app", "a", 20001)
	refresh()
	// A matching imported alias is not the Authcenter publication.
	g.mu.Lock()
	g.aliases = map[string]string{"authcenter." + testDomain: "app"}
	err := g.writeConfig()
	g.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	refresh()
	if f.count() != 0 {
		t.Fatal("polled absent Authcenter or a matching alias")
	}
	registerRoutingPublication(t, g, "authcenter", "d", 20004)
	refresh()
	if f.count() != 1 || !aliasPresent(t, g) {
		t.Fatal("live Authcenter did not enable polling")
	}
	controlCall(t, g, "/unregister?id=authcenter&session="+strings.Repeat("d", 64), "")
	refresh()
	if f.count() != 1 || !aliasPresent(t, g) {
		t.Fatal("unregister did not suspend polling while retaining aliases")
	}
	registerRoutingPublication(t, g, "authcenter", "e", 20004)
	g.mu.Lock()
	p := g.publications["authcenter"]
	p.Expires = time.Now().Add(-time.Second)
	g.publications[p.ID] = p
	g.mu.Unlock()
	refresh()
	if f.count() != 1 {
		t.Fatal("polled an expired Authcenter lease")
	}
	registerRoutingPublication(t, g, "authcenter", "f", 20004)
	refresh()
	if f.count() != 2 {
		t.Fatal("Authcenter re-registration did not resume polling")
	}
}

func TestRoutingRegistrationWakesDormantPoller(t *testing.T) {
	g := routingGateway(t, time.Minute)
	g.cfg.RoutingSubdomain, g.cfg.RoutingSecret, g.cfg.RoutingInterval = "authcenter", "test-secret", time.Hour
	f := &routingFixture{}
	f.set(200, "application/json", testSnapshot)
	s := f.server(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go g.pollRouting(ctx, routingFixtureClient(t, s))
	registerRoutingPublication(t, g, "app", "a", 20001)
	registerRoutingPublication(t, g, "authcenter", "d", 20004)
	deadline := time.Now().Add(time.Second)
	for !aliasPresent(t, g) {
		if time.Now().After(deadline) {
			t.Fatal("registration waited for dormant poll interval")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRoutingDepartingAuthcenterCannotReplaceSnapshot(t *testing.T) {
	g := routingGateway(t, time.Minute)
	registerRoutingPublication(t, g, "app", "a", 20001)
	f := &routingFixture{}
	f.set(200, "application/json", testSnapshot)
	client := prepareRoutingPoll(t, g, f.server(t))
	if err := g.refreshRouting(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	stalled := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"routes":[]}`)
	}))
	defer stalled.Close()
	client = routingFixtureClient(t, stalled)
	done := make(chan error, 1)
	go func() { done <- g.refreshRouting(context.Background(), client) }()
	<-started
	controlCall(t, g, "/unregister?id=authcenter&session="+strings.Repeat("d", 64), "")
	close(release)
	if err := <-done; err != nil || !aliasPresent(t, g) {
		t.Fatalf("departing Authcenter changed accepted snapshot: %v", err)
	}
}

func TestRoutingRequestCancellation(t *testing.T) {
	started := make(chan struct{})
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer s.Close()
	g := routingGateway(t, time.Minute)
	client := prepareRoutingPoll(t, g, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- g.refreshRouting(ctx, client) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled hanging poll succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("poll ignored context cancellation")
	}
}

func TestRoutingBodyTimeoutRetainsSnapshot(t *testing.T) {
	g := routingGateway(t, time.Minute)
	registerRoutingPublication(t, g, "app", "a", 20001)
	f := &routingFixture{}
	f.set(200, "application/json", testSnapshot)
	s := f.server(t)
	client := prepareRoutingPoll(t, g, s)
	if err := g.refreshRouting(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	stalled := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"routes":[`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer stalled.Close()
	client = routingFixtureClient(t, stalled)
	client.Timeout = 0
	start := time.Now()
	// A client without its own timeout verifies that refresh bounds body reads.
	if err := g.refreshRouting(context.Background(), client); err == nil || !aliasPresent(t, g) {
		t.Fatalf("stalled body changed routes: %v", err)
	}
	if elapsed := time.Since(start); elapsed > routingTimeout+time.Second {
		t.Fatalf("poll exceeded its timeout: %v", elapsed)
	}
}
