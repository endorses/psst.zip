#!/usr/bin/env python3
"""Measure actual isolated upgrades after both native candidates are assembled.

This producer has no publication, attestation, VPS or operator-approval path.
It requires retained native inputs for BOTH architectures and the real assembled
manifest/bundle. Only the selected native architecture executes the disposable
Docker experiment. Registry acquisition is replaced with the exact loaded saved
pair, and encrypted export uses a separate store inside the same fixture daemon.
Neither substitution proves public provenance or an independent off-host backup.
"""

from __future__ import annotations

import argparse
import ast
from dataclasses import dataclass
from datetime import datetime, timezone
import hashlib
import platform as host_platform
from pathlib import Path, PurePosixPath
import re
import tarfile
import tempfile

from assemble_release_oci import StrictTarInfo, inspect_archive
import measure_native_browser_inputs
from generate_release_gate_reports import (
    NativeSourceContext,
    runtime_inputs,
    timestamp,
    validate_smoke,
)
from github_release_transport import command
from prepare_native_release import build_inputs, file_record, saved_pair
from publish_container_release import source_digest, validate_registry_index
from release_artifacts import (
    COMMIT,
    DIGEST,
    METADATA,
    PLATFORMS,
    allowed_path,
    assignments,
    create_output,
    fields,
    git,
    json_bytes,
    matches,
    read_bounded_file,
    read_json,
    require,
    validate_bundle,
    validate_manifest,
)
import test_release_updater_integration as integration

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = {
    "build_record",
    "original_archive",
    "final_archive",
    "native_measurement",
    "smoke_report",
    "source_verification",
    "runtime_pack",
    "runtime_source",
    "backend_archive",
    "web_archive",
}
DESCRIPTOR_FIELDS = {
    "schema_version",
    "kind",
    "source",
    "original_tested_configs",
    "tested_configs",
    "source_helper_config",
    "oci_exporter",
    "artifacts",
    "publication_authorized",
    "measurement_authentication_required",
}
FIXTURE_FILES = (
    "tools/measure_release_recovery.py",
    "tools/test_release_updater_integration.py",
    "tools/fixtures/release-updater/Dockerfile",
    "tools/fixtures/release-updater/controller.py",
    "tools/fixtures/release-updater/checkpoint.py",
    "tools/fixtures/release-updater/flows.py",
    "tools/fixtures/release-updater/verify.py",
)
EXPERIMENTS = (
    ("upgrade-repeat-restore", False, False),
    ("paused-upgrade-repeat-restore", False, True),
    ("post-migration-startup-fault-restore", True, False),
)
HEX = re.compile(r"[0-9a-f]{64}\Z")
TRANSACTION = re.compile(r"[0-9]{8}T[0-9]{6}Z-[0-9a-f]{12}\Z")


def native_platform() -> str:
    require(
        host_platform.system() == "Linux", "Recovery experiment requires native Linux"
    )
    machine = host_platform.machine()
    require(
        machine in {"x86_64", "aarch64", "arm64"}, "Unsupported native recovery host"
    )
    return "linux/amd64" if machine == "x86_64" else "linux/arm64"


def artifact_path(root: Path, value: object) -> Path:
    record = fields(value, {"file", "sha256", "size"}, "native artifact")
    name = record["file"]
    require(isinstance(name, str), "Invalid native artifact path")
    relative = PurePosixPath(name)
    require(
        name == relative.as_posix()
        and not relative.is_absolute()
        and relative.parts
        and all(part not in {".", ".."} for part in relative.parts),
        "Unsafe native artifact path",
    )
    path = root
    for part in relative.parts:
        path /= part
        require(not path.is_symlink(), "Native artifact path contains symlink")
    matches(record["sha256"], DIGEST, "Invalid native artifact hash")
    require(type(record["size"]) is int, "Invalid native artifact size")
    require(
        file_record(path, root) == record, "Native artifact changed after measurement"
    )
    return path


