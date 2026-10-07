#!/usr/bin/env python3
"""Smoke existing release images in a disposable, loopback-only Compose project.

No image pulls, production access, TLS/ACME claims or global Docker cleanup.
"""

import argparse
import hashlib
import io
import ipaddress
import json
import os
from pathlib import Path, PurePosixPath
import re
import secrets
import subprocess
import tarfile
import tempfile
import time
import urllib.error
import urllib.request
import http.cookiejar

ROOT = Path(__file__).resolve().parents[1]
ENV = {
    key: value
    for key, value in os.environ.items()
    if key
    in {
        "PATH",
        "HOME",
        "DOCKER_HOST",
        "DOCKER_CONTEXT",
        "DOCKER_CONFIG",
        "DOCKER_CERT_PATH",
        "DOCKER_TLS_VERIFY",
        "XDG_RUNTIME_DIR",
    }
}


def run(*command):
    result = subprocess.run(command, env=ENV, capture_output=True, timeout=90)
    if result.returncode:
        raise RuntimeError(
            "Docker command failed; output withheld to protect fixture secrets"
        )
    return result.stdout


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def check_runtime_offer(get, pack):
    """Check anonymous served bytes against the actual reviewed overlay pack."""
    overlays = pack.get("overlays", {}).get("web")
    require(
        isinstance(overlays, dict) and overlays, "Runtime overlay inventory missing"
    )
    required = {"THIRD_PARTY_NOTICES.txt", "runtime-inventory.json", "SOURCE.txt"}
    require(required <= set(overlays), "Runtime notice/source files missing")
    offered = {}
    for relative, checksum in overlays.items():
        path = PurePosixPath(relative)
        require(
            isinstance(relative, str)
            and str(path) == relative
            and not path.is_absolute()
            and ".." not in path.parts
            and "\\" not in relative
            and relative
            and isinstance(checksum, str)
            and re.fullmatch(r"[0-9a-f]{64}", checksum),
            "Unsafe runtime overlay path or checksum",
        )
        body = get("/licenses/runtime/" + relative)
        require(
            hashlib.sha256(body).hexdigest() == checksum,
            "Served runtime license bytes differ",
        )
        offered[relative] = body
    asset = pack.get("source_asset")
    require(
        isinstance(asset, dict)
        and isinstance(asset.get("url"), str)
        and asset["url"].startswith("https://")
        and isinstance(asset.get("sha256"), str)
        and re.fullmatch(r"[0-9a-f]{64}", asset["sha256"]),
        "Runtime source asset identity missing",
    )
    for value in (asset["url"], asset["sha256"]):
        require(
            value.encode() in offered["SOURCE.txt"],
            "Runtime source offer omits exact archive identity",
        )
    require(offered["THIRD_PARTY_NOTICES.txt"].strip(), "Runtime notices are empty")


