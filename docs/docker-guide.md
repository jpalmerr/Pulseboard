# Docker Guide

Run PulseBoard as a container without installing Go.

## Quick Start

```bash
# Create a config file
cat > config.yaml << 'EOF'
port: 8080
poll_interval: 10s

endpoints:
  - name: GitHub API
    url: https://api.github.com
  - name: Google
    url: https://www.google.com
EOF

# Run
docker run -v $(pwd)/config.yaml:/config.yaml -p 8080:8080 \
  ghcr.io/jpalmerr/pulseboard:latest
```

Open http://localhost:8080 to view the dashboard.

## Image Details

| Property | Value |
|----------|-------|
| Registry | `ghcr.io/jpalmerr/pulseboard` |
| Tags | `latest`, version tags (e.g., `v1.0.0`) |
| Base | Alpine Linux 3.19 |
| User | Non-root (`pulseboard`, UID 1000) |
| Exposed Port | 8080 (configurable) |

## Configuration

### Volume Mount

PulseBoard expects the config file at `/config.yaml`:

```bash
docker run -v /path/to/your/config.yaml:/config.yaml -p 8080:8080 \
  ghcr.io/jpalmerr/pulseboard:latest
```

### Environment Variables

Environment variables in your config are expanded at runtime:

```yaml
# config.yaml
endpoints:
  - name: API
    url: https://api.example.com/health
    headers:
      Authorization: "Bearer ${API_TOKEN}"
```

Pass them with `-e`:

```bash
docker run -v $(pwd)/config.yaml:/config.yaml -p 8080:8080 \
  -e API_TOKEN=your-secret-token \
  ghcr.io/jpalmerr/pulseboard:latest
```

### Custom Port

If your config uses a different port, update the port mapping:

```yaml
# config.yaml
port: 9090
```

```bash
docker run -v $(pwd)/config.yaml:/config.yaml -p 9090:9090 \
  ghcr.io/jpalmerr/pulseboard:latest
```

### Binding to Localhost Only

Restrict access to the host machine:

```bash
docker run -v $(pwd)/config.yaml:/config.yaml -p 127.0.0.1:8080:8080 \
  ghcr.io/jpalmerr/pulseboard:latest
```

## Docker Compose

```yaml
version: '3.8'

services:
  pulseboard:
    image: ghcr.io/jpalmerr/pulseboard:latest
    ports:
      - "8080:8080"
    volumes:
      - ./config.yaml:/config.yaml:ro
    environment:
      - API_TOKEN=${API_TOKEN}
    restart: unless-stopped
```

Run with:

```bash
docker-compose up -d
```

## Health Checks

Add a health check for orchestrators:

```yaml
version: '3.8'

services:
  pulseboard:
    image: ghcr.io/jpalmerr/pulseboard:latest
    ports:
      - "8080:8080"
    volumes:
      - ./config.yaml:/config.yaml:ro
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:8080/api/status"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 5s
```

## Kubernetes

Basic deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pulseboard
spec:
  replicas: 1
  selector:
    matchLabels:
      app: pulseboard
  template:
    metadata:
      labels:
        app: pulseboard
    spec:
      containers:
        - name: pulseboard
          image: ghcr.io/jpalmerr/pulseboard:latest
          ports:
            - containerPort: 8080
          volumeMounts:
            - name: config
              mountPath: /config.yaml
              subPath: config.yaml
          livenessProbe:
            httpGet:
              path: /api/status
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 30
          resources:
            requests:
              memory: "32Mi"
              cpu: "10m"
            limits:
              memory: "128Mi"
              cpu: "100m"
      volumes:
        - name: config
          configMap:
            name: pulseboard-config
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: pulseboard-config
data:
  config.yaml: |
    port: 8080
    poll_interval: 10s
    endpoints:
      - name: Example API
        url: https://api.example.com/health
---
apiVersion: v1
kind: Service
metadata:
  name: pulseboard
spec:
  selector:
    app: pulseboard
  ports:
    - port: 80
      targetPort: 8080
```

## Troubleshooting

### Config file not found

```
Error: open /config.yaml: no such file or directory
```

Ensure the volume mount path is correct:

```bash
# Wrong - missing leading slash in container path
docker run -v $(pwd)/config.yaml:config.yaml ...

# Correct
docker run -v $(pwd)/config.yaml:/config.yaml ...
```

### Permission denied

The container runs as non-root user (UID 1000). Ensure the config file is readable:

```bash
chmod 644 config.yaml
```

### Environment variable not expanded

Variables must use `${VAR}` syntax, not `$VAR`:

```yaml
# Wrong
url: https://api.example.com?key=$API_KEY

# Correct
url: https://api.example.com?key=${API_KEY}
```

### Port already in use

Change the host port (left side of `-p`):

```bash
docker run -v $(pwd)/config.yaml:/config.yaml -p 9090:8080 \
  ghcr.io/jpalmerr/pulseboard:latest
```

Then access at http://localhost:9090.

## Building Locally

For development, build the image locally:

```bash
# Build the binary
go build -o pulseboard ./cmd/pulseboard

# Build the image
docker build -t pulseboard:dev .

# Run
docker run -v $(pwd)/config.yaml:/config.yaml -p 8080:8080 pulseboard:dev
```
