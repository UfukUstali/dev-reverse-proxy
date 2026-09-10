package tailnet

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/UfukUstali/dev-reverse-proxy/internal/publication"
)

type serveConfig struct {
	TCP        map[string]struct{ TCPForward string }
	Foreground map[string]*serveConfig
}

func (c *serveConfig) uses(port string) bool {
	if c == nil {
		return false
	}
	if _, ok := c.TCP[port]; ok {
		return true
	}
	for _, f := range c.Foreground {
		if f.uses(port) {
			return true
		}
	}
	return false
}
func serveStatus(ctx context.Context) (*serveConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "tailscale", "serve", "status", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("tailscale serve status: %w", err)
	}
	var cfg *serveConfig
	if err = json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("tailscale serve status JSON: %w", err)
	}
	if cfg == nil {
		cfg = &serveConfig{}
	}
	return cfg, nil
}

// Forwarder owns exactly one foreground Serve subprocess. Stopping it closes
// that daemon session, leaving persistent and other foreground listeners intact.
type Forwarder struct {
	Port int
	cmd  *exec.Cmd
	Done chan struct{}
	Err  error
}

func StartForwarder(ctx context.Context, appPort int) (*Forwarder, error) {
	if _, err := exec.LookPath("tailscale"); err != nil {
		return nil, errors.New("tailscale CLI is required on PATH")
	}
	var last error
	for attempt := 0; attempt < 8; attempt++ {
		cfg, err := serveStatus(ctx)
		if err != nil {
			return nil, err
		}
		n, err := rand.Int(rand.Reader, big.NewInt(publication.ForwardMax-publication.ForwardMin+1))
		if err != nil {
			return nil, err
		}
		port := publication.ForwardMin + int(n.Int64())
		key := strconv.Itoa(port)
		if port == appPort || cfg.uses(key) {
			continue
		}
		ln, err := net.Listen("tcp", ":"+key)
		if err != nil {
			continue
		}
		_ = ln.Close()
		target := "127.0.0.1:" + strconv.Itoa(appPort)
		cmd := exec.Command("tailscale", "serve", "--tcp="+key, "tcp://"+target)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err = cmd.Start(); err != nil {
			return nil, err
		}
		f := &Forwarder{Port: port, cmd: cmd, Done: make(chan struct{})}
		go func() { f.Err = cmd.Wait(); close(f.Done) }()
		deadline := time.NewTimer(8 * time.Second)
		ticker := time.NewTicker(100 * time.Millisecond)
		started := false
	wait:
		for {
			select {
			case <-ctx.Done():
				last = ctx.Err()
				break wait
			case <-f.Done:
				last = fmt.Errorf("foreground Tailscale Serve exited: %v", f.Err)
				break wait
			case <-deadline.C:
				last = errors.New("foreground Tailscale Serve did not configure its listener")
				break wait
			case <-ticker.C:
				current, err := serveStatus(ctx)
				if err != nil {
					last = err
					continue
				}
				for session, sc := range current.Foreground {
					if _, existed := cfg.Foreground[session]; existed {
						continue
					}
					if handler, ok := sc.TCP[key]; ok && handler.TCPForward == target {
						started = true
						break wait
					}
				}
			}
		}
		ticker.Stop()
		deadline.Stop()
		if started {
			return f, nil
		}
		f.Stop()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// The daemon rejects concurrent configuration writes via its ETag. Retry
		// with a fresh snapshot and port rather than replacing any listener.
	}
	if last == nil {
		last = errors.New("no available forwarding port")
	}
	return nil, last
}

func (f *Forwarder) Stop() {
	if f == nil {
		return
	}
	select {
	case <-f.Done:
		return
	default:
	}
	_ = f.cmd.Process.Signal(os.Interrupt)
	select {
	case <-f.Done:
	case <-time.After(3 * time.Second):
		_ = f.cmd.Process.Kill()
		<-f.Done
	}
}
