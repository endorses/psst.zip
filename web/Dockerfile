ARG NODE_IMAGE=node:22-alpine
ARG CADDY_IMAGE=caddy:2-alpine

FROM ${NODE_IMAGE} AS web-build

ARG SOURCE_URL=https://github.com/endorses/psst.zip
ARG VERSION=dev
ARG REVISION=main

WORKDIR /build

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN SOURCE_URL="$SOURCE_URL" VERSION="$VERSION" REVISION="$REVISION" \
    node --input-type=module -e 'import { writeFileSync } from "node:fs"; const { SOURCE_URL: source, VERSION: version, REVISION: revision } = process.env; writeFileSync("static/licenses/release.json", JSON.stringify({ name: "psst.zip", version, revision, license: "AGPL-3.0-only", source, source_archive: `${source}/archive/${revision}.tar.gz`, notice_files: ["/licenses/backend/THIRD_PARTY_NOTICES.txt"] }, null, 2) + "\n");' && \
    npm run build

FROM ${CADDY_IMAGE}

ARG SOURCE_URL=https://github.com/endorses/psst.zip
ARG VERSION=dev
ARG REVISION=main

LABEL org.opencontainers.image.title="psst.zip web" \
    org.opencontainers.image.source="${SOURCE_URL}" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${REVISION}" \
    org.opencontainers.image.licenses="AGPL-3.0-only" \
    org.opencontainers.image.url="${SOURCE_URL}/tree/${REVISION}" \
    zip.psst.source.archive="${SOURCE_URL}/archive/${REVISION}.tar.gz"

# The upstream binary carries NET_BIND_SERVICE. Remove it so cap_drop: ALL
# works; Compose maps public ports to unprivileged internal listeners.
RUN setcap -r /usr/bin/caddy && \
    addgroup -g 10001 psst && adduser -D -H -u 10001 -G psst psst && \
    chown -R psst:psst /data /config

COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=web-build /build/build /srv/web
COPY backend/licenses /srv/web/licenses/backend

USER 10001:10001