def saved_layers(path: Path, configs: dict[str, str]) -> None:
    """Verify the saved layers actually loaded by Docker against tested diff IDs."""
    saved_pair(path, configs)
    with tarfile.open(path, "r:", tarinfo=StrictTarInfo) as archive:
        entries = archive.getmembers()
        require(len(entries) <= 20_000, "Saved pair has excess entries")
        names = set()
        for entry in entries:
            name = PurePosixPath(entry.name)
            require(
                not name.is_absolute()
                and ".." not in name.parts
                and entry.name not in names
                and (entry.isfile() or entry.isdir())
                and not entry.sparse,
                "Unsafe/duplicate saved-pair member",
            )
            names.add(entry.name)

        def metadata(name):
            item = archive.getmember(name)
            require(
                item.isfile() and 0 < item.size <= 8 * 1024**2, "Invalid saved metadata"
            )
            return read_json(archive.extractfile(item).read())

        for image in metadata("manifest.json"):
            config = metadata(image["Config"])
            diff_ids = config.get("rootfs", {}).get("diff_ids")
            layers = image.get("Layers")
            require(
                isinstance(diff_ids, list)
                and isinstance(layers, list)
                and len(diff_ids) == len(layers)
                and 0 < len(layers) <= 128,
                "Saved layer count differs from tested configuration",
            )
            for name, expected in zip(layers, diff_ids, strict=True):
                matches(expected, DIGEST, "Invalid tested layer diff ID")
                item = archive.getmember(name)
                require(
                    item.isfile() and 0 < item.size <= 512 * 1024**2,
                    "Saved layer exceeds bounds",
                )
                with archive.extractfile(item) as stream:
                    actual = (
                        "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()
                    )
                require(
                    actual == expected, "Saved layer differs from tested native image"
                )


@dataclass(frozen=True)
class Inputs:
    context: NativeSourceContext
    manifest: dict
    manifest_path: Path
    bundle: Path
    natives: dict[str, dict]
    paths: dict[str, dict[str, Path]]
    images: dict[str, dict]
    snapshots: dict[Path, str]

    def unchanged(self) -> None:
        require(
            all(
                source_digest(path) == digest for path, digest in self.snapshots.items()
            ),
            "Recovery inputs changed during experiment",
        )


