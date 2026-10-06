ARG GO_IMAGE=golang:1.26.8-alpine
ARG RUNTIME_IMAGE=alpine:3.21

FROM ${GO_IMAGE} AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /server ./cmd/server

FROM ${RUNTIME_IMAGE}

ARG SOURCE_URL=https://github.com/endorses/psst.zip
ARG VERSION=dev
ARG REVISION=main

LABEL org.opencontainers.image.title="psst.zip backend" \
    org.opencontainers.image.source="${SOURCE_URL}" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${REVISION}" \
    org.opencontainers.image.licenses="AGPL-3.0-only" \
    org.opencontainers.image.url="${SOURCE_URL}/tree/${REVISION}" \
    zip.psst.source.archive="${SOURCE_URL}/archive/${REVISION}.tar.gz"

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -h /app appuser

WORKDIR /app

COPY --from=builder /server /app/server
COPY --from=builder /usr/local/go/LICENSE /app/licenses/go/LICENSE
COPY licenses /app/licenses

RUN printf 'psst.zip %s\nRevision: %s\nSource: %s/archive/%s.tar.gz\nLicense: AGPL-3.0-only\n' \
    "$VERSION" "$REVISION" "$SOURCE_URL" "$REVISION" > /app/licenses/SOURCE.txt

RUN mkdir -p /app/data/files && chown -R appuser:appuser /app

USER appuser

ENV LISTEN_ADDR=:8080 \
    STORAGE_PATH=/app/data/files \
    DB_PATH=/app/data/psst.db

EXPOSE 8080

VOLUME ["/app/data"]

ENTRYPOINT ["/app/server"]
