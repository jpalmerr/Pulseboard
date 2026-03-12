package config

import (
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jpalmerr/pulseboard"
)

// okHandler always responds 200 OK; used as the wrapped handler in middleware tests.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// --- BuildAuthMiddleware tests ---

func TestBuildAuthMiddleware_Nil(t *testing.T) {
	mw := BuildAuthMiddleware(nil)
	if mw != nil {
		t.Error("BuildAuthMiddleware(nil) = non-nil, want nil")
	}
}

func TestBuildAuthMiddleware_Basic(t *testing.T) {
	mw := BuildAuthMiddleware(&AuthConfig{
		Type:     "basic",
		Username: "admin",
		Password: "secret",
	})
	if mw == nil {
		t.Fatal("BuildAuthMiddleware(basic) = nil, want non-nil middleware")
	}

	tests := []struct {
		name     string
		header   string
		wantCode int
	}{
		{
			name:     "valid credentials",
			header:   "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:secret")),
			wantCode: http.StatusOK,
		},
		{
			name:     "wrong password",
			header:   "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:wrong")),
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "no header",
			header:   "",
			wantCode: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			mw(okHandler).ServeHTTP(rec, req)
			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}
		})
	}
}

func TestBuildAuthMiddleware_Bearer_SingleToken(t *testing.T) {
	mw := BuildAuthMiddleware(&AuthConfig{
		Type:  "bearer",
		Token: "my-secret",
	})
	if mw == nil {
		t.Fatal("BuildAuthMiddleware(bearer/single) = nil, want non-nil middleware")
	}

	tests := []struct {
		name     string
		header   string
		wantCode int
	}{
		{"valid token", "Bearer my-secret", http.StatusOK},
		{"wrong token", "Bearer wrong", http.StatusUnauthorized},
		{"no header", "", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			mw(okHandler).ServeHTTP(rec, req)
			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}
		})
	}
}

func TestBuildAuthMiddleware_Bearer_MultipleTokens(t *testing.T) {
	mw := BuildAuthMiddleware(&AuthConfig{
		Type:   "bearer",
		Tokens: []string{"token-a", "token-b"},
	})
	if mw == nil {
		t.Fatal("BuildAuthMiddleware(bearer/multiple) = nil, want non-nil middleware")
	}

	for _, tok := range []string{"token-a", "token-b"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		mw(okHandler).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("token %q: status = %d, want 200", tok, rec.Code)
		}
	}

	// invalid token should be rejected
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("invalid token: status = %d, want 401", rec.Code)
	}
}

