package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// executeServeCmd runs the serve command with the given config path and returns any error.
func executeServeCmd(t *testing.T, configPath string) error {
	t.Helper()
	rootCmd.SetArgs([]string{"serve", "-c", configPath})
	return rootCmd.Execute()
}

func TestNewLogger(t *testing.T) {
	logger := newLogger()
	if logger == nil {
		t.Fatal("newLogger() returned nil")
	}
	// Verify it is a slog.Logger (not just a non-nil pointer to a zero value).
	var _ *slog.Logger = logger
}

func TestRunServe_ConfigNotFound(t *testing.T) {
	err := executeServeCmd(t, "/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("serve expected error for missing config file, got nil")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("expected 'failed to load config' in error, got: %v", err)
	}
}

func TestRunServe_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "bad.yaml")
	if err := os.WriteFile(configPath, []byte("not: {valid: [yaml"), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	err := executeServeCmd(t, configPath)
	if err == nil {
		t.Fatal("serve expected error for invalid YAML, got nil")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("expected 'failed to load config' in error, got: %v", err)
	}
}

func TestRunServe_InvalidEndpointName(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	content := `
port: 8080
poll_interval: 10s
endpoints:
  - name: ""
    url: https://example.com
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	err := executeServeCmd(t, configPath)
	if err == nil {
		t.Fatal("serve expected error for invalid endpoint name, got nil")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("expected 'failed to load config' in error, got: %v", err)
	}
}

func TestRunServe_InvalidPort(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	content := `
port: 99999
poll_interval: 10s
endpoints:
  - name: Test
    url: https://example.com
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	err := executeServeCmd(t, configPath)
	if err == nil {
		t.Fatal("serve expected error for out-of-range port, got nil")
	}
	// Port validation happens in pulseboard.New, after config is loaded.
	if !strings.Contains(err.Error(), "port must be between") {
		t.Errorf("expected port validation error, got: %v", err)
	}
}
