package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// executeValidateCmd runs the validate command with the given config path
// and returns captured stdout and any error.
func executeValidateCmd(t *testing.T, configPath string) (string, error) {
	t.Helper()

	// capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// execute via root command with validate subcommand
	rootCmd.SetArgs([]string{"validate", "-c", configPath})
	err := rootCmd.Execute()

	// restore stdout
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	return buf.String(), err
}

func TestRunValidate_ValidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
port: 8080
poll_interval: 10s
endpoints:
  - name: Test
    url: https://example.com
grids:
  - name: Platform
    url_template: "https://{{.env}}.example.com/health"
    dimensions:
      env: [prod, staging]
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	output, err := executeValidateCmd(t, configPath)
	if err != nil {
		t.Fatalf("validate command error = %v", err)
	}

	expectedPhrases := []string{
		"Config is valid!",
		"Port:          8080",
		"Poll interval: 10s",
		"1 direct + 2 from grids = 3 total",
	}

	for _, phrase := range expectedPhrases {
		if !strings.Contains(output, phrase) {
			t.Errorf("output missing %q\nGot: %s", phrase, output)
		}
	}
}

func TestRunValidate_InvalidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")

	configContent := `
port: 8080
endpoints:
  - name: ""
    url: https://example.com
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	_, err := executeValidateCmd(t, configPath)
	if err == nil {
		t.Fatal("validate command expected error for invalid config, got nil")
	}

	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("error should mention 'name is required', got: %v", err)
	}
}

func TestRunValidate_MissingFile(t *testing.T) {
	_, err := executeValidateCmd(t, "/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("validate command expected error for missing file, got nil")
	}

	if !strings.Contains(err.Error(), "failed to read") {
		t.Errorf("error should mention 'failed to read', got: %v", err)
	}
}
