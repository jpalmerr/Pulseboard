package poller

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			// no default timeout - we use per-request timeouts via context
			Transport: &http.Transport{
				MaxIdleConns:        defaultMaxIdleConns,
				MaxIdleConnsPerHost: defaultMaxIdleConnsPerHost,
				MaxConnsPerHost:     defaultMaxConnsPerHost,
				IdleConnTimeout:     defaultIdleConnTimeout,
				DisableKeepAlives:   false, // explicitly enable connection reuse
			},
		},
	}
}

// Fetch performs an HTTP request and returns a structured [Response].
//
// The request is made with the provided context, method, URL, headers, and timeout.
// If method is empty, GET is used. The timeout is applied via context cancellation.
// Response bodies are limited to 1MB to prevent memory exhaustion.
//
// Fetch always returns a Response; errors are captured in the Error field
// rather than returned separately. This simplifies handling in the scheduler.
func (c *Client) Fetch(ctx context.Context, method, url string, headers map[string]string, timeout time.Duration) Response {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	// default to GET if method is empty
	if method == "" {
		method = http.MethodGet
	}

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
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

	// read body with size limit
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
