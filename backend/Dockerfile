# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS builder

WORKDIR /src/backend

# Install git and ca-certificates
RUN apk add --no-cache git ca-certificates

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy backend source code
COPY . .

# Build statically compiled binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /src/bin/brainbird-api ./cmd/api

# --- Runtime Stage ---
FROM alpine:3.21

WORKDIR /app/backend

RUN apk add --no-cache ca-certificates tzdata curl

# Copy binary from builder
COPY --from=builder /src/bin/brainbird-api /app/backend/api

# Copy frontend static files so relative path "../frontend/ui" works seamlessly
COPY ../frontend/ui /app/frontend/ui

# Set production environment defaults
ENV APP_ENV=production \
    HTTP_ADDR=:8081 \
    DATABASE_URL=root@tcp(mariadb:3306)/brainbir1_brainbird_local?parseTime=true \
    REDIS_ADDR=redis:6379 \
    JWT_SECRET=production-secret-change-me

EXPOSE 8081

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8081/api/v1/health || exit 1

ENTRYPOINT ["/app/backend/api"]
