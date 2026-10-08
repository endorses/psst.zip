# Build locally; this helper is never an application or published release image.
# Record its actual image ID and installed packages with each source collection.
ARG ALPINE_IMAGE=alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
FROM ${ALPINE_IMAGE}

RUN apk add --no-cache abuild=3.17.0-r0 ca-certificates

USER 65532:65532
ENTRYPOINT ["abuild"]
