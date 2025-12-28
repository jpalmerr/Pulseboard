package config

import (
	"strings"
	"testing"
	"time"
)

func TestParse_MinimalConfig(t *testing.T) {
	yaml := `
endpoints:
  - name: Test
    url: https://example.com
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	// check defaults applied
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.PollInterval.Duration() != 15*time.Second {
		t.Errorf("PollInterval = %v, want 15s", cfg.PollInterval.Duration())
	}
	if len(cfg.Endpoints) != 1 {
		t.Errorf("len(Endpoints) = %d, want 1", len(cfg.Endpoints))
	}
}

func TestParse_FullEndpointConfig(t *testing.T) {
	yaml := `
port: 9090
poll_interval: 30s

endpoints:
  - name: Full Test
    url: https://api.example.com/health
    method: POST
    timeout: 5s
    headers:
      Authorization: Bearer token123
      X-Custom: value
    labels:
      env: prod
      team: platform
    extractor: json:data.status
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.PollInterval.Duration() != 30*time.Second {
		t.Errorf("PollInterval = %v, want 30s", cfg.PollInterval.Duration())
	}

	ep := cfg.Endpoints[0]
	if ep.Name != "Full Test" {
		t.Errorf("Name = %q, want %q", ep.Name, "Full Test")
	}
	if ep.URL != "https://api.example.com/health" {
		t.Errorf("URL = %q, want %q", ep.URL, "https://api.example.com/health")
	}
	if ep.Method != "POST" {
		t.Errorf("Method = %q, want %q", ep.Method, "POST")
	}
	if ep.Timeout.Duration() != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", ep.Timeout.Duration())
	}
	if ep.Headers["Authorization"] != "Bearer token123" {
		t.Errorf("Headers[Authorization] = %q, want %q", ep.Headers["Authorization"], "Bearer token123")
	}
	if ep.Labels["env"] != "prod" {
		t.Errorf("Labels[env] = %q, want %q", ep.Labels["env"], "prod")
	}
	if ep.Extractor.Type != "json" {
		t.Errorf("Extractor.Type = %q, want %q", ep.Extractor.Type, "json")
	}
	if ep.Extractor.Path != "data.status" {
		t.Errorf("Extractor.Path = %q, want %q", ep.Extractor.Path, "data.status")
	}
}

func TestParse_GridConfig(t *testing.T) {
	yaml := `
grids:
  - name: Platform
    url_template: "https://{{.env}}.example.com/{{.svc}}/health"
    dimensions:
      env: [prod, staging]
      svc: [api, web]
    method: HEAD
    timeout: 3s
    headers:
      X-Source: pulseboard
    labels:
      tier: critical
    extractor:
      type: json
      path: health.status
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(cfg.Grids) != 1 {
		t.Fatalf("len(Grids) = %d, want 1", len(cfg.Grids))
	}

	g := cfg.Grids[0]
	if g.Name != "Platform" {
		t.Errorf("Name = %q, want %q", g.Name, "Platform")
	}
	if g.URLTemplate != "https://{{.env}}.example.com/{{.svc}}/health" {
		t.Errorf("URLTemplate = %q", g.URLTemplate)
	}
	if len(g.Dimensions) != 2 {
		t.Errorf("len(Dimensions) = %d, want 2", len(g.Dimensions))
	}
	if len(g.Dimensions["env"]) != 2 {
		t.Errorf("len(Dimensions[env]) = %d, want 2", len(g.Dimensions["env"]))
	}
	if g.Method != "HEAD" {
		t.Errorf("Method = %q, want HEAD", g.Method)
	}
	if g.Timeout.Duration() != 3*time.Second {
		t.Errorf("Timeout = %v, want 3s", g.Timeout.Duration())
	}
	if g.Extractor.Type != "json" {
		t.Errorf("Extractor.Type = %q, want json", g.Extractor.Type)
	}
	if g.Extractor.Path != "health.status" {
		t.Errorf("Extractor.Path = %q, want health.status", g.Extractor.Path)
	}
}

func TestParse_ExtractorShorthand(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		wantType string
		wantPath string
		wantText string
	}{
		{
			name:     "json with path",
			yaml:     `extractor: json:status`,
			wantType: "json",
			wantPath: "status",
		},
		{
			name:     "json with nested path",
			yaml:     `extractor: json:data.health.status`,
			wantType: "json",
			wantPath: "data.health.status",
		},
		{
			name:     "contains",
			yaml:     `extractor: contains:ok`,
			wantType: "contains",
			wantText: "ok",
		},
		{
			name:     "contains with spaces",
			yaml:     `extractor: "contains:service is healthy"`,
			wantType: "contains",
			wantText: "service is healthy",
		},
		{
			name:     "default",
			yaml:     `extractor: default`,
			wantType: "default",
		},
		{
			name:     "http",
			yaml:     `extractor: http`,
			wantType: "http",
		},
		{
			name:     "empty (uses default)",
			yaml:     ``,
			wantType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fullYaml := `
endpoints:
  - name: Test
    url: https://example.com
    ` + tt.yaml

			cfg, err := Parse([]byte(fullYaml))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			e := cfg.Endpoints[0].Extractor
			if e.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", e.Type, tt.wantType)
			}
			if e.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", e.Path, tt.wantPath)
			}
			if e.Text != tt.wantText {
				t.Errorf("Text = %q, want %q", e.Text, tt.wantText)
			}
		})
	}
}

func TestParse_ExtractorStructured(t *testing.T) {
	yaml := `
endpoints:
  - name: Test
    url: https://example.com
    extractor:
      type: json
      path: data.health.status
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	e := cfg.Endpoints[0].Extractor
	if e.Type != "json" {
		t.Errorf("Type = %q, want json", e.Type)
	}
	if e.Path != "data.health.status" {
		t.Errorf("Path = %q, want data.health.status", e.Path)
	}
}

