package pulseboard

import "testing"

func TestToPollerEndpoints_LabelsCopied(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com",
		WithLabels("env", "prod", "region", "us-east"),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(WithEndpoint(ep), WithPort(19100))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// get poller endpoints (same package, so we can call private method)
	pollerEndpoints := pb.toPollerEndpoints()
	if len(pollerEndpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(pollerEndpoints))
	}

	// mutate the labels in EndpointInfo
	pollerEndpoints[0].Labels["env"] = "modified"
	pollerEndpoints[0].Labels["new_key"] = "new_value"

	// verify original endpoint is unchanged
	originalLabels := ep.Labels()
	if originalLabels["env"] != "prod" {
		t.Errorf("mutation affected original: Labels[env] = %q, want %q", originalLabels["env"], "prod")
	}
	if _, exists := originalLabels["new_key"]; exists {
		t.Error("mutation added new key to original endpoint")
	}
}

func TestToPollerEndpoints_HeadersCopied(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com",
		WithHeaders("Authorization", "Bearer token", "X-Custom", "value"),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(WithEndpoint(ep), WithPort(19101))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	pollerEndpoints := pb.toPollerEndpoints()
	if len(pollerEndpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(pollerEndpoints))
	}

	// mutate the headers in EndpointInfo
	pollerEndpoints[0].Headers["Authorization"] = "modified"
	pollerEndpoints[0].Headers["new_header"] = "new_value"

	// verify original endpoint is unchanged
	originalHeaders := ep.Headers()
	if originalHeaders["Authorization"] != "Bearer token" {
		t.Errorf("mutation affected original: Headers[Authorization] = %q, want %q",
			originalHeaders["Authorization"], "Bearer token")
	}
	if _, exists := originalHeaders["new_header"]; exists {
		t.Error("mutation added new header to original endpoint")
	}
}

func TestToPollerEndpoints_NilLabels(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com") // no labels
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(WithEndpoint(ep), WithPort(19102))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	pollerEndpoints := pb.toPollerEndpoints()
	if len(pollerEndpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(pollerEndpoints))
	}

	// should not panic - copyMap returns nil for nil input
	labels := pollerEndpoints[0].Labels
	if len(labels) != 0 {
		t.Errorf("expected nil or empty labels, got %v", labels)
	}
}

func TestToPollerEndpoints_NilHeaders(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com") // no headers
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(WithEndpoint(ep), WithPort(19103))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	pollerEndpoints := pb.toPollerEndpoints()
	if len(pollerEndpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(pollerEndpoints))
	}

	// should not panic - copyMap returns nil for nil input
	headers := pollerEndpoints[0].Headers
	if len(headers) != 0 {
		t.Errorf("expected nil or empty headers, got %v", headers)
	}
}
