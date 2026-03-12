package urltmpl_test

import (
	"strings"
	"testing"

	"github.com/jpalmerr/pulseboard/internal/urltmpl"
)

// =============================================================================
// Expand
// =============================================================================

func TestExpand_BasicSubstitution(t *testing.T) {
	got, err := urltmpl.Expand("https://{{.env}}.example.com/health", map[string]string{
		"env": "prod",
	})
	if err != nil {
		t.Fatalf("Expand() unexpected error: %v", err)
	}
	want := "https://prod.example.com/health"
	if got != want {
		t.Errorf("Expand() = %q, want %q", got, want)
	}
}

func TestExpand_MultipleKeys(t *testing.T) {
	got, err := urltmpl.Expand("https://{{.env}}.example.com/{{.svc}}/health", map[string]string{
		"env": "prod",
		"svc": "api",
	})
	if err != nil {
		t.Fatalf("Expand() unexpected error: %v", err)
	}
	want := "https://prod.example.com/api/health"
	if got != want {
		t.Errorf("Expand() = %q, want %q", got, want)
	}
}

func TestExpand_UnusedDimensions(t *testing.T) {
	// Extra keys in values map must not cause an error.
	got, err := urltmpl.Expand("https://{{.env}}.example.com/health", map[string]string{
		"env":    "staging",
		"region": "us-east",
	})
	if err != nil {
		t.Fatalf("Expand() unexpected error: %v", err)
	}
	want := "https://staging.example.com/health"
	if got != want {
		t.Errorf("Expand() = %q, want %q", got, want)
	}
}

func TestExpand_MissingDimension(t *testing.T) {
	_, err := urltmpl.Expand("https://{{.env}}.example.com/{{.missing}}/health", map[string]string{
		"env": "prod",
	})
	if err == nil {
		t.Fatal("Expand() expected error for missing key, got nil")
	}
	if !strings.Contains(err.Error(), "{{.missing}}") {
		t.Errorf("error = %q, want to contain placeholder name", err.Error())
	}
}

func TestExpand_UnbalancedBracesNoClose(t *testing.T) {
	// {{ without }} must return an error — this is the staff engineer blocker fix.
	_, err := urltmpl.Expand("https://{{.env}.example.com", map[string]string{
		"env": "prod",
	})
	if err == nil {
		t.Fatal("Expand() expected error for unbalanced {{ without }}, got nil")
	}
	if !strings.Contains(err.Error(), "unmatched {{") {
		t.Errorf("error = %q, want to contain 'unmatched {{'", err.Error())
	}
}

func TestExpand_InjectionAttempt(t *testing.T) {
	// A dimension value containing {{ must not be evaluated as a placeholder.
	// The value is URL-encoded by the caller, so {{ becomes %7B%7B.
	encoded := map[string]string{
		"env": "%7B%7B.evil%7D%7D",
	}
	got, err := urltmpl.Expand("https://example.com/health?env={{.env}}", encoded)
	if err != nil {
		t.Fatalf("Expand() unexpected error: %v", err)
	}
	if strings.Contains(got, "{{") {
		t.Errorf("Expand() result contains unevaluated template syntax: %q", got)
	}
}

func TestExpand_EmptyValuesMap(t *testing.T) {
	_, err := urltmpl.Expand("https://{{.env}}.example.com/health", map[string]string{})
	if err == nil {
		t.Fatal("Expand() expected error for empty values map with placeholder, got nil")
	}
}

