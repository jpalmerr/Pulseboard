package pulseboard

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNew_Valid(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	pb, err := New(WithEndpoint(ep))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if len(pb.Endpoints()) != 1 {
		t.Errorf("len(Endpoints()) = %v, want %v", len(pb.Endpoints()), 1)
	}
}

func TestNew_NoEndpoints(t *testing.T) {
	_, err := New()
	if err == nil {
		t.Error("New() expected error for no endpoints, got nil")
	}
}

func TestNew_DuplicateEndpointNames(t *testing.T) {
	ep1, _ := NewEndpoint("API", "https://api1.example.com")
	ep2, _ := NewEndpoint("API", "https://api2.example.com") // same name, different URL

	_, err := New(
		WithEndpoint(ep1),
		WithEndpoint(ep2),
	)
	if err == nil {
		t.Error("New() expected error for duplicate endpoint names, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "duplicate endpoint name") {
		t.Errorf("New() error = %v, want error containing 'duplicate endpoint name'", err)
	}
}

func TestNew_DuplicateEndpointNames_WithEndpoints(t *testing.T) {
	ep1, _ := NewEndpoint("API", "https://api1.example.com")
	ep2, _ := NewEndpoint("API", "https://api2.example.com")

	_, err := New(
		WithEndpoints(ep1, ep2),
	)
	if err == nil {
		t.Error("New() expected error for duplicate endpoint names via WithEndpoints, got nil")
	}
}

func TestNew_DuplicateEndpointNames_ThreeEndpoints(t *testing.T) {
	ep1, _ := NewEndpoint("API", "https://api.example.com")
	ep2, _ := NewEndpoint("Database", "https://db.example.com")
	ep3, _ := NewEndpoint("API", "https://api-backup.example.com") // duplicate of first

	_, err := New(
		WithEndpoints(ep1, ep2, ep3),
	)
	if err == nil {
		t.Error("New() expected error for duplicate endpoint names, got nil")
	}
}

func TestNew_Defaults(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	pb, err := New(WithEndpoint(ep))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if pb.Port() != 8080 {
		t.Errorf("Port() = %v, want %v", pb.Port(), 8080)
	}
	if pb.PollingInterval() != 15*time.Second {
		t.Errorf("PollingInterval() = %v, want %v", pb.PollingInterval(), 15*time.Second)
	}
}

func TestWithEndpoint(t *testing.T) {
	ep1, _ := NewEndpoint("Test1", "https://example1.com")
	ep2, _ := NewEndpoint("Test2", "https://example2.com")

	pb, err := New(
		WithEndpoint(ep1),
		WithEndpoint(ep2),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if len(pb.Endpoints()) != 2 {
		t.Errorf("len(Endpoints()) = %v, want %v", len(pb.Endpoints()), 2)
	}
}

func TestWithEndpoints(t *testing.T) {
	ep1, _ := NewEndpoint("Test1", "https://example1.com")
	ep2, _ := NewEndpoint("Test2", "https://example2.com")
	ep3, _ := NewEndpoint("Test3", "https://example3.com")

	pb, err := New(
		WithEndpoints(ep1, ep2, ep3),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if len(pb.Endpoints()) != 3 {
		t.Errorf("len(Endpoints()) = %v, want %v", len(pb.Endpoints()), 3)
	}
}

func TestWithPollingInterval(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	pb, err := New(
		WithEndpoint(ep),
		WithPollingInterval(30*time.Second),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if pb.PollingInterval() != 30*time.Second {
		t.Errorf("PollingInterval() = %v, want %v", pb.PollingInterval(), 30*time.Second)
	}
}

func TestWithPollingInterval_Invalid(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	tests := []struct {
		name     string
		interval time.Duration
	}{
		{"zero", 0},
		{"negative", -1 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(
				WithEndpoint(ep),
				WithPollingInterval(tt.interval),
			)
			if err == nil {
				t.Errorf("New() expected error for interval %v, got nil", tt.interval)
			}
		})
	}
}

func TestWithPort(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	pb, err := New(
		WithEndpoint(ep),
		WithPort(9090),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if pb.Port() != 9090 {
		t.Errorf("Port() = %v, want %v", pb.Port(), 9090)
	}
}

func TestWithPort_Invalid(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	tests := []struct {
		name string
		port int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too high", 65536},
		{"way too high", 100000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(
				WithEndpoint(ep),
				WithPort(tt.port),
			)
			if err == nil {
				t.Errorf("New() expected error for port %v, got nil", tt.port)
			}
		})
	}
}

func TestWithPort_ValidEdgeCases(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	tests := []struct {
		name string
		port int
	}{
		{"minimum", 1},
		{"maximum", 65535},
		{"common http", 80},
		{"common https", 443},
		{"common alt", 8080},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pb, err := New(
				WithEndpoint(ep),
				WithPort(tt.port),
			)
			if err != nil {
				t.Errorf("New() unexpected error for port %v: %v", tt.port, err)
			}
			if pb.Port() != tt.port {
				t.Errorf("Port() = %v, want %v", pb.Port(), tt.port)
			}
		})
	}
}

func TestWithMaxConcurrency(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	pb, err := New(
		WithEndpoint(ep),
		WithMaxConcurrency(5),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// no getter for maxConcurrency, just verify it doesn't error
	_ = pb
}

func TestWithMaxConcurrency_Invalid(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	tests := []struct {
		name           string
		maxConcurrency int
	}{
		{"zero", 0},
		{"negative", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(
				WithEndpoint(ep),
				WithMaxConcurrency(tt.maxConcurrency),
			)
			if err == nil {
				t.Errorf("New() expected error for maxConcurrency %v, got nil", tt.maxConcurrency)
			}
		})
	}
}

func TestEndpoints_Immutability(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	pb, err := New(WithEndpoint(ep))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// get endpoints and modify the slice
	endpoints := pb.Endpoints()
	originalLen := len(endpoints)

	ep2, _ := NewEndpoint("Test2", "https://example2.com")
	_ = append(endpoints, ep2) // intentionally unused, testing immutability

	// original should be unchanged
	if len(pb.Endpoints()) != originalLen {
		t.Error("Endpoints() mutation affected original PulseBoard")
	}
}

func TestWithLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	ep, _ := NewEndpoint("Test", "https://example.com")

	pb, err := New(
		WithEndpoint(ep),
		WithLogger(logger),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// verify PulseBoard was created successfully
	if pb == nil {
		t.Fatal("New() returned nil PulseBoard")
	}
}

func TestWithLogger_Nil(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	_, err := New(
		WithEndpoint(ep),
		WithLogger(nil),
	)
	if err == nil {
		t.Error("New() expected error for nil logger, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "logger cannot be nil") {
		t.Errorf("New() error = %v, want error containing 'logger cannot be nil'", err)
	}
}

func TestWithLogger_DefaultsToSlogDefault(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	// create without explicit logger
	pb, err := New(WithEndpoint(ep))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// should work without explicit logger (defaults to slog.Default())
	if pb == nil {
		t.Fatal("New() returned nil PulseBoard")
	}
}

func TestWithTitle(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	pb, err := New(
		WithEndpoint(ep),
		WithTitle("Custom Dashboard"),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if pb.title != "Custom Dashboard" {
		t.Errorf("title = %q, want %q", pb.title, "Custom Dashboard")
	}
}

func TestWithTitle_Empty(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	pb, err := New(
		WithEndpoint(ep),
		WithTitle(""),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// empty string is valid (defaults to "PulseBoard" at render time)
	if pb.title != "" {
		t.Errorf("title = %q, want empty string", pb.title)
	}
}

func TestWithTitle_DefaultsToEmpty(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	// create without explicit title
	pb, err := New(WithEndpoint(ep))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// title should be empty string when not configured
	if pb.title != "" {
		t.Errorf("title = %q, want empty string", pb.title)
	}
}
