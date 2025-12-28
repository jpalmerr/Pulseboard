package config

import (
	"bytes"
	"fmt"
	"sort"
	"text/template"

	"github.com/jpalmerr/pulseboard"
)

// BuildEndpoints converts parsed configuration into SDK Endpoint objects.
//
// It processes both direct endpoints and grids, returning a combined slice.
// Grid dimensions are expanded via cartesian product.
func BuildEndpoints(cfg *Config) ([]pulseboard.Endpoint, error) {
	var endpoints []pulseboard.Endpoint

	// convert direct endpoints
	for _, ec := range cfg.Endpoints {
		ep, err := buildEndpoint(ec)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, ep)
	}

	// convert grids (cartesian product expansion)
	for _, gc := range cfg.Grids {
		gridEndpoints, err := buildGridEndpoints(gc)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, gridEndpoints...)
	}

	return endpoints, nil
}

// buildEndpoint converts a single EndpointConfig to an SDK Endpoint.
func buildEndpoint(ec EndpointConfig) (pulseboard.Endpoint, error) {
	var opts []pulseboard.EndpointOption

	if ec.Method != "" {
		opts = append(opts, pulseboard.WithMethod(ec.Method))
	}

	if ec.Timeout != 0 {
		opts = append(opts, pulseboard.WithTimeout(ec.Timeout.Duration()))
	}

	if len(ec.Headers) > 0 {
		opts = append(opts, pulseboard.WithHeaders(mapToKeyValuePairs(ec.Headers)...))
	}

	if len(ec.Labels) > 0 {
		opts = append(opts, pulseboard.WithLabels(mapToKeyValuePairs(ec.Labels)...))
	}

	extractor := buildExtractor(ec.Extractor)
	if extractor != nil {
		opts = append(opts, pulseboard.WithExtractor(extractor))
	}

	if ec.Interval != 0 {
		opts = append(opts, pulseboard.WithInterval(ec.Interval.Duration()))
	}

	return pulseboard.NewEndpoint(ec.Name, ec.URL, opts...)
}

// mapToKeyValuePairs converts a map to a sorted slice of key-value pairs.
func mapToKeyValuePairs(m map[string]string) []string {
	// sort keys for deterministic ordering
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(m)*2)
	for _, k := range keys {
		pairs = append(pairs, k, m[k])
	}
	return pairs
}

// buildGridEndpoints expands a GridConfig into multiple endpoints via cartesian product.
func buildGridEndpoints(gc GridConfig) ([]pulseboard.Endpoint, error) {
	// use missingkey=error to fail fast on missing template variables
	tmpl, err := template.New("url").Option("missingkey=error").Parse(gc.URLTemplate)
	if err != nil {
		return nil, err
	}

	// generate all dimension combinations
	combinations := cartesianProduct(gc.Dimensions)

	var endpoints []pulseboard.Endpoint
	for _, combo := range combinations {
		// execute template with this combination
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, combo); err != nil {
			return nil, fmt.Errorf("grid (%s) with dimensions %v: template execution failed: %w", gc.Name, combo, err)
		}
		url := buf.String()

		// build name from combination values
		name := buildGridName(gc.Name, combo)

		// merge grid labels with dimension labels
		labels := make(map[string]string)
		for k, v := range gc.Labels {
			labels[k] = v
		}
		for k, v := range combo {
			labels[k] = v
		}

		// build endpoint config for this combination
		ec := EndpointConfig{
			Name:      name,
			URL:       url,
			Method:    gc.Method,
			Timeout:   gc.Timeout,
			Headers:   gc.Headers,
			Labels:    labels,
			Extractor: gc.Extractor,
			Interval:  gc.Interval,
		}

		ep, err := buildEndpoint(ec)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, ep)
	}

	return endpoints, nil
}

// buildGridName creates a display name for a grid endpoint.
func buildGridName(baseName string, combo map[string]string) string {
	// sort keys for deterministic ordering
	keys := make([]string, 0, len(combo))
	for k := range combo {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	name := baseName
	for _, k := range keys {
		name += " " + combo[k]
	}
	return name
}

// cartesianProduct generates all combinations of dimension values.
func cartesianProduct(dimensions map[string][]string) []map[string]string {
	if len(dimensions) == 0 {
		return nil
	}

	// sort dimension keys for deterministic ordering
	keys := make([]string, 0, len(dimensions))
	for k := range dimensions {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// start with single empty combination
	result := []map[string]string{{}}

	for _, key := range keys {
		values := dimensions[key]
		var newResult []map[string]string

		for _, combo := range result {
			for _, val := range values {
				// copy existing combo and add new dimension
				newCombo := make(map[string]string)
				for k, v := range combo {
					newCombo[k] = v
				}
				newCombo[key] = val
				newResult = append(newResult, newCombo)
			}
		}
		result = newResult
	}

	return result
}

// buildExtractor converts ExtractorConfig to a StatusExtractor function.
// Returns nil for default/empty extractors (SDK uses DefaultExtractor).
func buildExtractor(ec ExtractorConfig) pulseboard.StatusExtractor {
	switch ec.Type {
	case "", "default":
		// nil signals SDK to use DefaultExtractor
		return nil
	case "http":
		return pulseboard.HTTPStatusExtractor
	case "json":
		return pulseboard.JSONFieldExtractor(ec.Path)
	case "contains":
		return pulseboard.ContainsExtractor(ec.Text)
	default:
		// validation should catch this, but return nil as fallback
		return nil
	}
}
