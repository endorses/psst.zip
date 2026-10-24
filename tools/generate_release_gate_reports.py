"""Run the retained native smoke experiment and describe its measured bytes.

Shared Go tooling owns source CI, source/OCI replay and release result aggregation.
This adapter cannot sign, approve or publish a release.
"""

from __future__ import annotations

import os
import sys
import tempfile
from dataclasses import dataclass
from datetime import datetime
from functools import cache
from pathlib import Path
from typing import Protocol

from assemble_release_oci import inspect_archive
from publish_container_release import (
    SOURCE_NAME,
    Binding,
    sha256,
)
from release_artifacts import (
    COMMIT,
    DIGEST,
    PLATFORMS,
    VERSION,
    fields,
    matches,
    read_bounded_file,
    read_json,
    repository_name,
    require,
)
from release_process import command

ROOT = Path(__file__).resolve().parents[1]
CHECKS = frozenset(
    {
        "image-metadata",
        "fixture-isolation",
        "container-hardening",
        "health-public-config",
        "compiled-web-assets",
        "application-notices",
        "runtime-offer",
        "administrator-authentication",
        "credential-free-restart",
        "cleanup",
    }
)
SMOKE_FIELDS = {
    "schema_version",
    "kind",
    "version",
    "revision",
    "platform",
    "execution",
    "tested_configs",
    "runtime_pack_sha256",
    "checks",
    "completed_at",
    "publication_authorized",
}


class MeasurementAuthenticator(Protocol):
    def authenticate(self, content: bytes, binding: Binding) -> None: ...


@dataclass(frozen=True)
class NativeSourceContext:
    """Matrix-local identity, available before cross-platform release assembly."""

    repository: str
    version: str
    commit: str
    platform: str

    def checked(self) -> dict[str, str]:
        repository_name(self.repository)
        matches(self.version, VERSION, "Invalid native source version")
        matches(self.commit, COMMIT, "Invalid native source commit")
        require(self.platform in PLATFORMS, "Invalid native source platform")
        return {
            "repository": self.repository,
            "version": self.version,
            "commit": self.commit,
            "platform": self.platform,
        }


def checked_binding(binding: Binding) -> dict[str, str]:
    repository_name(binding.repository)
    matches(binding.version, VERSION, "Invalid report version")
    matches(binding.commit, COMMIT, "Invalid report source commit")
    subjects = dict(binding.subjects)
    require(len(subjects) == len(binding.subjects), "Duplicate report subjects")
    require(
        {
            "manifest",
            "bundle",
            "backend-index",
            "web-index",
            "backend-amd64",
            "backend-arm64",
            "web-amd64",
            "web-arm64",
        }
        <= set(subjects)
        and any(key.startswith("source:") for key in subjects),
        "Reports require the complete image/file/source binding",
    )
    return subjects


def timestamp(value: object) -> str:
    require(isinstance(value, str) and 0 < len(value) <= 64, "Missing completion time")
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        require(False, "Invalid completion time")
    require(parsed.tzinfo is not None, "Completion time lacks timezone")
    return value


@cache
def _release_tool() -> str:
    # Transitional native-orchestration adapter. The source, not this adapter,
    # owns collection/replay. Remove with the remaining Python native harness.
    return (
        command(
            ["sh", str(ROOT / "tools/release/build.sh")],
            environment=dict(os.environ),
            timeout=300,
        )
        .decode()
        .strip()
    )


def source_command(context: NativeSourceContext, mode: str, *args: str) -> bytes:
    context.checked()
    return command(
        [
            _release_tool(),
            "--root",
            str(ROOT),
            "sources",
            "runtime",
            mode,
            "--repository",
            context.repository,
            "--version",
            context.version,
            "--commit",
            context.commit,
            "--platform",
            context.platform,
            *args,
        ],
        environment=dict(os.environ),
        timeout=1800,
    )


