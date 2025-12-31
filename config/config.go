// Package config provides YAML configuration parsing for PulseBoard.
//
// This package enables running PulseBoard as a standalone binary with a
// configuration file, as an alternative to the programmatic SDK approach.
//
// Example configuration:
//
//	port: 8080
//	poll_interval: 10s
//
//	endpoints:
//	  - name: GitHub API
//	    url: https://api.github.com
//	    timeout: 5s
//	    extractor: json:status
//
//	grids:
//	  - name: Platform
//	    url_template: "https://{{.env}}.example.com/health"
//	    dimensions:
//	      env: [prod, staging]
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

// minPollInterval is the minimum allowed polling interval for production configs.
// This prevents accidental DoS of endpoints with overly aggressive polling.
const minPollInterval = 1 * time.Second

// Config is the root configuration structure for PulseBoard.
//
// It maps directly to the YAML configuration file structure.
// Use [Load] or [Parse] to create a Config from YAML.
type Config struct {
	// Title is the dashboard title. Defaults to "PulseBoard" if not set.
	Title string `yaml:"title"`

	// Port is the HTTP server port. Defaults to 8080.
	Port int `yaml:"port"`

	// PollInterval is the time between health check cycles.
	// Accepts duration strings like "10s", "1m", "500ms".
	// Defaults to 10s.
	PollInterval Duration `yaml:"poll_interval"`

	// Endpoints defines individual health check endpoints.
	Endpoints []EndpointConfig `yaml:"endpoints"`

	// Grids defines endpoint grids that expand via cartesian product.
	Grids []GridConfig `yaml:"grids"`
}

// EndpointConfig defines a single health check endpoint.
type EndpointConfig struct {
	// Name is the display name shown in the dashboard.
	Name string `yaml:"name"`

	// URL is the health check endpoint URL.
	// Supports environment variable substitution: ${VAR} or ${VAR:-default}
	URL string `yaml:"url"`

	// Method is the HTTP method (GET, HEAD, POST). Defaults to GET.
	Method string `yaml:"method"`

	// Timeout is the request timeout. Defaults to 10s.
	Timeout Duration `yaml:"timeout"`

	// Headers are custom HTTP headers sent with each request.
	// Values support environment variable substitution.
	Headers map[string]string `yaml:"headers"`

	// Labels are metadata key-value pairs for grouping/filtering.
	Labels map[string]string `yaml:"labels"`

	// Extractor determines how to interpret the response as a status.
	// Can be shorthand ("json:status", "contains:ok") or structured.
	Extractor ExtractorConfig `yaml:"extractor"`

	// Interval is the custom polling interval for this endpoint.
	// If not specified, uses the global poll_interval.
	// Must be between 1s and 1h.
	Interval Duration `yaml:"interval"`
}

// GridConfig defines an endpoint grid that expands via cartesian product.
//
// For example, with dimensions {env: [prod, staging], svc: [api, web]},
// the grid expands to 4 endpoints: prod/api, prod/web, staging/api, staging/web.
type GridConfig struct {
	// Name is the base name for generated endpoints.
	Name string `yaml:"name"`

	// URLTemplate is a Go template for generating endpoint URLs.
	// Dimension keys are available as template variables: {{.env}}, {{.svc}}
	// Supports environment variable substitution in the template.
	URLTemplate string `yaml:"url_template"`

	// Dimensions maps dimension names to their possible values.
	// The cartesian product of all dimensions generates the endpoints.
	Dimensions map[string][]string `yaml:"dimensions"`

	// Method is the HTTP method for all generated endpoints.
	Method string `yaml:"method"`

	// Timeout is the request timeout for all generated endpoints.
	Timeout Duration `yaml:"timeout"`

	// Headers are custom HTTP headers for all generated endpoints.
	Headers map[string]string `yaml:"headers"`

	// Labels are additional labels applied to all generated endpoints.
	// These are merged with auto-generated dimension labels.
	Labels map[string]string `yaml:"labels"`

	// Extractor determines how to interpret responses for all endpoints.
	Extractor ExtractorConfig `yaml:"extractor"`

	// Interval is the custom polling interval for all generated endpoints.
	// If not specified, uses the global poll_interval.
	// Must be between 1s and 1h.
	Interval Duration `yaml:"interval"`
}

// ExtractorConfig specifies how to determine health status from a response.
//
// It supports two formats in YAML:
//
// Shorthand string:
//
//	extractor: json:status
//	extractor: json:data.health.status
//	extractor: contains:ok
//	extractor: default
//
// Structured object:
//
//	extractor:
//	  type: json
//	  path: data.health.status
type ExtractorConfig struct {
	// Type is the extractor type: "default", "json", "contains", "http".
	Type string

	// Path is the JSON field path (for type: json).
	Path string

	// Text is the substring to search for (for type: contains).
	Text string
}

// Duration wraps time.Duration for YAML unmarshalling.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler for Duration.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}

	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}

	*d = Duration(parsed)
	return nil
}

