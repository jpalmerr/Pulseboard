package poller

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const maxResponseBodySize = 1 << 20 // 1MB

// connection pooling limits to prevent resource exhaustion when polling many endpoints
const (
	defaultMaxIdleConns        = 100
	defaultMaxIdleConnsPerHost = 10
	defaultMaxConnsPerHost     = 10
	defaultIdleConnTimeout     = 60 * time.Second // conservative: matches common ALB defaults
)

// blockedCIDRs contains the private/loopback/link-local CIDR ranges blocked by SSRF protection.
var blockedCIDRs []*net.IPNet

func init() {
	cidrs := []string{
		"0.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("invalid built-in CIDR %q: %v", cidr, err))
		}
		blockedCIDRs = append(blockedCIDRs, network)
	}
}

// ssrfConfig holds SSRF protection settings for the client.
type ssrfConfig struct {
	blockPrivateNetworks bool
	allowedHosts         []string
}

// ClientOption is a functional option for configuring a [Client].
type ClientOption func(*Client)

// WithBlockedCIDRs enables private network blocking on the client.
// When set, requests to RFC1918, loopback, link-local, and cloud metadata
// addresses are rejected before the HTTP request is made.
func WithBlockedCIDRs() ClientOption {
	return func(c *Client) {
		c.ssrf.blockPrivateNetworks = true
	}
}

// WithClientAllowedHosts sets an allowlist of permitted hosts on the client.
// When non-empty, only requests to the listed hostnames are allowed.
// Host matching is case-insensitive; all hosts are stored lowercased.
func WithClientAllowedHosts(hosts ...string) ClientOption {
	return func(c *Client) {
		for _, h := range hosts {
			c.ssrf.allowedHosts = append(c.ssrf.allowedHosts, strings.ToLower(h))
		}
	}
}

// WithTLSConfig applies a custom TLS configuration to the polling client's
// HTTP transport. When set, it overrides Go's default TLS settings.
// Intended for internal use; prefer the top-level SDK options.
func WithTLSConfig(tlsCfg *tls.Config) ClientOption {
	return func(c *Client) {
		c.tlsCfg = tlsCfg
	}
}

// Response holds the result of an HTTP request made by [Client].
//
// Response captures all relevant information from an HTTP request including
// the body (limited to 1MB), status code, latency, and any error that occurred.
type Response struct {
	// Body contains the HTTP response body, limited to 1MB.
	Body []byte

	// StatusCode is the HTTP status code (e.g., 200, 404, 500).
	// Zero if the request failed before receiving a response.
	StatusCode int

	// Latency is the total time taken for the request.
	Latency time.Duration

	// Error contains any error that occurred during the request.
	// nil indicates the request completed (though status may indicate an error).
	Error error
}

// Client is an HTTP client wrapper optimized for polling health endpoints.
//
// Client uses per-request timeouts via context rather than a global timeout,
// allowing different endpoints to have different timeout configurations.
// Response bodies are limited to 1MB to prevent memory issues.
type Client struct {
	httpClient *http.Client
	ssrf       ssrfConfig
	tlsCfg     *tls.Config
}

// NewClient creates a new polling [Client].
//
// The client is configured with connection pooling limits to prevent resource
// exhaustion when polling many endpoints. Timeouts are applied per-request via
// the context parameter in [Client.Fetch], not as a global client timeout.
//
// Connection pooling configuration:
//   - MaxIdleConns: 100 total idle connections
//   - MaxIdleConnsPerHost: 10 idle connections per host
//   - MaxConnsPerHost: 10 concurrent connections per host
//   - IdleConnTimeout: 60 seconds before closing idle connections
//
// Optional [ClientOption] values configure SSRF protection. Without any options,
// behaviour is unchanged from the previous zero-argument constructor.
func NewClient(opts ...ClientOption) *Client {
	c := &Client{}

	// Apply options before constructing the http.Client so that
	// CheckRedirect can close over the finalised ssrf config.
	for _, opt := range opts {
		opt(c)
	}

	ssrf := c.ssrf // capture for closure

	// ssrfDialContext is a custom dialer that validates the resolved IP against
	// blockedCIDRs at connection time, eliminating the TOCTOU window that exists
	// when DNS resolution and CIDR checking happen in separate steps.  The dialer
	// resolves the hostname itself, checks every returned IP, then connects
	// directly to the chosen IP so that the OS cannot re-resolve between check
	// and connect.
	ssrfDialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("host blocked by SSRF policy: invalid address %q: %w", addr, err)
		}

		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("host blocked by SSRF policy: could not resolve %s: %w", host, err)
		}

		// Validate every resolved address before attempting a connection.
		for _, a := range addrs {
			ip := net.ParseIP(a)
			if ip == nil {
				continue
			}
			// Normalise IPv4-mapped IPv6 addresses (e.g. ::ffff:127.0.0.1) to
			// their IPv4 form so the IPv4 CIDR rules match them correctly.
			if v4 := ip.To4(); v4 != nil {
				ip = v4
			}
			for _, network := range blockedCIDRs {
				if network.Contains(ip) {
					return nil, fmt.Errorf("host blocked by SSRF policy: %s resolves to a private/reserved address (%s)", host, a)
				}
			}
		}

		// Connect directly to the first validated IP to avoid a second DNS
		// resolution by the OS (the key TOCTOU fix).
		dialer := &net.Dialer{}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addrs[0], port))
	}

	// Only attach the custom dialer when private network blocking is requested.
	// Without it the transport behaves exactly as before.
	var dialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	if ssrf.blockPrivateNetworks {
		dialContext = ssrfDialContext
	}

	c.httpClient = &http.Client{
		// no default timeout - we use per-request timeouts via context
		Transport: &http.Transport{
			DialContext:         dialContext,
			TLSClientConfig:     c.tlsCfg, // nil = use Go defaults
			MaxIdleConns:        defaultMaxIdleConns,
			MaxIdleConnsPerHost: defaultMaxIdleConnsPerHost,
			MaxConnsPerHost:     defaultMaxConnsPerHost,
			IdleConnTimeout:     defaultIdleConnTimeout,
			DisableKeepAlives:   false, // explicitly enable connection reuse
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			// DialContext handles CIDR checks for redirects when blockPrivateNetworks
			// is set. We still validate the allowlist and scheme here so a redirect
			// to a disallowed host is caught before the connection is attempted.
			if err := validateURL(req.URL.String(), ssrf); err != nil {
				return err
			}
			return nil
		},
	}

	return c
}

