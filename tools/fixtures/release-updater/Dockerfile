# Isolated test daemon only; never mount the host Docker socket.
FROM docker:29-dind@sha256:7dcdfc4a20246236f558175182ccace1eb15a41bd3eb119dd2284f393498b7c1
RUN apk add --no-cache python3 py3-cryptography curl tar && \
    ln -s /usr/local/bin/docker /usr/bin/docker
