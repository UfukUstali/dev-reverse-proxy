package gateway_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UfukUstali/dev-reverse-proxy/internal/gateway"
	"github.com/UfukUstali/dev-reverse-proxy/internal/tailnet"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestTraefikPublicTraffic(t *testing.T) {
	binary := os.Getenv("TRAEFIK_BINARY")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("traefik")
		if err != nil {
			t.Skip("set TRAEFIK_BINARY to exercise real Traefik")
		}
	}
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "devrp fixture"}, DNSNames: []string{"*.devrp.example.com", "*.myapp.devrp.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	_ = os.WriteFile(filepath.Join(dir, "cert.pem"), certPEM, 0600)
	_ = os.WriteFile(filepath.Join(dir, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600)
	_ = os.WriteFile(filepath.Join(dir, "fixture.yml"), []byte(fmt.Sprintf("tls:\n  certificates:\n    - certFile: %s\n      keyFile: %s\n", filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))), 0600)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stream":
			fmt.Fprintln(w, "first")
			w.(http.Flusher).Flush()
			<-release
			fmt.Fprintln(w, "second")
		case "/ws":
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n")
			_ = rw.Flush()
			frame := make([]byte, 8)
			if _, err := io.ReadFull(rw, frame); err != nil {
				return
			}
			if frame[0] != 0x81 || frame[1] != 0x82 {
				return
			}
			_, _ = conn.Write([]byte{0x81, 2, frame[6] ^ frame[2], frame[7] ^ frame[3]})
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"host": r.Host, "scheme": r.Header.Get("X-Forwarded-Proto"), "visitor": r.Header.Get("X-Forwarded-For"), "forwarded": r.Header.Get("Forwarded")})
		}
	}))
	defer backend.Close()
	// Bridge a policy-range forwarding port to the loopback HTTP fixture.
	var listener net.Listener
	for port := 20000; port <= 29999; port++ {
		listener, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				up, e := net.Dial("tcp", strings.TrimPrefix(backend.URL, "http://"))
				if e != nil {
					return
				}
				defer up.Close()
				go io.Copy(up, c)
				_, _ = io.Copy(c, up)
			}()
		}
	}()
	public, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := public.Addr().String()
	public.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dns := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected DNS write %s", r.URL)
			http.Error(w, "unexpected", 500)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/rrsets") {
			fmt.Fprint(w, `{"rrsets":[{"name":"myapp.devrp","type":"A","records":[{"value":"127.0.0.1"}]},{"name":"*.myapp.devrp","type":"A","records":[{"value":"127.0.0.1"}]}],"meta":{"pagination":{"page":1,"last_page":1}}}`)
		} else {
			fmt.Fprint(w, `{"zone":{"id":1,"name":"example.com","mode":"primary"}}`)
		}
	}))
	defer dns.Close()
	g, err := gateway.New(ctx, gateway.Config{DNS: &gateway.DNS{Client: hcloud.NewClient(hcloud.WithToken("fixture"), hcloud.WithEndpoint(dns.URL)), Zone: "example.com"}, Lease: 2 * time.Second, ConfigDir: dir, Domain: "devrp.example.com", PublicIP: "127.0.0.1", EntryPoint: "websecure", Resolver: "dns", Identify: func(context.Context, string, string) (tailnet.Identity, error) {
		return tailnet.Identity{ID: "node", IP: "127.0.0.1"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	session := strings.Repeat("e", 64)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("POST", "/register", strings.NewReader(fmt.Sprintf(`{"id":"myapp","port":%d,"session":"%s"}`, listener.Addr().(*net.TCPAddr).Port, session))))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	logFile, err := os.Create(filepath.Join(t.TempDir(), "traefik.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd := exec.Command(binary, "--entrypoints.websecure.address="+address, "--providers.file.directory="+dir, "--providers.providersThrottleDuration=10ms", "--log.level=ERROR")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			data, _ := os.ReadFile(logFile.Name())
			if len(data) > 8000 {
				data = data[len(data)-8000:]
			}
			t.Log(string(data))
		}
	}()
	tlsConfig := &tls.Config{RootCAs: roots, ServerName: "myapp.devrp.example.com", MinVersion: tls.VersionTLS12}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig}}
	request := func(path, host string) (*http.Response, error) {
		r, _ := http.NewRequest("GET", "https://"+address+path, nil)
		r.Host = host
		r.Header.Set("X-Forwarded-For", "198.51.100.44")
		r.Header.Set("X-Forwarded-Proto", "http")
		r.Header.Set("Forwarded", "for=198.51.100.44;proto=http")
		return client.Do(r)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		resp, err := request("/", "myapp.devrp.example.com")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Traefik did not become ready: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	resp, err := request("/", "myapp.devrp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	var headers map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&headers)
	resp.Body.Close()
	if headers["host"] != "myapp.devrp.example.com" || headers["scheme"] != "https" || headers["visitor"] != "127.0.0.1" || headers["forwarded"] != "" {
		t.Fatalf("untrusted metadata: %v", headers)
	}
	resp, err = request("/stream", "myapp.devrp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	close(release)
	resp.Body.Close()
	if err != nil || line != "first\n" {
		t.Fatalf("stream buffered: %q %v", line, err)
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", address, tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: myapp.devrp.example.com\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	reader := bufio.NewReader(conn)
	upgrade, err := http.ReadResponse(reader, nil)
	if err != nil || upgrade.StatusCode != 101 {
		t.Fatalf("websocket upgrade: %v %v", upgrade, err)
	}
	_, _ = conn.Write([]byte{0x81, 0x82, 1, 2, 3, 4, 'h' ^ 1, 'i' ^ 2})
	frame := make([]byte, 4)
	_, err = io.ReadFull(reader, frame)
	if err != nil || string(frame[2:]) != "hi" {
		t.Fatalf("websocket exchange: %v %v", frame, err)
	}
	resp, err = request("/", "missing.devrp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("unknown route: %d", resp.StatusCode)
	}
	w = httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("POST", "/unregister?id=myapp&session="+session, nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		resp, err = request("/", "myapp.devrp.example.com")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 404 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("route survived removal")
		}
		time.Sleep(50 * time.Millisecond)
	}
	wildcardSession := strings.Repeat("f", 64)
	w = httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("POST", "/register", strings.NewReader(fmt.Sprintf(`{"id":"myapp","port":%d,"wildcard":true,"session":"%s"}`, listener.Addr().(*net.TCPAddr).Port, wildcardSession))))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		resp, err = request("/", "tenant.myapp.devrp.example.com")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("wildcard route not installed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	resp, err = request("/", "nested.tenant.myapp.devrp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("wildcard matched more than one label: %d", resp.StatusCode)
	}
	childTLS := tlsConfig.Clone()
	childTLS.ServerName = "tenant.myapp.devrp.example.com"
	childConn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, childTLS)
	if err != nil {
		t.Fatal("child wildcard TLS:", err)
	}
	childConn.Close()

	deadline = time.Now().Add(5 * time.Second)
	for {
		resp, err = request("/", "tenant.myapp.devrp.example.com")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 404 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("expired wildcard route survived")
		}
		time.Sleep(50 * time.Millisecond)
	}

}
