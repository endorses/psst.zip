#!/usr/bin/env python3
"""Derive recovery evidence from both authenticated native terminal measurements.

Aggregation reads bounded metadata and committed helper policy, never replays OCI,
source archives, Docker or the already completed disposable recovery experiments.
The trusted workflow must authenticate the returned gate before consuming it.
"""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import os
from pathlib import Path, PurePosixPath
import re

from generate_release_gate_reports import (
    MeasurementAuthenticator,
    NativeSourceContext,
    checked_binding,
    timestamp,
)
from github_release_evidence import GhEvidenceVerifier
from measure_release_recovery import (
    ARTIFACTS,
    DESCRIPTOR_FIELDS,
    EXPERIMENTS,
    FIXTURE_FILES,
    checked_experiment,
    flow_policy,
)
from prepare_release_candidate import IMAGE_TYPES
from publish_container_release import (
    RECOVERY_CHECKS,
    Binding,
    image_subjects,
    sha256,
    source_digest,
)
from release_artifacts import (
    COMMIT,
    DIGEST,
    PLATFORMS,
    InvalidRelease,
    create_output,
    fields,
    git,
    json_bytes,
    matches,
    read_bounded_file,
    read_json,
    require,
    validate_manifest,
)

ROOT = Path(__file__).resolve().parents[1]
MAX_OUTPUT = 16 * 1024**2
CHECKS = list(RECOVERY_CHECKS)
SCOPE_FLAGS = (
    "public_provenance_verified",
    "off_host_provider_verified",
    "browser_mobile_flows_verified",
    "publication_authorized",
)
MEASUREMENT_FIELDS = {
    "schema_version",
    "kind",
    "source",
    "execution",
    "manifest_sha256",
    "bundle_sha256",
    "native_descriptors",
    "tested_configs",
    "saved_pair",
    "images",
    "historical_source_commit",
    "execution_inputs",
    "tools",
    "experiments",
    "started_at",
    "completed_at",
    "upgrade_recovery_gate_pending",
    "measurement_authentication_required",
} | set(SCOPE_FLAGS)


def instant(value: object) -> datetime:
    return datetime.fromisoformat(timestamp(value).replace("Z", "+00:00"))


def checked_tools(value: object, platform: str) -> dict:
    tools = fields(
        value, {"docker", "compose", "architecture"}, "native recovery tools"
    )
    require(
        tools["architecture"]
        in ({"x86_64", "amd64"} if platform == "linux/amd64" else {"aarch64", "arm64"})
        and all(
            isinstance(tools[name], str)
            and re.fullmatch(r"v?[0-9]+\.[0-9]+\.[0-9]+", tools[name])
            for name in ("docker", "compose")
        ),
        "Recovery tool version/platform profile differs",
    )
    return tools


def checked_artifact(value: object) -> dict:
    record = fields(
        value, {"file", "sha256", "size"}, "retained native artifact metadata"
    )
    name = record["file"]
    require(isinstance(name, str), "Invalid retained artifact filename")
    path = PurePosixPath(name)
    require(
        name == path.as_posix()
        and not path.is_absolute()
        and path.parts
        and all(part not in {".", ".."} for part in path.parts)
        and "\\" not in name,
        "Unsafe retained artifact filename",
    )
    matches(record["sha256"], DIGEST, "Missing retained artifact hash")
    require(
        type(record["size"]) is int and 0 < record["size"] <= 2 * 1024**3,
        "Invalid retained artifact size",
    )
    return record


def checked_descriptor(value: object, context: NativeSourceContext) -> dict:
    descriptor = fields(value, DESCRIPTOR_FIELDS, "retained native recovery descriptor")
    require(
        type(descriptor["schema_version"]) is int
        and descriptor["schema_version"] == 2
        and descriptor["kind"] == "native-release-artifacts"
        and descriptor["source"] == context.checked()
        and descriptor["oci_exporter"] == "docker-save-byte-preserving-oci-v1"
        and descriptor["publication_authorized"] is False
        and descriptor["measurement_authentication_required"] is True,
        "Recovery native descriptor identity/policy differs",
    )
    for key in ("tested_configs", "original_tested_configs"):
        pair = fields(
            descriptor[key], {"backend", "web"}, "native recovery configuration pair"
        )
        for config in pair.values():
            matches(config, DIGEST, "Invalid native recovery configuration")
        require(
            len(set(pair.values())) == 2, "Overlapping native recovery configurations"
        )
    matches(
        descriptor["source_helper_config"],
        DIGEST,
        "Missing source helper configuration",
    )
    artifacts = fields(
        descriptor["artifacts"],
        ARTIFACTS | {"browser_inputs", "browser_verification"},
        "retained native artifact inventory",
    )
    for artifact in artifacts.values():
        checked_artifact(artifact)
    return descriptor


