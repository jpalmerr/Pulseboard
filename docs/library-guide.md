# Library How-To Guide

This guide covers common tasks using the PulseBoard Go library.

## Installation

```bash
go get github.com/jpalmerr/pulseboard
```

Requires Go 1.23 or later.

## Basic Usage

### Minimal Setup

```go
package main

import (
    "context"
    "log"
    "os/signal"
    "syscall"

    "github.com/jpalmerr/pulseboard"
)

func main() {
    // create an endpoint
    api, err := pulseboard.NewEndpoint("My API", "https://api.example.com/health")
    if err != nil {
        log.Fatal(err)
    }

    // create the dashboard
    pb, err := pulseboard.New(pulseboard.WithEndpoint(api))
    if err != nil {
        log.Fatal(err)
    }

    // start with graceful shutdown
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    pb.Start(ctx)  // blocks until ctx is cancelled
}
```

## How-To Guides

### Configure Multiple Endpoints

```go
api, _ := pulseboard.NewEndpoint("API", "https://api.example.com/health")
db, _ := pulseboard.NewEndpoint("Database", "https://db.example.com/health")
cache, _ := pulseboard.NewEndpoint("Cache", "https://redis.example.com/health")

pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithEndpoint(db),
    pulseboard.WithEndpoint(cache),
)
```

Or use `WithEndpoints` for a slice:

```go
endpoints := []*pulseboard.Endpoint{api, db, cache}

pb, err := pulseboard.New(
    pulseboard.WithEndpoints(endpoints...),
)
```

### Add Authentication Headers

```go
ep, err := pulseboard.NewEndpoint("Protected API", "https://api.example.com/health",
    pulseboard.WithHeaders(
        "Authorization", "Bearer my-token",
        "X-API-Key", "my-api-key",
    ),
)
```

### Add Labels for Grouping

Labels appear in the dashboard and help categorise endpoints:

```go
ep, err := pulseboard.NewEndpoint("Payment API", "https://payments.example.com/health",
    pulseboard.WithLabels(
        "env", "production",
        "team", "payments",
        "tier", "critical",
    ),
)
```

### Set Custom Timeouts

```go
// per-endpoint timeout
ep, err := pulseboard.NewEndpoint("Slow API", "https://slow.example.com/health",
    pulseboard.WithTimeout(30 * time.Second),  // default is 10s
)
```

### Set Per-Endpoint Polling Intervals

Different endpoints can poll at different frequencies:

```go
// critical endpoints poll frequently
critical, _ := pulseboard.NewEndpoint("Payment API", "https://payments.example.com/health",
    pulseboard.WithInterval(5 * time.Second),
)

// background services poll less often
background, _ := pulseboard.NewEndpoint("Batch Jobs", "https://jobs.example.com/health",
    pulseboard.WithInterval(60 * time.Second),
)

pb, err := pulseboard.New(
    pulseboard.WithEndpoint(critical),
    pulseboard.WithEndpoint(background),
    pulseboard.WithPollingInterval(15 * time.Second),  // default for endpoints without interval
)
```

### Configure the Dashboard Server

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithTitle("Payment Gateway Status"),   // default: "PulseBoard"
    pulseboard.WithPort(9090),                        // default: 8080
    pulseboard.WithPollingInterval(30 * time.Second), // default: 15s
    pulseboard.WithMaxConcurrency(20),                // default: 10 concurrent polls
)
```

### Use Endpoint Grids

Generate multiple endpoints from a template using cartesian product expansion:

```go
// creates 4 endpoints: prod/users, prod/orders, staging/users, staging/orders
endpoints, err := pulseboard.NewEndpointGrid("Platform",
    pulseboard.WithURLTemplate("https://{{.env}}.example.com/{{.service}}/health"),
    pulseboard.WithDimensions(map[string][]string{
        "env":     {"prod", "staging"},
        "service": {"users", "orders"},
    }),
)
if err != nil {
    log.Fatal(err)
}

