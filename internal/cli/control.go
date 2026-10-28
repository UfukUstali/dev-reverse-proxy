package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/UfukUstali/dev-reverse-proxy/internal/publication"
)

type control struct {
	server string
	client *http.Client
}
type apiError struct {
	code    int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("gateway returned %d: %s", e.code, e.message) }
func sessionID() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (c control) call(ctx context.Context, path string, body any) (publication.Response, error) {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", c.server+path, bytes.NewReader(data))
	if err != nil {
		return publication.Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return publication.Response{}, err
	}
	defer resp.Body.Close()
	var result publication.Response
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, fmt.Errorf("invalid gateway response: %s", resp.Status)
	}
	if resp.StatusCode != 200 {
		return result, &apiError{code: resp.StatusCode, message: result.Message}
	}
	return result, nil
}
func publicationPath(action, id, session string) string {
	return "/" + action + "?" + url.Values{"id": {id}, "session": {session}}.Encode()
}
func newControl(server string) (control, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return control{}, fmt.Errorf("--server or SERVER must be the gateway HTTP(S) origin on Tailscale")
	}
	// Control traffic must connect directly, never through a user's HTTP proxy.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return control{server: strings.TrimRight(server, "/"), client: &http.Client{Transport: transport, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func validateAssignment(id string, r publication.Response) error {
	if r.Domain == "" || r.URL != "https://"+id+"."+r.Domain {
		return fmt.Errorf("gateway returned an invalid publication URL")
	}
	if _, err := netip.ParseAddr(r.PublicIP); err != nil {
		return fmt.Errorf("gateway returned an invalid public IP")
	}
	return nil
}

// validAliases accepts only distinct single-label HTTPS URLs beneath the gateway
// domain, so gateway-supplied text never reaches the terminal unchecked.
func validAliases(r publication.Response, aliases []string) bool {
	seen := map[string]bool{}
	for _, alias := range aliases {
		host, secure := strings.CutPrefix(alias, "https://")
		label, inDomain := strings.CutSuffix(host, "."+r.Domain)
		if !secure || !inDomain || !publication.ValidName(label) || alias == r.URL || seen[alias] {
			return false
		}
		seen[alias] = true
	}
	return true
}