def validate_inputs(
    context: NativeSourceContext,
    *,
    natives: dict[str, Path],
    manifest_path: Path,
    bundle: Path,
    root: Path = ROOT,
    allow_legacy_browser: bool = False,
) -> Inputs:
    """Replay real four-image inputs; missing ARM evidence is never manufactured."""
    context.checked()
    fields(natives, set(PLATFORMS), "both retained native descriptors")
    raw = read_bounded_file(manifest_path)
    manifest = validate_manifest(read_json(raw), context.repository)
    require(
        manifest["payload_profile"] == "deployment-ready"
        and manifest["version"] == context.version
        and manifest["source"]["commit"] == context.commit,
        "Recovery manifest source/version/profile differs",
    )
    validate_bundle(manifest, bundle)
    validate_bundle_source(context, bundle, root)
    snapshots = {
        manifest_path: source_digest(manifest_path),
        bundle: source_digest(bundle),
    }
    descriptors, paths, images = {}, {}, {}
    for platform in PLATFORMS:
        local_context = NativeSourceContext(
            context.repository, context.version, context.commit, platform
        )
        path = natives[platform]
        descriptor = fields(
            read_json(read_bounded_file(path)), DESCRIPTOR_FIELDS, "native descriptor"
        )
        require(
            type(descriptor["schema_version"]) is int
            and (
                descriptor["schema_version"] == 2
                or allow_legacy_browser
                and descriptor["schema_version"] == 1
            )
            and descriptor["kind"] == "native-release-artifacts"
            and descriptor["source"] == local_context.checked()
            and descriptor["oci_exporter"] == "docker-save-byte-preserving-oci-v1"
            and descriptor["publication_authorized"] is False
            and descriptor["measurement_authentication_required"] is True,
            "Native descriptor source/export/authentication policy differs",
        )
        for name in ("tested_configs", "original_tested_configs"):
            pair = fields(
                descriptor[name], {"backend", "web"}, "native configuration pair"
            )
            for config in pair.values():
                matches(config, DIGEST, "Invalid tested native configuration")
            require(len(set(pair.values())) == 2, "Native pair overlaps")
        matches(
            descriptor["source_helper_config"], DIGEST, "Invalid source helper identity"
        )
        fields(
            descriptor["artifacts"],
            ARTIFACTS
            | (
                {"browser_inputs", "browser_verification"}
                if descriptor["schema_version"] == 2
                else set()
            ),
            "retained native artifacts",
        )
        paths[platform] = {
            name: artifact_path(path.parent, value)
            for name, value in descriptor["artifacts"].items()
        }
        snapshots[path] = source_digest(path)
        snapshots.update({p: source_digest(p) for p in paths[platform].values()})
        local = paths[platform]
        candidate_build, originals = build_inputs(
            local_context,
            read_json(read_bounded_file(local["build_record"])),
            local["original_archive"],
        )
        require(
            originals == descriptor["original_tested_configs"]
            and candidate_build["base_images"] == manifest["build"]["base_images"],
            "Retained original native build/bases differ from assembly",
        )
        raw_runtime_pack, runtime = runtime_inputs(
            local_context, local["runtime_pack"].parent
        )
        require(
            local["runtime_source"]
            == local["runtime_pack"].parent / runtime["source_asset"]["name"]
            and descriptor["artifacts"]["runtime_source"]["sha256"]
            == runtime["source_asset"]["digest"]
            and descriptor["artifacts"]["runtime_source"]["size"]
            == runtime["source_asset"]["size"],
            "Retained runtime source descriptor differs from validated pack asset",
        )
        measurement = read_json(read_bounded_file(local["native_measurement"]))
        require(
            measurement.get("schema_version") == 2
            and measurement.get("kind") == "native-release-measurement"
            and measurement.get("source") == local_context.checked()
            and measurement.get("runtime") == runtime
            and measurement.get("publication_authorized") is False,
            "Native runtime measurement differs",
        )
        smoke = validate_smoke(
            measurement["smoke"], local_context, runtime["runtime_pack_sha256"]
        )
        require(
            smoke["execution"] == "native",
            "Recovery refuses emulated candidate evidence",
        )
        require(
            smoke["tested_configs"] == descriptor["tested_configs"],
            "Native smoke pair differs",
        )
        require(
            read_json(read_bounded_file(local["smoke_report"])) == smoke,
            "Retained native smoke differs",
        )
        replay = read_json(read_bounded_file(local["source_verification"]))
        require(
            isinstance(replay, dict)
            and replay.get("schema_version") == 1
            and replay.get("kind") == "runtime-source-completeness"
            and replay.get("repository") == context.repository
            and replay.get("version") == context.version
            and replay.get("revision") == context.commit
            and replay.get("platform") == platform
            and replay.get("runtime_source_inputs_verified") is True
            and replay.get("distribution_authorized") is False
            and replay.get("runtime_pack_sha256") == runtime["runtime_pack_sha256"]
            and replay.get("runtime_source_asset_sha256")
            == runtime["source_asset"]["digest"]
            and replay.get("native_smoke_report_sha256")
            == source_digest(local["smoke_report"])
            and replay.get("images") == measurement["images"],
            "Retained native source replay describes another runtime/image pair",
        )
        for component in ("backend", "web"):
            actual = inspect_archive(
                local[component + "_archive"],
                platform=platform,
                repository=context.repository,
                version=context.version,
                commit=context.commit,
                tested_config=descriptor["tested_configs"][component],
                component=component,
            )
            require(
                actual == measurement["images"][component]
                and actual["manifest_digest"]
                == manifest["images"][component]["platform_digests"][platform],
                "Recovery OCI bytes differ from actual native measurement/manifest",
            )
            images[component + "-" + platform.split("/")[1]] = actual
        if descriptor["schema_version"] == 2:
            browser = read_json(read_bounded_file(local["browser_verification"]))
            matches(
                browser.get("builder_config"),
                DIGEST,
                "Missing original browser builder configuration",
            )
            expected_browser = measure_native_browser_inputs.replay(
                local_context,
                local["browser_inputs"],
                local["web_archive"],
                descriptor["tested_configs"]["web"],
                raw_runtime_pack,
                source_root=root,
            )
            require(
                browser
                == {**expected_browser, "builder_config": browser["builder_config"]},
                "Recovery browser evidence differs from exact Git/npm/final OCI replay",
            )
        saved_layers(local["final_archive"], descriptor["tested_configs"])
        descriptors[platform] = descriptor
    for component in ("backend", "web"):
        index = manifest_path.parent / (component + "-index.json")
        validate_registry_index(read_bounded_file(index), manifest["images"][component])
        # Index verification binds immutable digest and both architecture children;
        # actual payload size/media-type correspondence is independently checked.
        parsed = read_json(read_bounded_file(index))
        require(
            len(parsed["manifests"]) == 2,
            "Recovery index must contain the exact native pair",
        )
        for child in parsed["manifests"]:
            actual = images[component + "-" + child["platform"]["architecture"]]
            require(
                child["size"] == actual["manifest_size"]
                and child["mediaType"] == actual["manifest_media_type"],
                "Recovery index child metadata differs",
            )
        snapshots[index] = source_digest(index)
    result = Inputs(
        context, manifest, manifest_path, bundle, descriptors, paths, images, snapshots
    )
    result.unchanged()
    return result