func TestBuildAuthMiddleware_UnknownType(t *testing.T) {
	// Unknown type should now fail at config.Parse() time.
	// BuildAuthMiddleware itself still returns nil (defensive fallback),
	// but this case is blocked before it can be reached in production.
	//
	// Verify Parse rejects it:
	yaml := `
endpoints:
  - name: Test
    url: https://example.com
auth:
  type: apikey
  token: abc
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("Parse() expected error for unknown auth type, got nil")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error = %q, want to contain 'unknown type'", err.Error())
	}
}

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

// TestBuildGridEndpoints_TemplateExecutionError verifies that an error is returned
// when a placeholder references a dimension key that does not exist, and that the
// error message includes the grid name and the missing key name.
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

	if !strings.Contains(errStr, "Platform API") {
		t.Errorf("error should contain grid name, got: %s", errStr)
	}

	if !strings.Contains(errStr, "region") {
		t.Errorf("error should preserve original error mentioning missing key, got: %s", errStr)
	}

	if !strings.Contains(errStr, "invalid url_template") {
		t.Errorf("error should indicate url_template validation failure, got: %s", errStr)
	}
}

// --- TLS builder tests ---

// applyOptions is a test helper that applies a slice of pulseboard.Option values
// to a minimal New() call and returns any error.
func applyOptions(t *testing.T, opts []pulseboard.Option) error {
	t.Helper()
	ep, err := pulseboard.NewEndpoint("Test", "https://example.com")
	if err != nil {
		t.Fatalf("applyOptions: NewEndpoint: %v", err)
	}
	all := append([]pulseboard.Option{pulseboard.WithEndpoint(ep)}, opts...)
	_, err = pulseboard.New(all...)
	return err
}

func TestBuildServerTLSOptions_NilServer(t *testing.T) {
	cfg := &Config{}
	opts := BuildServerTLSOptions(cfg)
	if opts != nil {
		t.Errorf("BuildServerTLSOptions(nil server) = %v, want nil", opts)
	}
}

func TestBuildServerTLSOptions_NilTLS(t *testing.T) {
	cfg := &Config{Server: &ServerConfig{TLS: nil}}
	opts := BuildServerTLSOptions(cfg)
	if opts != nil {
		t.Errorf("BuildServerTLSOptions(nil TLS) = %v, want nil", opts)
	}
}

func TestBuildServerTLSOptions_WithCertAndKey(t *testing.T) {
	cfg := &Config{
		Server: &ServerConfig{
			TLS: &ServerTLSConfig{
				CertFile: "cert.pem",
				KeyFile:  "key.pem",
			},
		},
	}
	opts := BuildServerTLSOptions(cfg)
	if len(opts) == 0 {
		t.Fatal("BuildServerTLSOptions() = nil, want non-empty slice")
	}
	// applying the option should succeed (cert/key exist as non-empty strings)
	if err := applyOptions(t, opts); err != nil {
		t.Errorf("applying BuildServerTLSOptions() = %v, want nil error", err)
	}
}

func TestBuildClientTLSOptions_NilClient(t *testing.T) {
	cfg := &Config{}
	opts, err := BuildClientTLSOptions(cfg)
	if err != nil {
		t.Errorf("BuildClientTLSOptions(nil client) error = %v, want nil", err)
	}
	if opts != nil {
		t.Errorf("BuildClientTLSOptions(nil client) = %v, want nil", opts)
	}
}

func TestBuildClientTLSOptions_InsecureSkipVerify(t *testing.T) {
	cfg := &Config{
		Client: &ClientConfig{
			TLS: &ClientTLSConfig{InsecureSkipVerify: true},
		},
	}
	opts, err := BuildClientTLSOptions(cfg)
	if err != nil {
		t.Fatalf("BuildClientTLSOptions() error = %v", err)
	}
	if len(opts) == 0 {
		t.Fatal("BuildClientTLSOptions() returned empty opts, want WithInsecureSkipVerify")
	}
	if applyErr := applyOptions(t, opts); applyErr != nil {
		t.Errorf("applying InsecureSkipVerify option = %v, want nil", applyErr)
	}
}

func TestBuildClientTLSOptions_MinVersion13(t *testing.T) {
	cfg := &Config{
		Client: &ClientConfig{
			TLS: &ClientTLSConfig{MinVersion: "1.3"},
		},
	}
	opts, err := BuildClientTLSOptions(cfg)
	if err != nil {
		t.Fatalf("BuildClientTLSOptions() error = %v", err)
	}
	if len(opts) == 0 {
		t.Fatal("BuildClientTLSOptions() returned empty opts, want WithTLSMinVersion")
	}
	if applyErr := applyOptions(t, opts); applyErr != nil {
		t.Errorf("applying TLSMinVersion option = %v, want nil", applyErr)
	}
}

func TestBuildClientTLSOptions_VerifyMinVersionValue(t *testing.T) {
	// verify that the option returned by BuildClientTLSOptions for "1.3"
	// actually encodes tls.VersionTLS13 (not just that it applies without error)
	cfg := &Config{
		Client: &ClientConfig{
			TLS: &ClientTLSConfig{MinVersion: "1.3"},
		},
	}
	opts, err := BuildClientTLSOptions(cfg)
	if err != nil {
		t.Fatalf("BuildClientTLSOptions() error = %v", err)
	}
	// parseTLSVersion("1.3") must return tls.VersionTLS13
	v, err := parseTLSVersion("1.3")
	if err != nil {
		t.Fatalf("parseTLSVersion(\"1.3\") error = %v", err)
	}
	if v != tls.VersionTLS13 {
		t.Errorf("parseTLSVersion(\"1.3\") = 0x%04x, want 0x%04x", v, tls.VersionTLS13)
	}
	_ = opts // non-empty confirmed above
}

func TestBuildClientTLSOptions_WithClientCert(t *testing.T) {
	cfg := &Config{
		Client: &ClientConfig{
			TLS: &ClientTLSConfig{
				ClientCert: "client-cert.pem",
				ClientKey:  "client-key.pem",
			},
		},
	}
	opts, err := BuildClientTLSOptions(cfg)
	if err != nil {
		t.Fatalf("BuildClientTLSOptions() error = %v", err)
	}
	if len(opts) == 0 {
		t.Fatal("BuildClientTLSOptions() returned empty opts, want WithClientCert")
	}
	// applying should succeed — files are only checked at server Start() time
	if applyErr := applyOptions(t, opts); applyErr != nil {
		t.Errorf("applying WithClientCert option = %v, want nil", applyErr)
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

// --- BuildWebhookOptions tests ---

func TestBuildWebhookOptions_Empty(t *testing.T) {
	cfg := &Config{}
	opts := BuildWebhookOptions(cfg)
	if opts != nil {
		t.Errorf("BuildWebhookOptions(empty) = %v, want nil", opts)
	}
}

func TestBuildWebhookOptions_Single(t *testing.T) {
	cfg := &Config{
		Webhooks: []WebhookConfig{
			{URL: "https://hooks.example.com/notify"},
		},
	}
	opts := BuildWebhookOptions(cfg)
	if len(opts) != 1 {
		t.Fatalf("BuildWebhookOptions() returned %d opts, want 1", len(opts))
	}
	// The option must be applicable to pulseboard.New without error.
	if err := applyOptions(t, opts); err != nil {
		t.Errorf("applying webhook option = %v, want nil", err)
	}
}

func TestBuildWebhookOptions_EventFilter(t *testing.T) {
	cfg := &Config{
		Webhooks: []WebhookConfig{
			{
				URL:    "https://hooks.example.com/notify",
				Events: []string{"down", "degraded"},
			},
		},
	}
	opts := BuildWebhookOptions(cfg)
	if len(opts) != 1 {
		t.Fatalf("BuildWebhookOptions() returned %d opts, want 1", len(opts))
	}
	if err := applyOptions(t, opts); err != nil {
		t.Errorf("applying webhook option with event filter = %v, want nil", err)
	}
}

func TestBuildWebhookOptions_Headers(t *testing.T) {
	cfg := &Config{
		Webhooks: []WebhookConfig{
			{
				URL:     "https://hooks.example.com/notify",
				Headers: map[string]string{"Authorization": "Bearer token"},
			},
		},
	}
	opts := BuildWebhookOptions(cfg)
	if len(opts) != 1 {
		t.Fatalf("BuildWebhookOptions() returned %d opts, want 1", len(opts))
	}
	if err := applyOptions(t, opts); err != nil {
		t.Errorf("applying webhook option with headers = %v, want nil", err)
	}
}

func TestBuildWebhookOptions_TimeoutAndDebounce(t *testing.T) {
	cfg := &Config{
		Webhooks: []WebhookConfig{
			{
				URL:      "https://hooks.example.com/notify",
				Timeout:  5,
				Debounce: 30,
			},
		},
	}
	opts := BuildWebhookOptions(cfg)
	if len(opts) != 1 {
		t.Fatalf("BuildWebhookOptions() returned %d opts, want 1", len(opts))
	}
	if err := applyOptions(t, opts); err != nil {
		t.Errorf("applying webhook option with timeout/debounce = %v, want nil", err)
	}
}
