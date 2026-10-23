"""Temporary read-only API boundary for unmigrated source-CI measurements.

Publication uses Go and supported gh/skopeo commands. Remove this adapter when
the last Python measurement/recovery consumer moves to Go; do not change helpers
captured in supported immutable deployment bundles.
"""

from __future__ import annotations

import http.client
import socket
import threading
from dataclasses import dataclass
from urllib.parse import urlsplit

from release_artifacts import InvalidRelease, require

API_VERSION = "2026-03-10"
MAX_JSON = 16 * 1024**2


@dataclass(frozen=True)
class Response:
    status: int
    headers: dict[str, str]
    body: bytes = b""


class HTTPS:
    """Read only the fixed GitHub API; no proxy, redirect, upload or mutation."""

    def request(
        self, method: str, url: str, *, headers: dict | None = None
    ) -> Response:
        parsed = urlsplit(url)
        require(
            method == "GET"
            and parsed.scheme == "https"
            and parsed.hostname == "api.github.com"
            and parsed.port in {None, 443}
            and parsed.username is None
            and not parsed.fragment,
            "Untrusted measurement API route",
        )
        connection = http.client.HTTPSConnection("api.github.com", timeout=30)

        def abort():
            if connection.sock is not None:
                try:
                    connection.sock.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
            connection.close()

        watchdog = threading.Timer(180, abort)
        watchdog.daemon = True
        watchdog.start()
        try:
            connection.request(
                "GET",
                parsed.path + ("?" + parsed.query if parsed.query else ""),
                headers=dict(headers or {}),
            )
            response = connection.getresponse()
            metadata = {name.lower(): value for name, value in response.getheaders()}
            length = metadata.get("content-length")
            require(
                length is None or length.isdecimal() and int(length) <= MAX_JSON,
                "Measurement API response exceeds bound",
            )
            content = response.read(MAX_JSON + 1)
            require(len(content) <= MAX_JSON, "Measurement API response exceeds bound")
            return Response(response.status, metadata, content)
        except (OSError, http.client.HTTPException, ValueError):
            raise InvalidRelease("Measurement API inspection failed") from None
        finally:
            watchdog.cancel()
            connection.close()