pb, err := pulseboard.New(
    pulseboard.WithEndpoints(endpoints...),
)
```

#### Grid with Options

```go
endpoints, err := pulseboard.NewEndpointGrid("Platform",
    pulseboard.WithURLTemplate("https://{{.env}}.example.com/{{.service}}/health"),
    pulseboard.WithDimensions(map[string][]string{
        "env":     {"prod", "staging"},
        "service": {"users", "orders"},
    }),
    pulseboard.WithGridExtractor(pulseboard.JSONFieldExtractor("status")),
    pulseboard.WithGridInterval(10 * time.Second),
    pulseboard.WithGridHeaders("Authorization", "Bearer token"),
    pulseboard.WithGridTimeout(5 * time.Second),
)
```

## Status Extractors

Extractors determine how HTTP responses are interpreted as status values.

### Built-in Extractors

#### HTTP Status Code

Uses only the HTTP status code:

```go
// 2xx → up, 4xx → degraded, 5xx → down
pulseboard.HTTPStatusExtractor
```

#### JSON Field

Extracts a field from JSON responses:

```go
// for {"status": "healthy"}
pulseboard.JSONFieldExtractor("status")

// for {"data": {"health": {"status": "ok"}}}
pulseboard.JSONFieldExtractor("data.health.status")
```

Recognised values:
- **Up**: `ok`, `healthy`, `up`, `active`, `running`, `pass`, `passed`, `true`, `green`, `none`, `operational`
- **Degraded**: `degraded`, `warning`, `partial`, `yellow`, `amber`
- **Down**: any other value

#### Contains

Checks if the response body contains text (case-insensitive):

```go
// returns up if body contains "healthy"
pulseboard.ContainsExtractor("healthy")
```

#### Regex

Matches against a regular expression:

```go
// capture group compared to "ok" (case-insensitive)
extractor, err := pulseboard.RegexExtractor(`"status":\s*"(\w+)"`, "ok")

// or panic on invalid pattern (for compile-time constants)
extractor := pulseboard.MustRegexExtractor(`"health":\s*"(\w+)"`, "good")
```

#### FirstMatch (Composable)

Try multiple extractors in order:

```go
// try JSON first, fall back to HTTP status code
extractor := pulseboard.FirstMatch(
    pulseboard.JSONFieldExtractor("status"),
    pulseboard.JSONFieldExtractor("health"),  // try alternative field
    pulseboard.HTTPStatusExtractor,            // fallback
)
```

### Writing Custom Extractors

A `StatusExtractor` is a function with this signature:

```go
type StatusExtractor func(body []byte, statusCode int) Status
```

#### Basic Custom Extractor

```go
myExtractor := func(body []byte, statusCode int) pulseboard.Status {
    if statusCode == 200 {
        return pulseboard.StatusUp
    }
    return pulseboard.StatusDown
}

ep, _ := pulseboard.NewEndpoint("API", url,
    pulseboard.WithExtractor(myExtractor),
)
```

#### Maintenance Mode Detection

```go
maintenanceExtractor := func(body []byte, statusCode int) pulseboard.Status {
    bodyStr := string(body)

    // check for maintenance mode
    if strings.Contains(strings.ToLower(bodyStr), "maintenance") {
        return pulseboard.StatusDegraded
    }

    // normal status check
    if statusCode >= 200 && statusCode < 300 {
        return pulseboard.StatusUp
    }
    return pulseboard.StatusDown
}
```

#### Custom JSON Parsing

```go
type HealthResponse struct {
    Status    string `json:"status"`
    Message   string `json:"message"`
    Subsystems []struct {
        Name   string `json:"name"`
        Status string `json:"status"`
    } `json:"subsystems"`
}

subsystemExtractor := func(body []byte, statusCode int) pulseboard.Status {
    var resp HealthResponse
    if err := json.Unmarshal(body, &resp); err != nil {
        return pulseboard.StatusUnknown
    }

    // check if any subsystem is down
    for _, sub := range resp.Subsystems {
        if sub.Status == "down" {
            return pulseboard.StatusDown
        }
        if sub.Status == "degraded" {
            return pulseboard.StatusDegraded
        }
    }

    return pulseboard.StatusUp
}
```

#### Threshold Based Status

```go
type MetricsResponse struct {
    ErrorRate    float64 `json:"error_rate"`
    Latency      float64 `json:"latency_ms"`
    Healthy      bool    `json:"healthy"`
}

