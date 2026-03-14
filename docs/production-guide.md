# Production Guide

This guide walks through hardening PulseBoard for production deployments: authentication, network security, TLS, alerting, and observability.

Each section shows both the **SDK** (Go) and **CLI** (YAML) approach side by side.

## Authentication

### Basic Auth

**SDK:**

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithMiddleware(pulseboard.BasicAuth(func(u, p string) bool {
        return u == "admin" && p == os.Getenv("DASHBOARD_PASSWORD")
    })),
)
```

**YAML:**

```yaml
auth:
  type: basic
  username: admin
  password: "${DASHBOARD_PASSWORD}"
```

### Bearer Token

**SDK:**

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithMiddleware(pulseboard.BearerToken(
        os.Getenv("TOKEN_A"),
        os.Getenv("TOKEN_B"),
    )),
)
```

**YAML:**

```yaml
auth:
  type: bearer
  tokens:
    - "${TOKEN_A}"
    - "${TOKEN_B}"
```

Token comparison uses constant-time equality to prevent timing side-channels.

## SSRF Protection

If endpoint URLs come from configuration that may be edited by untrusted users, enable SSRF protection to prevent polling internal services.

**SDK:**

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithBlockPrivateNetworks(),
    pulseboard.WithAllowedHosts("api.example.com", "status.example.com"),
)
```

**YAML:**

```yaml
block_private_networks: true
```

`WithBlockPrivateNetworks` / `block_private_networks` blocks:
- RFC1918: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
- Loopback: 127.0.0.0/8
- Link-local / cloud metadata: 169.254.0.0/16
- IPv6 equivalents: ::1/128, fc00::/7, fe80::/10

Both initial URLs and redirect targets are validated.

`WithAllowedHosts` (SDK only) adds an allowlist on top — any host not in the list is rejected regardless of whether it is public or private.

## TLS

### HTTPS for the Dashboard

**SDK:**

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithTLS("/etc/pulseboard/cert.pem", "/etc/pulseboard/key.pem"),
)
```

**YAML:**

```yaml
server:
  tls:
    cert_file: /etc/pulseboard/cert.pem
    key_file: /etc/pulseboard/key.pem
```

### Mutual TLS for Polling

When polled endpoints require client certificates:

**SDK:**

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithTLSMinVersion(tls.VersionTLS12),
    pulseboard.WithClientCert("/etc/pulseboard/client.pem", "/etc/pulseboard/client-key.pem"),
)
```

**YAML:**

```yaml
client:
  tls:
    min_version: "1.2"
    client_cert: /etc/pulseboard/client.pem
    client_key: /etc/pulseboard/client-key.pem
```

### Self-Signed Certificates (Dev Only)

**SDK:**

```go
pulseboard.WithInsecureSkipVerify()
```

**YAML:**

```yaml
client:
  tls:
    insecure_skip_verify: true
```

## Webhook Alerting

Send HTTP POST notifications when endpoint statuses change.

### Slack Integration

**SDK:**

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithStatusChangeCallback(
        pulseboard.WebhookNotifier(os.Getenv("SLACK_WEBHOOK_URL"),
            pulseboard.WithWebhookEventFilter("down", "degraded"),
            pulseboard.WithWebhookDebounce(30 * time.Second),
        ),
    ),
)
```

**YAML:**

```yaml
webhooks:
  - url: "${SLACK_WEBHOOK_URL}"
    events: [down, degraded]
    debounce: 30
```

### Multiple Notification Channels

**SDK:**

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    // Slack for warnings
    pulseboard.WithStatusChangeCallback(
        pulseboard.WebhookNotifier(os.Getenv("SLACK_WEBHOOK_URL"),
            pulseboard.WithWebhookEventFilter("down", "degraded"),
            pulseboard.WithWebhookDebounce(30 * time.Second),
        ),
    ),
    // PagerDuty for outages only
    pulseboard.WithStatusChangeCallback(
        pulseboard.WebhookNotifier("https://events.pagerduty.com/v2/enqueue",
            pulseboard.WithWebhookEventFilter("down"),
            pulseboard.WithWebhookDebounce(60 * time.Second),
            pulseboard.WithWebhookHeaders(map[string]string{
                "Authorization": "Bearer " + os.Getenv("PAGERDUTY_TOKEN"),
            }),
        ),
    ),
)
```

**YAML:**

```yaml
webhooks:
  - url: "${SLACK_WEBHOOK_URL}"
    events: [down, degraded]
    debounce: 30

  - url: "https://events.pagerduty.com/v2/enqueue"
    events: [down]
    debounce: 60
    headers:
      Authorization: "Bearer ${PAGERDUTY_TOKEN}"
