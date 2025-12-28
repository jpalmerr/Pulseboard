package pulseboard

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"text/template"
)

// NewEndpointGrid creates multiple endpoints from a URL template and dimensions
// using cartesian product expansion.
//
// The URL template uses Go's text/template syntax. Dimension values are URL-encoded
// before interpolation. Missing template keys cause an error (fail-fast).
//
// Each endpoint name includes dimension values in the format:
// "Base Name (val1/val2)" (values from alphabetically sorted keys).
//
// Labels are automatically added from dimension values. Static labels from
// [WithGridLabels] take precedence over dimension labels on collision.
//
// Example:
//
//	endpoints, err := NewEndpointGrid("API Health",
//	    WithURLTemplate("https://api.com/health?region={{.region}}"),
//	    WithDimensions(map[string][]string{
//	        "region": {"us-east", "eu-west"},
//	    }),
//	)
//	// Returns 2 endpoints, usable with WithEndpoints(endpoints...)
func NewEndpointGrid(baseName string, opts ...GridOption) ([]Endpoint, error) {
	// validate base name
	if strings.TrimSpace(baseName) == "" {
		return nil, errors.New("base name cannot be empty")
	}

	// initialise config with empty maps
	cfg := &gridConfig{
		staticLabels: make(map[string]string),
		headers:      make(map[string]string),
	}

	// apply options
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}

	// validate required fields
	if cfg.urlTemplate == "" {
		return nil, errors.New("URL template required")
	}
	if len(cfg.dimensions) == 0 {
		return nil, errors.New("at least one dimension required")
	}

	// parse template with missingkey=error for fail-fast behaviour
	tmpl, err := template.New("url").Option("missingkey=error").Parse(cfg.urlTemplate)
	if err != nil {
		return nil, fmt.Errorf("invalid URL template: %w", err)
	}

	// generate combinations
	combinations := cartesianProduct(cfg.dimensions)
	if len(combinations) == 0 {
		return nil, nil
	}

	// create endpoints
	endpoints := make([]Endpoint, 0, len(combinations))
	for _, combo := range combinations {
		// URL-encode values for template, keep original for labels
		encoded := urlEncodeMap(combo)

		urlStr, err := executeTemplate(tmpl, encoded)
		if err != nil {
			return nil, fmt.Errorf("template execution failed: %w", err)
		}

		name := formatEndpointName(baseName, combo)

		// merge labels: dimension first, static overrides
		labels := mergeMaps(combo, cfg.staticLabels)

		// build endpoint options
		epOpts := []EndpointOption{
			WithLabels(flattenMap(labels)...),
		}
		if len(cfg.headers) > 0 {
			epOpts = append(epOpts, WithHeaders(flattenMap(cfg.headers)...))
		}
		if cfg.timeout > 0 {
			epOpts = append(epOpts, WithTimeout(cfg.timeout))
		}
		if cfg.extractor != nil {
			epOpts = append(epOpts, WithExtractor(cfg.extractor))
		}
		if cfg.method != "" {
			epOpts = append(epOpts, WithMethod(cfg.method))
		}
		if cfg.interval > 0 {
			epOpts = append(epOpts, WithInterval(cfg.interval))
		}

		ep, err := NewEndpoint(name, urlStr, epOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create endpoint '%s': %w", name, err)
		}
		endpoints = append(endpoints, ep)
	}

	return endpoints, nil
}

// cartesianProduct generates all combinations of dimension values.
// Keys are sorted alphabetically for deterministic output.
// Values maintain their original slice order.
//
// Example:
//
//	Input:  {"x": ["a","b"], "y": ["1","2"]}
//	Output: [{"x":"a","y":"1"}, {"x":"a","y":"2"}, {"x":"b","y":"1"}, {"x":"b","y":"2"}]
func cartesianProduct(dims map[string][]string) []map[string]string {
	if len(dims) == 0 {
		return nil
	}

	// sort keys for deterministic iteration
	keys := make([]string, 0, len(dims))
	for k := range dims {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// defensive check for empty dimensions (also validated in WithDimensions)
	for _, k := range keys {
		if len(dims[k]) == 0 {
			return nil
		}
	}

	// calculate total combinations
	total := 1
	for _, k := range keys {
		total *= len(dims[k])
	}

	result := make([]map[string]string, 0, total)

	// cartesian product
	indices := make([]int, len(keys))
	for {
		// combo is like our position in grid
		combo := make(map[string]string, len(keys))
		for i, k := range keys {
			combo[k] = dims[k][indices[i]]
		}
		result = append(result, combo)

		// increment indices (rightmost first)
		for i := len(keys) - 1; i >= 0; i-- {
			indices[i]++
			if indices[i] < len(dims[keys[i]]) {
				break
			}
			indices[i] = 0
			if i == 0 {
				return result
			}
		}

	}
}

// urlEncodeMap returns a new map with all values URL-encoded.
func urlEncodeMap(m map[string]string) map[string]string {
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = url.QueryEscape(v)
	}
	return result
}

// executeTemplate renders the template with the given data.
func executeTemplate(tmpl *template.Template, data map[string]string) (string, error) {
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// formatEndpointName creates a name in the format "Base (v1/v2)".
// Values are ordered by sorted keys for consistent naming.
func formatEndpointName(baseName string, combo map[string]string) string {
	keys := make([]string, 0, len(combo))
	for k := range combo {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = combo[k]
	}
	return fmt.Sprintf("%s (%s)", baseName, strings.Join(parts, "/"))
}

// mergeMaps merges multiple maps, with later maps taking precedence.
func mergeMaps(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

// flattenMap converts a map to a slice of key-value pairs for variadic functions.
// Keys are sorted for deterministic output.
func flattenMap(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := make([]string, 0, len(m)*2)
	for _, k := range keys {
		result = append(result, k, m[k])
	}
	return result
}
