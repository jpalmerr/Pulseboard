# PulseBoard Docker Image
#
# This Dockerfile is used by GoReleaser to build the container image.
# GoReleaser pre-builds the binary and copies it in.
#
# Usage with GoReleaser:
#   goreleaser release --snapshot --clean
#
# Manual build (for development):
#   go build -o pulseboard ./cmd/pulseboard
#   docker build -t pulseboard .
#
# Run:
#   docker run -v $(pwd)/config.yaml:/config.yaml -p 8080:8080 pulseboard

FROM alpine:3.19

# Install CA certificates for HTTPS and timezone data
RUN apk add --no-cache ca-certificates tzdata

# Copy pre-built binary (from GoReleaser or manual build)
COPY pulseboard /usr/local/bin/pulseboard
RUN chmod +x /usr/local/bin/pulseboard

# Create non-root user for security
RUN adduser -D -u 1000 pulseboard
USER pulseboard

# Default port (configurable via YAML)
EXPOSE 8080

ENTRYPOINT ["pulseboard"]
CMD ["serve", "-c", "/config.yaml"]
