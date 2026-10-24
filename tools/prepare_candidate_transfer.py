#!/usr/bin/env python3
"""Thin compatibility adapter for the Go allowlisted candidate transfer.

Transfer verifies byte hashes and producer references. The receiving evaluator
must authenticate the exact inventory/descriptor for its selected run before
replaying source, browser, scanner and OCI semantics. No fallback to historical
browser/source schemas is implicit.
"""

from __future__ import annotations

import argparse
from pathlib import Path

from prepare_release_candidate import ROOT, _invoke
from release_artifacts import PLATFORMS, read_json, require

METADATA = "candidate-transfer.json"
SOURCE_KINDS = {"version-tag", "planned-main-dispatch"}


def _transfer(context, mode: str, source_kind: str, *args: str) -> dict:
    context.checked()
    require(source_kind in SOURCE_KINDS, "Unknown candidate source kind")
    return read_json(
        _invoke(
            ROOT,
            "transfer",
            mode,
            "--repository",
            context.repository,
            "--version",
            context.version,
            "--commit",
            context.commit,
            "--platform",
            context.platform,
            "--source-kind",
            source_kind,
            *args,
        )
    )


def stage_native(
    context,
    private: Path,
    output: Path,
    source_kind: str,
    *,
    allow_legacy_browser=False,
) -> dict:
    require(
        not allow_legacy_browser,
        "Historical transfers require their original archived helper",
    )
    return _transfer(
        context,
        "collect",
        source_kind,
        "--private",
        str(private),
        "--output",
        str(output),
    )


def verify_native(
    context, transfer: Path, source_kind: str, *, allow_legacy_browser=False
) -> dict:
    require(
        not allow_legacy_browser,
        "Historical transfers require their original archived helper",
    )
    return _transfer(context, "verify", source_kind, "--transfer", str(transfer))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("stage", "collect", "verify"))
    for name in ("repository", "version", "commit", "platform", "source-kind"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--private", type=Path)
    parser.add_argument("--transfer", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    from generate_release_gate_reports import NativeSourceContext

    context = NativeSourceContext(
        args.repository, args.version, args.commit, args.platform
    )
    require(args.platform in PLATFORMS, "Invalid native transfer platform")
    if args.mode in {"stage", "collect"}:
        require(
            args.private is not None and args.transfer is None,
            "Collect requires private input and new output",
        )
        stage_native(context, args.private, args.output, args.source_kind)
    else:
        require(
            args.transfer is not None and args.private is None,
            "Verify requires only transfer input",
        )
        _transfer(
            context,
            "verify",
            args.source_kind,
            "--transfer",
            str(args.transfer),
            "--output",
            str(args.output),
        )


if __name__ == "__main__":
    main()
