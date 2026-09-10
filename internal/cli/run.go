// Package cli supervises the application's foreground publication workflow.
package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/UfukUstali/dev-reverse-proxy/internal/publication"
	"github.com/UfukUstali/dev-reverse-proxy/internal/tailnet"
)

type Config struct {
	Server, ID string
	Port       int
	Wildcard   bool
}

func Run(ctx context.Context, cfg Config, args []string) error {
	if cfg.Server == "" {
		return errors.New("set --server or SERVER to the Tailscale gateway URL")
	}
	if !publication.ValidName(cfg.ID) {
		return errors.New("id must be one lowercase DNS label, at most 63 characters")
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return errors.New("application port must be 1-65535")
	}
	if cfg.Port == 0 {
		if len(args) == 0 {
			return errors.New("existing-port mode requires --port or PORT")
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		cfg.Port = ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
	}
	api, err := newControl(cfg.Server)
	if err != nil {
		return err
	}
	forwarder, err := tailnet.StartForwarder(ctx, cfg.Port)
	if err != nil {
		return err
	}
	defer func() { forwarder.Stop() }()
	session := sessionID()
	register := func() (publication.Response, error) {
		return api.call(ctx, "/register", publication.RegisterRequest{ID: cfg.ID, Port: forwarder.Port, Wildcard: cfg.Wildcard, Session: session})
	}
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := api.call(cleanupCtx, publicationPath("unregister", cfg.ID, session), nil); err != nil {
			fmt.Fprintln(os.Stderr, "Publication removal pending lease expiry:", err)
		}
	}
	defer cleanup()
	assigned, err := register()
	if err != nil {
		return err
	}
	if err = validateAssignment(cfg.ID, assigned); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "URL assigned:", assigned.URL)
	var child *exec.Cmd
	var childDone <-chan error
	if len(args) > 0 {
		child = exec.Command(args[0], args[1:]...)
		child.Stdin = os.Stdin
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		child.Env = append(os.Environ(), "PORT="+strconv.Itoa(cfg.Port), "DEVRP_ID="+cfg.ID, "DEVRP_BASE_URL="+assigned.URL)
		if err = child.Start(); err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- child.Wait() }()
		childDone = done
	}
	stopReady := startReadiness(ctx, cfg, assigned)
	defer func() { stopReady() }()
	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()
	active := true
	readinessRunning := true
	var sharingError error
	for {
		var forwardDone <-chan struct{}
		if active && forwarder != nil {
			forwardDone = forwarder.Done
		}
		select {
		case <-ctx.Done():
			if child != nil {
				stopChild(child, childDone)
			}
			return sharingError
		case err := <-childDone:
			if err != nil {
				return err
			}
			return sharingError
		case <-forwardDone:
			fmt.Fprintln(os.Stderr, "Tailscale forwarder stopped; retrying sharing.")
			stopReady()
			readinessRunning = false
			cleanup()
			forwarder = nil
		case <-heartbeat.C:
			if !active {
				continue
			}
			if forwarder != nil {
				_, err = api.call(ctx, publicationPath("heartbeat", cfg.ID, session), nil)
				if err == nil {
					if !readinessRunning {
						stopReady = startReadiness(ctx, cfg, assigned)
						readinessRunning = true
					}
					continue
				}
			}
			if ctx.Err() != nil {
				continue
			}
			stopReady()
			readinessRunning = false
			if forwarder == nil {
				forwarder, err = tailnet.StartForwarder(ctx, cfg.Port)
				if err != nil {
					fmt.Fprintln(os.Stderr, "Sharing pending:", err)
					continue
				}
				session = sessionID()
			}
			assigned, err = register()
			var failure *apiError
			if errors.As(err, &failure) && failure.code == 410 {
				session = sessionID()
				assigned, err = register()
			}
			if err != nil {
				if errors.As(err, &failure) && (failure.code == 409 || failure.code == 403 || failure.code == 400 || failure.code == 422) {
					sharingError = fmt.Errorf("sharing stopped: %w", err)
					fmt.Fprintln(os.Stderr, sharingError)
					forwarder.Stop()
					forwarder = nil
					active = false
					if child == nil {
						return sharingError
					}
					fmt.Fprintln(os.Stderr, "Application remains running; Ctrl-C stops it.")
				} else {
					fmt.Fprintln(os.Stderr, "Sharing pending; application remains running:", err)
				}
				continue
			}
			if err = validateAssignment(cfg.ID, assigned); err != nil {
				fmt.Fprintln(os.Stderr, "Sharing pending:", err)
				continue
			}
			fmt.Fprintln(os.Stdout, "Sharing reconnected:", assigned.URL)
			stopReady = startReadiness(ctx, cfg, assigned)
			readinessRunning = true
		}
	}
}

func stopChild(child *exec.Cmd, done <-chan error) {
	_ = syscall.Kill(-child.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
		<-done
	}
}