def validate_bundle_source(
    context: NativeSourceContext, bundle: Path, root: Path
) -> None:
    """Bind every installed source-owned file to the exact committed candidate.

    Rehashed manifest/bundle inputs prove internal consistency, not source
    identity. Generated bundle metadata is validated separately by validate_bundle.
    """
    expected = {}
    for entry in git(root, "ls-tree", "-rz", "--full-tree", context.commit).split(
        b"\0"
    ):
        if not entry:
            continue
        attributes, raw_name = entry.split(b"\t", 1)
        name = raw_name.decode("utf-8")
        if not allowed_path(name):
            continue
        mode, kind, oid = attributes.split()
        require(
            mode in {b"100644", b"100755"} and kind == b"blob",
            "Candidate bundle source is not a regular tracked file",
        )
        expected[name] = oid.decode("ascii")
    actual = set()
    with tarfile.open(bundle, "r:gz") as archive:
        for member in archive:
            if member.name == METADATA:
                continue
            actual.add(member.name)
            require(
                member.name in expected
                and archive.extractfile(member).read()
                == git(root, "cat-file", "blob", expected[member.name]),
                "Deployment bundle source differs from exact committed candidate",
            )
    require(
        actual == set(expected),
        "Deployment bundle omits exact committed candidate source",
    )


def extract_bundle(inputs: Inputs, output: Path) -> None:
    """Extract only previously validated regular bundle members, never executables."""
    validate_bundle(inputs.manifest, inputs.bundle)
    output.mkdir(mode=0o700)
    with tarfile.open(inputs.bundle, "r:gz") as archive:
        for member in archive:
            path = output / member.name
            path.parent.mkdir(parents=True, exist_ok=True)
            with archive.extractfile(member) as source, path.open("xb") as target:
                target.write(source.read())
            path.chmod(0o644)


def flow_policy(path: Path) -> tuple[set[str], set[str]]:
    """Read the exact installed helper's constant policy without privileged import."""
    constants = {}
    for node in ast.parse(read_bounded_file(path)).body:
        if (
            isinstance(node, ast.Assign)
            and len(node.targets) == 1
            and isinstance(node.targets[0], ast.Name)
        ):
            name = node.targets[0].id
            if name not in {"FLOW_CHECKS", "RESTORE_CHECKS"}:
                continue
            require(
                isinstance(node.value, ast.Call)
                and isinstance(node.value.func, ast.Name)
                and node.value.func.id == "frozenset"
                and len(node.value.args) == 1
                and not node.value.keywords,
                "Installed helper flow policy is unsupported",
            )
            value = ast.literal_eval(node.value.args[0])
            require(
                isinstance(value, set)
                and value
                and all(isinstance(item, str) for item in value),
                "Invalid installed flow check policy",
            )
            constants[name] = value
    require(
        set(constants) == {"FLOW_CHECKS", "RESTORE_CHECKS"},
        "Missing installed helper flow policy",
    )
    return constants["FLOW_CHECKS"], constants["RESTORE_CHECKS"]


