package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/UfukUstali/dev-reverse-proxy/internal/gateway"
	"github.com/UfukUstali/dev-reverse-proxy/internal/tailnet"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestWildcardRecordsAreRetainedAndOrdinaryPublicationDoesNotUseDNS(t *testing.T) {
	var mu sync.Mutex
	records := []map[string]any{}
	calls := 0
	dns := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/zones/example.com":
			_ = json.NewEncoder(w).Encode(map[string]any{"zone": map[string]any{"id": 1, "name": "example.com", "mode": "primary"}})
		case r.Method == "GET" && r.URL.Path == "/zones/1/rrsets":
			_ = json.NewEncoder(w).Encode(map[string]any{"rrsets": records, "meta": map[string]any{"pagination": map[string]any{"page": 1, "last_page": 1}}})
		case r.Method == "POST" && r.URL.Path == "/zones/1/rrsets":
			var record map[string]any
			_ = json.NewDecoder(r.Body).Decode(&record)
			records = append(records, record)
			_ = json.NewEncoder(w).Encode(map[string]any{"rrset": record, "action": map[string]any{"id": 1, "status": "success"}})
		default:
			t.Errorf("unexpected DNS mutation: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer dns.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g, err := gateway.New(ctx, gateway.Config{ConfigDir: t.TempDir(), Domain: "devrp.example.com", PublicIP: "203.0.113.10", EntryPoint: "websecure", Resolver: "dns", Identify: func(context.Context, string, string) (tailnet.Identity, error) {
		return tailnet.Identity{ID: "node", IP: "100.64.0.2"}, nil
	}, DNS: &gateway.DNS{Client: hcloud.NewClient(hcloud.WithToken("fixture"), hcloud.WithEndpoint(dns.URL)), Zone: "example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	register := func(wildcard bool, session string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"id": "myapp", "port": 20001, "wildcard": wildcard, "session": session})
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest("POST", "/register", strings.NewReader(string(body))))
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	remove := func(session string) {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest("POST", "/unregister?id=myapp&session="+session, nil))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	a, b, c := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	register(false, a)
	remove(a)
	if calls != 0 {
		t.Fatal("ordinary startup contacted DNS")
	}
	register(true, b)
	remove(b)
	mu.Lock()
	if len(records) != 2 || records[0]["name"] != "myapp.devrp" || records[1]["name"] != "*.myapp.devrp" {
		t.Fatalf("wrong records: %v", records)
	}
	before := calls
	mu.Unlock()
	register(false, c)
	remove(c)
	if calls != before {
		t.Fatal("ordinary reuse contacted DNS")
	}
	register(true, strings.Repeat("d", 64))
	if len(records) != 2 {
		t.Fatal("duplicate records")
	}
}

func TestDNSConflictsAndOutagesDoNotDisturbOrdinaryPublication(t *testing.T) {
	for _, scenario := range []string{"wrong-address", "alias", "other-family", "outage"} {
		t.Run(scenario, func(t *testing.T) {
			writes := 0
			dns := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes++
					http.Error(w, "unexpected mutation", 500)
					return
				}
				if scenario == "outage" {
					http.Error(w, "unavailable", 503)
					return
				}
				if r.URL.Path == "/zones/example.com" {
					json.NewEncoder(w).Encode(map[string]any{"zone": map[string]any{"id": 1, "name": "example.com", "mode": "primary"}})
					return
				}
				recordType, value := "A", "203.0.113.99"
				if scenario == "alias" {
					recordType, value = "CNAME", "elsewhere.example.com."
				}
				if scenario == "other-family" {
					recordType, value = "AAAA", "2001:db8::1"
				}
				json.NewEncoder(w).Encode(map[string]any{"rrsets": []any{map[string]any{"name": "*.tenant.devrp", "type": recordType, "records": []any{map[string]any{"value": value}}}}, "meta": map[string]any{"pagination": map[string]any{"page": 1, "last_page": 1}}})
			}))
			defer dns.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			g, err := gateway.New(ctx, gateway.Config{ConfigDir: t.TempDir(), Domain: "devrp.example.com", PublicIP: "203.0.113.10", EntryPoint: "websecure", Resolver: "dns", Identify: func(context.Context, string, string) (tailnet.Identity, error) {
				return tailnet.Identity{ID: "node", IP: "100.64.0.2"}, nil
			}, DNS: &gateway.DNS{Client: hcloud.NewClient(hcloud.WithToken("fixture"), hcloud.WithEndpoint(dns.URL)), Zone: "example.com"}})
			if err != nil {
				t.Fatal(err)
			}
			call := func(id string, wildcard bool, session string) int {
				body, _ := json.Marshal(map[string]any{"id": id, "port": 20001, "wildcard": wildcard, "session": session})
				w := httptest.NewRecorder()
				g.ServeHTTP(w, httptest.NewRequest("POST", "/register", strings.NewReader(string(body))))
				return w.Code
			}
			if got := call("ordinary", false, strings.Repeat("a", 64)); got != 200 {
				t.Fatalf("ordinary failed: %d", got)
			}
			if got := call("tenant", true, strings.Repeat("b", 64)); got < 400 {
				t.Fatalf("wildcard conflict accepted: %d", got)
			}
			if writes != 0 {
				t.Fatal("conflict overwrote DNS")
			}
			w := httptest.NewRecorder()
			g.ServeHTTP(w, httptest.NewRequest("POST", "/heartbeat?id=ordinary&session="+strings.Repeat("a", 64), nil))
			if w.Code != 200 {
				t.Fatal("DNS failure disturbed ordinary route")
			}
		})
	}
}
