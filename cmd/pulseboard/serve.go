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
	"github.com/jpalmerr/pulseboard/config"
	"github.com/spf13/cobra"
)

const (
	shutdownTimeout = 10 * time.Second
)

// newLogger creates a JSON logger for CLI use.
func newLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// serveCmd starts the PulseBoard dashboard server.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the dashboard server",
	Long: `Start the PulseBoard dashboard server.

The server will:
  - Load configuration from the specified YAML file
  - Start polling all configured endpoints
  - Serve the dashboard UI on the configured port

The server runs until interrupted (Ctrl+C) or receives SIGTERM.

Example:
  pulseboard serve -c config.yaml
  pulseboard serve --config /etc/pulseboard/config.yaml`,
	RunE: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)

	serveCmd.Flags().StringP("config", "c", "", "path to config file (required)")
	_ = serveCmd.MarkFlagRequired("config")
}

func runServe(cmd *cobra.Command, args []string) error {
	logger := newLogger()

	configFile, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	logger.Info("config loaded",
		"endpoints", len(cfg.Endpoints),
		"grids", len(cfg.Grids),
	)
	logger.Info("starting server",
		"port", cfg.Port,
		"poll_interval", cfg.PollInterval.Duration().String(),
	)

	// convert config to SDK endpoints
	endpoints, err := config.BuildEndpoints(cfg)
	if err != nil {
		return fmt.Errorf("failed to build endpoints: %w", err)
	}

	if len(endpoints) == 0 {
		return fmt.Errorf("no endpoints configured")
	}

	// create PulseBoard with options
	opts := []pulseboard.Option{
		pulseboard.WithEndpoints(endpoints...),
		pulseboard.WithPort(cfg.Port),
		pulseboard.WithPollingInterval(cfg.PollInterval.Duration()),
		pulseboard.WithLogger(logger),
	}
	if cfg.Title != "" {
		opts = append(opts, pulseboard.WithTitle(cfg.Title))
	}

	pb, err := pulseboard.New(opts...)
	if err != nil {
		return fmt.Errorf("failed to create PulseBoard: %w", err)
	}

	// set up context with signal handling - cancel on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// start server - blocks until context cancelled
	errChan := make(chan error, 1)
	go func() {
		errChan <- pb.Start(ctx)
	}()

	// wait for server to finish
	select {
	case err := <-errChan:
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
		logger.Info("shutdown complete")
		return nil

	case <-ctx.Done():
		// signal received, wait for graceful shutdown with timeout
		select {
		case err := <-errChan:
			if err != nil {
				return fmt.Errorf("server error: %w", err)
			}
			logger.Info("shutdown complete")
			return nil
		case <-time.After(shutdownTimeout):
			logger.Warn("shutdown timed out",
				"timeout", shutdownTimeout.String(),
				"action", "forcing exit",
			)
			return nil
		}
	}
}
