# ────────────────────────────────────────────────────────────────────────────
# Stage 1: Build
# ────────────────────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# Cache dependencies first
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build bot binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w -X main.version=$(git describe --tags --always 2>/dev/null || echo dev)" \
    -o /out/coinex-bot ./cmd/bot

# Build backtest binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" \
    -o /out/coinex-backtest ./cmd/backtest

# Build optimizer binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" \
    -o /out/coinex-optimize ./cmd/optimize

# ────────────────────────────────────────────────────────────────────────────
# Stage 2: Runtime
# ────────────────────────────────────────────────────────────────────────────
FROM scratch

# Copy certs and timezone data from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy binaries
COPY --from=builder /out/coinex-bot       /usr/local/bin/coinex-bot
COPY --from=builder /out/coinex-backtest  /usr/local/bin/coinex-backtest
COPY --from=builder /out/coinex-optimize  /usr/local/bin/coinex-optimize

# Config and data directories
COPY configs/ /configs/

VOLUME ["/data"]

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/coinex-bot"]
CMD ["--config", "/configs/config.yaml"]
