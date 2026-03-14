# CLI How-To Guide

This guide covers common tasks using the PulseBoard CLI.

## Installation

```bash
# Using go install
go install github.com/jpalmerr/pulseboard/cmd/pulseboard@latest

# Verify installation
pulseboard version
```

## Basic Usage

### Start the Dashboard

```bash
pulseboard serve -c config.yaml
```

The dashboard will be available at `http://localhost:8080` (or your configured port).

### Validate Configuration

Check your config file for errors without starting the server:

```bash
pulseboard validate -c config.yaml
```

## Configuration File Structure

### Minimal Configuration

```yaml
port: 8080
poll_interval: 10s

endpoints:
  - name: My API
    url: https://api.example.com/health
```

### Full Configuration Reference

```yaml
# Server settings
title: My Dashboard     # Dashboard title (default: "PulseBoard")
port: 8080              # HTTP port for dashboard (default: 8080)
poll_interval: 15s      # Global polling interval (default: 15s)

# SSRF protection — block polling to private networks
block_private_networks: true

# Dashboard authentication
auth:
  type: basic           # "basic" or "bearer"
  username: admin
  password: "${DASHBOARD_PASSWORD}"

# Server TLS (HTTPS for the dashboard)
server:
  tls:
    cert_file: /path/to/cert.pem
    key_file: /path/to/key.pem

# Client TLS (for polling endpoints)
client:
  tls:
    insecure_skip_verify: false       # skip cert verification (dev only)
    min_version: "1.2"                # "1.0", "1.1", "1.2", or "1.3"
    client_cert: /path/to/client.pem  # mTLS client certificate
    client_key: /path/to/client-key.pem

# Direct endpoints
endpoints:
  - name: My API                    # Display name (required)
    url: https://api.example.com    # URL to poll (required)
    method: GET                     # GET, HEAD, or POST (default: GET)
    timeout: 5s                     # Request timeout (default: 10s)
    interval: 30s                   # Override global poll interval
    headers:                        # Custom HTTP headers
      Authorization: "Bearer ${TOKEN}"
    labels:                         # Metadata for grouping
      env: production
      team: platform
    extractor: json:status          # Status extraction method

# Grid endpoints (generate multiple endpoints from a template)
grids:
  - name: Platform Services
    url_template: "https://{{.env}}.example.com/{{.svc}}/health"
    dimensions:
      env: [prod, staging]
      svc: [users, orders, payments]
    interval: 15s                   # Applied to all generated endpoints
    extractor:
      type: json
      path: data.health.status

# Webhook notifications on status transitions
webhooks:
  - url: "https://hooks.slack.com/services/${SLACK_WEBHOOK}"
    events: [down, degraded]        # omit for all transitions
    debounce: 30                    # seconds before firing (prevents flapping)
    timeout: 5                      # HTTP request timeout in seconds
    headers:
      Authorization: "Bearer ${WEBHOOK_TOKEN}"

# Prometheus metrics
metrics:
  enabled: true                     # exposes /metrics endpoint
```

## How-To Guides

### Monitor Multiple Environments

Use grids to monitor the same services across environments:

```yaml
poll_interval: 10s

grids:
  - name: API Services
    url_template: "https://{{.env}}.mycompany.com/{{.service}}/health"
    dimensions:
      env: [production, staging, development]
      service: [auth, users, orders, payments]
    extractor: json:status
```

This creates 12 endpoints (3 environments x 4 services) from a single declaration.

### Use Authentication

#### Static Bearer Token

```yaml
endpoints:
  - name: Protected API
    url: https://api.example.com/health
    headers:
      Authorization: "Bearer my-api-token"
```

#### Token from Environment Variable

```yaml
endpoints:
  - name: Protected API
    url: https://api.example.com/health
    headers:
      Authorization: "Bearer ${API_TOKEN}"
```

Run with:
```bash
API_TOKEN=your-secret-token pulseboard serve -c config.yaml
```

#### API Key Authentication

```yaml
endpoints:
  - name: API with Key
    url: https://api.example.com/health
    headers:
      X-API-Key: "${API_KEY}"
```

### Configure Status Extraction

#### JSON Field (Most Common)

For responses like `{"status": "healthy"}`:

```yaml
endpoints:
  - name: My API
    url: https://api.example.com/health
    extractor: json:status
```

For nested responses like `{"data": {"health": {"status": "ok"}}}`:

```yaml
endpoints:
  - name: My API
    url: https://api.example.com/health
    extractor: json:data.health.status
```

#### Plain Text Response

For endpoints returning plain text like "OK" or "healthy":

```yaml
endpoints:
  - name: Simple Health Check
    url: https://api.example.com/ping
    extractor: contains:ok
```

#### HTTP Status Code Only

For endpoints where you only care about 2xx responses:

```yaml
endpoints:
  - name: Basic Endpoint
    url: https://api.example.com
    extractor: http
```

### Set Different Polling Intervals

#### Per-Endpoint Intervals

Critical services can be polled more frequently:

```yaml
poll_interval: 30s  # Global default

endpoints:
  - name: Critical Payment API
    url: https://payments.example.com/health
    interval: 5s    # Override: poll every 5 seconds

  - name: Background Job Status
    url: https://jobs.example.com/health
    interval: 60s   # Override: poll every minute

  - name: Standard API
    url: https://api.example.com/health
    # Uses global 30s interval
```

#### Grid-Wide Intervals

```yaml
grids:
  - name: Critical Services
    url_template: "https://{{.env}}.example.com/{{.svc}}/health"
    dimensions:
      env: [prod]
      svc: [payments, auth]
    interval: 5s    # All generated endpoints polled every 5s

  - name: Standard Services
    url_template: "https://{{.env}}.example.com/{{.svc}}/health"
    dimensions:
      env: [prod, staging]
      svc: [users, orders]
    # Uses global poll_interval
```

