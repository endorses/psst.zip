FROM golang:1.23-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /server ./cmd/server

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -h /app appuser

WORKDIR /app

COPY --from=builder /server /app/server
COPY licenses /app/licenses

RUN mkdir -p /app/data/files && chown -R appuser:appuser /app

USER appuser

ENV LISTEN_ADDR=:8080 \
    STORAGE_PATH=/app/data/files \
    DB_PATH=/app/data/psst.db

EXPOSE 8080

VOLUME ["/app/data"]

ENTRYPOINT ["/app/server"]
