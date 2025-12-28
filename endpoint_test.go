package pulseboard

import (
	"testing"
	"time"
)

func TestNewEndpoint_Valid(t *testing.T) {
	ep, err := NewEndpoint("Test API", "https://api.example.com/health")
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	if ep.Name() != "Test API" {
		t.Errorf("Name() = %v, want %v", ep.Name(), "Test API")
	}
	if ep.URL() != "https://api.example.com/health" {
		t.Errorf("URL() = %v, want %v", ep.URL(), "https://api.example.com/health")
	}
	if ep.Timeout() != 10*time.Second {
		t.Errorf("Timeout() = %v, want %v", ep.Timeout(), 10*time.Second)
	}
}

func TestNewEndpoint_EmptyName(t *testing.T) {
	_, err := NewEndpoint("", "https://api.example.com/health")
	if err == nil {
		t.Error("NewEndpoint() expected error for empty name, got nil")
	}
}

func TestNewEndpoint_InvalidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"no scheme", "api.example.com/health"},
		{"empty url", ""},
		{"just path", "/health"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEndpoint("Test", tt.url)
			if err == nil {
				t.Errorf("NewEndpoint() expected error for URL %q, got nil", tt.url)
			}
		})
	}
}

func TestNewEndpoint_ValidURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"https", "https://api.example.com/health"},
		{"http", "http://localhost:8080/health"},
		{"with port", "https://api.example.com:443/health"},
		{"with query", "https://api.example.com/health?check=full"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEndpoint("Test", tt.url)
			if err != nil {
				t.Errorf("NewEndpoint() unexpected error for URL %q: %v", tt.url, err)
			}
		})
	}
}

func TestWithLabels(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com",
		WithLabels("env", "prod", "service", "api"),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	labels := ep.Labels()
	if labels["env"] != "prod" {
		t.Errorf("Labels()[env] = %v, want %v", labels["env"], "prod")
	}
	if labels["service"] != "api" {
		t.Errorf("Labels()[service] = %v, want %v", labels["service"], "api")
	}
}

func TestWithLabels_OddArgs(t *testing.T) {
	_, err := NewEndpoint("Test", "https://example.com",
		WithLabels("env", "prod", "orphan"),
	)
	if err == nil {
		t.Error("NewEndpoint() expected error for odd number of label args, got nil")
	}
}

func TestWithLabels_Immutability(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com",
		WithLabels("env", "prod"),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	// modify returned labels
	labels := ep.Labels()
	labels["env"] = "modified"
	labels["new"] = "value"

	// original should be unchanged
	originalLabels := ep.Labels()
	if originalLabels["env"] != "prod" {
		t.Error("Labels() mutation affected original endpoint")
	}
	if _, exists := originalLabels["new"]; exists {
		t.Error("Labels() mutation added new key to original endpoint")
	}
}

func TestWithHeaders(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com",
		WithHeaders("Authorization", "Bearer token", "X-Custom", "value"),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	headers := ep.Headers()
	if headers["Authorization"] != "Bearer token" {
		t.Errorf("Headers()[Authorization] = %v, want %v", headers["Authorization"], "Bearer token")
	}
	if headers["X-Custom"] != "value" {
		t.Errorf("Headers()[X-Custom] = %v, want %v", headers["X-Custom"], "value")
	}
}

func TestWithHeaders_OddArgs(t *testing.T) {
	_, err := NewEndpoint("Test", "https://example.com",
		WithHeaders("Authorization"),
	)
	if err == nil {
		t.Error("NewEndpoint() expected error for odd number of header args, got nil")
	}
}

func TestWithTimeout(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com",
		WithTimeout(30*time.Second),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	if ep.Timeout() != 30*time.Second {
		t.Errorf("Timeout() = %v, want %v", ep.Timeout(), 30*time.Second)
	}
}

func TestWithTimeout_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{"zero", 0},
		{"negative", -1 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEndpoint("Test", "https://example.com",
				WithTimeout(tt.timeout),
			)
			if err == nil {
				t.Errorf("NewEndpoint() expected error for timeout %v, got nil", tt.timeout)
			}
		})
	}
}

func TestWithExtractor(t *testing.T) {
	customExtractor := func(body []byte, statusCode int) Status {
		return StatusDegraded
	}

	ep, err := NewEndpoint("Test", "https://example.com",
		WithExtractor(customExtractor),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	extractor := ep.Extractor()
	if extractor == nil {
		t.Fatal("Extractor() = nil, want non-nil")
	}

	// verify it's our extractor
	if extractor(nil, 200) != StatusDegraded {
		t.Error("Extractor() returned wrong extractor")
	}
}

func TestWithMethod(t *testing.T) {
	tests := []struct {
		name   string
		method string
		want   string
	}{
		{"GET method", "GET", "GET"},
		{"HEAD method", "HEAD", "HEAD"},
		{"POST method", "POST", "POST"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep, err := NewEndpoint("Test", "https://example.com",
				WithMethod(tt.method),
			)
			if err != nil {
				t.Fatalf("NewEndpoint() error = %v", err)
			}
			if ep.Method() != tt.want {
				t.Errorf("Method() = %v, want %v", ep.Method(), tt.want)
			}
		})
	}
}