### Use Environment Variables

#### Required Variables

```yaml
endpoints:
  - name: API
    url: https://${API_HOST}/health
```

If `API_HOST` is not set, validation fails with a clear error.

#### Variables with Defaults

```yaml
endpoints:
  - name: API
    url: https://${API_HOST:-localhost}:${API_PORT:-8080}/health
```

Uses defaults if environment variables are not set.

#### Multiple Variables

```yaml
endpoints:
  - name: Database Health
    url: https://${DB_HOST}:${DB_PORT:-5432}/health
    headers:
      Authorization: "Bearer ${DB_TOKEN}"
```

### Monitor Kubernetes Services

```yaml
poll_interval: 10s

grids:
  - name: K8s Services
    url_template: "http://{{.service}}.{{.namespace}}.svc.cluster.local:8080/health"
    dimensions:
      namespace: [production, staging]
      service: [api, worker, scheduler]
    extractor: json:status
```

### Use HEAD Requests

For endpoints where you only need to check reachability:

```yaml
endpoints:
  - name: Static Assets
    url: https://cdn.example.com/assets/main.js
    method: HEAD
    extractor: http
```

### Handle Slow Endpoints

Increase timeout for slow health checks:

```yaml
endpoints:
  - name: Slow Database Check
    url: https://db.example.com/health
    timeout: 30s    # Wait up to 30 seconds
```

### Customise the Dashboard Title

Set a custom title for the browser tab and page header:

```yaml
title: Payment Gateway Status
port: 8080

endpoints:
  - name: Stripe
    url: https://api.stripe.com/health
  - name: PayPal
    url: https://api.paypal.com/health
```

This is useful when running multiple dashboards or embedding in internal tools.

### Protect the Dashboard with Authentication

#### Basic Auth

```yaml
auth:
  type: basic
  username: admin
  password: "${DASHBOARD_PASSWORD}"
```

Run with:
```bash
DASHBOARD_PASSWORD=secret pulseboard serve -c config.yaml
```

#### Bearer Token

```yaml
auth:
  type: bearer
  token: "${DASHBOARD_TOKEN}"
```

Multiple tokens are supported:

```yaml
auth:
  type: bearer
  tokens:
    - "${TOKEN_TEAM_A}"
    - "${TOKEN_TEAM_B}"
```

### Set Up Webhook Notifications

Send alerts when endpoints change status:

```yaml
webhooks:
  - url: "https://hooks.slack.com/services/${SLACK_WEBHOOK}"
    events: [down, degraded]
    debounce: 30
```

Multiple webhooks can be configured:

```yaml
webhooks:
  - url: "https://hooks.slack.com/services/${SLACK_WEBHOOK}"
    events: [down, degraded]
    debounce: 30

  - url: "https://events.pagerduty.com/v2/enqueue"
    events: [down]
    debounce: 60
    headers:
      Authorization: "Bearer ${PAGERDUTY_TOKEN}"
```

| Field | Default | Description |
|-------|---------|-------------|
| `url` | (required) | HTTP endpoint to POST status changes to |
| `events` | all | Status values that trigger the webhook |
| `debounce` | 0 | Seconds the status must hold before firing |
| `timeout` | 10 | HTTP request timeout in seconds |
| `headers` | none | Custom HTTP headers (supports `${VAR}` expansion) |

### Enable SSRF Protection

Block polling to private networks (recommended when endpoint URLs come from untrusted config):

```yaml
block_private_networks: true
```

Blocks RFC1918 (10.x, 172.16.x, 192.168.x), loopback (127.x), link-local (169.254.x, cloud metadata), and IPv6 equivalents.

### Enable TLS

#### HTTPS for the Dashboard

```yaml
server:
  tls:
    cert_file: /path/to/cert.pem
    key_file: /path/to/key.pem
```

#### Client TLS for Polling

```yaml
client:
  tls:
    min_version: "1.2"
    client_cert: /path/to/client-cert.pem
    client_key: /path/to/client-key.pem
```

For development with self-signed certificates:

```yaml
client:
  tls:
    insecure_skip_verify: true
```

### Enable Prometheus Metrics

Expose a `/metrics` endpoint in Prometheus text format:

```yaml
metrics:
  enabled: true
```

Records per-endpoint poll latency histograms, status transition counters, and error rates. Scrape at `http://localhost:8080/metrics`.

## Recognised Status Values

When using JSON extractors, these values are recognised:

| Status | Values |
|--------|--------|
| **Up** | `ok`, `healthy`, `up`, `active`, `running`, `pass`, `passed`, `true`, `green`, `none`, `operational` |
| **Degraded** | `degraded`, `warning`, `partial`, `yellow`, `amber` |
| **Down** | Any other value |
| **Error** | Automatic — assigned when the extractor itself fails (JSON parse error, regex mismatch, panic) |

## Troubleshooting

### Port Already in Use

```
Error: listen tcp :8080: bind: address already in use
```

Change the port in your config:
```yaml
port: 9090
```

### Environment Variable Not Set

```
Error: environment variable API_TOKEN is not set
```

Either set the variable or provide a default:
```yaml
# Option 1: Set the variable
# export API_TOKEN=your-token

# Option 2: Provide a default
url: https://api.example.com?token=${API_TOKEN:-default-token}
```

### Invalid Extractor Syntax

```
Error: invalid extractor shorthand: "json"
```

Ensure you provide a path: `json:status` not just `json`.

### Config Validation

Always validate before running:
```bash
pulseboard validate -c config.yaml
```
