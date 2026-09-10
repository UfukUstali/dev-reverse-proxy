package readiness_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/UfukUstali/dev-reverse-proxy/internal/readiness"
)

func TestReadinessRequiresApplicationDNSAndAllCertificateNames(t *testing.T) {
	// httptest's certificate covers *.example.com, but not two labels beneath it.
	app := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer app.Close()
	public := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer public.Close()
	roots := x509.NewCertPool()
	roots.AddCert(public.Certificate())
	checker := readiness.Checker{
		Lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("203.0.113.10")}, nil
		},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if strings.HasSuffix(address, ":443") {
				address = public.Listener.Addr().String()
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
		TLS: &tls.Config{RootCAs: roots},
	}
	target := readiness.Target{Application: app.Listener.Addr().String(), URL: "https://tenant.example.com", PublicIP: "203.0.113.10"}
	if err := checker.Check(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	target.Wildcard = true
	if err := checker.Check(context.Background(), target); err == nil || !strings.Contains(err.Error(), "public TLS") {
		t.Fatalf("accepted missing child certificate: %v", err)
	}
	target.Wildcard = false
	target.PublicIP = "203.0.113.11"
	if err := checker.Check(context.Background(), target); err == nil || !strings.Contains(err.Error(), "DNS") {
		t.Fatalf("accepted wrong DNS: %v", err)
	}
	app.Close()
	target.PublicIP = "203.0.113.10"
	if err := checker.Check(context.Background(), target); err == nil || !strings.Contains(err.Error(), "local application") {
		t.Fatalf("accepted unavailable app: %v", err)
	}
}

func TestPendingDiagnosticContinuesUntilRecovery(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer app.Close()
	public := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer public.Close()
	roots := x509.NewCertPool()
	roots.AddCert(public.Certificate())
	recovered := false
	pending := false
	ready := false
	checker := readiness.Checker{Lookup: func(context.Context, string) ([]netip.Addr, error) {
		ip := "203.0.113.11"
		if recovered {
			ip = "203.0.113.10"
		}
		return []netip.Addr{netip.MustParseAddr(ip)}, nil
	}, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		if strings.HasSuffix(address, ":443") {
			address = public.Listener.Addr().String()
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}, TLS: &tls.Config{RootCAs: roots}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	checker.Wait(ctx, readiness.Target{Application: app.Listener.Addr().String(), URL: "https://tenant.example.com", PublicIP: "203.0.113.10"}, time.Millisecond, 5*time.Millisecond, func(err error) {
		if err != nil {
			pending = true
			recovered = true
		} else {
			ready = true
		}
	})
	if !pending || !ready {
		t.Fatalf("pending=%v ready=%v", pending, ready)
	}
}
