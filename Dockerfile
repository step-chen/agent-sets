# =============================================================================
# Build Stage
# =============================================================================
FROM golang:1.25-bookworm AS builder

# Add image metadata following OCI standard
LABEL org.opencontainers.image.authors="https://github.com/step-chen" \
    org.opencontainers.image.description="PR Review Automation - An AI-powered pull request review service built with Google ADK-Go and MCP (Model Context Protocol)" \
    org.opencontainers.image.vendor="https://github.com/step-chen" \
    org.opencontainers.image.source="https://github.com/step-chen/agent-sets"

# Install necessary build tools (Debian uses apt)
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy dependency files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Run unit tests to ensure stability before final build
RUN go test ./... -v

# Build the application
# CGO_ENABLED=0 for static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags '-static'" \
    -o /app/pr-review-server \
    ./cmd/server

# =============================================================================
# Production Stage
# =============================================================================
FROM debian:bookworm-slim AS production

# Security: Run as non-root user
# Debian uses 'adduser' with different flags
RUN groupadd -g 1000 appgroup && \
    useradd -u 1000 -g appgroup -s /bin/sh -m appuser

# Install CA certificates and basic utilities for networking/debugging
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Create directories for config and logs
RUN mkdir -p /app/config /app/logs && \
    chown -R appuser:appgroup /app

# Copy binary from builder
COPY --from=builder /app/pr-review-server /app/pr-review-server
COPY --from=builder /app/prompts /app/prompts

# Default environment variables
ENV LOG_LEVEL=INFO \
    LOG_FORMAT=text \
    LOG_OUTPUT=stdout \
    CONFIG_PATH=/app/config/config.yaml

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8080

# Health check (using curl on Debian)
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

# Run the application
ENTRYPOINT ["/app/pr-review-server"]