func TestParse_EnvVarSubstitution(t *testing.T) {
	// t.Setenv auto-restores after test (Go 1.17+)
	t.Setenv("TEST_API_HOST", "api.test.com")
	t.Setenv("TEST_API_TOKEN", "secret123")

	yaml := `
endpoints:
  - name: Test
    url: https://${TEST_API_HOST}/health
    headers:
      Authorization: "Bearer ${TEST_API_TOKEN}"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	ep := cfg.Endpoints[0]
	if ep.URL != "https://api.test.com/health" {
		t.Errorf("URL = %q, want https://api.test.com/health", ep.URL)
	}
	if ep.Headers["Authorization"] != "Bearer secret123" {
		t.Errorf("Headers[Authorization] = %q, want 'Bearer secret123'", ep.Headers["Authorization"])
	}
}

func TestParse_EnvVarDefault(t *testing.T) {
	// t.Setenv with empty then unset approach isn't needed
	// just ensure the var doesn't exist in the environment

	yaml := `
endpoints:
  - name: Test
    url: https://${UNSET_VAR:-fallback.example.com}/health
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.Endpoints[0].URL != "https://fallback.example.com/health" {
		t.Errorf("URL = %q, want https://fallback.example.com/health", cfg.Endpoints[0].URL)
	}
}

func TestParse_EnvVarMissing(t *testing.T) {
	// MISSING_VAR is expected to not exist in the environment
	yaml := `
endpoints:
  - name: Test
    url: https://${MISSING_VAR}/health
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("Parse() expected error for missing env var, got nil")
	}
	if !strings.Contains(err.Error(), "MISSING_VAR") {
		t.Errorf("error should mention MISSING_VAR: %v", err)
	}
}

func TestParse_EnvVarInGridTemplate(t *testing.T) {
	t.Setenv("TEST_DOMAIN", "example.com")

	yaml := `
grids:
  - name: Test
    url_template: "https://{{.env}}.${TEST_DOMAIN}/health"
    dimensions:
      env: [prod]
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.Grids[0].URLTemplate != "https://{{.env}}.example.com/health" {
		t.Errorf("URLTemplate = %q", cfg.Grids[0].URLTemplate)
	}
}