```

### Webhook Payload

The POST body is a JSON-serialised `StatusChange`:

```json
{
  "endpoint_name": "Payment API",
  "url": "https://payments.example.com/health",
  "labels": {"env": "production", "team": "payments"},
  "previous_status": "up",
  "current_status": "down",
  "latency_ms": 5023,
  "checked_at": "2026-03-14T10:30:00Z",
  "error": "connection refused"
}
```

### Custom Callbacks (SDK Only)

For logic beyond webhooks, use `WithStatusChangeCallback` directly:

```go
pulseboard.WithStatusChangeCallback(func(change pulseboard.StatusChange) {
    if change.CurrentStatus == pulseboard.StatusDown {
        // page on-call, write to database, trigger runbook, etc.
        go notifyOnCall(change)
    }
})
```

## Prometheus Metrics

Expose a `/metrics` endpoint for Prometheus scraping.

**SDK:**

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithMetrics(),
)
```

**YAML:**

```yaml
metrics:
  enabled: true
```

### Available Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `pulseboard_info` | Gauge | Build information |
| `pulseboard_endpoint_status` | Gauge | Current status per endpoint (1=current, 0=not) |
| `pulseboard_polls_total` | Counter | Total polls by endpoint and result (success/error) |
| `pulseboard_poll_duration_seconds` | Histogram | Per-endpoint poll latency |
| `pulseboard_status_changes_total` | Counter | Status transitions by endpoint, from, and to |

### Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: pulseboard
    static_configs:
      - targets: ['localhost:8080']
    scrape_interval: 15s
```

## Stale Endpoint Detection

Mark endpoints as stale when polling results stop arriving (e.g., due to network partitions or scheduler issues).

**SDK:**

```go
pb, err := pulseboard.New(
    pulseboard.WithEndpoint(api),
    pulseboard.WithStaleThreshold(2 * time.Minute),
)
```

Default: 3x the polling interval. Pass `0` to disable.

Stale detection is an SDK-only option — it is not configurable via YAML.

## Full Production Example

### SDK

```go
package main

import (
    "context"
    "crypto/tls"
    "log/slog"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/jpalmerr/pulseboard"
)

func main() {
    api, _ := pulseboard.NewEndpoint("Payment API", "https://payments.example.com/health",
        pulseboard.WithLabels("env", "production", "team", "payments"),
        pulseboard.WithInterval(5 * time.Second),
    )

    pb, err := pulseboard.New(
        pulseboard.WithEndpoint(api),
        pulseboard.WithPort(8443),

        // Security
        pulseboard.WithTLS("/etc/pulseboard/cert.pem", "/etc/pulseboard/key.pem"),
        pulseboard.WithMiddleware(pulseboard.BearerToken(os.Getenv("DASHBOARD_TOKEN"))),
        pulseboard.WithBlockPrivateNetworks(),
        pulseboard.WithTLSMinVersion(tls.VersionTLS12),

        // Alerting
        pulseboard.WithStatusChangeCallback(
            pulseboard.WebhookNotifier(os.Getenv("SLACK_WEBHOOK_URL"),
                pulseboard.WithWebhookEventFilter("down", "degraded"),
                pulseboard.WithWebhookDebounce(30 * time.Second),
            ),
        ),

        // Observability
        pulseboard.WithMetrics(),
        pulseboard.WithStaleThreshold(90 * time.Second),
    )
    if err != nil {
        slog.Error("failed to create pulseboard", "error", err)
        os.Exit(1)
    }

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    if err := pb.Start(ctx); err != nil {
        slog.Error("pulseboard error", "error", err)
        os.Exit(1)
    }
}
```

### YAML

```yaml
title: Payment Gateway Status
port: 8443

# Security
block_private_networks: true

auth:
  type: bearer
  token: "${DASHBOARD_TOKEN}"

server:
  tls:
    cert_file: /etc/pulseboard/cert.pem
    key_file: /etc/pulseboard/key.pem

client:
  tls:
    min_version: "1.2"

# Monitoring
poll_interval: 5s

endpoints:
  - name: Payment API
    url: https://payments.example.com/health
    labels:
      env: production
      team: payments

# Alerting
webhooks:
  - url: "${SLACK_WEBHOOK_URL}"
    events: [down, degraded]
    debounce: 30

# Observability
metrics:
  enabled: true
```

```bash
DASHBOARD_TOKEN=secret SLACK_WEBHOOK_URL=https://hooks.slack.com/services/... \
  pulseboard serve -c config.yaml
```
