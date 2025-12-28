package pulseboard

import (
	"testing"
)

func TestHTTPStatusExtractor(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       Status
	}{
		// 2xx = up
		{"200 OK", 200, StatusUp},
		{"201 Created", 201, StatusUp},
		{"204 No Content", 204, StatusUp},
		{"299 edge case", 299, StatusUp},

		// 4xx = degraded
		{"400 Bad Request", 400, StatusDegraded},
		{"401 Unauthorized", 401, StatusDegraded},
		{"404 Not Found", 404, StatusDegraded},
		{"499 edge case", 499, StatusDegraded},

		// 5xx = down
		{"500 Internal Server Error", 500, StatusDown},
		{"502 Bad Gateway", 502, StatusDown},
		{"503 Service Unavailable", 503, StatusDown},

		// other = down
		{"0 no response", 0, StatusDown},
		{"100 Continue", 100, StatusDown},
		{"301 Redirect", 301, StatusDown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HTTPStatusExtractor(nil, tt.statusCode)
			if got != tt.want {
				t.Errorf("HTTPStatusExtractor(%d) = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestJSONFieldExtractor(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		want Status
	}{
		// simple field
		{"status ok", "status", `{"status": "ok"}`, StatusUp},
		{"status healthy", "status", `{"status": "healthy"}`, StatusUp},
		{"status up", "status", `{"status": "up"}`, StatusUp},
		{"status active", "status", `{"status": "active"}`, StatusUp},
		{"status running", "status", `{"status": "running"}`, StatusUp},
		{"status pass", "status", `{"status": "pass"}`, StatusUp},
		{"status passed", "status", `{"status": "passed"}`, StatusUp},
		{"status green", "status", `{"status": "green"}`, StatusUp},

		// degraded statuses
		{"status degraded", "status", `{"status": "degraded"}`, StatusDegraded},
		{"status warning", "status", `{"status": "warning"}`, StatusDegraded},
		{"status partial", "status", `{"status": "partial"}`, StatusDegraded},
		{"status yellow", "status", `{"status": "yellow"}`, StatusDegraded},
		{"status amber", "status", `{"status": "amber"}`, StatusDegraded},

		// down statuses
		{"status down", "status", `{"status": "down"}`, StatusDown},
		{"status error", "status", `{"status": "error"}`, StatusDown},
		{"status failed", "status", `{"status": "failed"}`, StatusDown},
		{"status unknown value", "status", `{"status": "something_else"}`, StatusDown},

		// nested paths
		{"nested data.status", "data.status", `{"data": {"status": "ok"}}`, StatusUp},
		{"deeply nested", "a.b.c.status", `{"a": {"b": {"c": {"status": "healthy"}}}}`, StatusUp},

		// boolean values
		{"boolean true", "healthy", `{"healthy": true}`, StatusUp},
		{"boolean false", "healthy", `{"healthy": false}`, StatusDown},

		// numeric values - 0 and 1 treated as boolean-like
		{"numeric 1", "status", `{"status": 1}`, StatusUp},
		{"numeric 0", "status", `{"status": 0}`, StatusDown},

		// numeric values - other numbers convert to string (unmapped → Down per default)
		{"numeric 100", "status", `{"status": 100}`, StatusDown},
		{"numeric 200", "status", `{"status": 200}`, StatusDown},
		{"numeric float 0.5", "status", `{"status": 0.5}`, StatusDown},
		{"numeric float 99.9", "status", `{"status": 99.9}`, StatusDown},
		{"numeric negative", "status", `{"status": -1}`, StatusDown},

		// case insensitive
		{"uppercase OK", "status", `{"status": "OK"}`, StatusUp},
		{"mixed case Healthy", "status", `{"status": "Healthy"}`, StatusUp},

		// missing field
		{"missing field", "status", `{"other": "value"}`, StatusUnknown},
		{"missing nested", "data.status", `{"data": {"other": "value"}}`, StatusUnknown},

		// invalid JSON
		{"invalid json", "status", `not json`, StatusUnknown},
		{"empty body", "status", ``, StatusUnknown},

		// wrong type at path
		{"array at path", "status", `{"status": ["a", "b"]}`, StatusUnknown},
		{"object at path", "status", `{"status": {"nested": "value"}}`, StatusUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor := JSONFieldExtractor(tt.path)
			got := extractor([]byte(tt.body), 200)
			if got != tt.want {
				t.Errorf("JSONFieldExtractor(%q)(%q) = %v, want %v", tt.path, tt.body, got, tt.want)
			}
		})
	}
}

func TestRegexExtractor(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		upMatch string
		body    string
		want    Status
	}{
		// basic matching
		{"matches ok", `"status":\s*"(\w+)"`, "ok", `{"status": "ok"}`, StatusUp},
		{"matches healthy", `"status":\s*"(\w+)"`, "healthy", `{"status": "healthy"}`, StatusUp},
		{"no match", `"status":\s*"(\w+)"`, "ok", `{"status": "down"}`, StatusDown},

		// case insensitive match
		{"case insensitive", `"status":\s*"(\w+)"`, "OK", `{"status": "ok"}`, StatusUp},

		// no capture group match
		{"no capture match", `"status":\s*"(\w+)"`, "ok", `no status here`, StatusUnknown},

		// XML-style
		{"xml status", `<status>(\w+)</status>`, "healthy", `<status>healthy</status>`, StatusUp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor, err := RegexExtractor(tt.pattern, tt.upMatch)
			if err != nil {
				t.Fatalf("RegexExtractor() error = %v", err)
			}
			got := extractor([]byte(tt.body), 200)
			if got != tt.want {
				t.Errorf("RegexExtractor(%q, %q)(%q) = %v, want %v", tt.pattern, tt.upMatch, tt.body, got, tt.want)
			}
		})
	}
}