func TestParse_GridTemplateValidation(t *testing.T) {
	tests := []struct {
		name        string
		template    string
		wantErr     bool
		wantErrLike string
	}{
		{
			name:     "valid template",
			template: "https://{{.env}}.example.com/{{.svc}}/health",
			wantErr:  false,
		},
		{
			name:     "valid template with conditionals",
			template: "https://{{if .secure}}secure{{else}}api{{end}}.example.com",
			wantErr:  false,
		},
		{
			name:        "unclosed braces",
			template:    "https://{{.env}.example.com",
			wantErr:     true,
			wantErrLike: "invalid url_template",
		},
		{
			name:        "invalid action",
			template:    "https://{{.env | badfunction}}.example.com",
			wantErr:     true,
			wantErrLike: "invalid url_template",
		},
		{
			name:        "unclosed action",
			template:    "https://{{.env",
			wantErr:     true,
			wantErrLike: "invalid url_template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := `
grids:
  - name: Test
    url_template: "` + tt.template + `"
    dimensions:
      env: [prod]
`
			_, err := Parse([]byte(yaml))

			if tt.wantErr {
				if err == nil {
					t.Fatal("Parse() expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErrLike) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErrLike)
				}
			} else {
				if err != nil {
					t.Fatalf("Parse() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestParse_ValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		wantErrLike string
	}{
		{
			name:        "no endpoints or grids",
			yaml:        `port: 8080`,
			wantErrLike: "at least one endpoint or grid",
		},
		{
			name: "endpoint missing name",
			yaml: `
endpoints:
  - url: https://example.com
`,
			wantErrLike: "name is required",
		},
		{
			name: "endpoint missing url",
			yaml: `
endpoints:
  - name: Test
`,
			wantErrLike: "url is required",
		},
		{
			name: "endpoint invalid method",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    method: DELETE
`,
			wantErrLike: "method must be GET, HEAD, or POST",
		},
		{
			name: "grid missing name",
			yaml: `
grids:
  - url_template: https://example.com
    dimensions:
      env: [prod]
`,
			wantErrLike: "name is required",
		},
		{
			name: "grid missing url_template",
			yaml: `
grids:
  - name: Test
    dimensions:
      env: [prod]
`,
			wantErrLike: "url_template is required",
		},
		{
			name: "grid missing dimensions",
			yaml: `
grids:
  - name: Test
    url_template: https://example.com
`,
			wantErrLike: "at least one dimension is required",
		},
		{
			name: "grid empty dimension values",
			yaml: `
grids:
  - name: Test
    url_template: https://example.com
    dimensions:
      env: []
`,
			wantErrLike: "has no values",
		},
		{
			name: "invalid extractor type",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    extractor: unknown:value
`,
			wantErrLike: "unknown extractor type",
		},
		{
			name: "json extractor without path",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    extractor:
      type: json
`,
			wantErrLike: "requires a path",
		},
		{
			name: "contains extractor without text",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    extractor:
      type: contains
`,
			wantErrLike: "requires text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatal("Parse() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrLike) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErrLike)
			}
		})
	}
}

func TestParse_GridDimensionDuplicateValues(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		wantErrLike string
	}{
		{
			name: "duplicate at end",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [prod, staging, prod]
`,
			wantErrLike: `dimension "env" has duplicate value "prod"`,
		},
		{
			name: "duplicate at start",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [staging, staging, prod]
`,
			wantErrLike: `dimension "env" has duplicate value "staging"`,
		},
		{
			name: "duplicate in second dimension",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.{{.region}}.example.com
    dimensions:
      env: [prod, staging]
      region: [us, eu, us]
`,
			wantErrLike: `dimension "region" has duplicate value "us"`,
		},
		{
			name: "case sensitive - different case is valid",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [prod, Prod, PROD]
`,
			wantErrLike: "", // should pass - case sensitive
		},
		{
			name: "unique values is valid",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.{{.region}}.example.com
    dimensions:
      env: [prod, staging]
      region: [us, eu]
`,
			wantErrLike: "", // should pass
		},
		{
			name: "single value dimension is valid",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [prod]
`,
			wantErrLike: "", // should pass
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))

			if tt.wantErrLike == "" {
				if err != nil {
					t.Fatalf("Parse() unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("Parse() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrLike) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErrLike)
			}
		})
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	yaml := `
this is not: valid: yaml: at all
  - broken
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("Parse() expected error for invalid YAML, got nil")
	}
}

func TestParse_InvalidDuration(t *testing.T) {
	yaml := `
poll_interval: not-a-duration
endpoints:
  - name: Test
    url: https://example.com
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("Parse() expected error for invalid duration, got nil")
	}
	if !strings.Contains(err.Error(), "invalid duration") {
		t.Errorf("error = %q, want to contain 'invalid duration'", err.Error())
	}
}

func TestDuration_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"seconds", "10s", 10 * time.Second, false},
		{"milliseconds", "1500ms", 1500 * time.Millisecond, false},
		{"minutes", "2m", 2 * time.Minute, false},
		{"hours", "1h", 1 * time.Hour, false},
		{"combined", "1m30s", 90 * time.Second, false},
		{"invalid", "not-a-duration", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// use endpoint timeout to test Duration parsing (values must be >= 1s due to timeout validation)
			yaml := `
endpoints:
  - name: Test
    url: https://example.com
    timeout: ` + tt.input

			cfg, err := Parse([]byte(yaml))
			if tt.wantErr {
				if err == nil {
					t.Fatal("Parse() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if cfg.Endpoints[0].Timeout.Duration() != tt.want {
				t.Errorf("Timeout = %v, want %v", cfg.Endpoints[0].Timeout.Duration(), tt.want)
			}
		})
	}
}

func TestParse_MixedEndpointsAndGrids(t *testing.T) {
	yaml := `
endpoints:
  - name: Direct
    url: https://direct.example.com

grids:
  - name: Platform
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [prod, staging]
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(cfg.Endpoints) != 1 {
		t.Errorf("len(Endpoints) = %d, want 1", len(cfg.Endpoints))
	}
	if len(cfg.Grids) != 1 {
		t.Errorf("len(Grids) = %d, want 1", len(cfg.Grids))
	}
}

func TestParse_URLValidation(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{
			name:    "no scheme",
			url:     "example.com/health",
			wantErr: "url must have a scheme (http:// or https://)",
		},
		{
			name:    "invalid scheme ftp",
			url:     "ftp://example.com/health",
			wantErr: "url scheme must be http or https",
		},
		{
			name:    "invalid scheme file",
			url:     "file:///etc/passwd",
			wantErr: "url scheme must be http or https",
		},
		{
			name:    "valid http",
			url:     "http://example.com/health",
			wantErr: "",
		},
		{
			name:    "valid https",
			url:     "https://example.com/health",
			wantErr: "",
		},
		{
			name:    "valid with port",
			url:     "https://example.com:8080/health",
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := `
endpoints:
  - name: Test
    url: ` + tt.url

			_, err := Parse([]byte(yaml))

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Parse() unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("Parse() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestParse_PollIntervalMinimum(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "negative duration",
			yaml: `
poll_interval: -5s
endpoints:
  - name: Test
    url: https://example.com
`,
			wantErr: "poll_interval must be at least 1s",
		},
		{
			name: "too short 100ms",
			yaml: `
poll_interval: 100ms
endpoints:
  - name: Test
    url: https://example.com
`,
			wantErr: "poll_interval must be at least 1s",
		},
		{
			name: "too short 999ms",
			yaml: `
poll_interval: 999ms
endpoints:
  - name: Test
    url: https://example.com
`,
			wantErr: "poll_interval must be at least 1s",
		},
		{
			name: "minimum 1s",
			yaml: `
poll_interval: 1s
endpoints:
  - name: Test
    url: https://example.com
`,
			wantErr: "",
		},
		{
			name: "typical 10s",
			yaml: `
poll_interval: 10s
endpoints:
  - name: Test
    url: https://example.com
`,
			wantErr: "",
		},
		{
			name: "zero gets default",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
`,
			wantErr: "", // 0 becomes 10s via default
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Parse() unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("Parse() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestParse_TimeoutValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "not specified uses default",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com`,
			wantErr: "",
		},
		{
			name: "zero treated as default",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    timeout: 0s`,
			wantErr: "",
		},
		{
			name: "negative rejected",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    timeout: -1s`,
			wantErr: "timeout cannot be negative",
		},
		{
			name: "sub-second rejected",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    timeout: 500ms`,
			wantErr: "timeout must be at least 1s",
		},
		{
			name: "minimum 1s accepted",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    timeout: 1s`,
			wantErr: "",
		},
		{
			name: "typical timeout accepted",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    timeout: 10s`,
			wantErr: "",
		},
		{
			name: "grid sub-second rejected",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [prod]
    timeout: 500ms`,
			wantErr: "timeout must be at least 1s",
		},
		{
			name: "grid negative rejected",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [prod]
    timeout: -5s`,
			wantErr: "timeout cannot be negative",
		},
		{
			name: "grid no timeout uses default",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [prod]`,
			wantErr: "",
		},
		{
			name: "error includes endpoint name",
			yaml: `
endpoints:
  - name: MyService
    url: https://example.com
    timeout: 100ms`,
			wantErr: "(MyService)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Parse() unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("Parse() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestParse_IntervalValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "endpoint no interval uses global",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com`,
			wantErr: "",
		},
		{
			name: "endpoint valid interval 5s",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    interval: 5s`,
			wantErr: "",
		},
		{
			name: "endpoint valid interval 1h",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    interval: 1h`,
			wantErr: "",
		},
		{
			name: "endpoint interval too short",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    interval: 500ms`,
			wantErr: "interval must be at least 1s",
		},
		{
			name: "endpoint interval too long",
			yaml: `
endpoints:
  - name: Test
    url: https://example.com
    interval: 2h`,
			wantErr: "interval must not exceed 1h",
		},
		{
			name: "endpoint interval error includes name",
			yaml: `
endpoints:
  - name: MyService
    url: https://example.com
    interval: 100ms`,
			wantErr: "(MyService)",
		},
		{
			name: "grid no interval uses global",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [prod]`,
			wantErr: "",
		},
		{
			name: "grid valid interval 30s",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [prod]
    interval: 30s`,
			wantErr: "",
		},
		{
			name: "grid interval too short",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [prod]
    interval: 999ms`,
			wantErr: "interval must be at least 1s",
		},
		{
			name: "grid interval too long",
			yaml: `
grids:
  - name: Platform
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [prod]
    interval: 90m`,
			wantErr: "interval must not exceed 1h",
		},
		{
			name: "grid interval error includes name",
			yaml: `
grids:
  - name: MyGrid
    url_template: https://{{.env}}.example.com
    dimensions:
      env: [prod]
    interval: 100ms`,
			wantErr: "(MyGrid)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Parse() unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("Parse() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestParse_EndpointWithInterval(t *testing.T) {
	yaml := `
endpoints:
  - name: Custom Interval
    url: https://example.com
    interval: 45s
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	ep := cfg.Endpoints[0]
	if ep.Interval.Duration() != 45*time.Second {
		t.Errorf("Interval = %v, want 45s", ep.Interval.Duration())
	}
}

func TestParse_GridWithInterval(t *testing.T) {
	yaml := `
grids:
  - name: Platform
    url_template: "https://{{.env}}.example.com"
    dimensions:
      env: [prod, staging]
    interval: 2m
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	g := cfg.Grids[0]
	if g.Interval.Duration() != 2*time.Minute {
		t.Errorf("Interval = %v, want 2m", g.Interval.Duration())
	}
}

func TestExpandEnvVars(t *testing.T) {
	t.Setenv("TEST_VAR", "value")
	t.Setenv("EMPTY_VAR", "") // set but empty

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"no vars", "plain text", "plain text", false},
		{"simple var", "${TEST_VAR}", "value", false},
		{"var in text", "prefix ${TEST_VAR} suffix", "prefix value suffix", false},
		{"multiple vars", "${TEST_VAR}-${TEST_VAR}", "value-value", false},
		{"with default (var set)", "${TEST_VAR:-default}", "value", false},
		{"with default (var unset)", "${UNSET:-default}", "default", false},
		{"missing required", "${MISSING}", "", true},
		// P0 fix: empty default should work
		{"empty default (var unset)", "${UNSET:-}", "", false},
		// set-but-empty env var should substitute empty string
		{"set but empty var", "${EMPTY_VAR}", "", false},
		{"set but empty with default", "${EMPTY_VAR:-fallback}", "", false}, // set var takes precedence
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// UNSET and MISSING are expected to not exist in environment
			got, err := expandEnvVars(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expandEnvVars() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("expandEnvVars() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("expandEnvVars() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParse_Title(t *testing.T) {
	yaml := `
title: Video Channel Healthchecks
endpoints:
  - name: Test
    url: https://example.com
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.Title != "Video Channel Healthchecks" {
		t.Errorf("Title = %q, want %q", cfg.Title, "Video Channel Healthchecks")
	}
}

func TestParse_TitleEmpty(t *testing.T) {
	yaml := `
endpoints:
  - name: Test
    url: https://example.com
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	// empty title is valid (defaults to "PulseBoard" at render time)
	if cfg.Title != "" {
		t.Errorf("Title = %q, want empty string", cfg.Title)
	}
}