def aggregate_recovery(
    binding: Binding,
    *,
    measurements: dict[str, Path],
    native_descriptors: dict[str, Path],
    manifest: Path,
    bundle: Path,
    root: Path,
    authenticator: MeasurementAuthenticator,
) -> dict:
    subjects = checked_binding(binding)
    fields(measurements, set(PLATFORMS), "both native recovery measurements")
    fields(native_descriptors, set(PLATFORMS), "both native recovery descriptors")
    snapshots = {}

    def snapshot(path: Path) -> bytes:
        raw = read_bounded_file(path)
        snapshots[path] = raw
        return raw

    manifest_raw = snapshot(manifest)
    assembled = validate_manifest(read_json(manifest_raw), binding.repository)
    manifest_digest, bundle_digest = sha256(manifest_raw), source_digest(bundle)
    require(
        assembled["payload_profile"] == "deployment-ready"
        and assembled["version"] == binding.version
        and assembled["source"]["commit"] == binding.commit
        and subjects["manifest"] == "file:release-manifest.json@" + manifest_digest
        and subjects["bundle"]
        == "file:" + assembled["bundle"]["name"] + "@" + bundle_digest
        and bundle.name == assembled["bundle"]["name"]
        and bundle_digest == "sha256:" + assembled["bundle"]["sha256"]
        and all(
            subjects[name] == value for name, value in image_subjects(assembled).items()
        ),
        "Recovery assembly differs from exact release subjects",
    )
    descriptors, descriptor_digests = {}, {}
    for platform in PLATFORMS:
        raw = snapshot(native_descriptors[platform])
        context = NativeSourceContext(
            binding.repository, binding.version, binding.commit, platform
        )
        descriptors[platform] = checked_descriptor(read_json(raw), context)
        descriptor_digests[platform] = sha256(raw)
    require(
        git(root, "rev-parse", "--verify", binding.commit + "^{commit}")
        .decode()
        .strip()
        == binding.commit,
        "Recovery candidate commit is missing",
    )
    helper_inputs = {}
    for name in (*FIXTURE_FILES, "deploy/update.py"):
        raw = snapshot(root / name)
        require(
            raw == git(root, "show", binding.commit + ":" + name),
            "Recovery helper differs from exact committed candidate",
        )
        if name in FIXTURE_FILES:
            helper_inputs[name] = sha256(raw)
    policy = flow_policy(root / "deploy/update.py")
    images = None
    previous = None
    measured = {}
    experiments = {}
    tool_profiles = {}
    for platform in PLATFORMS:
        raw = snapshot(measurements[platform])
        authenticator.authenticate(raw, binding)
        record = fields(
            read_json(raw), MEASUREMENT_FIELDS, "native recovery terminal measurement"
        )
        context = NativeSourceContext(
            binding.repository, binding.version, binding.commit, platform
        )
        descriptor = descriptors[platform]
        require(
            type(record["schema_version"]) is int
            and record["schema_version"] == 1
            and record["kind"] == "native-upgrade-recovery-measurement"
            and record["source"] == context.checked()
            and record["execution"] == "native"
            and record["manifest_sha256"] == manifest_digest
            and record["bundle_sha256"] == bundle_digest
            and record["native_descriptors"] == descriptor_digests
            and record["tested_configs"] == descriptor["tested_configs"]
            and record["saved_pair"] == descriptor["artifacts"]["final_archive"]
            and record["execution_inputs"] == helper_inputs
            and all(record[flag] is False for flag in SCOPE_FLAGS)
            and record["upgrade_recovery_gate_pending"] is True
            and record["measurement_authentication_required"] is True,
            "Recovery terminal measurement identity/input/scope differs",
        )
        start, end = instant(record["started_at"]), instant(record["completed_at"])
        require(
            start <= end <= datetime.now(timezone.utc),
            "Recovery terminal timestamps are inconsistent",
        )
        historical = matches(
            record["historical_source_commit"],
            COMMIT,
            "Missing committed historical source",
        )
        require(
            historical != binding.commit
            and (previous is None or previous == historical),
            "Native recovery historical sources differ",
        )
        require(
            git(root, "rev-parse", "--verify", historical + "^{commit}")
            .decode()
            .strip()
            == historical,
            "Historical recovery source is missing",
        )
        git(root, "merge-base", "--is-ancestor", historical, binding.commit)
        previous = historical
        recorded_images = fields(
            record["images"],
            {c + "-" + p.split("/")[1] for p in PLATFORMS for c in ("backend", "web")},
            "all four final recovery images",
        )
        require(
            images is None or images == recorded_images,
            "Native recovery final images differ",
        )
        for p in PLATFORMS:
            for component in ("backend", "web"):
                image = fields(
                    recorded_images[component + "-" + p.split("/")[1]],
                    {
                        "platform",
                        "manifest_digest",
                        "manifest_size",
                        "manifest_media_type",
                        "config_digest",
                        "archive_digest",
                        "blob_count",
                    },
                    "final native recovery image",
                )
                require(
                    image["platform"] == p
                    and image["manifest_digest"]
                    == assembled["images"][component]["platform_digests"][p]
                    and image["config_digest"]
                    == descriptors[p]["tested_configs"][component]
                    and image["archive_digest"]
                    == descriptors[p]["artifacts"][component + "_archive"]["sha256"]
                    and image["manifest_media_type"] in IMAGE_TYPES
                    and type(image["manifest_size"]) is int
                    and image["manifest_size"] > 0
                    and type(image["blob_count"]) is int
                    and image["blob_count"] > 0,
                    "Recovery final image/config differs from retained native assembly",
                )
        images = recorded_images
        tools = checked_tools(record["tools"], platform)
        actual_experiments = fields(
            record["experiments"],
            {name for name, _, _ in EXPERIMENTS},
            "all three native recovery experiments",
        )
        for name, failure, paused in EXPERIMENTS:
            checked = checked_experiment(
                actual_experiments[name],
                context=context,
                previous=historical,
                configs=descriptor["tested_configs"],
                failure=failure,
                paused=paused,
                policy=policy,
                started=record["started_at"],
                completed_before=record["completed_at"],
            )
            checked_tools(checked["nested_tools"], platform)
            receipt = checked["checkpoint"]["encrypted_export_receipt"]
            require(
                start
                <= instant(receipt["verified_at"])
                <= instant(checked["completed_at"]),
                "Recovery receipt timestamp is outside actual experiment",
            )
        measured[platform] = {
            "record_digest": sha256(raw),
            "completed_at": record["completed_at"],
        }
        experiments[platform] = actual_experiments
        tool_profiles[platform] = tools
    require(
        all(read_bounded_file(path) == raw for path, raw in snapshots.items())
        and source_digest(bundle) == bundle_digest,
        "Recovery inputs changed during authentication",
    )
    return {
        "schema_version": 1,
        "gate": "upgrade-recovery",
        "binding_digest": binding.digest,
        "passed": True,
        "details": {
            "schema_version": 1,
            "execution": {p: "native" for p in PLATFORMS},
            "checks": CHECKS,
            "native_measurements": measured,
            "manifest_sha256": manifest_digest,
            "bundle_sha256": bundle_digest,
            "native_descriptors": descriptor_digests,
            "historical_source_commit": previous,
            "execution_inputs": helper_inputs,
            "images": images,
            "tools": tool_profiles,
            "experiments": experiments,
            **{flag: False for flag in SCOPE_FLAGS},
        },
    }


