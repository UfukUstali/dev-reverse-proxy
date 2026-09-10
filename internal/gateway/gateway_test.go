package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/UfukUstali/dev-reverse-proxy/internal/gateway"
	"github.com/UfukUstali/dev-reverse-proxy/internal/tailnet"
)

func TestPublicationOwnership(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g, err := gateway.New(ctx, gateway.Config{
		ConfigDir: t.TempDir(), Domain: "devrp.example.com", PublicIP: "203.0.113.10", EntryPoint: "websecure", Resolver: "dns", Lease: time.Second,
		Identify: func(ctx context.Context, remote, local string) (tailnet.Identity, error) {
			return tailnet.Identity{ID: remote, IP: "100.64.0.2"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(owner, path, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.RemoteAddr = owner
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: got %d %s, want %d", path, w.Code, w.Body.String(), want)
		}
		var result map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result
	}
	session := strings.Repeat("a", 64)
	body := `{"id":"myapp","port":20001,"session":"` + session + `"}`
	result := call("machine-a", "/register", body, 200)
	if result["url"] != "https://myapp.devrp.example.com" {
		t.Fatal(result)
	}
	call("machine-b", "/register", body, 409)
	call("machine-b", "/heartbeat?id=myapp&session="+session, "", 403)
	call("machine-b", "/unregister?id=myapp&session="+session, "", 403)
	call("machine-a", "/heartbeat?id=myapp&session="+session, "", 200)
	call("machine-a", "/unregister?id=myapp&session="+session, "", 200)
	newer := strings.Repeat("b", 64)
	call("machine-a", "/register", strings.ReplaceAll(body, session, newer), 200)
	call("machine-a", "/heartbeat?id=myapp&session="+session, "", 403)
	call("machine-a", "/unregister?id=myapp&session="+session, "", 403)
	call("machine-a", "/heartbeat?id=myapp&session="+newer, "", 200)
}

func TestValidationExpiryAndConcurrentClaims(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lease := 100 * time.Millisecond
	g, err := gateway.New(ctx, gateway.Config{ConfigDir: t.TempDir(), Domain: "devrp.example.com", PublicIP: "203.0.113.10", EntryPoint: "websecure", Resolver: "dns", Lease: lease, Identify: func(context.Context, string, string) (tailnet.Identity, error) {
		return tailnet.Identity{ID: "node", IP: "100.64.0.2"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	session := strings.Repeat("a", 64)
	for _, name := range []string{"a.b", "UPPER", "-bad", "bad-", strings.Repeat("x", 64)} {
		body := `{"id":"` + name + `","port":20001,"session":"` + session + `"}`
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest("POST", "/register", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("accepted %s: %d", name, w.Code)
		}
	}
	for _, extra := range []string{`,"host":"100.64.0.3"`, `,"destination":"evil"`} {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest("POST", "/register", strings.NewReader(`{"id":"myapp","port":20001,"session":"`+session+`"`+extra+`}`)))
		if w.Code != 400 {
			t.Fatal("accepted supplied destination")
		}
	}
	results := make(chan int, 2)
	for _, token := range []string{session, strings.Repeat("b", 64)} {
		go func(token string) {
			w := httptest.NewRecorder()
			g.ServeHTTP(w, httptest.NewRequest("POST", "/register", strings.NewReader(`{"id":"myapp","port":20001,"session":"`+token+`"}`)))
			results <- w.Code
		}(token)
	}
	a, b := <-results, <-results
	if !((a == 200 && b == 409) || (a == 409 && b == 200)) {
		t.Fatalf("simultaneous claims: %d %d", a, b)
	}
	time.Sleep(2 * lease)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("POST", "/register", strings.NewReader(`{"id":"myapp","port":20002,"session":"`+strings.Repeat("c", 64)+`"}`)))
	if w.Code != 200 {
		t.Fatalf("name not freed: %s", w.Body.String())
	}
}