metricsExtractor := func(body []byte, statusCode int) pulseboard.Status {
    var resp MetricsResponse
    if err := json.Unmarshal(body, &resp); err != nil {
        return pulseboard.StatusUnknown
    }

    // down if explicitly unhealthy or high error rate
    if !resp.Healthy || resp.ErrorRate > 0.1 {
        return pulseboard.StatusDown
    }

    // degraded if elevated latency
    if resp.Latency > 500 {
        return pulseboard.StatusDegraded
    }

    return pulseboard.StatusUp
}
```

#### Combining Custom with Built-in

```go
// custom logic with fallback to standard JSON extraction
extractor := pulseboard.FirstMatch(
    myCustomExtractor,
    pulseboard.JSONFieldExtractor("status"),
    pulseboard.HTTPStatusExtractor,
)
```

#### Returning Unknown

Return `StatusUnknown` when you cannot determine the status. This allows `FirstMatch` to try the next extractor:

```go
conditionalExtractor := func(body []byte, statusCode int) pulseboard.Status {
    // only handle JSON responses
    if !json.Valid(body) {
        return pulseboard.StatusUnknown  // let next extractor try
    }

    // your JSON parsing logic here
    return pulseboard.StatusUp
}

// chain with fallback
extractor := pulseboard.FirstMatch(
    conditionalExtractor,
    pulseboard.HTTPStatusExtractor,  // handles non-JSON
)
```

## Status Callbacks

React to every poll result with the `WithStatusCallback` option.

### Basic Callback

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithStatusCallback(func(result pulseboard.StatusResult) {
        if result.Status == pulseboard.StatusDown {
            log.Printf("ALERT: %s is down!", result.EndpointName)
        }
    }),
)
```

### StatusResult Fields

The callback receives a `StatusResult` with these fields:

| Field | Type | Description |
|-------|------|-------------|
| `EndpointName` | `string` | Display name of the endpoint |
| `URL` | `string` | The URL that was polled |
| `Status` | `Status` | Determined status (Up/Down/Degraded/Unknown/Error) |
| `StatusCode` | `int` | HTTP response status code |
| `RawResponse` | `[]byte` | Response body (useful for debugging) |
| `Latency` | `time.Duration` | Request duration |
| `CheckedAt` | `time.Time` | When the poll occurred |
| `Labels` | `map[string]string` | Endpoint metadata |
| `Error` | `error` | Any error that occurred (nil on success) |

### Use Cases

#### Alerting Integration

```go
pulseboard.WithStatusCallback(func(result pulseboard.StatusResult) {
    if result.Status == pulseboard.StatusDown {
        go alerting.SendSlackAlert(fmt.Sprintf(
            "%s is DOWN: %v",
            result.EndpointName,
            result.Error,
        ))
    }
})
```

#### Metrics Export

```go
pulseboard.WithStatusCallback(func(result pulseboard.StatusResult) {
    // record to Prometheus, StatsD, etc.
    metrics.RecordLatency(result.EndpointName, result.Latency)
    metrics.RecordStatus(result.EndpointName, string(result.Status))
})
```

#### Custom Logging

```go
pulseboard.WithStatusCallback(func(result pulseboard.StatusResult) {
    logger.Info("poll completed",
        "endpoint", result.EndpointName,
        "status", result.Status,
        "latency_ms", result.Latency.Milliseconds(),
        "status_code", result.StatusCode,
    )
})
```

### Multiple Callbacks

