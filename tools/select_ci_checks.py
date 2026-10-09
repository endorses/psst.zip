#!/usr/bin/env python3
"""Select app CI checks from a complete Git change range, failing closed."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
import json
import os
from pathlib import Path
import re
import subprocess

JOBS = frozenset({"backend", "web", "android", "ios"})
NATIVE_JOBS = frozenset({"android", "ios"})
MAX_EVENT_BYTES = 2 * 1024 * 1024
GIT_TIMEOUT_SECONDS = 10
SHA = re.compile(r"[0-9a-fA-F]{40}\Z")

# Keep reviewed release/security tooling explicit: newly introduced tools need
# review before their changes can skip application checks.
SECURITY_ONLY_TOOLS = frozenset(
    "tools/" + name
    for name in (
        "backend_preferred_source_relationships.py",
        "browser_preferred_source_relationships.py",
        "check_repository.py",
        "collect_caddy_sources.py",
        "collect_runtime_notices.py",
        "container-distribution-policy.json",
        "final_image_notice_inventory.py",
        "generate_corresponding_source_review.py",
        "generate_distribution_review.py",
        "measure_native_browser_inputs.py",
        "package_application_dependencies.py",
        "package_runtime_sources.py",
        "package_upstream_application_sources.py",
        "prepare_candidate_transfer.py",
        "prepare_native_release.py",
        "prepare_publication_inputs.py",
        "publication_diagnostics.py",
        "publish_container_release.py",
        "publish_verified_release.py",
        "release_artifacts.py",
        "request_production_deployment.py",
        "sqlite_vendoring.py",
        "sqlite_vendoring.go",
        "sqlite_vendoring_test.go",
        "temporary_caddy_acceptance.py",
        "test_caddy_source_signatures.py",
        "test_external_proxy.py",
        "test_native_browser_inputs.py",
        "test_native_notices.py",
        "test_native_release_preparation.py",
        "test_repository_checks.py",
        "test_runtime_notices.py",
        "test_runtime_source_packaging.py",
        "test_runtime_source_replay.py",
        "verify_application_dependency_inputs.py",
        "verify_caddy_source_signatures.py",
        "verify_runtime_source_pack.py",
    )
)
RELEASE_TOOL_PREFIXES = (
    "aggregate_release_",
    "assemble_release_",
    "generate_release_",
    "github_release_",
    "measure_release_",
    "prepare_release_",
    "verify_release_",
    "test_release_",
)


class UncertainRange(Exception):
    """The event or Git history cannot safely establish a change range."""


@dataclass(frozen=True)
class Selection:
    jobs: frozenset[str]
    reason: str


def classify_path(path: str) -> frozenset[str]:
    # Reviewed release orchestration is covered by repository security checks.
    # Tags/dispatch/full-validation still force every application check in select().
    if path == ".github/workflows/release.yml":
        return frozenset()
    if path.startswith(
        (
            ".github/workflows/",
            "assets/brand/",
            "docs/protocol/",
            "docs/testing/fixtures/",
            "docs/security/fixtures/",
        )
    ) or path in {
        "LICENSE",
        "tools/select_ci_checks.py",
        "tools/test_ci_change_scope.py",
    }:
        return JOBS
    if path.startswith("backend/"):
        # Web integration/browser checks compile and launch the backend.
        return frozenset({"backend", "web"})
    if path.startswith("web/") or path == "tools/measure_browser_source_inventory.py":
        return frozenset({"web"})
    if path.startswith("shared/") or path in {"tools/generate_native_notices.py"}:
        return NATIVE_JOBS
    if path.startswith("android/app/src/main/assets/licenses/"):
        return NATIVE_JOBS
    if path.startswith("android/app/src/"):
        return frozenset({"android"})
    if path.startswith("android/"):
        # iOS also exports the shared module with Android's Gradle wrapper.
        return NATIVE_JOBS
    if path.startswith("ios/Shared/LegalResources/"):
        return NATIVE_JOBS
    if path.startswith("ios/"):
        return frozenset({"ios"})
    if path in {"README.md", "AGENTS.md"} or (
        path.startswith(("docs/plans/", "docs/security/", "docs/research/"))
        and path.endswith(".md")
    ):
        return frozenset()
    if (
        path.startswith(
            ("deploy/", "tools/runtime-legal/", "tools/fixtures/release-updater/")
        )
        or path in SECURITY_ONLY_TOOLS
    ):
        return frozenset()
    if path.startswith("tools/"):
        name = path.removeprefix("tools/")
        if (
            "/" not in name
            and name.endswith(".py")
            and name.startswith(RELEASE_TOOL_PREFIXES)
        ):
            return frozenset()
    return JOBS


def git(root: Path, *args: str) -> bytes:
    try:
        result = subprocess.run(
            ["git", *args],
            cwd=root,
            capture_output=True,
            check=False,
            timeout=GIT_TIMEOUT_SECONDS,
        )
    except (OSError, subprocess.TimeoutExpired):
        raise UncertainRange from None
    if result.returncode:
        raise UncertainRange
    return result.stdout


def valid_sha(value: object) -> bool:
    return isinstance(value, str) and bool(SHA.fullmatch(value)) and int(value, 16) != 0


def read_event(path: str) -> dict:
    try:
        with open(path, "rb") as stream:
            contents = stream.read(MAX_EVENT_BYTES + 1)
        if len(contents) > MAX_EVENT_BYTES:
            raise UncertainRange
        event = json.loads(contents)
        if not isinstance(event, dict):
            raise UncertainRange
        return event
    except (OSError, ValueError, RecursionError):
        raise UncertainRange from None


def select(root: Path, env: dict[str, str], force_full: bool) -> Selection:
    if force_full:
        return Selection(JOBS, "full-validation-requested")
    event_name = env.get("GITHUB_EVENT_NAME")
    ref = env.get("GITHUB_REF", "")
    if ref.startswith("refs/tags/"):
        return Selection(JOBS, "tag-validation")
    if event_name not in {"pull_request", "push"} or (
        event_name == "push" and ref != "refs/heads/main"
    ):
        return Selection(JOBS, "event-requires-full-validation")

    try:
        head = env.get("GITHUB_SHA")
        if not valid_sha(head):
            raise UncertainRange
        actual_head = git(root, "rev-parse", "--verify", "HEAD").strip()
        if actual_head.lower() != head.lower().encode("ascii"):
            raise UncertainRange
        event = read_event(env.get("GITHUB_EVENT_PATH", ""))
        if event_name == "pull_request":
            pull_request = event.get("pull_request")
            base_info = (
                pull_request.get("base") if isinstance(pull_request, dict) else None
            )
            base = base_info.get("sha") if isinstance(base_info, dict) else None
        else:
            base = event.get("before")
        if not valid_sha(base):
            raise UncertainRange
        git(root, "cat-file", "-e", f"{base}^{{commit}}")
        if event_name == "pull_request":
            base = git(root, "merge-base", base, "HEAD").strip().decode("ascii")
            if not valid_sha(base):
                raise UncertainRange
        changed = git(
            root, "diff", "--no-renames", "--name-only", "-z", f"{base}..HEAD"
        )
        if changed and not changed.endswith(b"\0"):
            raise UncertainRange
    except (UncertainRange, UnicodeError):
        return Selection(JOBS, "change-range-unavailable")

    jobs: set[str] = set()
    for path in changed.split(b"\0"):
        if path:
            jobs.update(classify_path(os.fsdecode(path)))
    return Selection(frozenset(jobs), "classified-change-range")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--job", choices=sorted(JOBS), required=True)
    parser.add_argument("--force-full", choices=("true", "false"), required=True)
    parser.add_argument("--github-output", type=Path, required=True)
    parser.add_argument(
        "--root", type=Path, default=Path(__file__).resolve().parents[1]
    )
    args = parser.parse_args()
    selection = select(args.root, os.environ, args.force_full == "true")
    run = "true" if args.job in selection.jobs else "false"
    scope = ",".join(sorted(selection.jobs)) or "none"
    # All values are fixed vocabulary. Never echo event data, paths or Git errors.
    with args.github_output.open("a", encoding="utf-8") as stream:
        stream.write(f"run={run}\nreason={selection.reason}\nscope={scope}\n")
    print(f"run={run} reason={selection.reason} scope={scope}")


if __name__ == "__main__":
    main()
