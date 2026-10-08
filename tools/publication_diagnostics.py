#!/usr/bin/env python3
"""Project small credential-free publication progress, never replay authority."""

from __future__ import annotations

import argparse
import hashlib
import os
from pathlib import Path
import stat

from github_release_transport import NUMBER
from publish_container_release import SOURCE_NAME
from release_artifacts import (
    COMMIT,
    DIGEST,
    VERSION,
    create_output,
    fields,
    json_bytes,
    matches,
    read_json,
    repository_name,
    require,
)

MAX_JOURNAL = 1024**2
MAX_RECORDS = 128
MAX_OUTPUT = 64 * 1024
OPERATIONS = (
    {
        "transaction",
        "reserve-draft",
        "attest-reviewed-subjects",
        "attest-registry-readback",
        "attest-anonymous-pull",
        "attest-asset-readback",
        "publish-release",
        "public-readback",
    }
    | {
        "push-" + component + "-" + target
        for component in ("backend", "web")
        for target in ("amd64", "arm64", "index")
    }
    | {"tag-backend", "tag-web"}
)
PHASES = {"begin", "intent", "complete", "uncertain", "stopped"}
OUTCOMES = {"success", "failure", "cancelled", "skipped"}


def project(
    state: Path,
    *,
    repository: str,
    version: str,
    commit: str,
    run_id: str,
    attempt: str,
    outcome: str,
) -> dict:
    repository_name(repository)
    matches(version, VERSION, "Invalid diagnostic version")
    matches(commit, COMMIT, "Invalid diagnostic commit")
    run = int(matches(run_id, NUMBER, "Invalid diagnostic run"))
    run_attempt = int(matches(attempt, NUMBER, "Invalid diagnostic attempt"))
    require(outcome in OUTCOMES, "Invalid diagnostic outcome")
    require(not state.is_symlink(), "Unsafe diagnostic state directory")
    identity = {
        "repository": repository,
        "version": version,
        "commit": commit,
        "run_id": run,
        "attempt": run_attempt,
    }
    result = {
        "schema_version": 1,
        "kind": "publication-diagnostics",
        **identity,
        "step_outcome": outcome,
        "journal_present": False,
        "records": [],
    }
    journal = state / (version + ".jsonl")
    if not journal.exists() and not journal.is_symlink():
        return result
    descriptor = os.open(journal, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(descriptor, "rb") as stream:
        metadata = os.fstat(stream.fileno())
        require(
            stat.S_ISREG(metadata.st_mode) and 0 < metadata.st_size <= MAX_JOURNAL,
            "Diagnostic journal exceeds bounds or is not regular",
        )
        raw = stream.read(MAX_JOURNAL + 1)
    require(len(raw) <= MAX_JOURNAL, "Diagnostic journal exceeds bounds")
    lines = raw.splitlines()
    require(0 < len(lines) <= MAX_RECORDS, "Diagnostic record count exceeds bounds")
    binding = None
    for sequence, line in enumerate(lines, 1):
        record = fields(
            read_json(line),
            {"sequence", "binding", "phase", "operation", "details"},
            "diagnostic journal record",
        )
        digest = matches(record["binding"], DIGEST, "Invalid diagnostic binding")
        require(
            type(record["sequence"]) is int
            and record["sequence"] == sequence
            and isinstance(record["phase"], str)
            and record["phase"] in PHASES,
            "Invalid diagnostic journal sequence or phase",
        )
        operation = record["operation"]
        require(isinstance(operation, str), "Invalid diagnostic operation")
        if operation.startswith("upload-"):
            matches(operation[7:], SOURCE_NAME, "Invalid diagnostic asset operation")
            operation = "upload-asset"
        else:
            require(operation in OPERATIONS, "Unknown diagnostic operation")
        if sequence == 1:
            require(
                record["phase"] == "begin"
                and operation == "transaction"
                and record["details"] == identity,
                "Diagnostic journal identity differs",
            )
            binding = digest
        else:
            require(
                digest == binding and record["phase"] != "begin",
                "Diagnostic journal binding differs",
            )
        # Details, asset names, errors, API responses and environment values
        # are deliberately absent from this projection, even when malformed.
        result["records"].append(
            {"sequence": sequence, "phase": record["phase"], "operation": operation}
        )
    result.update(
        journal_present=True,
        binding_digest=binding,
        journal_sha256="sha256:" + hashlib.sha256(raw).hexdigest(),
    )
    require(len(json_bytes(result)) <= MAX_OUTPUT, "Diagnostic output exceeds bounds")
    return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state", type=Path, required=True)
    for name in ("repository", "version", "commit", "run-id", "attempt", "outcome"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    value = project(
        args.state,
        repository=args.repository,
        version=args.version,
        commit=args.commit,
        run_id=args.run_id,
        attempt=args.attempt,
        outcome=args.outcome,
    )
    create_output(args.output, json_bytes(value))


if __name__ == "__main__":
    try:
        main()
    except (Exception, KeyboardInterrupt):
        raise SystemExit(
            "Publication diagnostics unavailable; no private details exported."
        ) from None
