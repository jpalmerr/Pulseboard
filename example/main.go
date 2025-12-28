package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jpalmerr/pulseboard"
)

func main() {
	// start mock server (see mock_server.go)
	go StartMockHealthServer(":9999")
	time.Sleep(100 * time.Millisecond)

	// grid API: 2 services × 2 envs = 4 endpoints from one declaration
	endpoints, err := pulseboard.NewEndpointGrid("API",
		pulseboard.WithURLTemplate("http://localhost:9999/health?svc={{.svc}}&env={{.env}}"),
		pulseboard.WithDimensions(map[string][]string{
			"svc": {"users", "orders"},
			"env": {"prod", "staging"},
		}),
		pulseboard.WithGridExtractor(pulseboard.JSONFieldExtractor("status")),
	)
	if err != nil {
		slog.Error("failed to create endpoint grid", "error", err)
		os.Exit(1)
	}

	// add an external endpoint with its own polling interval (overrides global 5s)
	github, _ := pulseboard.NewEndpoint("GitHub", "https://api.github.com",
		pulseboard.WithInterval(30*time.Second),
	)
	endpoints = append(endpoints, github)

	// start the dashboard
	pb, err := pulseboard.New(
		pulseboard.WithEndpoints(endpoints...),
		pulseboard.WithPollingInterval(5*time.Second),
		pulseboard.WithPort(8080),
	)
	if err != nil {
		slog.Error("failed to create pulseboard", "error", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════════════════════════╗")
	fmt.Println("  ║                                                       ║")
	fmt.Println("  ║   PulseBoard Demo                                     ║")
	fmt.Println("  ║                                                       ║")
	fmt.Println("  ║   Open http://localhost:8080 in your browser          ║")
	fmt.Println("  ║                                                       ║")
	fmt.Println("  ║   Endpoints:                                          ║")
	fmt.Println("  ║   • 4 mock (2 services × 2 envs via Grid)             ║")
	fmt.Println("  ║   • 1 external (GitHub, 30s interval)          ║")
	fmt.Println("  ║                                                       ║")
	fmt.Println("  ║   Press Ctrl+C to stop                                ║")
	fmt.Println("  ║                                                       ║")
	fmt.Println("  ╚═══════════════════════════════════════════════════════╝")
	fmt.Println()

	// set up context with signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := pb.Start(ctx); err != nil {
		slog.Error("pulseboard error", "error", err)
		os.Exit(1)
	}
}
