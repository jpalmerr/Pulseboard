package pulseboard

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// HTTPStatusExtractor is a [StatusExtractor] that determines status from
// the HTTP status code alone, ignoring the response body.
//
// Status mapping:
//   - 2xx (200-299): [StatusUp]
//   - 4xx (400-499): [StatusDegraded]
//   - All other codes: [StatusDown]
//
// This is useful for simple health endpoints that return 200 OK when healthy.
var HTTPStatusExtractor StatusExtractor = func(body []byte, statusCode int) Status {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return StatusUp
	case statusCode >= 400 && statusCode < 500:
		return StatusDegraded
	default:
		return StatusDown
	}
}

// JSONFieldExtractor returns a [StatusExtractor] that extracts status from
// a JSON field using dot notation to navigate nested objects.
//
// The path parameter specifies the field to extract using dot notation.
// For example, "data.health.status" navigates to {"data": {"health": {"status": "ok"}}}.
//
// The extracted value is mapped to a [Status] using common health check conventions:
//   - [StatusUp]: "ok", "healthy", "up", "active", "running", "pass", "passed", "true", "green", "none", "operational"
//   - [StatusDegraded]: "degraded", "warning", "partial", "yellow", "amber"
//   - [StatusDown]: any other value
//   - [StatusUnknown]: if JSON parsing fails or the field doesn't exist
//
// Boolean and numeric values are converted: true/1 → "true", false/0 → "false".
//
// Example:
//
//	// For response: {"data": {"status": "healthy"}}
//	extractor := pulseboard.JSONFieldExtractor("data.status")
func JSONFieldExtractor(path string) StatusExtractor {
	parts := strings.Split(path, ".")

	return func(body []byte, statusCode int) Status {
		var data interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			return StatusUnknown
		}

		value := extractJSONPath(data, parts)
		if value == "" {
			return StatusUnknown
		}

		return mapStringToStatus(strings.ToLower(value))
	}
}

// extractJSONPath walks a JSON structure using dot notation parts.
func extractJSONPath(data interface{}, parts []string) string {
	current := data

	for _, part := range parts {
		obj, ok := current.(map[string]interface{})
		if !ok {
			return ""
		}
		current, ok = obj[part]
		if !ok {
			return ""
		}
	}

	switch v := current.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if v == 0 {
			return "false"
		}
		if v == 1 {
			return "true"
		}
		// convert other numbers to string representation
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

// mapStringToStatus maps common status strings to Status values.
func mapStringToStatus(s string) Status {
	switch s {
	case "ok", "healthy", "up", "active", "running", "pass", "passed", "true", "green", "none", "operational":
		return StatusUp
	case "degraded", "warning", "partial", "yellow", "amber":
		return StatusDegraded
	default:
		return StatusDown
	}
}

// RegexExtractor returns a [StatusExtractor] that matches the response body
// against a regular expression pattern.
//
// The pattern must contain at least one capture group. The first capture group
// is compared (case-insensitively) against the upMatch string:
//   - If equal: [StatusUp]
//   - If not equal: [StatusDown]
//   - If no match found: [StatusUnknown]
//
// Returns an error if the pattern is invalid.
//
// Example:
//
//	// Match {"status": "ok"} or {"status": "healthy"}
//	extractor, err := pulseboard.RegexExtractor(`"status":\s*"(\w+)"`, "ok")
func RegexExtractor(pattern string, upMatch string) (StatusExtractor, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	return func(body []byte, statusCode int) Status {
		matches := re.FindSubmatch(body)
		if len(matches) < 2 {
			return StatusUnknown
		}

		captured := string(matches[1])
		if strings.EqualFold(captured, upMatch) {
			return StatusUp
		}
		return StatusDown
	}, nil
}

// MustRegexExtractor is like [RegexExtractor] but panics if the pattern
// is invalid.
//
// Use this for compile-time constant patterns where you want to fail fast
// on invalid regex. For runtime patterns, use [RegexExtractor] instead.
//
// Example:
//
//	var statusExtractor = pulseboard.MustRegexExtractor(`"health":\s*"(\w+)"`, "good")
func MustRegexExtractor(pattern string, upMatch string) StatusExtractor {
	extractor, err := RegexExtractor(pattern, upMatch)
	if err != nil {
		panic("pulseboard: invalid regex pattern: " + err.Error())
	}
	return extractor
}

// FirstMatch returns a [StatusExtractor] that tries multiple extractors in
// order, returning the first result that is not [StatusUnknown].
//
// This is useful for composing extractors with fallback behavior. Each
// extractor is tried in sequence until one returns a definitive status.
//
// If all extractors return [StatusUnknown], FirstMatch returns [StatusUnknown].
//
// Example:
//
//	// Try JSON field first, fall back to HTTP status code
//	extractor := pulseboard.FirstMatch(
//	    pulseboard.JSONFieldExtractor("health.status"),
//	    pulseboard.HTTPStatusExtractor,
//	)
func FirstMatch(extractors ...StatusExtractor) StatusExtractor {
	return func(body []byte, statusCode int) Status {
		for _, extractor := range extractors {
			status := extractor(body, statusCode)
			if status != StatusUnknown {
				return status
			}
		}
		return StatusUnknown
	}
}

// ContainsExtractor returns a [StatusExtractor] that checks if the response
// body contains the specified text (case-insensitive).
//
// Status mapping:
//   - [StatusUp]: body contains the text (case-insensitive)
//   - [StatusDown]: body does not contain the text
//
// This is useful for simple health endpoints that return plain text like "OK"
// or "healthy" without JSON structure.
//
// Example:
//
//	// Check if response contains "healthy"
//	extractor := pulseboard.ContainsExtractor("healthy")
func ContainsExtractor(text string) StatusExtractor {
	lower := strings.ToLower(text)
	return func(body []byte, statusCode int) Status {
		if strings.Contains(strings.ToLower(string(body)), lower) {
			return StatusUp
		}
		return StatusDown
	}
}

// DefaultExtractor is the [StatusExtractor] used when no extractor is
// specified on an [Endpoint].
//
// DefaultExtractor uses [FirstMatch] to try:
//  1. [JSONFieldExtractor] with path "status" (for JSON responses with a status field)
//  2. [HTTPStatusExtractor] (falls back to HTTP status code)
//
// This provides sensible default behavior for most health check endpoints.
var DefaultExtractor = FirstMatch(
	JSONFieldExtractor("status"),
	HTTPStatusExtractor,
)
