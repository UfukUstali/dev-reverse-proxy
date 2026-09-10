package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/UfukUstali/dev-reverse-proxy/internal/publication"
	"github.com/UfukUstali/dev-reverse-proxy/internal/readiness"
)

func reportReadiness(ctx context.Context, cfg Config, response publication.Response) {
	target := readiness.Target{Application: net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port)), URL: response.URL, PublicIP: response.PublicIP, Wildcard: cfg.Wildcard}
	(readiness.Checker{}).Wait(ctx, target, 2*time.Second, 60*time.Second, func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "Still pending after 60 seconds:", err)
		} else {
			fmt.Fprintln(os.Stdout, "Ready:", response.URL)
		}
	})
}

func startReadiness(ctx context.Context, cfg Config, response publication.Response) context.CancelFunc {
	readyCtx, cancel := context.WithCancel(ctx)
	go reportReadiness(readyCtx, cfg, response)
	return cancel
}
