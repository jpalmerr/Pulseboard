package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildEndpoints_SingleEndpoint(t *testing.T) {
	cfg := &Config{
		Endpoints: []EndpointConfig{
			{
				Name: "GitHub",
				URL:  "https://api.github.com",
			},
		},
	}

	endpoints, err := BuildEndpoints(cfg)
	if err != nil {
		t.Fatalf("BuildEndpoints() error = %v", err)
	}

	if len(endpoints) != 1 {
		t.Fatalf("len(endpoints) = %d, want 1", len(endpoints))
	}

	ep := endpoints[0]
	if ep.Name() != "GitHub" {
		t.Errorf("Name() = %q, want %q", ep.Name(), "GitHub")
	}
	if ep.URL() != "https://api.github.com" {
		t.Errorf("URL() = %q, want %q", ep.URL(), "https://api.github.com")
	}
}

func TestBuildEndpoints_EndpointWithAllOptions(t *testing.T) {
	cfg := &Config{
		Endpoints: []EndpointConfig{
			{
				Name:    "Full Test",
				URL:     "https://api.example.com/health",
				Method:  "POST",
				Timeout: Duration(5 * time.Second),
				Headers: map[string]string{
					"Authorization": "Bearer token",
					"X-Custom":      "value",
				},
				Labels: map[string]string{
					"env":  "prod",
					"team": "platform",
				},
				Extractor: ExtractorConfig{
					Type: "json",
					Path: "data.status",
				},
			},
		},
	}

	endpoints, err := BuildEndpoints(cfg)
	if err != nil {
		t.Fatalf("BuildEndpoints() error = %v", err)
	}

	ep := endpoints[0]

	if ep.Method() != "POST" {
		t.Errorf("Method() = %q, want %q", ep.Method(), "POST")
	}
	if ep.Timeout() != 5*time.Second {
		t.Errorf("Timeout() = %v, want %v", ep.Timeout(), 5*time.Second)
	}

	headers := ep.Headers()
	if headers["Authorization"] != "Bearer token" {
		t.Errorf("Headers()[Authorization] = %q, want %q", headers["Authorization"], "Bearer token")
	}

	labels := ep.Labels()
	if labels["env"] != "prod" {
		t.Errorf("Labels()[env] = %q, want %q", labels["env"], "prod")
	}

	if ep.Extractor() == nil {
		t.Error("Extractor() = nil, want non-nil")
	}
}

func TestBuildEndpoints_Grid(t *testing.T) {
	cfg := &Config{
		Grids: []GridConfig{
			{
				Name:        "Platform",
				URLTemplate: "https://{{.env}}.example.com/{{.svc}}/health",
				Dimensions: map[string][]string{
					"env": {"prod", "staging"},
					"svc": {"api", "web"},
				},
			},
		},
	}

	endpoints, err := BuildEndpoints(cfg)
	if err != nil {
		t.Fatalf("BuildEndpoints() error = %v", err)
	}

	// 2 envs * 2 svcs = 4 endpoints
	if len(endpoints) != 4 {
		t.Fatalf("len(endpoints) = %d, want 4", len(endpoints))
	}

	// verify all endpoints have labels from dimensions
	for _, ep := range endpoints {
		labels := ep.Labels()
		if labels["env"] == "" {
			t.Errorf("endpoint %q missing 'env' label", ep.Name())
		}
		if labels["svc"] == "" {
			t.Errorf("endpoint %q missing 'svc' label", ep.Name())
		}
	}
}

func TestBuildEndpoints_MixedEndpointsAndGrids(t *testing.T) {
	cfg := &Config{
		Endpoints: []EndpointConfig{
			{Name: "Direct", URL: "https://direct.example.com"},
		},
		Grids: []GridConfig{
			{
				Name:        "Platform",
				URLTemplate: "https://{{.env}}.example.com",
				Dimensions: map[string][]string{
					"env": {"prod", "staging"},
				},
			},
		},
	}

	endpoints, err := BuildEndpoints(cfg)
	if err != nil {
		t.Fatalf("BuildEndpoints() error = %v", err)
	}

	// 1 direct + 2 from grid = 3
	if len(endpoints) != 3 {
		t.Fatalf("len(endpoints) = %d, want 3", len(endpoints))
	}
}