Register multiple callbacks—they execute in registration order:

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithStatusCallback(logCallback),
    pulseboard.WithStatusCallback(metricsCallback),
    pulseboard.WithStatusCallback(alertingCallback),
)
```

### Important: Non-Blocking Callbacks

Callbacks are invoked synchronously in a single results-processing goroutine. This has important implications:

- **Blocking callbacks delay result processing** - A slow callback doesn't block individual polls (those run concurrently), but it delays processing of queued results from other endpoints.
- **Callbacks execute sequentially** - Results queue up while a callback runs; they're processed in order once it returns.
- **Dashboard updates wait** - SSE clients won't see new results until all callbacks for the previous result complete.

**For slow operations, dispatch to a goroutine:**

```go
pulseboard.WithStatusCallback(func(result pulseboard.StatusResult) {
    go func() {
        // slow operation: webhook, database write, etc.
        sendWebhook(result)
    }()
})
```

**Rule of thumb:** If your callback does I/O (HTTP requests, database writes, file operations), wrap it in a goroutine.

### Panic Safety

Callbacks are wrapped in panic recovery. If a callback panics:
- The panic is logged with endpoint context
- Other callbacks still execute
- The scheduler continues running

This ensures one bad callback cannot crash your application.

### Callback Timing

Callbacks fire **after** the store update. This means:
- SSE clients see the update before callbacks complete
- Callbacks see "official" data matching what's in the store
- If a callback blocks, it doesn't delay dashboard updates

## Status Change Callbacks

React only to status transitions (not every poll) with `WithStatusChangeCallback`. The first poll for each endpoint is always considered a transition.

### Basic Change Callback

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithStatusChangeCallback(func(change pulseboard.StatusChange) {
        log.Printf("%s: %s → %s", change.EndpointName, change.PreviousStatus, change.CurrentStatus)
    }),
)
```

### StatusChange Fields

| Field | Type | Description |
|-------|------|-------------|
| `EndpointName` | `string` | Display name of the endpoint |
| `URL` | `string` | The URL that was polled |
| `Labels` | `map[string]string` | Endpoint metadata |
| `PreviousStatus` | `Status` | Status before the transition (`""` on first poll) |
| `CurrentStatus` | `Status` | Status after the transition |
| `LatencyMs` | `int64` | Request duration in milliseconds |
| `CheckedAt` | `time.Time` | When the poll occurred |
| `Error` | `string` | Error message, if any |

### Difference from StatusCallback

- `WithStatusCallback` fires on **every poll**, regardless of whether the status changed.
- `WithStatusChangeCallback` fires **only when the status transitions** to a different value.

Use `WithStatusCallback` for logging or metrics that need every data point. Use `WithStatusChangeCallback` for alerting, webhooks, or notifications where you only care about transitions.

## Webhook Notifications

Send HTTP POST requests on status transitions using `WebhookNotifier`:

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithStatusChangeCallback(
        pulseboard.WebhookNotifier("https://hooks.slack.com/services/...",
            pulseboard.WithWebhookEventFilter("down", "degraded"),
            pulseboard.WithWebhookDebounce(30 * time.Second),
            pulseboard.WithWebhookHeaders(map[string]string{
                "Authorization": "Bearer token",
            }),
            pulseboard.WithWebhookTimeout(5 * time.Second),
        ),
    ),
)
```

`WebhookNotifier` returns a `func(StatusChange)` — it plugs into `WithStatusChangeCallback`. The POST body is the `StatusChange` struct serialised as JSON.

### Webhook Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithWebhookEventFilter(events...)` | all | Only fire for transitions to these statuses |
| `WithWebhookDebounce(d)` | 0 (off) | Minimum time a status must hold before firing |
| `WithWebhookHeaders(m)` | none | Custom HTTP headers on every POST |
| `WithWebhookTimeout(d)` | 10s | HTTP request timeout |

### Multiple Webhooks

Register separate notifiers for different channels:

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    // Slack for all transitions
    pulseboard.WithStatusChangeCallback(
        pulseboard.WebhookNotifier("https://hooks.slack.com/services/..."),
    ),
    // PagerDuty only for down
    pulseboard.WithStatusChangeCallback(
        pulseboard.WebhookNotifier("https://events.pagerduty.com/...",
            pulseboard.WithWebhookEventFilter("down"),
            pulseboard.WithWebhookDebounce(60 * time.Second),
        ),
    ),
)
```

## Authentication Middleware

Protect the dashboard and API with built-in middleware.

### Basic Auth

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithMiddleware(pulseboard.BasicAuth(func(u, p string) bool {
        return u == "admin" && p == os.Getenv("DASHBOARD_PASSWORD")
    })),
)
```

