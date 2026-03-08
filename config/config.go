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
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jpalmerr/pulseboard/internal/urltmpl"
)

// minPollInterval is the minimum allowed polling interval for production configs.
// This prevents accidental DoS of endpoints with overly aggressive polling.
const minPollInterval = 1 * time.Second

// ServerTLSConfig holds TLS configuration for the dashboard server.
type ServerTLSConfig struct {
	// CertFile is the path to the TLS certificate PEM file.
	CertFile string `yaml:"cert_file"`
	// KeyFile is the path to the TLS private key PEM file.
	KeyFile string `yaml:"key_file"`
}

// ClientTLSConfig holds TLS configuration for the polling HTTP client.
type ClientTLSConfig struct {
	// InsecureSkipVerify disables TLS certificate verification.
	// WARNING: Use only for development or trusted internal services.
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
	// MinVersion sets the minimum TLS version: "1.0", "1.1", "1.2", "1.3".
	// Defaults to TLS 1.2 when any client TLS option is active.
	MinVersion string `yaml:"min_version"`
	// ClientCert is the path to the client certificate PEM file (for mTLS).
	ClientCert string `yaml:"client_cert"`
	// ClientKey is the path to the client private key PEM file (for mTLS).
	ClientKey string `yaml:"client_key"`
}

// ServerConfig holds server-level configuration.
type ServerConfig struct {
	// TLS holds optional TLS settings. Omit for plain HTTP.
	TLS *ServerTLSConfig `yaml:"tls"`
}

// ClientConfig holds client-level configuration for polling.
type ClientConfig struct {
	// TLS holds optional TLS settings for the polling client.
	TLS *ClientTLSConfig `yaml:"tls"`
}

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

	// BlockPrivateNetworks enables SSRF protection when true.
	// Requests to RFC1918, loopback, link-local, and cloud metadata
	// addresses are rejected before the HTTP request is made.
	BlockPrivateNetworks bool `yaml:"block_private_networks"`

	// Auth configures authentication for the dashboard and API.
	// If nil, all endpoints are open (no authentication required).
	Auth *AuthConfig `yaml:"auth"`

	// Server holds server-level configuration including TLS.
	Server *ServerConfig `yaml:"server"`

	// Client holds polling client configuration including TLS.
	Client *ClientConfig `yaml:"client"`

	// Endpoints defines individual health check endpoints.
	Endpoints []EndpointConfig `yaml:"endpoints"`

	// Grids defines endpoint grids that expand via cartesian product.
	Grids []GridConfig `yaml:"grids"`

	// Webhooks defines HTTP webhook notifications triggered by status changes.
	Webhooks []WebhookConfig `yaml:"webhooks"`

	// Metrics configures the optional Prometheus /metrics endpoint.
	// If nil or Enabled is false, no /metrics route is registered.
	Metrics *MetricsConfig `yaml:"metrics"`
}

// MetricsConfig holds Prometheus metrics exposition settings.
type MetricsConfig struct {
	// Enabled controls whether /metrics is registered. Default: false.
	Enabled bool `yaml:"enabled"`
}

// WebhookConfig defines an HTTP webhook that fires on status transitions.
type WebhookConfig struct {
	// URL is the HTTP endpoint to POST the status change to.
	URL string `yaml:"url"`

	// Events restricts notifications to specific target statuses.
	// Example: ["down", "degraded"] — only fires when current status is down or degraded.
	// If empty, all transitions fire the webhook.
	Events []string `yaml:"events"`

	// Headers are custom HTTP headers included in each POST request.
	// Values support environment variable substitution: ${VAR} or ${VAR:-default}.
	Headers map[string]string `yaml:"headers"`

	// Timeout is the HTTP request timeout in seconds. Default: 10.
	Timeout int `yaml:"timeout"`

	// Debounce is the minimum seconds a status must remain changed before the
	// webhook fires. Default: 0 (no debounce).
	Debounce int `yaml:"debounce"`
}