def verify_output(
    output: Path, binding: Binding, authenticator: MeasurementAuthenticator
) -> dict:
    from publish_container_release import recovery_review_details

    raw = read_bounded_file(output)
    require(0 < len(raw) <= MAX_OUTPUT, "Recovery gate exceeds output bounds")
    authenticator.authenticate(raw, binding)
    gate = fields(
        read_json(raw),
        {"schema_version", "gate", "binding_digest", "passed", "details"},
        "authenticated recovery gate",
    )
    require(
        type(gate["schema_version"]) is int
        and gate["schema_version"] == 1
        and gate["gate"] == "upgrade-recovery"
        and gate["binding_digest"] == binding.digest
        and gate["passed"] is True
        and json_bytes(gate) == raw,
        "Recovery gate is noncanonical or belongs to another release",
    )
    recovery_review_details(gate["details"], binding)
    require(
        read_bounded_file(output) == raw, "Recovery gate changed during authentication"
    )
    return gate


def authenticated_command_inputs(args, authenticator: MeasurementAuthenticator):
    """Use a signed corresponding-source gate to bind retained small assembly files."""
    from generate_corresponding_source_review import PREPARED_RECORD_FIELDS
    from measure_browser_source_inventory import root_directory
    from publish_container_release import SOURCE_NAME, source_review_details

    root_directory(args.root)
    root_directory(args.prepared)
    NativeSourceContext(
        args.repository, args.version, args.commit, PLATFORMS[0]
    ).checked()
    record_raw = read_bounded_file(args.prepared / "release-inputs.json")
    record = fields(
        read_json(record_raw), PREPARED_RECORD_FIELDS, "prepared recovery binding"
    )
    require(
        type(record["schema_version"]) is int
        and record["schema_version"] == 1
        and record["kind"] == "prepared-release-inputs"
        and record["source_kind"] == "version-tag"
        and record["repository"] == args.repository
        and record["version"] == args.version
        and record["source_commit"] == args.commit
        and record["tagged_source_ci_gate_verified"] is False
        and record["signer_identity_verified"] is False
        and record["publication_authorized"] is False
        and record["measurement_authentication_required"] is True
        and isinstance(record["dependency_replays"], dict)
        and set(record["dependency_replays"]) == set(PLATFORMS)
        and isinstance(record["upstream_replay"], dict)
        and isinstance(record["subjects"], dict)
        and 0 < len(record["subjects"]) <= 100,
        "Prepared recovery binding context/policy differs",
    )
    require(
        all(
            isinstance(k, str) and isinstance(v, str)
            for k, v in record["subjects"].items()
        ),
        "Invalid prepared recovery subjects",
    )
    binding = Binding(
        args.repository,
        args.version,
        args.commit,
        tuple(sorted(record["subjects"].items())),
    )
    subjects = checked_binding(binding)
    require(
        record["binding_sha256"] == binding.digest,
        "Prepared recovery binding digest differs",
    )
    assets = record["assets"]
    require(
        isinstance(assets, dict) and 2 < len(assets) <= 64,
        "Prepared recovery asset inventory differs",
    )
    manifest_path = args.prepared / "release-manifest.json"
    manifest_raw = read_bounded_file(manifest_path)
    assembled = validate_manifest(read_json(manifest_raw), binding.repository)
    bundle = args.prepared / assembled["bundle"]["name"]
    bundle_digest = source_digest(bundle)
    require(
        set(assets)
        == {manifest_path.name, bundle.name}
        | {
            name.removeprefix("source:")
            for name in subjects
            if name.startswith("source:")
        },
        "Prepared recovery asset inventory lacks exact source coverage",
    )
    for name, digest in assets.items():
        matches(name, SOURCE_NAME, "Unsafe prepared recovery asset name")
        matches(digest, DIGEST, "Missing prepared recovery asset digest")
        subject_name = (
            "manifest"
            if name == manifest_path.name
            else "bundle" if name == bundle.name else "source:" + name
        )
        require(
            subjects.get(subject_name) == "file:" + name + "@" + digest,
            "Prepared asset differs from recovery subjects",
        )
    require(
        assembled["payload_profile"] == "deployment-ready"
        and assembled["version"] == binding.version
        and assembled["source"]["commit"] == binding.commit
        and subjects["manifest"] == "file:release-manifest.json@" + sha256(manifest_raw)
        and subjects["bundle"] == "file:" + bundle.name + "@" + bundle_digest
        and bundle_digest == "sha256:" + assembled["bundle"]["sha256"]
        and assets.get(manifest_path.name) == sha256(manifest_raw)
        and assets.get(bundle.name) == bundle_digest
        and all(
            subjects[name] == value for name, value in image_subjects(assembled).items()
        ),
        "Prepared recovery assembly differs from release subjects",
    )
    names = {"release-inputs.json", manifest_path.name, bundle.name}
    require(
        {p.name for p in args.prepared.iterdir()} == names
        and all(p.is_file() and not p.is_symlink() for p in args.prepared.iterdir()),
        "Small prepared recovery file set differs",
    )
    source_raw = read_bounded_file(args.source_report)
    authenticator.authenticate(source_raw, binding)
    source_gate = fields(
        read_json(source_raw),
        {"schema_version", "gate", "binding_digest", "passed", "details"},
        "authenticated source binding gate",
    )
    require(
        type(source_gate["schema_version"]) is int
        and source_gate["schema_version"] == 1
        and source_gate["gate"] == "corresponding-source"
        and source_gate["binding_digest"] == binding.digest
        and source_gate["passed"] is True
        and json_bytes(source_gate) == source_raw,
        "Source binding gate is noncanonical or belongs to another release",
    )
    source_review_details(source_gate["details"], binding, distribution=False)
    snapshots = {
        manifest_path: sha256(manifest_raw),
        bundle: bundle_digest,
        args.source_report: sha256(source_raw),
    }
    require(
        read_bounded_file(args.prepared / "release-inputs.json") == record_raw
        and all(source_digest(path) == digest for path, digest in snapshots.items()),
        "Small recovery binding inputs changed during authentication",
    )
    return binding, record_raw, names, snapshots


