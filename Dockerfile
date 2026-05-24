# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache dependencies separately from source
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a statically linked binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags="-s -w" \
      -o /mockwave \
      ./cmd/mockwave/

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

# Copy the compiled binary from the builder stage
COPY --from=builder /mockwave /mockwave

# Expose mock server, admin UI, and gRPC ports
EXPOSE 8080 9090 50051

ENTRYPOINT ["/mockwave"]
