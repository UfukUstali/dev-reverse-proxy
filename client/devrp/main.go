package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/UfukUstali/dev-reverse-proxy/internal/cli"
)

func main() { os.Exit(run()) }
func run() int {
	var cfg cli.Config
	flag.StringVar(&cfg.Server, "server", os.Getenv("SERVER"), "Gateway URL on Tailscale, or SERVER")
	flag.StringVar(&cfg.Server, "s", os.Getenv("SERVER"), "Gateway URL")
	id := os.Getenv("ID")
	if id == "" {
		id = "myapp"
	}
	flag.StringVar(&cfg.ID, "id", id, "Publication name, one lowercase DNS label")
	flag.StringVar(&cfg.ID, "i", id, "Publication name")
	port := 0
	if value := os.Getenv("PORT"); value != "" {
		var err error
		port, err = strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			fmt.Fprintln(os.Stderr, "PORT must be 1-65535")
			return 1
		}
	}
	flag.IntVar(&cfg.Port, "port", port, "Application port, required without a command")
	flag.IntVar(&cfg.Port, "p", port, "Application port")
	wildcard := false
	switch os.Getenv("WILDCARD") {
	case "1", "true", "TRUE", "True", "yes", "on":
		wildcard = true
	}
	flag.BoolVar(&cfg.Wildcard, "wildcard", wildcard, "Also publish one subdomain label beneath the name")
	flag.BoolVar(&cfg.Wildcard, "w", wildcard, "Wildcard publication")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := cli.Run(ctx, cfg, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exit.ExitCode()
		}
		return 1
	}
	return 0
}
