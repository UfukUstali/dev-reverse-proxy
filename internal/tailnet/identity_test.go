package tailnet_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/UfukUstali/dev-reverse-proxy/internal/tailnet"
)

func TestLocalIdentityRequiresPeerCapabilityAndMatchingAddress(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "tailscaled.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var authorized atomic.Bool
	authorized.Store(true)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("addr") != "100.64.0.2:32100" || r.URL.Query().Get("dst_ip") != "100.64.0.1" {
			t.Errorf("wrong identity lookup: %s", r.URL)
		}
		caps := map[string]any{}
		if authorized.Load() {
			caps["devrp.liuf.uk/publish"] = []any{map[string]any{}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Node": map[string]any{"StableID": "node-a", "Addresses": []string{"100.64.0.2/32"}}, "CapMap": caps})
	})}
	go srv.Serve(ln)
	defer srv.Close()
	identify := tailnet.LocalIdentity(socket)
	identity, err := identify(context.Background(), "100.64.0.2:32100", "100.64.0.1:8080")
	if err != nil || identity.ID != "node-a" || identity.IP != "100.64.0.2" {
		t.Fatalf("%+v %v", identity, err)
	}
	authorized.Store(false)
	if _, err = identify(context.Background(), "100.64.0.2:32100", "100.64.0.1:8080"); err == nil {
		t.Fatal("accepted unauthorized machine")
	}
	if _, err = identify(context.Background(), "127.0.0.1:32100", "100.64.0.1:8080"); err == nil {
		t.Fatal("accepted non-tailnet peer")
	}
}