func TestBuildEndpoints_ExtractorTypes(t *testing.T) {
	tests := []struct {
		name      string
		extractor ExtractorConfig
		wantNil   bool // nil means SDK uses DefaultExtractor
	}{
		{
			name:      "empty (default)",
			extractor: ExtractorConfig{},
			wantNil:   true,
		},
		{
			name:      "explicit default",
			extractor: ExtractorConfig{Type: "default"},
			wantNil:   true,
		},
		{
			name:      "http",
			extractor: ExtractorConfig{Type: "http"},
			wantNil:   false,
		},
		{
			name:      "json",
			extractor: ExtractorConfig{Type: "json", Path: "status"},
			wantNil:   false,
		},
		{
			name:      "contains",
			extractor: ExtractorConfig{Type: "contains", Text: "ok"},
			wantNil:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Endpoints: []EndpointConfig{
					{
						Name:      "Test",
						URL:       "https://example.com",
						Extractor: tt.extractor,
					},
				},
			}

			endpoints, err := BuildEndpoints(cfg)
			if err != nil {
				t.Fatalf("BuildEndpoints() error = %v", err)
			}

			ep := endpoints[0]
			if tt.wantNil && ep.Extractor() != nil {
				t.Errorf("Extractor() = non-nil, want nil")
			}
			if !tt.wantNil && ep.Extractor() == nil {
				t.Errorf("Extractor() = nil, want non-nil")
			}
		})
	}
}

func TestBuildEndpoints_ExtractorBehavior(t *testing.T) {
	// Test that extractors actually work correctly
	tests := []struct {
		name       string
		extractor  ExtractorConfig
		body       string
		statusCode int
		wantStatus string // "up", "down", "degraded", "unknown"
	}{
		{
			name:       "json extractor finds ok",
			extractor:  ExtractorConfig{Type: "json", Path: "status"},
			body:       `{"status": "ok"}`,
			statusCode: 200,
			wantStatus: "up",
		},
		{
			name:       "json extractor finds down",
			extractor:  ExtractorConfig{Type: "json", Path: "status"},
			body:       `{"status": "down"}`,
			statusCode: 200,
			wantStatus: "down",
		},
		{
			name:       "contains extractor matches",
			extractor:  ExtractorConfig{Type: "contains", Text: "healthy"},
			body:       "service is healthy",
			statusCode: 200,
			wantStatus: "up",
		},
		{
			name:       "contains extractor no match",
			extractor:  ExtractorConfig{Type: "contains", Text: "healthy"},
			body:       "service is down",
			statusCode: 200,
			wantStatus: "down",
		},
		{
			name:       "http extractor 200",
			extractor:  ExtractorConfig{Type: "http"},
			body:       "",
			statusCode: 200,
			wantStatus: "up",
		},
		{
			name:       "http extractor 500",
			extractor:  ExtractorConfig{Type: "http"},
			body:       "",
			statusCode: 500,
			wantStatus: "down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Endpoints: []EndpointConfig{
					{
						Name:      "Test",
						URL:       "https://example.com",
						Extractor: tt.extractor,
					},
				},
			}

			endpoints, err := BuildEndpoints(cfg)
			if err != nil {
				t.Fatalf("BuildEndpoints() error = %v", err)
			}

			extractor := endpoints[0].Extractor()
			if extractor == nil {
				t.Fatal("Extractor() = nil, want non-nil for this test")
			}

			status := extractor([]byte(tt.body), tt.statusCode)
			if status.String() != tt.wantStatus {
				t.Errorf("extractor() = %q, want %q", status.String(), tt.wantStatus)
			}
		})
	}
}

