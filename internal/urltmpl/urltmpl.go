// Package urltmpl provides safe URL placeholder expansion using {{.key}} syntax.
// It is a strict subset of Go's text/template: only simple {{.key}} placeholders
// are supported. Conditionals, pipelines, loops, and functions are not.
package urltmpl

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// allBraces matches any {{ ... }} sequence (non-greedy within the constraint
// that [^}]* cannot cross a } character).
var allBraces = regexp.MustCompile(`\{\{[^}]*\}\}`)

// validPlaceholder matches exactly {{.wordchars}}.
var validPlaceholder = regexp.MustCompile(`^\{\{\.(\w+)\}\}$`)

// Expand replaces {{.key}} placeholders in urlTemplate with the corresponding
// values from values. Values must already be URL-encoded by the caller (see
// EncodeMap).
//
// Returns an error if any {{...}} sequence remains after substitution, including:
//   - a placeholder whose key is not in values
//   - any {{ without a matching }}
//
// Injection safety: values are treated as literals, never re-evaluated as templates.
func Expand(urlTemplate string, values map[string]string) (string, error) {
	result := urlTemplate
	for key, val := range values {
		result = strings.ReplaceAll(result, "{{."+key+"}}", val)
	}
	// Any remaining {{ means an unresolved placeholder or unbalanced brace.
	if idx := strings.Index(result, "{{"); idx != -1 {
		end := strings.Index(result[idx:], "}}")
		if end != -1 {
			return "", fmt.Errorf("unresolved placeholder %q: key not found in dimension values",
				result[idx:idx+end+2])
		}
		// {{ without matching }} — unbalanced
		return "", fmt.Errorf("unresolved placeholder: unmatched {{ at position %d", idx)
	}
	return result, nil
}

// Validate verifies that every {{...}} sequence in urlTemplate is a valid
// {{.key}} placeholder and that every referenced key exists in dimensions.
//
// Checks in order:
//  1. {{ count must equal }} count (balanced)
//  2. Every {{ ... }} must match exactly {{.key}} where key is \w+
//  3. Every referenced key must exist in dimensions
func Validate(urlTemplate string, dimensions map[string][]string) error {
	if strings.Count(urlTemplate, "{{") != strings.Count(urlTemplate, "}}") {
		return fmt.Errorf("unbalanced placeholders in url_template")
	}

	for _, match := range allBraces.FindAllString(urlTemplate, -1) {
		sub := validPlaceholder.FindStringSubmatch(match)
		if sub == nil {
			return fmt.Errorf("unsupported template syntax %q: only {{.key}} placeholders are supported", match)
		}
		key := sub[1]
		if _, exists := dimensions[key]; !exists {
			return fmt.Errorf("url_template references undefined dimension %q", key)
		}
	}
	return nil
}

// EncodeMap returns a new map with all values URL-encoded using url.QueryEscape.
// The returned map is safe to pass to Expand.
//
// Note: QueryEscape encodes spaces as '+' which is correct for query parameters
// but technically incorrect for path segments (where %20 is preferred).
// This matches the existing behaviour of the pulseboard grid expander.
func EncodeMap(m map[string]string) map[string]string {
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = url.QueryEscape(v)
	}
	return result
}