def runtime_inputs(context: NativeSourceContext, pack: Path) -> tuple[dict, dict]:
    context.checked()
    raw = read_bounded_file(pack / "source-inventory.json")
    inventory = read_json(raw)
    require(
        isinstance(inventory, dict)
        and inventory.get("schema_version") == 2
        and inventory.get("kind") == "container-source-inventory"
        and inventory.get("source")
        == {
            "repository": context.repository,
            "version": context.version,
            "commit": context.commit,
        },
        "Runtime inventory belongs to another release",
    )
    record = read_json(source_command(context, "describe", "--collection", str(pack)))
    require(
        record.get("schema_version") == 2
        and record.get("kind") == "native-runtime-source-description"
        and record.get("source_inventory_sha256") == sha256(raw)
        and record.get("version") == context.version
        and record.get("revision") == context.commit
        and record.get("architecture") == context.platform.split("/")[1],
        "Runtime byte description belongs to another native source context",
    )
    asset = record["source_asset"]
    name = matches(asset["file"], SOURCE_NAME, "Unsafe runtime asset name")
    checksum = matches(
        "sha256:" + asset["sha256"], DIGEST, "Runtime source digest missing"
    )
    require(
        asset["url"]
        == f"https://github.com/{context.repository}/releases/download/{context.version}/{name}",
        "Runtime source delivery URL differs",
    )
    return record, {
        "runtime_pack_sha256": sha256(raw),
        "source_asset": {"name": name, "digest": checksum, "size": asset["size"]},
        "notice_files": record["overlays"],
        "distribution_review_required": True,
    }


def validate_smoke(
    record: object, context: NativeSourceContext, runtime_digest: str
) -> dict:
    context.checked()
    record = fields(record, SMOKE_FIELDS, "actual completed smoke measurement")
    require(
        type(record["schema_version"]) is int
        and record["schema_version"] == 1
        and record["kind"] == "release-image-smoke"
        and record["publication_authorized"] is False
        and record["version"] == context.version
        and record["revision"] == context.commit
        and record["platform"] == context.platform
        and record["execution"] == "native"
        and record["runtime_pack_sha256"] == runtime_digest,
        "Smoke is incomplete, lacks runtime coverage or belongs to another image pair",
    )
    checks = record["checks"]
    require(
        isinstance(checks, list)
        and all(isinstance(item, str) for item in checks)
        and len(checks) == len(CHECKS)
        and set(checks) == CHECKS,
        "Smoke did not complete every application/runtime/cleanup check",
    )
    configs = fields(
        record["tested_configs"], {"backend", "web"}, "actually tested configurations"
    )
    for digest in configs.values():
        matches(digest, DIGEST, "Missing actually tested configuration")
    timestamp(record["completed_at"])
    return record


def collect_native_measurement(
    context: NativeSourceContext,
    *,
    pack: Path,
    archives: dict[str, Path],
    tested_configs: dict[str, str],
    execute=command,
) -> dict:
    """Run real final-image checks; accept no caller-provided completion JSON."""
    source = context.checked()
    platform = context.platform
    fields(archives, {"backend", "web"}, "native pair archives")
    fields(tested_configs, {"backend", "web"}, "native tested configurations")
    _, runtime = runtime_inputs(context, pack)

    def inspect_pair():
        result = {}
        for component in ("backend", "web"):
            value = inspect_archive(
                archives[component],
                platform=platform,
                repository=context.repository,
                version=context.version,
                commit=context.commit,
                tested_config=tested_configs[component],
                component=component,
            )
            result[component] = value
        return result

    images = inspect_pair()
    environment = {
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
    with tempfile.TemporaryDirectory(prefix="psst-native-report-") as temporary:
        report = Path(temporary) / "smoke.json"
        execute(
            [
                sys.executable,
                str(ROOT / "tools/verify_release_images.py"),
                "--backend-image",
                tested_configs["backend"],
                "--web-image",
                tested_configs["web"],
                "--version",
                context.version,
                "--revision",
                context.commit,
                "--platform",
                platform,
                "--source-url",
                "https://github.com/" + context.repository,
                "--runtime-pack",
                str(pack.resolve()),
                "--report",
                str(report),
            ],
            environment=environment,
            timeout=1200,
        )
        smoke = validate_smoke(
            read_json(read_bounded_file(report)),
            context,
            runtime["runtime_pack_sha256"],
        )
        require(
            smoke["tested_configs"] == tested_configs,
            "Smoke tested another configuration pair",
        )
    require(images == inspect_pair(), "Native OCI exports changed during smoke")
    require(
        runtime == runtime_inputs(context, pack)[1],
        "Runtime inputs changed during smoke",
    )
    return {
        "schema_version": 2,
        "kind": "native-release-measurement",
        "source": source,
        "smoke": smoke,
        "images": images,
        "runtime": runtime,
        "publication_authorized": False,
    }