// validateURL checks the scheme, host allowlist, and (when blockPrivateNetworks
// is false) performs a pre-flight DNS-based CIDR check.  When blockPrivateNetworks
// is true the CIDR check is delegated to the DialContext so it is atomic with the
// actual connection — validateURL only handles scheme and allowlist in that path.
// It returns a non-nil error when the URL is blocked.
func validateURL(urlStr string, cfg ssrfConfig) error {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("host blocked by SSRF policy: invalid URL: %w", err)
	}

	// Scheme must be http or https regardless of other settings.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("host blocked by SSRF policy: unsupported scheme %q", parsed.Scheme)
	}

	host := strings.ToLower(parsed.Hostname()) // strips port; normalise case

	if host == "" {
		return fmt.Errorf("host blocked by SSRF policy: URL has no host")
	}

	// Allowlist check (independent of blockPrivateNetworks).
	if len(cfg.allowedHosts) > 0 {
		if !slices.Contains(cfg.allowedHosts, host) {
			return fmt.Errorf("host blocked by SSRF policy: %s is not in the allowed hosts list", host)
		}
	}

	if !cfg.blockPrivateNetworks {
		return nil
	}

	// When blockPrivateNetworks is true, CIDR validation is handled by DialContext
	// at connection time to avoid a TOCTOU race — no DNS lookup here.
	return nil
}

// Fetch performs an HTTP request and returns a structured [Response].
//
// The request is made with the provided context, method, URL, headers, and timeout.
// If method is empty, GET is used. The timeout is applied via context cancellation.
// Response bodies are limited to 1MB to prevent memory exhaustion.
//
// Fetch always returns a Response; errors are captured in the Error field
// rather than returned separately. This simplifies handling in the scheduler.
func (c *Client) Fetch(ctx context.Context, method, urlStr string, headers map[string]string, timeout time.Duration) Response {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	// SSRF validation before making any network request.
	if err := validateURL(urlStr, c.ssrf); err != nil {
		return Response{
			Latency: time.Since(start),
			Error:   err,
		}
	}

	if method == "" {
		method = http.MethodGet
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, nil)
	if err != nil {
		return Response{
			Latency: time.Since(start),
			Error:   fmt.Errorf("failed to create request: %w", err),
		}
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Response{
			Latency: time.Since(start),
			Error:   fmt.Errorf("request failed: %w", err),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	limitedReader := io.LimitReader(resp.Body, maxResponseBodySize)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return Response{
			StatusCode: resp.StatusCode,
			Latency:    time.Since(start),
			Error:      fmt.Errorf("failed to read response body: %w", err),
		}
	}

	return Response{
		Body:       body,
		StatusCode: resp.StatusCode,
		Latency:    time.Since(start),
		Error:      nil,
	}
}

// Close closes all idle connections in the client's connection pool.
//
// This should be called when the client is no longer needed to release
// resources immediately rather than waiting for the idle connection timeout.
// Safe to call multiple times. After Close, the client remains usable but
// new connections will be established as needed.
func (c *Client) Close() {
	if c == nil || c.httpClient == nil {
		return
	}
	if transport, ok := c.httpClient.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}