// Duration returns the underlying time.Duration value.
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// UnmarshalYAML implements yaml.Unmarshaler for ExtractorConfig.
func (e *ExtractorConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		return e.parseShorthand(s)
	}

	if node.Kind == yaml.MappingNode {
		// temporary struct to avoid infinite recursion
		var raw struct {
			Type string `yaml:"type"`
			Path string `yaml:"path"`
			Text string `yaml:"text"`
		}
		if err := node.Decode(&raw); err != nil {
			return err
		}
		e.Type = raw.Type
		e.Path = raw.Path
		e.Text = raw.Text
		return nil
	}

	return fmt.Errorf("extractor must be a string or object, got %v", node.Kind)
}

// parseShorthand parses extractor shorthand syntax.
//
// Supported formats:
//   - "default" → use default extractor
//   - "http" → use HTTP status code only
//   - "json:path" → extract from JSON field
//   - "contains:text" → check if body contains text
func (e *ExtractorConfig) parseShorthand(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	if idx := strings.Index(s, ":"); idx != -1 {
		e.Type = s[:idx]
		value := s[idx+1:]

		switch e.Type {
		case "json":
			e.Path = value
		case "contains":
			e.Text = value
		default:
			return fmt.Errorf("unknown extractor type %q", e.Type)
		}
		return nil
	}

	switch s {
	case "default", "http":
		e.Type = s
	default:
		return fmt.Errorf("unknown extractor %q (expected 'default', 'http', 'json:path', or 'contains:text')", s)
	}
	return nil
}

// envVarPattern matches ${VAR} and ${VAR:-default} patterns.
// Group 1: variable name
// Group 2: the ":-default" part (if present, indicates a default was specified)
// Group 3: the default value (may be empty for ${VAR:-})
var envVarPattern = regexp.MustCompile(`\$\{([^}:]+)(:-([^}]*))?\}`)

// expandEnvVars replaces ${VAR} and ${VAR:-default} patterns with environment values.
func expandEnvVars(s string) (string, error) {
	var firstErr error

	result := envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		// already have an error, skip processing
		if firstErr != nil {
			return match
		}

		submatches := envVarPattern.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}

		varName := submatches[1]
		// submatches[2] is ":-..." (non-empty if default syntax was used)
		// submatches[3] is the actual default value (may be empty for ${VAR:-})
		hasDefault := len(submatches) > 2 && submatches[2] != ""
		defaultVal := ""
		if hasDefault && len(submatches) > 3 {
			defaultVal = submatches[3]
		}

		value, exists := os.LookupEnv(varName)
		if !exists {
			if hasDefault {
				return defaultVal
			}
			firstErr = fmt.Errorf("environment variable %q is not set", varName)
			return match
		}
		return value
	})

	if firstErr != nil {
		return "", firstErr
	}
	return result, nil
}

// Load reads and parses a YAML configuration file.
//
// Environment variables in the file are expanded before parsing.
// Returns an error if the file cannot be read or parsed.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	return Parse(data)
}

// Parse parses YAML configuration data.
//
// Environment variables are expanded in URL, URLTemplate, and Header values.
// Defaults are applied for Port (8080) and PollInterval (10s).
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = Duration(15 * time.Second)
	}

	if err := cfg.expandAndValidate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// expandAndValidate expands environment variables and validates the config.