### Bearer Token

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithMiddleware(pulseboard.BearerToken("my-secret-token")),
)
```

Multiple tokens are supported. Token comparison uses constant-time equality to prevent timing side-channels.

### Custom Middleware

`WithMiddleware` accepts any `func(http.Handler) http.Handler`:

```go
rateLimiter := func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // your rate limiting logic
        next.ServeHTTP(w, r)
    })
}

pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithMiddleware(rateLimiter),
    pulseboard.WithMiddleware(pulseboard.BasicAuth(validateFunc)),
)
```

Multiple middleware are applied in registration order (first registered = outermost wrapper).

## SSRF Protection

Block requests to private networks and restrict polling to known hosts:

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithBlockPrivateNetworks(),          // blocks RFC1918, loopback, link-local, cloud metadata
    pulseboard.WithAllowedHosts("api.example.com"), // allowlist mode
)
```

- `WithBlockPrivateNetworks` blocks 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8, 169.254.0.0/16, and IPv6 equivalents.
- `WithAllowedHosts` enables allowlist mode — hosts not explicitly listed are rejected.
- Both options validate redirect targets, not just initial URLs.

These can be combined for defence-in-depth.

## TLS Support

### Server-Side HTTPS

Serve the dashboard over HTTPS:

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithTLS("/path/to/cert.pem", "/path/to/key.pem"),
)
```

### Client-Side TLS for Polling

Configure TLS for connections to polled endpoints:

```go
// skip certificate verification (dev/internal only)
pulseboard.WithInsecureSkipVerify()

// set minimum TLS version
pulseboard.WithTLSMinVersion(tls.VersionTLS13)

// mutual TLS — present a client certificate
pulseboard.WithClientCert("/path/to/client-cert.pem", "/path/to/client-key.pem")
```

These options can be combined:

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithTLSMinVersion(tls.VersionTLS12),
    pulseboard.WithClientCert("/path/to/cert.pem", "/path/to/key.pem"),
)
```

## Prometheus Metrics

Expose a `/metrics` endpoint in Prometheus text format:

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithMetrics(),
)
```

Records per-endpoint poll latency histograms, status transition counters, and error rates. Disabled by default — no `/metrics` route exists without this option.

## Stale Endpoint Detection

Mark endpoints as stale when no update is received within a threshold:

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithStaleThreshold(2 * time.Minute),
)
```

- Default threshold: 3x the polling interval (e.g., 45s with the default 15s interval).
- Pass `0` to disable staleness detection entirely.

## Integration Patterns

### Embed in Existing HTTP Server

PulseBoard runs its own server. To embed alongside existing routes, use a reverse proxy or run on a different port:

```go
// your existing server on :8080
go func() {
    http.ListenAndServe(":8080", yourHandler)
}()

// pulseboard on :8081
pb, _ := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithPort(8081),
)
pb.Start(ctx)
```

### With Graceful Shutdown

```go
func main() {
    pb, _ := pulseboard.New(pulseboard.WithEndpoint(api))

    // set up signal handling
    ctx, stop := signal.NotifyContext(context.Background(),
        syscall.SIGINT,
        syscall.SIGTERM,
    )
    defer stop()

    // Start blocks until ctx is cancelled
    if err := pb.Start(ctx); err != nil {
        log.Printf("pulseboard error: %v", err)
    }

    log.Println("shutdown complete")
}
```

### With Context Timeout

```go
// run for a limited time (useful for testing)
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()

pb.Start(ctx)
```

### Dynamic Endpoint Configuration

Endpoints are configured at startup. For dynamic changes, restart the dashboard:

