"""Produce exact release reports from completed checks, never review approval.

Native measurements are produced by running the real smoke harness with runtime
overlays. Both measurements must then be authenticated before aggregation. The
trusted workflow signs the returned bytes; this module cannot sign or publish.
"""

from __future__ import annotations

import os
import sys
import tempfile
from datetime import datetime
from pathlib import Path
from typing import Protocol

from assemble_release_oci import inspect_archive
from github_release_transport import API_VERSION, HTTPS, command
from publish_container_release import (
    SOURCE_NAME,
    WORKFLOW,
    Binding,
    sha256,
    source_digest,
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

ROOT = Path(__file__).resolve().parents[1]
CI_NAMES = {
    "security": "Repository security",
    "backend": "Backend",
    "web": "Web",
    "android": "Android and shared module",
    "ios": "Native iOS app, extension and XCTest",
}
CI_PREFIX = "Exact tagged commit CI / "
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


def source_ci_report(
    binding: Binding, *, run_id: int, attempt: int, token: str, http=None
) -> dict:
    """Read the selected tag-run attempt's five completed reusable CI jobs.

    A successful old main run, cancelled/skipped jobs, API errors and incomplete
    pagination cannot yield a report. Unrelated release jobs may still be active.
    """
    checked_binding(binding)
    require(
        type(run_id) is int and run_id > 0 and type(attempt) is int and attempt > 0,
        "Invalid selected run attempt",
    )
    require(
        isinstance(token, str) and token and "\n" not in token and "\r" not in token,
        "Explicit Actions read credential required",
    )
    http = http or HTTPS()
    root = f"https://api.github.com/repos/{binding.repository}/actions/runs/{run_id}/attempts/{attempt}"

    def get(url):
        response = http.request(
            "GET",
            url,
            headers={
                "Authorization": "Bearer " + token,
                "Accept": "application/vnd.github+json",
                "X-GitHub-Api-Version": API_VERSION,
                "User-Agent": "psst.zip-release-evidence",
            },
        )
        require(response.status == 200, "Source CI API failed; success is unknown")
        return read_json(response.body)

    def same_run(value):
        require(
            isinstance(value, dict)
            and value.get("id") == run_id
            and value.get("run_attempt") == attempt
            and value.get("head_sha") == binding.commit
            and value.get("head_branch") == binding.version
            and value.get("event") == "push"
            and value.get("path") == WORKFLOW
            and value.get("status") in {"in_progress", "completed"}
            and value.get("repository", {}).get("full_name") == binding.repository
            and value.get("head_repository", {}).get("full_name") == binding.repository,
            "Source CI is not the selected version-tag release attempt",
        )

    same_run(get(root))
    jobs, expected_total = [], None
    for page in range(1, 21):
        value = get(root + f"/jobs?per_page=100&page={page}")
        require(
            isinstance(value, dict)
            and type(value.get("total_count")) is int
            and 0 < value["total_count"] <= 2000
            and isinstance(value.get("jobs"), list)
            and len(value["jobs"]) <= 100,
            "Malformed source CI job page",
        )
        if expected_total is None:
            expected_total = value["total_count"]
        require(expected_total == value["total_count"], "Source CI pagination changed")
        jobs.extend(value["jobs"])
        if len(value["jobs"]) < 100:
            break
    require(len(jobs) == expected_total, "Incomplete source CI job pagination")
    seen, selected = set(), {}
    names = {CI_PREFIX + display: key for key, display in CI_NAMES.items()}
    for job in jobs:
        require(
            isinstance(job, dict)
            and type(job.get("id")) is int
            and job["id"] > 0
            and job["id"] not in seen
            and isinstance(job.get("name"), str),
            "Invalid or repeated source CI job",
        )
        seen.add(job["id"])
        name = job["name"]
        if not name.startswith(CI_PREFIX):
            continue
        require(name in names, "Unexpected reusable source CI job")
        key = names[name]
        require(
            key not in selected
            and job.get("run_id") == run_id
            and job.get("head_sha") == binding.commit
            and job.get("status") == "completed"
            and job.get("conclusion") == "success",
            "Exact source CI job is duplicate, unfinished or unsuccessful",
        )
        selected[key] = {
            "job_id": job["id"],
            "name": name,
            "completed_at": timestamp(job.get("completed_at")),
        }
    require(set(selected) == set(CI_NAMES), "Source CI lacks all five successful jobs")
    same_run(get(root))
    return {
        "schema_version": 1,
        "gate": "source-ci",
        "binding_digest": binding.digest,
        "passed": True,
        "details": {
            "jobs": {key: "success" for key in CI_NAMES},
            "run_id": run_id,
            "run_attempt": attempt,
            "source_workflow": ".github/workflows/ci.yml",
            "job_evidence": selected,
        },
    }


def runtime_inputs(binding: Binding, platform: str, pack: Path) -> tuple[dict, dict]:
    subjects = checked_binding(binding)
    require(platform in PLATFORMS, "Invalid native release platform")
    raw = read_bounded_file(pack / "runtime-pack.json")
    record = read_json(raw)
    require(
        isinstance(record, dict)
        and type(record.get("schema_version")) is int
        and record["schema_version"] == 1
        and record.get("version") == binding.version
        and record.get("revision") == binding.commit
        and record.get("architecture") == platform.split("/")[1],
        "Runtime pack belongs to another release/platform",
    )
    asset = record.get("source_asset")
    require(isinstance(asset, dict), "Runtime source asset missing")
    name = matches(asset.get("file"), SOURCE_NAME, "Unsafe runtime source asset name")
    checksum = matches(
        "sha256:" + str(asset.get("sha256")),
        DIGEST,
        "Runtime source asset checksum missing",
    )
    path = pack / name
    require(
        source_digest(path) == checksum
        and type(asset.get("size")) is int
        and asset["size"] == path.stat().st_size
        and subjects.get("source:" + name) == "file:" + name + "@" + checksum
        and asset.get("url")
        == f"https://github.com/{binding.repository}/releases/download/{binding.version}/{name}",
        "Runtime source bytes/offer differ from the immutable release binding",
    )
    overlays = fields(record.get("overlays"), {"backend", "web"}, "runtime overlays")
    for files in overlays.values():
        require(
            isinstance(files, dict)
            and {"THIRD_PARTY_NOTICES.txt", "runtime-inventory.json", "SOURCE.txt"}
            <= set(files),
            "Runtime notice/source overlay files missing",
        )
        for relative, digest in files.items():
            matches("sha256:" + str(digest), DIGEST, "Invalid runtime notice checksum")
            require(
                isinstance(relative, str)
                and relative
                and not Path(relative).is_absolute()
                and ".." not in Path(relative).parts
                and "\\" not in relative,
                "Unsafe runtime notice path",
            )
    return record, {
        "runtime_pack_sha256": sha256(raw),
        "source_asset": {"name": name, "digest": checksum, "size": path.stat().st_size},
        "notice_files": overlays,
        "distribution_review_required": True,
    }


def validate_smoke(
    record: object, binding: Binding, platform: str, runtime_digest: str
) -> dict:
    record = fields(record, SMOKE_FIELDS, "actual completed smoke measurement")
    require(
        type(record["schema_version"]) is int
        and record["schema_version"] == 1
        and record["kind"] == "release-image-smoke"
        and record["publication_authorized"] is False
        and record["version"] == binding.version
        and record["revision"] == binding.commit
        and record["platform"] == platform
        and record["execution"] in {"native", "emulated"}
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
    binding: Binding,
    *,
    platform: str,
    pack: Path,
    archives: dict[str, Path],
    tested_configs: dict[str, str],
    execute=command,
) -> dict:
    """Run real final-image checks; accept no caller-provided completion JSON."""
    subjects = checked_binding(binding)
    fields(archives, {"backend", "web"}, "native pair archives")
    fields(tested_configs, {"backend", "web"}, "native tested configurations")
    _, runtime = runtime_inputs(binding, platform, pack)

    def inspect_pair():
        result = {}
        for component in ("backend", "web"):
            value = inspect_archive(
                archives[component],
                platform=platform,
                repository=binding.repository,
                version=binding.version,
                commit=binding.commit,
                tested_config=tested_configs[component],
                component=component,
            )
            require(
                subjects[component + "-" + platform.split("/")[1]].endswith(
                    "@" + value["manifest_digest"]
                ),
                "Native export is not the selected final child",
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
                binding.version,
                "--revision",
                binding.commit,
                "--platform",
                platform,
                "--source-url",
                "https://github.com/" + binding.repository,
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
            binding,
            platform,
            runtime["runtime_pack_sha256"],
        )
        require(
            smoke["tested_configs"] == tested_configs,
            "Smoke tested another configuration pair",
        )
    require(images == inspect_pair(), "Native OCI exports changed during smoke")
    require(
        runtime == runtime_inputs(binding, platform, pack)[1],
        "Runtime inputs changed during smoke",
    )
    return {
        "schema_version": 1,
        "kind": "native-release-measurement",
        "binding_digest": binding.digest,
        "platform": platform,
        "smoke": smoke,
        "images": images,
        "runtime": runtime,
        "publication_authorized": False,
    }


def aggregate_native_reports(
    binding: Binding,
    measurements: dict[str, Path],
    authenticator: MeasurementAuthenticator,
) -> dict[str, dict]:
    """Authenticate both full measurements before producing complete gate bytes."""
    subjects = checked_binding(binding)
    fields(measurements, set(PLATFORMS), "both native measurement reports")
    records, configs = {}, {}
    for platform, path in measurements.items():
        raw = read_bounded_file(path)
        authenticator.authenticate(raw, binding)
        value = fields(
            read_json(raw),
            {
                "schema_version",
                "kind",
                "binding_digest",
                "platform",
                "smoke",
                "images",
                "runtime",
                "publication_authorized",
            },
            "signed native measurement",
        )
        require(
            type(value["schema_version"]) is int
            and value["schema_version"] == 1
            and value["kind"] == "native-release-measurement"
            and value["binding_digest"] == binding.digest
            and value["platform"] == platform
            and value["publication_authorized"] is False,
            "Stale or malformed native measurement",
        )
        runtime = value["runtime"]
        fields(
            runtime,
            {
                "runtime_pack_sha256",
                "source_asset",
                "notice_files",
                "distribution_review_required",
            },
            "authenticated runtime checks",
        )
        require(
            runtime["distribution_review_required"] is True,
            "Runtime checks cannot clear distribution review",
        )
        matches(
            runtime.get("runtime_pack_sha256"),
            DIGEST,
            "Missing checked runtime pack digest",
        )
        smoke = validate_smoke(
            value["smoke"], binding, platform, runtime["runtime_pack_sha256"]
        )
        asset = runtime.get("source_asset")
        fields(asset, {"name", "digest", "size"}, "authenticated runtime source asset")
        require(
            isinstance(asset.get("name"), str)
            and type(asset.get("size")) is int
            and asset["size"] > 0
            and subjects.get("source:" + asset["name"])
            == "file:" + asset["name"] + "@" + str(asset.get("digest")),
            "Authenticated runtime source asset differs from release binding",
        )
        notices = fields(
            runtime["notice_files"], {"backend", "web"}, "authenticated runtime notices"
        )
        for entries in notices.values():
            require(
                isinstance(entries, dict)
                and {"THIRD_PARTY_NOTICES.txt", "runtime-inventory.json", "SOURCE.txt"}
                <= set(entries),
                "Authenticated runtime notice coverage is incomplete",
            )
            for relative, checksum in entries.items():
                require(
                    isinstance(relative, str)
                    and relative
                    and not Path(relative).is_absolute()
                    and ".." not in Path(relative).parts
                    and "\\" not in relative,
                    "Unsafe authenticated runtime notice path",
                )
                matches(
                    "sha256:" + str(checksum),
                    DIGEST,
                    "Invalid authenticated notice checksum",
                )
        images = fields(value["images"], {"backend", "web"}, "authenticated final pair")
        for component, image in images.items():
            require(
                isinstance(image, dict)
                and image.get("platform") == platform
                and image.get("config_digest") == smoke["tested_configs"][component]
                and subjects[component + "-" + platform.split("/")[1]].endswith(
                    "@" + str(image.get("manifest_digest"))
                ),
                "Authenticated smoke/config/export correspondence differs",
            )
            configs[component + "-" + platform.split("/")[1]] = image["config_digest"]
        records[platform] = {
            "measurement_digest": sha256(raw),
            "smoke": smoke,
            "images": images,
            "runtime": runtime,
        }
    common = {"schema_version": 1, "binding_digest": binding.digest, "passed": True}
    return {
        "final-image-smoke": {
            **common,
            "gate": "final-image-smoke",
            "details": {
                "execution": {
                    platform: record["smoke"]["execution"]
                    for platform, record in records.items()
                },
                "tested_configs": configs,
                "native_measurements": records,
            },
        },
        "runtime-notices": {
            **common,
            "gate": "runtime-notices",
            "details": {
                "native_measurements": records,
                "distribution_review_required": True,
            },
        },
    }


def source_asset_measurements(binding: Binding, sources: dict[str, Path]) -> dict:
    """Measure immutable source bytes without asserting completeness or approval."""
    subjects = checked_binding(binding)
    expected = {
        key.removeprefix("source:"): value
        for key, value in subjects.items()
        if key.startswith("source:")
    }
    fields(sources, set(expected), "all bound corresponding-source assets")
    measured = {}
    for name, path in sources.items():
        require(name == path.name, "Renamed corresponding-source asset")
        digest = source_digest(path)
        require(
            expected[name] == "file:" + name + "@" + digest,
            "Source asset differs from release binding",
        )
        measured[name] = {"digest": digest, "size": path.stat().st_size}
    return {
        "schema_version": 1,
        "kind": "release-source-asset-measurements",
        "binding_digest": binding.digest,
        "assets": measured,
        "corresponding_source_completeness_verified": False,
        "distribution_review_required": True,
        "publication_authorized": False,
    }