func TestRegexExtractor_InvalidPattern(t *testing.T) {
	_, err := RegexExtractor(`[invalid`, "ok")
	if err == nil {
		t.Error("RegexExtractor() expected error for invalid pattern, got nil")
	}
}

func TestMustRegexExtractor_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustRegexExtractor() expected panic for invalid pattern")
		}
	}()

	MustRegexExtractor(`[invalid`, "ok")
}

func TestMustRegexExtractor_Valid(t *testing.T) {
	// should not panic
	extractor := MustRegexExtractor(`"status":\s*"(\w+)"`, "ok")
	got := extractor([]byte(`{"status": "ok"}`), 200)
	if got != StatusUp {
		t.Errorf("MustRegexExtractor() = %v, want %v", got, StatusUp)
	}
}

func TestFirstMatch(t *testing.T) {
	// extractor that always returns unknown
	unknownExtractor := func(body []byte, statusCode int) Status {
		return StatusUnknown
	}

	// extractor that always returns up
	upExtractor := func(body []byte, statusCode int) Status {
		return StatusUp
	}

	// extractor that always returns down
	downExtractor := func(body []byte, statusCode int) Status {
		return StatusDown
	}

	tests := []struct {
		name       string
		extractors []StatusExtractor
		want       Status
	}{
		{"first returns up", []StatusExtractor{upExtractor, downExtractor}, StatusUp},
		{"first unknown, second up", []StatusExtractor{unknownExtractor, upExtractor}, StatusUp},
		{"all unknown", []StatusExtractor{unknownExtractor, unknownExtractor}, StatusUnknown},
		{"first unknown, second down", []StatusExtractor{unknownExtractor, downExtractor}, StatusDown},
		{"empty extractors", []StatusExtractor{}, StatusUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor := FirstMatch(tt.extractors...)
			got := extractor(nil, 200)
			if got != tt.want {
				t.Errorf("FirstMatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContainsExtractor(t *testing.T) {
	tests := []struct {
		name string
		text string
		body string
		want Status
	}{
		// basic matching
		{"contains ok", "ok", "status: ok", StatusUp},
		{"contains healthy", "healthy", "the service is healthy", StatusUp},
		{"does not contain", "healthy", "the service is down", StatusDown},

		// case insensitive
		{"case insensitive upper", "OK", "status: ok", StatusUp},
		{"case insensitive lower", "ok", "STATUS: OK", StatusUp},
		{"case insensitive mixed", "HeAlThY", "The service is HEALTHY", StatusUp},

		// partial match
		{"partial word match", "health", "healthy service", StatusUp},
		{"substring match", "ok", "looking good", StatusUp},

		// empty cases
		{"empty body", "ok", "", StatusDown},
		{"empty search finds nothing", "", "some content", StatusUp}, // empty string is always found

		// special characters
		{"with newlines", "ok", "line1\nok\nline3", StatusUp},
		{"json body", "ok", `{"status": "ok"}`, StatusUp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor := ContainsExtractor(tt.text)
			got := extractor([]byte(tt.body), 200)
			if got != tt.want {
				t.Errorf("ContainsExtractor(%q)(%q) = %v, want %v", tt.text, tt.body, got, tt.want)
			}
		})
	}
}

func TestDefaultExtractor(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		statusCode int
		want       Status
	}{
		// JSON status field takes precedence
		{"json ok with 200", `{"status": "ok"}`, 200, StatusUp},
		{"json ok with 500", `{"status": "ok"}`, 500, StatusUp}, // JSON wins
		{"json down with 200", `{"status": "down"}`, 200, StatusDown},

		// falls back to HTTP status when no JSON status field
		{"no json, 200", `{"other": "field"}`, 200, StatusUp},
		{"no json, 500", `{"other": "field"}`, 500, StatusDown},
		{"invalid json, 200", `not json`, 200, StatusUp},
		{"invalid json, 503", `not json`, 503, StatusDown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefaultExtractor([]byte(tt.body), tt.statusCode)
			if got != tt.want {
				t.Errorf("DefaultExtractor(%q, %d) = %v, want %v", tt.body, tt.statusCode, got, tt.want)
			}
		})
	}
}
