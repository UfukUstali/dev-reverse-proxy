package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/UfukUstali/dev-reverse-proxy/internal/gateway"
	"github.com/UfukUstali/dev-reverse-proxy/internal/tailnet"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	addr, err := netip.ParseAddrPort(os.Getenv("LISTEN_ADDR"))
	if err != nil || !tailnet.IsAddress(addr.Addr()) || addr.Port() == 0 {
		return fmt.Errorf("LISTEN_ADDR must be the gateway Tailscale IP and port, for example 100.64.0.1:8080")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	cfg := gateway.Config{
		ConfigDir:  env("CONFIG_DIR", "/config"),
		Domain:     env("PUBLIC_DOMAIN", "devrp.liuf.uk"),
		PublicIP:   os.Getenv("GATEWAY_PUBLIC_IP"),
		EntryPoint: env("TRAEFIK_ENTRYPOINT", "websecure"),
		Resolver:   env("TRAEFIK_RESOLVER", "hetzner"),
		Identify:   tailnet.LocalIdentity(env("TAILSCALE_SOCKET", "/var/run/tailscale/tailscaled.sock")),
	}
	if token := os.Getenv("HETZNER_TOKEN"); token != "" {
		cfg.DNS = &gateway.DNS{Client: hcloud.NewClient(hcloud.WithToken(token)), Zone: os.Getenv("HETZNER_ZONE")}
	}
	// Claim the listener before clearing stale routes, so a second gateway cannot
	// erase the running instance's configuration when its bind fails.
	ln, err := net.Listen("tcp", addr.String())
	if err != nil {
		return err
	}
	defer ln.Close()
	manager, err := gateway.New(ctx, cfg)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: manager,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ln) }()
	log.Printf("Gateway listening on %s for %s", addr, cfg.Domain)
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, stop := context.WithTimeout(context.Background(), 25*time.Second)
	defer stop()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return err
	}
	return manager.Close()
}
func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
