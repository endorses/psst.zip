# Build locally; this helper is never an application or published release image.
# Record its actual image ID and installed packages with each source collection.
ARG ALPINE_IMAGE=alpine:3.21@sha256:ce64758a109eb420d874a118f87920e625e12d3634e03b4a5573fd9f6e5d3507
FROM ${ALPINE_IMAGE}

RUN apk add --no-cache abuild=3.14.1-r4 ca-certificates

USER 65532:65532
ENTRYPOINT ["abuild"]
