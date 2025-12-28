// Package main is the entry point for the pulseboard CLI.
//
// PulseBoard can be run either as a library (SDK) or as a standalone binary
// with YAML configuration. This CLI provides the standalone binary approach.
//
// Usage:
//
//	pulseboard serve -c config.yaml    # Start the dashboard
//	pulseboard validate -c config.yaml # Validate configuration
//	pulseboard version                 # Show version info
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version information - set by GoReleaser at build time via ldflags.
// Example: go build -ldflags "-X main.version=1.0.0"
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// rootCmd is the base command when called without subcommands.
// It just displays help - actual functionality is in subcommands.
var rootCmd = &cobra.Command{
	Use:   "pulseboard",
	Short: "A lightweight health check dashboard",
	Long: `PulseBoard is a lightweight, real-time health check dashboard.

It polls HTTP endpoints at configurable intervals and displays their
status in a web UI with Server-Sent Events for live updates.

Quick start:
  1. Create a config file (pulseboard.yaml)
  2. Run: pulseboard serve -c pulseboard.yaml
  3. Open http://localhost:8080 in your browser

Example config:
  port: 8080
  poll_interval: 10s
  endpoints:
    - name: GitHub API
      url: https://api.github.com
      extractor: json:status`,
	// No Run/RunE means this just shows help when called without subcommands
}

// Execute runs the root command.
// This is the main entry point called from main().
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// Cobra already prints the error, just exit with code 1
		os.Exit(1)
	}
}

func main() {
	Execute()
}

// versionCmd prints version information.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Print the version, commit hash, and build date of this pulseboard binary.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("pulseboard %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
	},
}

func init() {
	// Register subcommands with root
	rootCmd.AddCommand(versionCmd)
}
