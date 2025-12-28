package main

import (
	"fmt"

	"github.com/jpalmerr/pulseboard/config"
	"github.com/spf13/cobra"
)

// validateCmd validates a config file without starting the server.
var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a config file",
	Long: `Validate a PulseBoard configuration file without starting the server.

This command parses the YAML, expands environment variables, and validates
all fields. It's useful for CI/CD pipelines or pre-deployment checks.

Exit codes:
  0 - Config is valid
  1 - Config is invalid (error details printed to stderr)

Example:
  pulseboard validate -c config.yaml
  pulseboard validate --config /etc/pulseboard/config.yaml`,
	RunE: runValidate,
}

func init() {
	rootCmd.AddCommand(validateCmd)

	validateCmd.Flags().StringP("config", "c", "", "path to config file (required)")
	_ = validateCmd.MarkFlagRequired("config")
}

func runValidate(cmd *cobra.Command, args []string) error {
	configFile, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(configFile)
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Count total endpoints (direct + from grids)
	directEndpoints := len(cfg.Endpoints)
	gridEndpoints := 0
	for _, g := range cfg.Grids {
		// Calculate cartesian product size
		size := 1
		for _, vals := range g.Dimensions {
			size *= len(vals)
		}
		gridEndpoints += size
	}

	fmt.Printf("Config is valid!\n")
	fmt.Printf("  Port:          %d\n", cfg.Port)
	fmt.Printf("  Poll interval: %s\n", cfg.PollInterval.Duration())
	fmt.Printf("  Endpoints:     %d direct + %d from grids = %d total\n",
		directEndpoints, gridEndpoints, directEndpoints+gridEndpoints)

	return nil
}
