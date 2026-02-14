package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/config"
)

func runStatusCommand(ctx context.Context, args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: goclaw status")
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load: %v\n", err)
		return 1
	}

	addr := strings.TrimSpace(cfg.BindAddr)
	if addr == "" {
		addr = "127.0.0.1:18789"
	}

	healthURL := ""
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		healthURL = strings.TrimRight(addr, "/") + "/healthz"
	} else {
		// Normalize IPv6 host:port if needed.
		if host, port, err := net.SplitHostPort(addr); err == nil {
			addr = net.JoinHostPort(host, port)
		}
		healthURL = "http://" + addr + "/healthz"
	}

	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "request: %v\n", err)
		return 1
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	_, _ = os.Stdout.Write(body)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		_, _ = os.Stdout.Write([]byte("\n"))
	}
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
