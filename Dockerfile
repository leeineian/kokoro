# Build Stage
FROM golang:1.25.5-alpine AS builder

# Install build dependencies for CGO (required for SQLite)
RUN apk add --no-cache build-base

WORKDIR /app

# Copy dependency manifests
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
# CGO_ENABLED=1 is required for github.com/mattn/go-sqlite3
# Using -ldflags="-s -w" to reduce binary size
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -a -installsuffix cgo -o minder main.go

# Run Stage
FROM alpine:latest

# Install runtime dependencies
# ca-certificates: Required for HTTPS requests to Discord API
# tzdata: Allows the bot to handle timezones correctly
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/minder .

# Create directory for persistent data (SQLite)
RUN mkdir -p /app/data

# Run the bot
ENTRYPOINT ["./minder"]