def run_command(args) -> dict:
    from generate_corresponding_source_review import command_inputs
    from measure_browser_source_inventory import root_directory

    root_directory(args.output.parent)
    require(
        args.verify_only or not args.output.exists() and not args.output.is_symlink(),
        "Recovery output already exists",
    )
    authenticator = GhEvidenceVerifier(
        token=os.environ.get("GH_TOKEN"),
        run_id=args.run_id,
        run_attempt=args.run_attempt,
    )
    if args.source_report is not None:
        binding, record_raw, names, snapshots = authenticated_command_inputs(
            args, authenticator
        )
    else:
        binding, _, record_raw, names, snapshots = command_inputs(
            root=args.root,
            prepared=args.prepared,
            repository=args.repository,
            version=args.version,
            commit=args.commit,
        )
    if args.verify_only:
        report = verify_output(args.output, binding, authenticator)
    else:
        manifest = args.prepared / "release-manifest.json"
        assembled = read_json(read_bounded_file(manifest))
        trees = {"linux/amd64": args.amd64_inputs, "linux/arm64": args.arm64_inputs}
        for tree in trees.values():
            root_directory(tree)
        report = aggregate_recovery(
            binding,
            measurements={
                "linux/amd64": args.amd64_measurement,
                "linux/arm64": args.arm64_measurement,
            },
            native_descriptors={
                p: t / "native/native-artifacts.json" for p, t in trees.items()
            },
            manifest=manifest,
            bundle=args.prepared / assembled["bundle"]["name"],
            root=args.root,
            authenticator=authenticator,
        )
    require(
        read_bounded_file(args.prepared / "release-inputs.json") == record_raw
        and {path.name for path in args.prepared.iterdir()} == names
        and all(source_digest(path) == digest for path, digest in snapshots.items()),
        "Prepared inputs changed during recovery command",
    )
    if not args.verify_only:
        raw = json_bytes(report)
        require(0 < len(raw) <= MAX_OUTPUT, "Recovery gate exceeds output bounds")
        create_output(args.output, raw)
    return report


def main(argv=None) -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in (
        "root",
        "prepared",
        "amd64-inputs",
        "arm64-inputs",
        "amd64-measurement",
        "arm64-measurement",
        "output",
    ):
        parser.add_argument("--" + name, type=Path, required=True)
    for name in ("repository", "version", "commit"):
        parser.add_argument("--" + name, required=True)
    for name in ("run-id", "run-attempt"):
        parser.add_argument("--" + name, type=int, required=True)
    parser.add_argument("--verify-only", action="store_true")
    parser.add_argument(
        "--source-report",
        type=Path,
        help="Authenticate the exact corresponding-source gate and use only the three small prepared binding files",
    )
    args = parser.parse_args(argv)
    try:
        run_command(args)
    except (InvalidRelease, OSError, ValueError, RecursionError):
        parser.exit(
            1, "Recovery gate command failed; publication remains unauthorized.\n"
        )


if __name__ == "__main__":
    main()