// AuthConfig configures authentication for the PulseBoard HTTP server.
//
// Example YAML (Basic Auth):
//
//	auth:
//	  type: basic
//	  username: admin
//	  password: secret
//
// Example YAML (Bearer Token):
//
//	auth:
//	  type: bearer
//	  token: my-secret-token
type AuthConfig struct {
	// Type is the authentication type: "basic" or "bearer".
	Type string `yaml:"type"`

	// Username is the required username for Basic Auth.
	Username string `yaml:"username"`

	// Password is the required password for Basic Auth.
	Password string `yaml:"password"`

	// Token is a single valid Bearer token.
	Token string `yaml:"token"`

	// Tokens is a list of valid Bearer tokens.
	// Takes precedence over Token when both are set.
	Tokens []string `yaml:"tokens"`
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

	if typ, value, ok := strings.Cut(s, ":"); ok {
		e.Type = typ

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

		// fail fast: validate placeholder syntax and referenced dimensions
		if err := urltmpl.Validate(g.URLTemplate, g.Dimensions); err != nil {
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

	if c.Auth != nil {
		if err := c.expandAndValidateAuth(); err != nil {
			return err
		}
	}

	if c.Server != nil && c.Server.TLS != nil {
		if err := validateServerTLS(c.Server.TLS); err != nil {
			return err
		}
	}
	if c.Client != nil && c.Client.TLS != nil {
		if err := validateClientTLS(c.Client.TLS); err != nil {
			return err
		}
	}

	for i := range c.Webhooks {
		wh := &c.Webhooks[i]
		if wh.URL == "" {
			return fmt.Errorf("webhooks[%d]: url is required", i)
		}
		expanded, err := expandEnvVars(wh.URL)
		if err != nil {
			return fmt.Errorf("webhooks[%d]: url: %w", i, err)
		}
		wh.URL = expanded

		for k, v := range wh.Headers {
			expanded, err := expandEnvVars(v)
			if err != nil {
				return fmt.Errorf("webhooks[%d]: headers[%s]: %w", i, k, err)
			}
			wh.Headers[k] = expanded
		}

		if wh.Timeout < 0 {
			return fmt.Errorf("webhooks[%d]: timeout must be non-negative", i)
		}
		if wh.Debounce < 0 {
			return fmt.Errorf("webhooks[%d]: debounce must be non-negative", i)
		}
	}

	if len(c.Endpoints) == 0 && len(c.Grids) == 0 {
		return errors.New("at least one endpoint or grid must be defined")
	}

	return nil
}

// expandAndValidateAuth expands environment variables in auth fields and
// validates that the auth configuration is complete and coherent.
func (c *Config) expandAndValidateAuth() error {
	auth := c.Auth

	switch auth.Type {
	case "basic":
		expanded, err := expandEnvVars(auth.Username)
		if err != nil {
			return fmt.Errorf("auth.username: %w", err)
		}
		auth.Username = expanded

		expanded, err = expandEnvVars(auth.Password)
		if err != nil {
			return fmt.Errorf("auth.password: %w", err)
		}
		auth.Password = expanded

		if auth.Username == "" {
			return fmt.Errorf("auth: basic auth requires a non-empty username")
		}
		if auth.Password == "" {
			return fmt.Errorf("auth: basic auth requires a non-empty password")
		}

	case "bearer":
		expanded, err := expandEnvVars(auth.Token)
		if err != nil {
			return fmt.Errorf("auth.token: %w", err)
		}
		auth.Token = expanded

		for i := range auth.Tokens {
			expanded, err := expandEnvVars(auth.Tokens[i])
			if err != nil {
				return fmt.Errorf("auth.tokens[%d]: %w", i, err)
			}
			auth.Tokens[i] = expanded
		}

		// require at least one non-empty token
		hasToken := auth.Token != ""
		if !hasToken {
			for _, t := range auth.Tokens {
				if t != "" {
					hasToken = true
					break
				}
			}
		}
		if !hasToken {
			return fmt.Errorf("auth: bearer auth requires at least one non-empty token")
		}

	default:
		return fmt.Errorf("auth: unknown type %q (must be \"basic\" or \"bearer\")", auth.Type)
	}

	return nil
}

// validateServerTLS validates server TLS configuration.
func validateServerTLS(t *ServerTLSConfig) error {
	if t.CertFile == "" && t.KeyFile == "" {
		return nil // empty TLS section is a no-op
	}
	if t.CertFile == "" {
		return errors.New("server.tls.cert_file is required when server.tls is configured")
	}
	if t.KeyFile == "" {
		return errors.New("server.tls.key_file is required when server.tls is configured")
	}
	if _, err := os.Stat(t.CertFile); err != nil {
		return fmt.Errorf("server.tls.cert_file: %w", err)
	}
	if _, err := os.Stat(t.KeyFile); err != nil {
		return fmt.Errorf("server.tls.key_file: %w", err)
	}
	return nil
}

// validateClientTLS validates client TLS configuration.
func validateClientTLS(t *ClientTLSConfig) error {
	if t.MinVersion != "" {
		if _, err := parseTLSVersion(t.MinVersion); err != nil {
			return fmt.Errorf("client.tls.min_version: %w", err)
		}
	}
	if (t.ClientCert == "") != (t.ClientKey == "") {
		return errors.New("client.tls.client_cert and client.tls.client_key must both be set or both be empty")
	}
	if t.ClientCert != "" {
		if _, err := os.Stat(t.ClientCert); err != nil {
			return fmt.Errorf("client.tls.client_cert: %w", err)
		}
		if _, err := os.Stat(t.ClientKey); err != nil {
			return fmt.Errorf("client.tls.client_key: %w", err)
		}
	}
	return nil
}

// parseTLSVersion converts a version string to a crypto/tls version constant.
func parseTLSVersion(s string) (uint16, error) {
	switch s {
	case "1.0":
		return tls.VersionTLS10, nil
	case "1.1":
		return tls.VersionTLS11, nil
	case "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unknown TLS version %q (expected \"1.0\", \"1.1\", \"1.2\", or \"1.3\")", s)
	}
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
