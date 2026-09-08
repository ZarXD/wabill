# ==========================================
# Build Stage
# ==========================================
FROM golang:alpine AS builder

WORKDIR /app

ENV GOTOOLCHAIN=auto

# Install build dependencies
RUN apk add --no-cache ca-certificates tzdata git

# Copy dependency definitions
COPY go.mod go.sum ./
RUN go mod download

# Copy application source
COPY . .

# Build statically compiled binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/wabill ./cmd/bot

# ==========================================
# Runtime Stage
# ==========================================
FROM alpine:3.21

WORKDIR /app

# Install ca-certificates and timezone data
RUN apk add --no-cache ca-certificates tzdata

# Create data directories
RUN mkdir -p /data/payment-proofs

# Copy compiled binary from builder
COPY --from=builder /app/wabill /app/wabill

VOLUME ["/data/payment-proofs"]

CMD ["/app/wabill"]