def checked_experiment(
    record: object,
    *,
    context: NativeSourceContext,
    previous: str,
    configs: dict[str, str],
    failure: bool,
    paused: bool,
    policy: tuple[set[str], set[str]],
    started: str,
) -> dict:
    """Validate facts from the actual assertion controller, never PASS log lines."""
    value = fields(
        record,
        {
            "schema_version",
            "kind",
            "scenario",
            "candidate",
            "baseline",
            "prior_pause",
            "repeat_mode",
            "observations",
            "startup_observations",
            "schema",
            "checkpoint",
            "restore",
            "completed_at",
            "acquisition",
            "backup_provider",
            "nested_tools",
            "public_provenance_verified",
            "off_host_provider_verified",
            "publication_authorized",
        },
        "actual disposable experiment",
    )
    require(
        type(value["schema_version"]) is int
        and value["schema_version"] == 1
        and value["kind"] == "disposable-updater-experiment"
        and value["scenario"] == ("post-startup-failure" if failure else "normal")
        and value["prior_pause"] is paused
        and value["repeat_mode"] == "same-exact-candidate"
        and value["acquisition"] == "fixture-local-exact-loaded-configs"
        and value["backup_provider"]
        == "same-host-separated-store-encryption-simulation"
        and value["public_provenance_verified"] is False
        and value["off_host_provider_verified"] is False
        and value["publication_authorized"] is False,
        "Recovery experiment scope/policy differs",
    )
    require(
        value["candidate"]
        == {"version": context.version, "commit": context.commit, "configs": configs},
        "Experiment ran another candidate image pair",
    )
    baseline = fields(
        value["baseline"], {"version", "commit", "configs"}, "historical baseline"
    )
    require(
        baseline["version"] == "v0.0.0" and baseline["commit"] == previous,
        "Experiment used another historical source",
    )
    fields(baseline["configs"], {"backend", "web"}, "historical configuration pair")
    for config in baseline["configs"].values():
        matches(config, DIGEST, "Missing actual historical configuration")
    tools = fields(
        value["nested_tools"],
        {"docker", "compose", "architecture"},
        "actual nested tools",
    )
    require(
        tools["architecture"]
        in (
            {"x86_64", "amd64"}
            if context.platform == "linux/amd64"
            else {"arm64", "aarch64"}
        )
        and all(
            isinstance(tools[name], str)
            and re.fullmatch(r"v?[0-9]+\.[0-9]+\.[0-9]+", tools[name])
            for name in ("docker", "compose")
        ),
        "Nested experiment tools are not actual native versions",
    )
    lower = datetime.fromisoformat(timestamp(started))
    upper = datetime.fromisoformat(timestamp(value["completed_at"]))
    require(
        lower <= upper
        and abs((datetime.now(timezone.utc) - upper).total_seconds()) < 300,
        "Stale/invalid actual experiment completion",
    )
    expected = (
        ["post-startup-failure"]
        if failure
        else ["candidate-activation", "repeat-activation"]
    ) + ["isolated-restore-activation"]
    observations = value["observations"]
    require(
        isinstance(observations, list)
        and [item.get("stage") for item in observations] == expected,
        "Missing actual upgrade/repeat/failure/restore observations",
    )
    identifiers = set()
    for observation in observations:
        fields(
            observation,
            {
                "stage",
                "transaction",
                "version",
                "active_version",
                "previous_version",
                "phase",
                "mutation_started",
                "prior_pause",
                "restoring",
                "verification",
                "public_ingress",
            },
            "actual transaction observation",
        )
        matches(
            observation["transaction"],
            TRANSACTION,
            "Missing durable transaction identity",
        )
        restoring = observation["stage"] == "isolated-restore-activation"
        if restoring:
            require(
                observation["transaction"] == observations[-2]["transaction"],
                "Restore does not use the matching mutation checkpoint",
            )
        else:
            require(
                observation["transaction"] not in identifiers,
                "Experiment reused an upgrade transaction identity",
            )
            identifiers.add(observation["transaction"])
        failed = observation["stage"] == "post-startup-failure"
        require(
            observation["version"] == context.version
            and observation["mutation_started"] is True
            and observation["prior_pause"] is paused
            and observation["restoring"] is restoring,
            "Durable transaction facts differ",
        )
        if failed:
            require(
                observation["phase"] == "failed-closed"
                and observation["active_version"] is None
                and observation["verification"] is None,
                "Post-migration fault failed to close deployment",
            )
            require(
                observation["public_ingress"] == {"running_services": 0},
                "Post-migration failure left public services running",
            )
            continue
        expected_version = "v0.0.0" if restoring and failure else context.version
        require(
            observation["phase"] == "completed"
            and observation["active_version"] == expected_version,
            "Candidate/restore was not actually activated",
        )
        require(
            observation["public_ingress"]
            == {
                "https_port": 18443,
                "config_status": 200,
                "public_transfers_paused": paused,
            },
            "Public candidate/restore ingress failed to preserve prior pause",
        )
        report = fields(
            observation["verification"],
            {"checks", "observed_at", "version", "source_commit"},
            "actual authenticated flow report",
        )
        require(
            report["version"] == expected_version
            and report["source_commit"]
            == (
                "checkpoint:" + observation["transaction"]
                if restoring
                else context.commit
            ),
            "Authenticated flows verified another candidate/checkpoint",
        )
        require(
            lower <= datetime.fromisoformat(timestamp(report["observed_at"])) <= upper,
            "Authenticated flow observation is outside actual experiment",
        )
        fields(
            report["checks"],
            policy[0] | (policy[1] if restoring else set()),
            "authenticated flow/security checks",
        )
        for details in report["checks"].values():
            require(
                isinstance(details, str) and 12 <= len(details) <= 1000,
                "Missing directly asserted flow facts",
            )
            require(
                isinstance(read_json(details.encode()), dict),
                "Flow facts must contain structured observations",
            )
    startups = value["startup_observations"]
    require(
        isinstance(startups, list) and startups, "No actual candidate startup checks"
    )
    for item in startups:
        fields(
            item, {"transaction", "restoring", "observed_at"}, "actual startup checks"
        )
        require(
            item["transaction"] in identifiers
            and type(item["restoring"]) is bool
            and lower
            <= datetime.fromisoformat(timestamp(item["observed_at"]))
            <= upper,
            "Startup checks do not describe the actual transaction",
        )
    require(
        identifiers <= {item["transaction"] for item in startups},
        "Missing candidate/restore startup observation",
    )
    schema = fields(
        value["schema"],
        {"before", "original_after", "restored", "old_cli_rejected_migrated_original"},
        "actual SQLite migration counts",
    )
    require(
        all(
            type(schema[key]) is int and schema[key] > 0
            for key in ("before", "original_after", "restored")
        )
        and schema["original_after"] > schema["before"]
        and schema["old_cli_rejected_migrated_original"] is True,
        "Actual historical migration/old-CLI refusal was not exercised",
    )
    require(
        schema["restored"]
        == (schema["before"] if failure else schema["original_after"]),
        "Rollback did not use the matching old-schema checkpoint",
    )
    checkpoint = fields(
        value["checkpoint"],
        {
            "sha256",
            "records",
            "corrupt_archive_refused",
            "preserved_sha256",
            "encrypted_export_receipt",
        },
        "actual stopped checkpoint",
    )
    matches(checkpoint["sha256"], HEX, "Missing stopped checkpoint checksum")
    require(
        checkpoint["sha256"] == checkpoint["preserved_sha256"]
        and checkpoint["corrupt_archive_refused"] is True,
        "Corrupt/original checkpoint protection failed",
    )
    retained = fields(
        checkpoint["records"],
        {"image", "volume", "configuration"},
        "retained stopped artifacts",
    )
    require(
        all(type(count) is int for count in retained.values())
        and retained["image"] == 2
        and retained["volume"] == 3
        and retained["configuration"] >= 3,
        "Checkpoint omits old image pair, volumes or protected config",
    )
    receipt = fields(
        checkpoint["encrypted_export_receipt"],
        {
            "checkpoint_sha256",
            "encrypted_off_host_receipt",
            "restore_exercise",
            "verified_at",
        },
        "simulated encrypted export receipt",
    )
    require(
        receipt["checkpoint_sha256"] == checkpoint["sha256"]
        and all(
            isinstance(receipt[key], str) and len(receipt[key]) >= 64
            for key in ("encrypted_off_host_receipt", "restore_exercise")
        ),
        "Encrypted export does not cover the stopped checkpoint",
    )
    timestamp(receipt["verified_at"])
    restored = fields(
        value["restore"],
        {
            "volume_mapping",
            "original_volumes",
            "original_payload_sha256",
            "preserved_original_payload_sha256",
            "certificate_sha256",
            "restored_certificate_sha256",
        },
        "isolated restored state",
    )
    original = fields(
        restored["original_volumes"],
        {"backend:/app/data", "caddy:/data", "caddy:/config"},
        "original volume identities",
    )
    mapping = fields(
        restored["volume_mapping"], set(original.values()), "isolated volume mapping"
    )
    require(
        all(
            isinstance(name, str)
            and re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}", name)
            for name in mapping.values()
        )
        and len(set(mapping.values())) == 3
        and not set(mapping.values()) & set(original.values()),
        "Restore overwrote or reused original storage",
    )
    for key in ("original_payload_sha256", "certificate_sha256"):
        matches(restored[key], HEX, "Missing actual preserved data/certificate hash")
    require(
        restored["original_payload_sha256"]
        == restored["preserved_original_payload_sha256"]
        and restored["certificate_sha256"] == restored["restored_certificate_sha256"],
        "Restore changed original data or TLS state",
    )
    return value