```go
func runDashboard(ctx context.Context, endpoints []*pulseboard.Endpoint) error {
    pb, err := pulseboard.New(pulseboard.WithEndpoints(endpoints...))
    if err != nil {
        return err
    }
    return pb.Start(ctx)
}

// restart with new endpoints
cancel()  // stop current
endpoints = append(endpoints, newEndpoint)
ctx, cancel = context.WithCancel(context.Background())
go runDashboard(ctx, endpoints)
```

## Testing

### Test Custom Extractors

```go
func TestMyExtractor(t *testing.T) {
    extractor := myCustomExtractor

    tests := []struct {
        name       string
        body       string
        statusCode int
        want       pulseboard.Status
    }{
        {
            name:       "healthy response",
            body:       `{"status": "ok"}`,
            statusCode: 200,
            want:       pulseboard.StatusUp,
        },
        {
            name:       "maintenance mode",
            body:       `{"status": "maintenance"}`,
            statusCode: 200,
            want:       pulseboard.StatusDegraded,
        },
        {
            name:       "server error",
            body:       `{"error": "internal"}`,
            statusCode: 500,
            want:       pulseboard.StatusDown,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := extractor([]byte(tt.body), tt.statusCode)
            if got != tt.want {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

## API Reference

### Status Values

```go
pulseboard.StatusUp       // service is healthy
pulseboard.StatusDegraded // service is impaired but functional
pulseboard.StatusDown     // service is unavailable
pulseboard.StatusUnknown  // status cannot be determined
pulseboard.StatusError    // check mechanism failed (extractor panic, parse error)
```

`StatusError` is distinct from `StatusDown`: it means the check itself failed (e.g., extractor panic, JSON parse error), not that the service is unreachable.

### PulseBoard Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithEndpoint(ep)` | - | Add a single endpoint |
| `WithEndpoints(eps...)` | - | Add multiple endpoints |
| `WithTitle(title)` | "PulseBoard" | Dashboard title (browser tab and header) |
| `WithPort(port)` | 8080 | Dashboard HTTP port |
| `WithPollingInterval(d)` | 15s | Default polling interval |
| `WithMaxConcurrency(n)` | 10 | Max concurrent polls |
| `WithStatusCallback(cb)` | - | Register callback for every poll result |
| `WithStatusChangeCallback(cb)` | - | Register callback for status transitions only |
| `WithMiddleware(mw)` | - | Add HTTP middleware (auth, rate limiting, etc.) |
| `WithBlockPrivateNetworks()` | off | Block polling to private/loopback/metadata IPs |
| `WithAllowedHosts(hosts...)` | - | Restrict polling to listed hostnames only |
| `WithTLS(cert, key)` | - | Enable HTTPS for the dashboard server |
| `WithInsecureSkipVerify()` | off | Skip TLS verification when polling (dev only) |
| `WithTLSMinVersion(v)` | TLS 1.2 | Minimum TLS version for polling connections |
| `WithClientCert(cert, key)` | - | Client certificate for mTLS polling |
| `WithMetrics()` | off | Enable Prometheus `/metrics` endpoint |
| `WithStaleThreshold(d)` | 3x interval | Mark endpoints stale after this duration |
| `WithLogger(logger)` | slog.Default() | Custom logger |

### Endpoint Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithLabels(k, v, ...)` | - | Add metadata labels |
| `WithHeaders(k, v, ...)` | - | Add HTTP headers |
| `WithTimeout(d)` | 10s | Request timeout |
| `WithInterval(d)` | global | Per-endpoint poll interval |
| `WithExtractor(e)` | DefaultExtractor | Status extraction logic |
| `WithMethod(m)` | GET | HTTP method (GET/HEAD/POST) |

### Grid Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithURLTemplate(t)` | - | Go template for URL |
| `WithDimensions(m)` | - | Map of dimension values |
| `WithGridExtractor(e)` | DefaultExtractor | Extractor for all generated endpoints |
| `WithGridInterval(d)` | global | Interval for all generated endpoints |
| `WithGridHeaders(k, v, ...)` | - | Headers for all generated endpoints |
| `WithGridTimeout(d)` | 10s | Timeout for all generated endpoints |
