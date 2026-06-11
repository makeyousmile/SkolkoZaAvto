# Stage 1: Build stage
FROM golang:1.22-alpine AS builder

# Install build tools
RUN apk add --no-cache git build-base

WORKDIR /app

# Copy dependency files
COPY go.mod go.sum ./

# Copy source code
COPY main.go ./
COPY handlers/ ./handlers/

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o skolkozaavto main.go

# Stage 2: Final runner stage
FROM alpine:latest

# Install ca-certificates to allow HTTPS requests (needed for Telegram validation and Geolocation APIs)
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy built binary
COPY --from=builder /app/skolkozaavto /app/skolkozaavto

# Copy frontend static files
COPY static/ ./static/

# Create persistent storage directories
RUN mkdir -p /app/data /app/uploads

# Expose port
EXPOSE 8080

# Environment variables
ENV PORT=8080

# Run the server
CMD ["./skolkozaavto"]