func TestWithMethod_Default(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com")
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	// no WithMethod specified, should return empty string
	// (the poller defaults to GET when empty)
	if ep.Method() != "" {
		t.Errorf("Method() = %v, want empty string", ep.Method())
	}
}

func TestWithMethod_Invalid(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{"PUT", "PUT"},
		{"DELETE", "DELETE"},
		{"PATCH", "PATCH"},
		{"lowercase get", "get"},
		{"empty string", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEndpoint("Test", "https://example.com",
				WithMethod(tt.method),
			)
			if err == nil {
				t.Errorf("NewEndpoint() expected error for method %q, got nil", tt.method)
			}
		})
	}
}

func TestEndpoint_MultipleOptions(t *testing.T) {
	ep, err := NewEndpoint("Full Test", "https://api.example.com/health",
		WithLabels("env", "prod", "region", "us-east"),
		WithHeaders("Authorization", "Bearer token"),
		WithTimeout(5*time.Second),
		WithExtractor(HTTPStatusExtractor),
		WithMethod("POST"),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	if ep.Name() != "Full Test" {
		t.Errorf("Name() = %v, want %v", ep.Name(), "Full Test")
	}
	if len(ep.Labels()) != 2 {
		t.Errorf("len(Labels()) = %v, want %v", len(ep.Labels()), 2)
	}
	if len(ep.Headers()) != 1 {
		t.Errorf("len(Headers()) = %v, want %v", len(ep.Headers()), 1)
	}
	if ep.Timeout() != 5*time.Second {
		t.Errorf("Timeout() = %v, want %v", ep.Timeout(), 5*time.Second)
	}
	if ep.Extractor() == nil {
		t.Error("Extractor() = nil, want non-nil")
	}
	if ep.Method() != "POST" {
		t.Errorf("Method() = %v, want %v", ep.Method(), "POST")
	}
}

func TestEndpoint_DefaultInterval(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com")
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	// no WithInterval specified, should return 0 (use global default)
	if ep.Interval() != 0 {
		t.Errorf("Interval() = %v, want 0 (default)", ep.Interval())
	}
}

func TestWithInterval_Valid(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
	}{
		{"minimum", time.Second},
		{"5 seconds", 5 * time.Second},
		{"30 seconds", 30 * time.Second},
		{"1 minute", time.Minute},
		{"30 minutes", 30 * time.Minute},
		{"maximum", time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep, err := NewEndpoint("Test", "https://example.com",
				WithInterval(tt.interval),
			)
			if err != nil {
				t.Fatalf("NewEndpoint() error = %v", err)
			}
			if ep.Interval() != tt.interval {
				t.Errorf("Interval() = %v, want %v", ep.Interval(), tt.interval)
			}
		})
	}
}

func TestWithInterval_TooShort(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
	}{
		{"zero", 0},
		{"500ms", 500 * time.Millisecond},
		{"999ms", 999 * time.Millisecond},
		{"negative", -time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEndpoint("Test", "https://example.com",
				WithInterval(tt.interval),
			)
			if err == nil {
				t.Errorf("NewEndpoint() expected error for interval %v, got nil", tt.interval)
			}
		})
	}
}

func TestWithInterval_TooLong(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
	}{
		{"1h1s", time.Hour + time.Second},
		{"2 hours", 2 * time.Hour},
		{"24 hours", 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEndpoint("Test", "https://example.com",
				WithInterval(tt.interval),
			)
			if err == nil {
				t.Errorf("NewEndpoint() expected error for interval %v, got nil", tt.interval)
			}
		})
	}
}

func TestWithInterval_WithOtherOptions(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com",
		WithLabels("env", "prod"),
		WithInterval(10*time.Second),
		WithTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	if ep.Interval() != 10*time.Second {
		t.Errorf("Interval() = %v, want %v", ep.Interval(), 10*time.Second)
	}
	if ep.Timeout() != 5*time.Second {
		t.Errorf("Timeout() = %v, want %v", ep.Timeout(), 5*time.Second)
	}
	if ep.Labels()["env"] != "prod" {
		t.Errorf("Labels()[env] = %v, want %v", ep.Labels()["env"], "prod")
	}
}