def measure_recovery(
    context: NativeSourceContext,
    *,
    natives: dict[str, Path],
    manifest: Path,
    bundle: Path,
    previous_source: str,
    output: Path,
    root: Path = ROOT,
    execute=command,
    allow_legacy_browser: bool = False,
) -> dict:
    context.checked()
    matches(
        previous_source, COMMIT, "Historical source must be an explicit full commit"
    )
    require(
        not output.exists() and not output.is_symlink(),
        "Recovery output already exists",
    )
    require(
        output.parent.is_dir() and not output.parent.is_symlink(),
        "Recovery output parent must be a real directory",
    )
    require(
        context.platform == native_platform(),
        "Recovery requires actual native execution",
    )
    inputs = validate_inputs(
        context,
        natives=natives,
        manifest_path=manifest,
        bundle=bundle,
        root=root,
        allow_legacy_browser=allow_legacy_browser,
    )
    previous = (
        git(root, "rev-parse", "--verify", previous_source + "^{commit}")
        .decode()
        .strip()
    )
    matches(previous, COMMIT, "Historical source must resolve to a real full commit")
    require(
        previous != context.commit, "Historical schema baseline must precede candidate"
    )
    git(root, "merge-base", "--is-ancestor", previous, context.commit)
    require(
        git(root, "rev-parse", "--verify", context.commit + "^{commit}")
        .decode()
        .strip()
        == context.commit,
        "Candidate source not present in Git",
    )
    helper_inputs = {}
    for name in FIXTURE_FILES:
        raw = read_bounded_file(root / name)
        require(
            raw == git(root, "show", context.commit + ":" + name),
            "Recovery execution helper differs from exact committed candidate",
        )
        helper_inputs[name] = source_digest(root / name)
    architecture = (
        execute(["docker", "info", "--format", "{{.Architecture}}"], timeout=55)
        .decode()
        .strip()
    )
    require(
        architecture
        in (
            {"x86_64", "amd64"}
            if context.platform == "linux/amd64"
            else {"aarch64", "arm64"}
        ),
        "Docker daemon differs from native execution platform",
    )
    started = datetime.now(timezone.utc).isoformat()
    tools = {
        "docker": execute(
            ["docker", "version", "--format", "{{.Server.Version}}"], timeout=55
        )
        .decode()
        .strip(),
        "compose": execute(["docker", "compose", "version", "--short"], timeout=55)
        .decode()
        .strip(),
        "architecture": architecture,
    }
    records = {}
    with tempfile.TemporaryDirectory(prefix="psst-recovery-measurement-") as temporary:
        extracted = Path(temporary) / "bundle"
        extract_bundle(inputs, extracted)
        policy = flow_policy(extracted / "deploy/update.py")
        candidate = integration.ExactCandidate(
            context.version,
            context.commit,
            context.platform,
            inputs.natives[context.platform]["tested_configs"],
            inputs.paths[context.platform]["final_archive"],
            extracted,
            inputs.manifest,
        )
        for name, failure, paused in EXPERIMENTS:
            inputs.unchanged()
            experiment_started = datetime.now(timezone.utc).isoformat()
            actual = integration.execute_experiment(
                source=context.commit,
                previous_source=previous,
                failure_after_start=failure,
                require_schema_change=True,
                exact_candidate=candidate,
                initially_paused=paused,
                root=root,
            )
            records[name] = checked_experiment(
                actual,
                context=context,
                previous=previous,
                configs=candidate.configs,
                failure=failure,
                paused=paused,
                policy=policy,
                started=experiment_started,
            )
    inputs.unchanged()
    require(
        helper_inputs == {name: source_digest(root / name) for name in FIXTURE_FILES},
        "Recovery execution helper changed",
    )
    result = {
        "schema_version": 1,
        "kind": "native-upgrade-recovery-measurement",
        "source": context.checked(),
        "execution": "native",
        "manifest_sha256": source_digest(manifest),
        "bundle_sha256": source_digest(bundle),
        "native_descriptors": {p: source_digest(natives[p]) for p in PLATFORMS},
        "tested_configs": inputs.natives[context.platform]["tested_configs"],
        "saved_pair": inputs.natives[context.platform]["artifacts"]["final_archive"],
        "images": inputs.images,
        "historical_source_commit": previous,
        "execution_inputs": helper_inputs,
        "tools": tools,
        "experiments": records,
        "started_at": started,
        "completed_at": datetime.now(timezone.utc).isoformat(),
        "public_provenance_verified": False,
        "off_host_provider_verified": False,
        "browser_mobile_flows_verified": False,
        "publication_authorized": False,
        "upgrade_recovery_gate_pending": True,
        "measurement_authentication_required": True,
    }
    # A terminal descriptor exists only after all actual scenarios and replay
    # checks succeeded. A failed experiment cleans its own resources, no PASS flag.
    create_output(output, json_bytes(result))
    return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("repository", "version", "commit", "platform", "previous-source"):
        parser.add_argument("--" + name, required=True)
    for name in ("manifest", "bundle", "output", "root"):
        parser.add_argument(
            "--" + name,
            type=Path,
            required=name != "root",
            default=ROOT if name == "root" else None,
        )
    parser.add_argument(
        "--native",
        action="append",
        required=True,
        help="PLATFORM=retained native-artifacts.json, both platforms required",
    )
    args = parser.parse_args()
    measure_recovery(
        NativeSourceContext(args.repository, args.version, args.commit, args.platform),
        natives={p: Path(path) for p, path in assignments(args.native).items()},
        manifest=args.manifest,
        bundle=args.bundle,
        previous_source=args.previous_source,
        output=args.output,
        root=args.root,
    )


if __name__ == "__main__":
    main()