func TestBuildEndpoints_EmptyConfig(t *testing.T) {
	cfg := &Config{}

	endpoints, err := BuildEndpoints(cfg)
	if err != nil {
		t.Fatalf("BuildEndpoints() error = %v", err)
	}

	if len(endpoints) != 0 {
		t.Errorf("len(endpoints) = %d, want 0", len(endpoints))
	}
}

// TestBuildEndpoints_GridMissingScheme verifies that grids with missing URL schemes
// fail at build time with a clear error from the SDK layer.
//
// Note: Direct endpoints validate scheme (http/https only) at config.Parse() time.
// Grids validate at BuildEndpoints() time after template expansion, but the SDK
// currently only checks for scheme presence, not that it's http/https.
// TODO: Consider adding http/https validation to SDK's NewEndpoint for parity.
func TestBuildEndpoints_GridMissingScheme(t *testing.T) {
	cfg := &Config{
		Grids: []GridConfig{
			{
				Name:        "Invalid",
				URLTemplate: "{{.env}}.example.com/health", // missing scheme
				Dimensions: map[string][]string{
					"env": {"prod"},
				},
			},
		},
	}

	_, err := BuildEndpoints(cfg)
	if err == nil {
		t.Fatal("BuildEndpoints() expected error for missing scheme, got nil")
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Errorf("error = %q, want to contain 'scheme'", err.Error())
	}
}

// TestBuildGridEndpoints_TemplateExecutionError verifies that template execution
// errors include contextual information about which grid and dimension combination
// failed, making debugging easier.
func TestBuildGridEndpoints_TemplateExecutionError(t *testing.T) {
	cfg := &Config{
		Grids: []GridConfig{
			{
				Name:        "Platform API",
				URLTemplate: "https://{{.region}}.example.com/health", // .region not in dimensions
				Dimensions: map[string][]string{
					"env": {"prod"},
					"svc": {"api"},
				},
			},
		},
	}

	_, err := BuildEndpoints(cfg)

	if err == nil {
		t.Fatal("expected error for missing template variable, got nil")
	}

	errStr := err.Error()

	if !strings.Contains(errStr, "grid (Platform API)") {
		t.Errorf("error should contain grid name, got: %s", errStr)
	}

	// test dimensions separately to avoid map ordering issues
	if !strings.Contains(errStr, "env:prod") {
		t.Errorf("error should contain env dimension, got: %s", errStr)
	}

	if !strings.Contains(errStr, "svc:api") {
		t.Errorf("error should contain svc dimension, got: %s", errStr)
	}

	if !strings.Contains(errStr, "template execution failed") {
		t.Errorf("error should indicate template execution failure, got: %s", errStr)
	}

	if !strings.Contains(errStr, "region") {
		t.Errorf("error should preserve original error mentioning missing key, got: %s", errStr)
	}
}

// TestCartesianProduct_DeterministicOrder verifies that cartesianProduct produces
// identical output across multiple invocations with the same input.
// This guards against regressions if the key sorting is accidentally removed.
func TestCartesianProduct_DeterministicOrder(t *testing.T) {
	// keys in reverse alphabetical order to catch unsorted map iteration
	dims := map[string][]string{
		"z": {"3", "4"},
		"a": {"1", "2"},
	}

	// capture first result as reference
	first := cartesianProduct(dims)
	if len(first) != 4 {
		t.Fatalf("expected 4 combinations, got %d", len(first))
	}

	// run 100 iterations and verify identical output
	for i := 0; i < 100; i++ {
		result := cartesianProduct(dims)

		if len(result) != len(first) {
			t.Fatalf("iteration %d: length changed from %d to %d", i, len(first), len(result))
		}

		for j := range first {
			if !reflect.DeepEqual(result[j], first[j]) {
				t.Fatalf("iteration %d: combination[%d] differs: got %v, want %v",
					i, j, result[j], first[j])
			}
		}
	}
}
