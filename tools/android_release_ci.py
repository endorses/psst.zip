#!/usr/bin/env python3
"""Reuse only executed exact-source Android CI and enforce publication readiness."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess

REQUIRED = {
    "Repository security": {
        "Reject sensitive files and oversized blobs",
        "Scan complete Git history",
        "Repository check regression tests",
        "Check distributed project licenses",
        "Check native notice inputs and archive boundaries",
    },
    "Android and shared module": {
        "Assemble and test",
        "Verify actual resolved native dependency notices",
    },
}


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def gh_json(endpoint: str) -> object:
    result = subprocess.run(
        ["gh", "api", endpoint], capture_output=True, timeout=45, check=False
    )
    require(result.returncode == 0, "GitHub evidence inspection failed")
    require(len(result.stdout) <= 8 * 1024 * 1024, "GitHub response exceeds bounds")
    return json.loads(result.stdout)


def executed(job: dict, required: set[str]) -> bool:
    steps = {step.get("name"): step for step in job.get("steps", [])}
    return (
        job.get("status") == "completed"
        and job.get("conclusion") == "success"
        and all(
            steps.get(name, {}).get("conclusion") == "success"
            and steps[name].get("started_at")
            and steps[name].get("completed_at")
            for name in required
        )
    )


def protected_main(repository: str) -> None:
    require(bool(re.fullmatch(r"[\w.-]+/[\w.-]+", repository)), "Invalid repository")
    branch = gh_json(f"repos/{repository}/branches/main")
    require(
        branch.get("name") == "main" and branch.get("protected") is True,
        "Android release requires currently protected main",
    )


def ci_evidence(repository: str, revision: str) -> dict:
    require(bool(re.fullmatch(r"[\w.-]+/[\w.-]+", repository)), "Invalid repository")
    require(bool(re.fullmatch(r"[a-f0-9]{40}", revision)), "Invalid revision")
    result = {"sourceRevision": revision, "android": None, "security": None}
    # Only the trusted dispatcher/main workflow definition can grant reuse. A
    # changed historical definition falls back to current checks, never success.
    trusted = subprocess.check_output(
        ["git", "show", "origin/main:.github/workflows/ci.yml"], timeout=15
    )
    candidate = subprocess.check_output(
        ["git", "show", revision + ":.github/workflows/ci.yml"], timeout=15
    )
    if trusted != candidate:
        return result
    data = gh_json(
        f"repos/{repository}/actions/workflows/ci.yml/runs?head_sha={revision}&per_page=100"
    )
    for run in data["workflow_runs"]:
        if not (
            run.get("head_sha") == revision
            and run.get("head_branch") == "main"
            and run.get("event") in {"push", "workflow_dispatch"}
            and run.get("path") == ".github/workflows/ci.yml"
            and run.get("repository", {}).get("full_name") == repository
            and run.get("head_repository", {}).get("full_name") == repository
        ):
            continue
        attempt = run["run_attempt"]
        jobs = gh_json(
            f"repos/{repository}/actions/runs/{run['id']}/attempts/{attempt}/jobs?per_page=100"
        )["jobs"]
        for name, checks in REQUIRED.items():
            key = "android" if name.startswith("Android") else "security"
            matches = [job for job in jobs if job.get("name") == name]
            if (
                result[key] is None
                and len(matches) == 1
                and executed(matches[0], checks)
            ):
                result[key] = {
                    "run_id": run["id"],
                    "attempt": attempt,
                    "job_id": matches[0]["id"],
                    "steps": sorted(checks),
                }
        if result["android"] and result["security"]:
            break
    return result


def protected_publication(repository: str, tag: str) -> None:
    require(bool(re.fullmatch(r"android-v[0-9]+\.[0-9]+\.[0-9]+", tag)), "Invalid tag")
    require(
        os.environ.get("ANDROID_RELEASE_PUBLICATION_READY") == "true",
        "Device readiness is pending",
    )
    record = os.environ.get("ANDROID_RELEASE_DEVICE_EVIDENCE", "")
    require(
        8 <= len(record) <= 1024 and "\n" not in record,
        "Reviewed device evidence reference is missing",
    )
    require(
        bool(
            re.fullmatch(
                r"[a-fA-F0-9]{64}", os.environ.get("ANDROID_RELEASE_SIGNER_SHA256", "")
            )
        ),
        "Public signer identity is missing",
    )
    protected_main(repository)
    immutable = gh_json(f"repos/{repository}/immutable-releases")
    require(
        isinstance(immutable, dict) and immutable.get("enabled") is True,
        "Repository immutable releases are not enabled",
    )
    environment = gh_json(f"repos/{repository}/environments/android-release")
    require(
        any(
            rule.get("type") == "required_reviewers" and rule.get("reviewers")
            for rule in environment.get("protection_rules", [])
        ),
        "Android environment requires a configured reviewer",
    )
    rules = gh_json(f"repos/{repository}/rulesets?includes_parents=true&per_page=100")
    protected = set()
    for summary in rules:
        if summary.get("enforcement") != "active" or summary.get("target") != "tag":
            continue
        policy = gh_json(f"repos/{repository}/rulesets/{summary['id']}")
        refs = policy.get("conditions", {}).get("ref_name", {})
        # Accept only the explicit Android namespace or a universal protection;
        # broader pattern semantics are deliberately not guessed.
        if refs.get("exclude") or not set(refs.get("include", [])) & {
            "refs/tags/android-v*",
            "~ALL",
        }:
            continue
        require(
            policy.get("bypass_actors") == [],
            "Android tag bypass policy is hidden or permits bypass",
        )
        protected.update(rule["type"] for rule in policy.get("rules", []))
    require(
        {"update", "deletion"} <= protected, "Android tags lack immutable protection"
    )


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="mode", required=True)
    evidence = sub.add_parser("evidence")
    evidence.add_argument("--repository", required=True)
    evidence.add_argument("--revision", required=True)
    evidence.add_argument("--output", type=Path, required=True)
    evidence.add_argument("--github-output", type=Path, required=True)
    publication = sub.add_parser("publication-policy")
    publication.add_argument("--repository", required=True)
    publication.add_argument("--tag", required=True)
    source_policy = sub.add_parser("protected-main")
    source_policy.add_argument("--repository", required=True)
    args = parser.parse_args()
    try:
        if args.mode == "evidence":
            result = ci_evidence(args.repository, args.revision)
            args.output.write_text(json.dumps(result, indent=2) + "\n")
            with args.github_output.open("a") as output:
                for key in ("android", "security"):
                    output.write(f"{key}_reused={str(bool(result[key])).lower()}\n")
        elif args.mode == "publication-policy":
            protected_publication(args.repository, args.tag)
        else:
            protected_main(args.repository)
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError):
        raise SystemExit(
            "Android release evidence/readiness check failed; publication is disabled."
        )


if __name__ == "__main__":
    main()
