package config

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/jpalmerr/pulseboard"
	"github.com/jpalmerr/pulseboard/internal/urltmpl"
)

// BuildServerTLSOptions returns the SDK options for server TLS configuration.
// Returns nil slice if no server TLS is configured.
func BuildServerTLSOptions(cfg *Config) []pulseboard.Option {
	if cfg.Server == nil || cfg.Server.TLS == nil {
		return nil
	}
	t := cfg.Server.TLS
	if t.CertFile == "" || t.KeyFile == "" {
		return nil
	}
	return []pulseboard.Option{
		pulseboard.WithTLS(t.CertFile, t.KeyFile),
	}
}

// BuildClientTLSOptions returns the SDK options for client TLS configuration.
// Returns nil slice if no client TLS is configured.
func BuildClientTLSOptions(cfg *Config) ([]pulseboard.Option, error) {
	if cfg.Client == nil || cfg.Client.TLS == nil {
		return nil, nil
	}
	t := cfg.Client.TLS
	var opts []pulseboard.Option

	if t.InsecureSkipVerify {
		opts = append(opts, pulseboard.WithInsecureSkipVerify())
	}

	if t.MinVersion != "" {
		version, err := parseTLSVersion(t.MinVersion)
		if err != nil {
			// already validated during Parse, but be defensive
			return nil, fmt.Errorf("client.tls.min_version: %w", err)
		}
		opts = append(opts, pulseboard.WithTLSMinVersion(version))
	}

	if t.ClientCert != "" {
		opts = append(opts, pulseboard.WithClientCert(t.ClientCert, t.ClientKey))
	}

	return opts, nil
}

// BuildEndpoints converts parsed configuration into SDK Endpoint objects.
//
// It processes both direct endpoints and grids, returning a combined slice.
// Grid dimensions are expanded via cartesian product.
func BuildEndpoints(cfg *Config) ([]pulseboard.Endpoint, error) {
	var endpoints []pulseboard.Endpoint

	for _, ec := range cfg.Endpoints {
		ep, err := buildEndpoint(ec)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, ep)
	}

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
	if err := urltmpl.Validate(gc.URLTemplate, gc.Dimensions); err != nil {
		return nil, fmt.Errorf("grids[%s]: invalid url_template: %w", gc.Name, err)
	}

	combinations := cartesianProduct(gc.Dimensions)

	var endpoints []pulseboard.Endpoint
	for _, combo := range combinations {
		encoded := urlEncodeMap(combo)
		urlStr, err := urltmpl.Expand(gc.URLTemplate, encoded)
		if err != nil {
			return nil, fmt.Errorf("grid (%s) with dimensions %v: template expansion failed: %w", gc.Name, combo, err)
		}
		name := buildGridName(gc.Name, combo)

		// grid labels first, dimension values added on top
		labels := make(map[string]string)
		for k, v := range gc.Labels {
			labels[k] = v
		}
		for k, v := range combo {
			labels[k] = v
		}

		ec := EndpointConfig{
			Name:      name,
			URL:       urlStr,
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

// urlEncodeMap returns a new map with all values URL-encoded.
// NOTE: the original buildGridEndpoints did not URL-encode values — this
// fixes that pre-existing bug. Values like "us east" now produce correct URLs.
func urlEncodeMap(m map[string]string) map[string]string {
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = url.QueryEscape(v)
	}
	return result
}

// buildGridName creates a display name for a grid endpoint.
func buildGridName(baseName string, combo map[string]string) string {
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

	keys := make([]string, 0, len(dimensions))
	for k := range dimensions {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := []map[string]string{{}}

	for _, key := range keys {
		values := dimensions[key]
		var newResult []map[string]string

		for _, combo := range result {
			for _, val := range values {
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

// BuildAuthMiddleware converts an AuthConfig into a middleware function.
//
// Returns nil if auth is nil (no authentication configured).
// Validation is expected to have been run before calling this function —
// unknown types and missing credentials should be caught at parse time.
//
// Username and password comparisons use constant-time equality to prevent
// timing side-channels.
func BuildAuthMiddleware(auth *AuthConfig) func(http.Handler) http.Handler {
	if auth == nil {
		return nil
	}

	switch auth.Type {
	case "basic":
		wantUser := []byte(auth.Username)
		wantPass := []byte(auth.Password)
		return pulseboard.BasicAuth(func(u, p string) bool {
			userOK := subtle.ConstantTimeCompare([]byte(u), wantUser)
			passOK := subtle.ConstantTimeCompare([]byte(p), wantPass)
			return (userOK & passOK) == 1
		})
	case "bearer":
		tokens := auth.Tokens
		if len(tokens) == 0 && auth.Token != "" {
			tokens = []string{auth.Token}
		}
		return pulseboard.BearerToken(tokens...)
	default:
		return nil
	}
}

// BuildWebhookOptions returns PulseBoard options for all configured webhooks.
// Returns nil if no webhooks are configured.
func BuildWebhookOptions(cfg *Config) []pulseboard.Option {
	if len(cfg.Webhooks) == 0 {
		return nil
	}

	opts := make([]pulseboard.Option, 0, len(cfg.Webhooks))
	for _, wh := range cfg.Webhooks {
		var webhookOpts []pulseboard.WebhookOption

		if len(wh.Events) > 0 {
			webhookOpts = append(webhookOpts, pulseboard.WithWebhookEventFilter(wh.Events...))
		}
		if len(wh.Headers) > 0 {
			webhookOpts = append(webhookOpts, pulseboard.WithWebhookHeaders(wh.Headers))
		}
		if wh.Timeout > 0 {
			webhookOpts = append(webhookOpts,
				pulseboard.WithWebhookTimeout(time.Duration(wh.Timeout)*time.Second))
		}
		if wh.Debounce > 0 {
			webhookOpts = append(webhookOpts,
				pulseboard.WithWebhookDebounce(time.Duration(wh.Debounce)*time.Second))
		}

		notifier := pulseboard.WebhookNotifier(wh.URL, webhookOpts...)
		opts = append(opts, pulseboard.WithStatusChangeCallback(notifier))
	}
	return opts
}

// BuildMetricsOption returns the WithMetrics option when metrics are enabled.
// Returns nil if the metrics section is absent or Enabled is false.
func BuildMetricsOption(cfg *Config) []pulseboard.Option {
	if cfg.Metrics == nil || !cfg.Metrics.Enabled {
		return nil
	}
	return []pulseboard.Option{pulseboard.WithMetrics()}
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
