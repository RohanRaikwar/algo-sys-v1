# ─── Multi-stage Dockerfile for trading-systemv1 ───
# Produces ~50 MB final image with all 4 service binaries.
# Usage: docker compose -f docker-compose.core.yml up -d

# ── Build stage ──
FROM golang:1.21-bookworm AS builder

WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ .

# CGO_ENABLED=1 required for go-sqlite3
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /bin/mdengine   ./cmd/mdengine/
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /bin/indengine  ./cmd/indengine/
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /bin/api_gateway ./cmd/api_gateway/
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /bin/tickserver ./cmd/tickserver/

# ── Runtime stage ──
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    ca-certificates \
    sqlite3 \
    wget \
    tzdata && \
    rm -rf /var/lib/apt/lists/*

# IST timezone for market hours logic
ENV TZ=Asia/Kolkata

COPY --from=builder /bin/mdengine   /usr/local/bin/mdengine
COPY --from=builder /bin/indengine  /usr/local/bin/indengine
COPY --from=builder /bin/api_gateway /usr/local/bin/api_gateway
COPY --from=builder /bin/tickserver /usr/local/bin/tickserver

RUN mkdir -p /data

WORKDIR /app
