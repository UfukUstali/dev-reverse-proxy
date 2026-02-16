package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func main() {
	server := getenv("SERVER", "http://localhost:8080")
	id := getenv("ID", "myapp")

	portStr := os.Getenv("PORT")
	var port int
	var err error

	if portStr == "" {
		port, err = findFreePort(3000, 3100, 50)
		if err != nil {
			fmt.Println("Failed to find free port in range 3000–3100")
			os.Exit(1)
		}
	} else {
		port, err = strconv.Atoi(portStr)
		if err != nil {
			fmt.Println("Invalid PORT")
			os.Exit(1)
		}
	}

	os.Setenv("PORT", strconv.Itoa(port))

	if len(os.Args) < 2 {
		fmt.Println("No command provided")
		os.Exit(1)
	}

	if err := register(server, id, port); err != nil {
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go heartbeat(ctx, server, id)

	cmd := exec.Command(os.Args[1], os.Args[2:]...)
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

	err = cmd.Run()
	cancel()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}

func getenv(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}

func findFreePort(min, max, attempts int) (int, error) {
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
