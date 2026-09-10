// Package readiness verifies local acceptance, public DNS and TLS coverage.
package readiness

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"time"
)

type Target struct {
	Application, URL, PublicIP string
	Wildcard                   bool
}

// Checker exposes network boundaries so readiness can be checked without
// production DNS or certificates. Nil fields use system networking and roots.
type Checker struct {
	Lookup func(context.Context, string) ([]netip.Addr, error)
	Dial   func(context.Context, string, string) (net.Conn, error)
	TLS    *tls.Config
}

func (c Checker) Check(ctx context.Context, target Target) error {
	dial := c.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 2 * time.Second}).DialContext
	}
	lookup := c.Lookup
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	conn, err := dial(ctx, "tcp", target.Application)
	if err != nil {
		return fmt.Errorf("local application port: %w", err)
	}
	conn.Close()
	u, err := url.Parse(target.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return fmt.Errorf("gateway did not provide a public HTTPS URL")
	}
	hosts := []string{u.Hostname()}
	if target.Wildcard {
		hosts = append(hosts, "devrp-readiness."+u.Hostname())
	}
	want, err := netip.ParseAddr(target.PublicIP)
	if err != nil {
		return fmt.Errorf("gateway did not provide a valid public IP")
	}
	for _, host := range hosts {
		ips, err := lookup(ctx, host)
		if err != nil {
			return fmt.Errorf("DNS for %s: %w", host, err)
		}
		if len(ips) == 0 {
			return fmt.Errorf("DNS for %s has no addresses", host)
		}
		for _, ip := range ips {
			if ip.Unmap() != want.Unmap() {
				return fmt.Errorf("DNS for %s points to %s instead of gateway %s", host, ip, want)
			}
		}
		raw, err := dial(ctx, "tcp", net.JoinHostPort(want.String(), "443"))
		if err != nil {
			return fmt.Errorf("public TLS for %s: %w", host, err)
		}
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		if c.TLS != nil {
			tlsConfig = c.TLS.Clone()
		}
		tlsConfig.ServerName = host
		secure := tls.Client(raw, tlsConfig)
		err = secure.HandshakeContext(ctx)
		secure.Close()
		if err != nil {
			return fmt.Errorf("public TLS for %s: %w", host, err)
		}
	}
	return nil
}

// Wait reports the current pending condition after the deadline and keeps
// checking until ready or cancelled. A nil report means readiness succeeded.
func (c Checker) Wait(ctx context.Context, target Target, interval, pendingAfter time.Duration, report func(error)) {
	started := time.Now()
	reported := false
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := c.Check(checkCtx, target)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			report(nil)
			return
		}
		if !reported && time.Since(started) >= pendingAfter {
			report(err)
			reported = true
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