func (c *Config) expandAndValidate() error {
	if c.PollInterval.Duration() < minPollInterval {
		return fmt.Errorf("poll_interval must be at least %s, got %s", minPollInterval, c.PollInterval.Duration())
	}

	for i := range c.Endpoints {
		ep := &c.Endpoints[i]

		if ep.Name == "" {
			return fmt.Errorf("endpoints[%d]: name is required", i)
		}

		if ep.URL == "" {
			return fmt.Errorf("endpoints[%d] (%s): url is required", i, ep.Name)
		}
		expanded, err := expandEnvVars(ep.URL)
		if err != nil {
			return fmt.Errorf("endpoints[%d] (%s): url: %w", i, ep.Name, err)
		}
		ep.URL = expanded

		parsedURL, err := url.Parse(ep.URL)
		if err != nil {
			return fmt.Errorf("endpoints[%d] (%s): invalid url: %w", i, ep.Name, err)
		}
		if parsedURL.Scheme == "" {
			return fmt.Errorf("endpoints[%d] (%s): url must have a scheme (http:// or https://)", i, ep.Name)
		}
		if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			return fmt.Errorf("endpoints[%d] (%s): url scheme must be http or https, got %q", i, ep.Name, parsedURL.Scheme)
		}

		for k, v := range ep.Headers {
			expanded, err := expandEnvVars(v)
			if err != nil {
				return fmt.Errorf("endpoints[%d] (%s): headers[%s]: %w", i, ep.Name, k, err)
			}
			ep.Headers[k] = expanded
		}

		if ep.Method != "" && ep.Method != "GET" && ep.Method != "HEAD" && ep.Method != "POST" {
			return fmt.Errorf("endpoints[%d] (%s): method must be GET, HEAD, or POST", i, ep.Name)
		}

		if ep.Timeout != 0 {
			if ep.Timeout.Duration() < 0 {
				return fmt.Errorf("endpoints[%d] (%s): timeout cannot be negative, got %s",
					i, ep.Name, ep.Timeout.Duration())
			}
			if ep.Timeout.Duration() < time.Second {
				return fmt.Errorf("endpoints[%d] (%s): timeout must be at least 1s if specified, got %s",
					i, ep.Name, ep.Timeout.Duration())
			}
		}

		if ep.Interval != 0 {
			if ep.Interval.Duration() < time.Second {
				return fmt.Errorf("endpoints[%d] (%s): interval must be at least 1s, got %s",
					i, ep.Name, ep.Interval.Duration())
			}
			if ep.Interval.Duration() > time.Hour {
				return fmt.Errorf("endpoints[%d] (%s): interval must not exceed 1h, got %s",
					i, ep.Name, ep.Interval.Duration())
			}
		}

		if err := validateExtractor(&ep.Extractor, fmt.Sprintf("endpoints[%d] (%s)", i, ep.Name)); err != nil {
			return err
		}
	}

	for i := range c.Grids {
		g := &c.Grids[i]

		if g.Name == "" {
			return fmt.Errorf("grids[%d]: name is required", i)
		}

		if g.URLTemplate == "" {
			return fmt.Errorf("grids[%d] (%s): url_template is required", i, g.Name)
		}
		expanded, err := expandEnvVars(g.URLTemplate)
		if err != nil {
			return fmt.Errorf("grids[%d] (%s): url_template: %w", i, g.Name, err)
		}
		g.URLTemplate = expanded

		// fail fast before SDK tries to use invalid template
		if _, err := template.New("").Parse(g.URLTemplate); err != nil {
			return fmt.Errorf("grids[%d] (%s): invalid url_template: %w", i, g.Name, err)
		}

		if len(g.Dimensions) == 0 {
			return fmt.Errorf("grids[%d] (%s): at least one dimension is required", i, g.Name)
		}
		for dimName, dimValues := range g.Dimensions {
			if len(dimValues) == 0 {
				return fmt.Errorf("grids[%d] (%s): dimension %q has no values", i, g.Name, dimName)
			}
			seen := make(map[string]struct{}, len(dimValues))
			for _, v := range dimValues {
				if _, exists := seen[v]; exists {
					return fmt.Errorf("grids[%d] (%s): dimension %q has duplicate value %q", i, g.Name, dimName, v)
				}
				seen[v] = struct{}{}
			}
		}

		for k, v := range g.Headers {
			expanded, err := expandEnvVars(v)
			if err != nil {
				return fmt.Errorf("grids[%d] (%s): headers[%s]: %w", i, g.Name, k, err)
			}
			g.Headers[k] = expanded
		}

		if g.Method != "" && g.Method != "GET" && g.Method != "HEAD" && g.Method != "POST" {
			return fmt.Errorf("grids[%d] (%s): method must be GET, HEAD, or POST", i, g.Name)
		}

		if g.Timeout != 0 {
			if g.Timeout.Duration() < 0 {
				return fmt.Errorf("grids[%d] (%s): timeout cannot be negative, got %s",
					i, g.Name, g.Timeout.Duration())
			}
			if g.Timeout.Duration() < time.Second {
				return fmt.Errorf("grids[%d] (%s): timeout must be at least 1s if specified, got %s",
					i, g.Name, g.Timeout.Duration())
			}
		}

		if g.Interval != 0 {
			if g.Interval.Duration() < time.Second {
				return fmt.Errorf("grids[%d] (%s): interval must be at least 1s, got %s",
					i, g.Name, g.Interval.Duration())
			}
			if g.Interval.Duration() > time.Hour {
				return fmt.Errorf("grids[%d] (%s): interval must not exceed 1h, got %s",
					i, g.Name, g.Interval.Duration())
			}
		}

		if err := validateExtractor(&g.Extractor, fmt.Sprintf("grids[%d] (%s)", i, g.Name)); err != nil {
			return err
		}
	}

	if len(c.Endpoints) == 0 && len(c.Grids) == 0 {
		return errors.New("at least one endpoint or grid must be defined")
	}

	return nil
}

// validateExtractor validates an extractor configuration.
func validateExtractor(e *ExtractorConfig, context string) error {
	if e.Type == "" {
		return nil // empty means default, which is valid
	}

	switch e.Type {
	case "default", "http":
		// no additional validation needed
	case "json":
		if e.Path == "" {
			return fmt.Errorf("%s: extractor type 'json' requires a path", context)
		}
	case "contains":
		if e.Text == "" {
			return fmt.Errorf("%s: extractor type 'contains' requires text", context)
		}
	default:
		return fmt.Errorf("%s: unknown extractor type %q", context, e.Type)
	}

	return nil
}
