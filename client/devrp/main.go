package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

type Config struct {
	Server string
	ID     string
	Port   int
}

func main() {
	cfg, userCmd := parseArgs()

	if cfg.Server == "" {
		cfg.Server = getenv("SERVER", "http://localhost:8080")
	}
	if cfg.ID == "" {
		cfg.ID = getenv("ID", "myapp")
	}

	if cfg.Port == 0 {
		port, err := findFreePort(3000, 3100, 50)
		if err != nil {
			fmt.Println("Failed to find free port in range 3000–3100")
			os.Exit(1)
		}
		cfg.Port = port
	}

	os.Setenv("PORT", strconv.Itoa(cfg.Port))

	if err := register(cfg.Server, cfg.ID, cfg.Port); err != nil {
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go heartbeat(ctx, cfg.Server, cfg.ID)

	cmd := exec.Command(userCmd[0], userCmd[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
	}()

	err := cmd.Run()
	cancel()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}

func parseArgs() (Config, []string) {
	var cfg Config

	flag.StringVar(&cfg.Server, "server", "", "Server URL (default: http://localhost:8080)")
	flag.StringVar(&cfg.Server, "s", "", "Server URL (shorthand)")
	flag.StringVar(&cfg.ID, "id", "", "Client identifier (subdomain)")
	flag.StringVar(&cfg.ID, "i", "", "Client identifier (shorthand)")
	flag.IntVar(&cfg.Port, "port", 0, "Port number (auto-selected if not set)")
	flag.IntVar(&cfg.Port, "p", 0, "Port number (shorthand)")

	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Println("Usage: client [options] -- <command> [args...]")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		fmt.Println("\nExamples:")
		fmt.Println("  client -s http://localhost:8080 -i myapp -- npm run dev")
		fmt.Println("  client --server http://localhost:8080 --id api -p 3035 -- node server.js")
		fmt.Println("  SERVER=http://localhost:8080 ID=api client -- node server.js")
		os.Exit(1)
	}

	delimIdx := -1
	for i, arg := range args {
		if arg == "--" {
			delimIdx = i
			break
		}
	}

	var userCmd []string
	if delimIdx >= 0 {
		userCmd = args[delimIdx+1:]
	} else {
		userCmd = args
	}

	if len(userCmd) == 0 {
		fmt.Println("No command provided after options")
		os.Exit(1)
	}

	return cfg, userCmd
}

func getenv(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}

func findFreePort(min, max, attempts int) (int, error) {
	v := os.Getenv("PORT")
	if v != "" {
		p, err := strconv.Atoi(v)
		if err == nil {
			return p, nil
		}
	}
	for range attempts {
		p := min + rand.Intn(max-min+1)
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
		if err == nil {
			_ = ln.Close()
			return p, nil
		}
	}
	return 0, errors.New("no free port found")
}

func register(server, id string, port int) error {
	payload := map[string]any{
		"id":   id,
		"port": port,
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(
		server+"/register",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("register failed: %s", resp.Status)
	}
	return nil
}

func heartbeat(ctx context.Context, server, id string) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	client := &http.Client{Timeout: 5 * time.Second}

	for {
		select {
		case <-ctx.Done():
			req, _ := http.NewRequest("POST", server+"/unregister?id="+id, nil)
			_, _ = client.Do(req)
			return
		case <-ticker.C:
			req, _ := http.NewRequest(
				"POST",
				server+"/heartbeat?id="+id,
				nil,
			)
			_, _ = client.Do(req)
		}
	}
}