def subnet():
    networks = run("docker", "network", "ls", "-q").decode().split()
    occupied = []
    if networks:
        for network in json.loads(run("docker", "network", "inspect", *networks)):
            occupied.extend(
                ipaddress.ip_network(item["Subnet"])
                for item in (network["IPAM"]["Config"] or [])
                if item.get("Subnet")
            )
    for _ in range(100):
        candidate = ipaddress.ip_network(
            f"10.200.{secrets.randbelow(256)}.{secrets.randbelow(32) * 8}/29"
        )
        if not any(
            candidate.overlaps(other) for other in occupied if other.version == 4
        ):
            return candidate
    raise RuntimeError("No unused smoke subnet found")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("backend-image", "web-image", "version", "revision"):
        parser.add_argument(f"--{name}", required=True)
    parser.add_argument(
        "--platform", choices=("linux/amd64", "linux/arm64"), required=True
    )
    parser.add_argument("--source-url", default="https://github.com/endorses/psst.zip")
    parser.add_argument(
        "--runtime-pack",
        type=Path,
        help="Require exact runtime overlay/source bytes and discoverable metadata",
    )
    args = parser.parse_args()
    architecture = args.platform.split("/")[1]
    source_archive = f"{args.source_url}/archive/{args.revision}.tar.gz"
    stage = "image metadata"
    project = f"psst-release-smoke-{secrets.token_hex(8)}"
    owned = False
    compose = []
    try:
        runtime_pack = None
        if args.runtime_pack:
            from package_runtime_sources import verify_overlays
            from release_artifacts import read_bounded_file, read_json

            runtime_pack = read_json(
                read_bounded_file(args.runtime_pack / "runtime-pack.json")
            )
            require(
                isinstance(runtime_pack, dict)
                and runtime_pack.get("version") == args.version
                and runtime_pack.get("revision") == args.revision
                and runtime_pack.get("architecture") == architecture,
                "Runtime pack belongs to another tested release/platform",
            )
            verify_overlays(args.runtime_pack, args.backend_image, args.web_image)
        for image, title in ((args.backend_image, "backend"), (args.web_image, "web")):
            # Load/pull the selected platform beforehand. Plain inspect supports
            # Docker 27; the newer --platform inspection flag is not required.
            metadata = json.loads(run("docker", "image", "inspect", image))[0]
            require(
                metadata["Architecture"] == architecture and metadata["Os"] == "linux",
                "Image architecture differs",
            )
            require(
                metadata["Config"]["User"].split(":")[0] not in ("", "0", "root"),
                "Image runs as root",
            )
            labels = metadata["Config"]["Labels"] or {}
            expected = {
                "org.opencontainers.image.title": f"psst.zip {title}",
                "org.opencontainers.image.source": args.source_url,
                "org.opencontainers.image.version": args.version,
                "org.opencontainers.image.revision": args.revision,
                "org.opencontainers.image.licenses": "AGPL-3.0-only",
                "org.opencontainers.image.url": f"{args.source_url}/tree/{args.revision}",
                "zip.psst.source.archive": source_archive,
            }
            require(
                all(labels.get(key) == value for key, value in expected.items()),
                "Image labels differ",
            )
        native = run("docker", "info", "--format", "{{.Architecture}}").decode().strip()
        native = {"x86_64": "amd64", "aarch64": "arm64"}.get(native, native)
        stage = "isolated fixture setup"
        require(
            not run(
                "docker",
                "ps",
                "-aq",
                "--filter",
                f"label=com.docker.compose.project={project}",
            ).strip(),
            "Project already exists",
        )
        names = run("docker", "volume", "ls", "-q").decode().split()
        require(
            not any(name.startswith(project + "_") for name in names),
            "Fixture volumes already exist",
        )
        private = subnet()
        with tempfile.TemporaryDirectory(prefix="psst-release-smoke-") as temp:
            env_file, overlay = Path(temp) / "fixture.env", Path(temp) / "overlay.yml"
            username, password = "smoke-admin", secrets.token_urlsafe(32)
            values = {
                "COMPOSE_PROJECT_NAME": project,
                "BACKEND_IMAGE": args.backend_image,
                "WEB_IMAGE": args.web_image,
                "PSST_DOMAIN": "http://:8080",
                "PUBLIC_URL": "",
                "AUTH_ALLOW_INSECURE_HTTP": "true",
                "ADMIN_USERNAME": username,
                "ADMIN_PASSWORD": password,
                "PSST_PRIVATE_SUBNET": str(private),
                "PSST_BACKEND_IP": str(private[2]),
                "PSST_PROXY_IP": str(private[3]),
            }

            def write_env():
                require(
                    all(
                        "\n" not in str(value) and "\r" not in str(value)
                        for value in values.values()
                    ),
                    "Unsafe fixture environment value",
                )
                env_file.write_text(
                    "".join(f"{key}={value}\n" for key, value in values.items())
                )
                env_file.chmod(0o600)

            write_env()
            overlay.write_text(
                f'services:\n  backend:\n    platform: {args.platform}\n    pull_policy: never\n  caddy:\n    platform: {args.platform}\n    pull_policy: never\n    ports: !override\n      - "127.0.0.1::8080"\n'
            )
            compose = [
                "docker",
                "compose",
                "--project-name",
                project,
                "--env-file",
                str(env_file),
                "-f",
                str(ROOT / "deploy/compose.release.yml"),
                "-f",
                str(overlay),
            ]
            owned = True
            try:
                stage = "startup"
                run(*compose, "up", "-d", "--no-build", "--pull", "never")
                ids = {
                    name: run(*compose, "ps", "-q", name).decode().strip()
                    for name in ("backend", "caddy")
                }
                containers = {
                    name: json.loads(run("docker", "inspect", cid))[0]
                    for name, cid in ids.items()
                }
                stage = "container hardening"
                for container in containers.values():
                    host = container["HostConfig"]
                    require(
                        host["ReadonlyRootfs"]
                        and "ALL" in host["CapDrop"]
                        and "no-new-privileges:true" in host["SecurityOpt"],
                        "Container hardening differs",
                    )
                require(
                    not containers["backend"]["HostConfig"]["PortBindings"],
                    "Backend publishes ports",
                )
                binding = containers["caddy"]["NetworkSettings"]["Ports"]["8080/tcp"]
                require(
                    len(binding) == 1 and binding[0]["HostIp"] == "127.0.0.1",
                    "Proxy port is not loopback-only",
                )
                origin = f"http://127.0.0.1:{binding[0]['HostPort']}"
                cookies = http.cookiejar.CookieJar()
                client = urllib.request.build_opener(
                    urllib.request.ProxyHandler({}),
                    urllib.request.HTTPCookieProcessor(cookies),
                )

                def get(route, payload=None, content_types=None):
                    data = None if payload is None else json.dumps(payload).encode()
                    request = urllib.request.Request(
                        origin + route,
                        data,
                        headers={"Content-Type": "application/json", "Origin": origin},
                    )
                    with client.open(request, timeout=10) as response:
                        if content_types:
                            require(
                                response.headers.get_content_type() in content_types,
                                "Compiled asset content type differs",
                            )
                        body = response.read(16 * 1024 * 1024 + 1)
                        require(
                            len(body) <= 16 * 1024 * 1024,
                            "Smoke HTTP response exceeds size bound",
                        )
                        return body

                def ready():
                    for _ in range(60):
                        try:
                            health = json.loads(get("/api/v1/health"))
                            if (
                                health.get("service") == "psst.zip"
                                and health.get("api_version") == 1
                            ):
                                return
                        except (OSError, ValueError):
                            pass
                        time.sleep(1)
                    raise RuntimeError("Backend readiness timed out")

                stage = "health and public config"
                ready()
                require(
                    isinstance(json.loads(get("/api/v1/config")), dict),
                    "Public config is invalid",
                )
                stage = "compiled web document and asset"
                document = get("/")
                require(b"psst-web" in document, "Web document marker missing")
                asset = re.search(
                    rb"""(?:href|src)=["'](/_app/[^"']+\.(?:js|css))["']""",
                    document,
                )
                require(asset is not None, "Compiled asset reference missing")
                asset_path = asset[1].decode()
                types = (
                    ("text/css",)
                    if asset_path.endswith(".css")
                    else ("text/javascript", "application/javascript")
                )
                content = get(asset_path, content_types=types)
                require(
                    content
                    and not content.lstrip()
                    .lower()
                    .startswith((b"<!doctype", b"<html")),
                    "Compiled asset returned HTML",
                )
                stage = "runtime licenses and source offer"
                archive = tarfile.open(
                    fileobj=io.BytesIO(
                        run(
                            "docker",
                            "exec",
                            ids["backend"],
                            "tar",
                            "-cf",
                            "-",
                            "-C",
                            "/app/licenses",
                            ".",
                        )
                    )
                )
                files = {
                    item.name.removeprefix("./"): archive.extractfile(item).read()
                    for item in archive
                    if item.isfile()
                }
                license_text = (ROOT / "LICENSE").read_bytes()
                require(
                    files.get("AGPL-3.0-only.txt") == license_text
                    and len(files.get("go/LICENSE", b"")) > 1000,
                    "Backend license text differs",
                )
                require(
                    source_archive.encode() in files.get("SOURCE.txt", b""),
                    "Backend source locator differs",
                )
                inventory = json.loads(files["dependency-inventory.json"])
                require(inventory["modules"], "Backend dependency inventory is empty")
                for module in inventory["modules"]:
                    for notice in module["notices"]:
                        require(
                            hashlib.sha256(files[notice["path"]]).hexdigest()
                            == notice["sha256"],
                            "Backend dependency license differs",
                        )
                require(
                    get("/licenses/AGPL-3.0-only.txt") == license_text,
                    "Served AGPL license differs",
                )
                require(
                    get("/licenses/backend/THIRD_PARTY_NOTICES.txt")
                    == files["THIRD_PARTY_NOTICES.txt"]
                    and get("/licenses/backend/dependency-inventory.json")
                    == files["dependency-inventory.json"],
                    "Hosted backend notices differ from the paired backend image",
                )
                notice_text = get("/licenses/THIRD_PARTY_NOTICES.txt")
                for name in ("svelte", "@sveltejs/kit", "devalue", "esm-env"):
                    require(
                        f"=== {name}@".encode() in notice_text,
                        "Browser runtime notice missing",
                    )
                release = json.loads(get("/licenses/release.json"))
                require(
                    release
                    == {
                        "name": "psst.zip",
                        "version": args.version,
                        "revision": args.revision,
                        "license": "AGPL-3.0-only",
                        "source": args.source_url,
                        "source_archive": source_archive,
                        "notice_files": ["/licenses/backend/THIRD_PARTY_NOTICES.txt"]
                        + (
                            ["/licenses/runtime/THIRD_PARTY_NOTICES.txt"]
                            if runtime_pack
                            else []
                        ),
                    },
                    "Served source metadata differs",
                )
                if runtime_pack:
                    check_runtime_offer(get, runtime_pack)
                stage = "initial administrator authentication"
                credentials = {
                    "username": username,
                    "password": password,
                    "session_type": "web",
                }
                user = json.loads(get("/api/v1/auth/login", credentials))["user"]
                require(
                    user["role"] == "admin" and user["username"] == username,
                    "Administrator authentication differs",
                )
                require(
                    json.loads(get("/api/v1/auth/me"))["user"]["id"] == user["id"],
                    "Administrator session is invalid",
                )
                stage = "restart with bootstrap credentials removed"
                run(*compose, "stop", "backend")
                values["ADMIN_USERNAME"] = values["ADMIN_PASSWORD"] = ""
                write_env()
                run(
                    *compose,
                    "up",
                    "-d",
                    "--no-deps",
                    "--force-recreate",
                    "--pull",
                    "never",
                    "backend",
                )
                ready()
                require(
                    json.loads(get("/api/v1/auth/me"))["user"]["id"] == user["id"],
                    "Session did not persist",
                )
                require(
                    json.loads(get("/api/v1/auth/login", credentials))["user"]["id"]
                    == user["id"],
                    "Administrator did not persist",
                )
            finally:
                if owned:
                    run(
                        *compose,
                        "down",
                        "--volumes",
                        "--remove-orphans",
                        "--timeout",
                        "10",
                    )
                    owned = False
                    for kind, command in (
                        ("containers", ("docker", "ps", "-aq")),
                        ("volumes", ("docker", "volume", "ls", "-q")),
                        ("networks", ("docker", "network", "ls", "-q")),
                    ):
                        require(
                            not run(
                                *command,
                                "--filter",
                                f"label=com.docker.compose.project={project}",
                            ).strip(),
                            f"Owned fixture {kind} remain after cleanup",
                        )
        require(not Path(temp).exists(), "Temporary fixture files remain")
        print(
            f"Release image smoke passed: {args.platform} ({'native' if native == architecture else 'emulated'}); disposable HTTP fixture only; owned resources and temporary files removed."
        )
    except Exception as error:
        print(
            f"Release image smoke failed during {stage}: {type(error).__name__}. Captured output withheld to protect fixture secrets."
        )
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
