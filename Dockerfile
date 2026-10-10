# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
COPY backend ./backend
COPY cmd ./cmd
COPY frontend ./frontend

RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /src/bin/brainbird-api ./cmd/api

FROM alpine:3.21

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata curl

COPY --from=builder /src/bin/brainbird-api /app/api
COPY frontend/ui /app/frontend/ui

ENV APP_ENV=production \
    HTTP_ADDR=:8081 \
    DATABASE_URL=root@tcp(mariadb:3306)/brainbir1_brainbird_local?parseTime=true \
    REDIS_ADDR=redis:6379 \
    JWT_SECRET=production-secret-change-me

EXPOSE 8081

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8081/api/v1/health || exit 1

ENTRYPOINT ["/app/api"]