func TestExpand_EmptyTemplate(t *testing.T) {
	got, err := urltmpl.Expand("", map[string]string{"env": "prod"})
	if err != nil {
		t.Fatalf("Expand() unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("Expand(\"\") = %q, want \"\"", got)
	}
}

// =============================================================================
// Validate
// =============================================================================

func TestValidate_ValidSyntax(t *testing.T) {
	tests := []struct {
		name       string
		tmpl       string
		dimensions map[string][]string
	}{
		{
			name: "single placeholder",
			tmpl: "https://{{.env}}.example.com/health",
			dimensions: map[string][]string{
				"env": {"prod", "staging"},
			},
		},
		{
			name: "multiple placeholders",
			tmpl: "https://{{.env}}.example.com/{{.svc}}/health",
			dimensions: map[string][]string{
				"env": {"prod"},
				"svc": {"api"},
			},
		},
		{
			name:       "no placeholders",
			tmpl:       "https://example.com/health",
			dimensions: map[string][]string{},
		},
		{
			name: "query string placeholder",
			tmpl: "https://example.com/health?region={{.region}}",
			dimensions: map[string][]string{
				"region": {"us-east"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := urltmpl.Validate(tt.tmpl, tt.dimensions); err != nil {
				t.Errorf("Validate(%q) unexpected error: %v", tt.tmpl, err)
			}
		})
	}
}

func TestValidate_BadSyntax(t *testing.T) {
	dims := map[string][]string{"env": {"prod"}}
	tests := []struct {
		name string
		tmpl string
	}{
		{
			name: "conditional",
			tmpl: `https://{{if .secure}}secure{{else}}api{{end}}.example.com`,
		},
		{
			name: "pipe function",
			tmpl: `https://{{.env | upper}}.example.com`,
		},
		{
			name: "index function",
			tmpl: `https://{{index . "my.key"}}.example.com`,
		},
		{
			name: "unbalanced open",
			tmpl: `https://{{.env}.example.com`,
		},
		{
			name: "unbalanced close",
			tmpl: `https://env}}.example.com`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := urltmpl.Validate(tt.tmpl, dims); err == nil {
				t.Errorf("Validate(%q) expected error, got nil", tt.tmpl)
			}
		})
	}
}

func TestValidate_MissingKey(t *testing.T) {
	err := urltmpl.Validate(
		"https://{{.env}}.example.com/{{.svc}}/health",
		map[string][]string{"env": {"prod"}},
	)
	if err == nil {
		t.Fatal("Validate() expected error for undefined dimension, got nil")
	}
	if !strings.Contains(err.Error(), "svc") {
		t.Errorf("error = %q, want to contain 'svc'", err.Error())
	}
}

func TestValidate_EmptyTemplate(t *testing.T) {
	dims := map[string][]string{"env": {"prod"}}
	if err := urltmpl.Validate("", dims); err != nil {
		t.Errorf("Validate(\"\") unexpected error: %v", err)
	}
}

func TestValidate_EmptyDimensions(t *testing.T) {
	err := urltmpl.Validate(
		"https://{{.env}}.example.com/health",
		map[string][]string{},
	)
	if err == nil {
		t.Fatal("Validate() expected error for undefined key with empty dimensions, got nil")
	}
}

// =============================================================================
// EncodeMap
// =============================================================================

func TestEncodeMap_SpecialChars(t *testing.T) {
	got := urltmpl.EncodeMap(map[string]string{
		"a": "hello world",
		"b": "us/east",
		"c": "{{.evil}}",
	})

	tests := []struct {
		key  string
		want string
	}{
		{"a", "hello+world"},
		{"b", "us%2Feast"},
		{"c", "%7B%7B.evil%7D%7D"},
	}

	for _, tt := range tests {
		if got[tt.key] != tt.want {
			t.Errorf("EncodeMap()[%q] = %q, want %q", tt.key, got[tt.key], tt.want)
		}
	}
}

func TestEncodeMap_Empty(t *testing.T) {
	got := urltmpl.EncodeMap(map[string]string{})
	if len(got) != 0 {
		t.Errorf("EncodeMap({}) = %v, want empty map", got)
	}
}

func TestEncodeMap_AlreadyEncoded(t *testing.T) {
	// Already-encoded values are double-encoded — this is documented behaviour.
	// Callers are responsible for passing raw (unencoded) values.
	got := urltmpl.EncodeMap(map[string]string{
		"key": "hello%20world",
	})
	want := "hello%2520world"
	if got["key"] != want {
		t.Errorf("EncodeMap()[%q] = %q, want %q (double-encoded)", "key", got["key"], want)
	}
}
