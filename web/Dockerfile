FROM node:22-alpine AS web-build

WORKDIR /build

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

FROM caddy:2-alpine

# The upstream binary carries NET_BIND_SERVICE. Remove it so cap_drop: ALL
# works; Compose maps public ports to unprivileged internal listeners.
RUN setcap -r /usr/bin/caddy && \
    addgroup -g 10001 psst && adduser -D -H -u 10001 -G psst psst && \
    chown -R psst:psst /data /config

COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=web-build /build/build /srv/web

USER 10001:10001
