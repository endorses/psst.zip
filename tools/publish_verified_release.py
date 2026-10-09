#!/usr/bin/env python3
"""Publish only an authenticated release through the held workflow lease.

The official signing bridge and the transport perform actual publication actions.
There is no dry-run approval, arbitrary signer command, resume or cleanup mode.
"""

from __future__ import annotations

import argparse
from contextlib import contextmanager, ExitStack
from dataclasses import dataclass
import hashlib
import os
from pathlib import Path
import shutil
import tempfile
from typing import Protocol

from assemble_release_oci import inspect_archive
from generate_release_gate_reports import NativeSourceContext, validate_smoke
from github_release_evidence import GhEvidenceVerifier
from github_release_transport import (
    GitHubReleaseTransport,
    NUMBER,
    WorkflowContext,
    private_directory,
    sync_directory,
)
from publish_container_release import (
    Binding,
    EvidenceVerifier,
    GATES,
    PublicationPlan,
    READBACK_GATES,
    prepare_publication,
    reserve_draft,
    source_digest,
    verify_gates,
)
from release_artifacts import (
    DIGEST,
    InvalidRelease,
    PLATFORMS,
    fields,
    json_bytes,
    matches,
    read_bounded_file,
    read_json,
    require,
    VERSION,
)

TARGETS = {
    component + "-" + arch
    for component in ("backend", "web")
    for arch in ("amd64", "arm64")
}
WORKFLOW_ENV = {
    "GH_TOKEN",
    "PSST_IMMUTABLE_INSPECTION_TOKEN",
    "GITHUB_ACTIONS",
    "RUNNER_ENVIRONMENT",
    "GITHUB_REPOSITORY",
    "GITHUB_JOB",
    "GITHUB_REF",
    "GITHUB_SHA",
    "GITHUB_EVENT_NAME",
    "GITHUB_WORKFLOW_REF",
    "GITHUB_WORKFLOW_SHA",
    "GITHUB_RUN_ID",
    "GITHUB_RUN_ATTEMPT",
    "GITHUB_ACTOR",
    "GITHUB_API_URL",
    "GITHUB_SERVER_URL",
    "GITHUB_EVENT_PATH",
    "ACTIONS_ID_TOKEN_REQUEST_TOKEN",
    "ACTIONS_ID_TOKEN_REQUEST_URL",
    "PSST_INITIALIZE_GHCR_PACKAGES",
}
PUBLICATION_STAGES = frozenset(
    {
        "cli-inputs",
        "workflow-inputs",
        "verifier-initialization",
        "state-initialization",
        "snapshot-inputs",
        "release-gates",
        "workflow-context",
        "native-smoke",
        "final-oci",
        "attestor-initialization",
        "transport-initialization",
        "workflow-api-preflight",
        "package-preflight",
        "image-transport-preflight",
        "snapshot-durability",
        "publication-lease",
        "registry-publication",
        "package-visibility",
        "registry-readback",
        "provenance-publication",
        "release-assets",
        "version-tags",
        "immutable-publication",
        "public-readback",
    }
)


def publication_stage(stage: str) -> None:
    """Expose only reviewed constant boundaries, never publication input data."""
    require(stage in PUBLICATION_STAGES, "Unknown publication diagnostic stage")
    print("Publication stage: " + stage, flush=True)


def failure_category(error: BaseException) -> str:
    """Classify by type without exposing exception text or external responses."""
    if isinstance(error, KeyboardInterrupt):
        return "interrupted"
    if isinstance(error, InvalidRelease):
        return "rejected"
    if isinstance(error, TimeoutError):
        return "timeout"
    if isinstance(error, OSError):
        return "io-error"
    if isinstance(error, TypeError):
        return "interface-error"
    return "unexpected-error"


def package_initialization_enabled(environment: dict) -> bool:
    value = environment.get("PSST_INITIALIZE_GHCR_PACKAGES", "")
    require(
        isinstance(value, str) and value in {"", "false", "true"},
        "Invalid first-package initialization setting",
    )
    return value == "true"


@dataclass
class InputSnapshots:
    root: Path
    retain: bool = False


@contextmanager
def input_snapshots(state: Path):
    snapshots = InputSnapshots(
        Path(tempfile.mkdtemp(prefix="publication-inputs-", dir=state))
    )
    try:
        yield snapshots
    finally:
        if not snapshots.retain:
            shutil.rmtree(snapshots.root)


class WorkflowAttestor(Protocol):
    """Real reviewed action bridge; the independent verifier grants trust."""

    def attest_subjects(
        self, plan: PublicationPlan, assets: dict[str, Path], output: Path
    ) -> Path: ...
    def sign_report(self, binding: Binding, report: Path) -> None: ...


def exclusive_report(path: Path, value: dict) -> None:
    descriptor = os.open(
        path, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o400
    )
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(json_bytes(value))
        stream.flush()
        os.fsync(stream.fileno())
    sync_directory(path.parent)


def snapshot(path: Path, target: Path, maximum: int = 2 * 1024**3) -> str:
    require(
        path.is_file() and not path.is_symlink(),
        "Publication input must be a regular file",
    )
    before = path.stat()
    require(0 < before.st_size <= maximum, "Publication input exceeds bounds")
    checksum, count = hashlib.sha256(), 0
    with path.open("rb") as source, target.open("xb") as output:
        while chunk := source.read(1024**2):
            count += len(chunk)
            require(count <= maximum, "Publication input changed or exceeds bounds")
            checksum.update(chunk)
            output.write(chunk)
        output.flush()
        os.fchmod(output.fileno(), 0o400)
        os.fsync(output.fileno())
    after = path.stat()
    require(
        count == before.st_size
        and (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns)
        == (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns),
        "Publication input changed during snapshot",
    )
    return "sha256:" + checksum.hexdigest()


def tested_configurations(
    plan: PublicationPlan, report: Path, verifier: EvidenceVerifier
) -> dict[str, str]:
    receipt = verifier.verify("final-image-smoke", report, plan.binding)
    require(
        receipt.report_digest == dict(plan.evidence)["final-image-smoke"]
        and receipt.binding_digest == plan.binding.digest
        and receipt.passed is True,
        "Authenticated smoke evidence changed",
    )
    records = fields(
        receipt.details.get("native_measurements"),
        set(PLATFORMS),
        "actual native smoke measurements",
    )
    configs = fields(
        receipt.details.get("tested_configs"),
        TARGETS,
        "all four native tested configurations",
    )
    for platform, record in records.items():
        require(
            isinstance(record, dict) and isinstance(record.get("runtime"), dict),
            "Native smoke/runtime evidence missing",
        )
        smoke = validate_smoke(
            record.get("smoke"),
            NativeSourceContext(
                plan.binding.repository,
                plan.binding.version,
                plan.binding.commit,
                platform,
            ),
            record["runtime"].get("runtime_pack_sha256"),
        )
        images = fields(
            record.get("images"), {"backend", "web"}, "native smoke image pair"
        )
        for component, image in images.items():
            target = component + "-" + platform.split("/")[1]
            matches(configs[target], DIGEST, "Invalid tested configuration")
            require(
                isinstance(image, dict)
                and image.get("platform") == platform
                and image.get("config_digest")
                == configs[target]
                == smoke["tested_configs"][component]
                and image.get("manifest_digest")
                == plan.manifest["images"][component]["platform_digests"][platform],
                "Native smoke config/child differs from reviewed release",
            )
    return configs


def publish(
    *,
    root: Path,
    repository: str,
    ref: str,
    event_sha: str,
    reviewed_commit: str,
    manifest: Path,
    bundle: Path,
    indexes: dict[str, Path],
    archives: dict[str, Path],
    source_assets: dict[str, Path],
    reports: dict[str, Path],
    state: Path,
    environment: dict[str, str],
    verifier: EvidenceVerifier | None = None,
    attestor: WorkflowAttestor | None = None,
    transport_factory=GitHubReleaseTransport,
) -> dict:
    """Drive the actual lifecycle. Fixture adapters must be injected explicitly."""
    publication_stage("workflow-inputs")
    require(
        environment.get("GITHUB_REPOSITORY") == repository
        and environment.get("GITHUB_REF") == ref
        and environment.get("GITHUB_SHA") == event_sha,
        "Publication inputs differ from current workflow",
    )
    require(
        isinstance(environment.get("GH_TOKEN"), str)
        and environment["GH_TOKEN"]
        and isinstance(environment.get("PSST_IMMUTABLE_INSPECTION_TOKEN"), str)
        and environment["PSST_IMMUTABLE_INSPECTION_TOKEN"],
        "Explicit workflow and immutable-policy inspection credentials required",
    )
    initialize_packages = package_initialization_enabled(environment)
    fields(reports, set(GATES), "authenticated pre-publication gate reports")
    fields(indexes, {"backend", "web"}, "both native indexes")
    fields(archives, TARGETS, "all four staged final OCI archives")
    run_id = int(
        matches(environment.get("GITHUB_RUN_ID"), NUMBER, "Invalid workflow run ID")
    )
    attempt = int(
        matches(
            environment.get("GITHUB_RUN_ATTEMPT"), NUMBER, "Invalid workflow attempt"
        )
    )
    publication_stage("verifier-initialization")
    verifier = verifier or GhEvidenceVerifier(
        token=environment["GH_TOKEN"], run_id=run_id, run_attempt=attempt
    )
    publication_stage("state-initialization")
    private_directory(state)
    version = ref.removeprefix("refs/tags/")
    matches(version, VERSION, "Invalid publication version tag")
    require(
        not (state / (version + ".jsonl")).exists()
        and not (state / (version + ".jsonl")).is_symlink(),
        "Publication journal exists; explicit reconciliation is required",
    )
    publication_stage("snapshot-inputs")
    with input_snapshots(state) as snapshots, ExitStack() as signer_scope:
        private = snapshots.root

        def snapshot_input(path: Path, target: Path, maximum=2 * 1024**3):
            snapshot(path, target, maximum)

        asset_paths, index_paths, archive_paths, report_paths = {}, {}, {}, {}
        for subdir in ("assets", "indexes", "archives", "gates", "readbacks"):
            (private / subdir).mkdir(mode=0o700)
        source_assets = fields(
            source_assets, set(source_assets), "corresponding-source assets"
        )
        require(
            not {"release-manifest.json", bundle.name}.intersection(source_assets)
            and bundle.name != "release-manifest.json",
            "Duplicate source/bundle/manifest names",
        )
        for name, path in {
            "release-manifest.json": manifest,
            bundle.name: bundle,
            **source_assets,
        }.items():
            require(
                isinstance(name, str)
                and Path(name).name == name
                and name not in {".", ".."},
                "Unsafe release asset filename",
            )
            target = private / "assets" / name
            snapshot_input(path, target)
            asset_paths[name] = target
        for component, path in indexes.items():
            target = private / "indexes" / (component + ".json")
            snapshot_input(path, target, 16 * 1024**2)
            index_paths[component] = target
        for target, path in archives.items():
            copied = private / "archives" / (target + ".tar")
            snapshot_input(path, copied, 4 * 1024**3)
            archive_paths[target] = copied
        for gate, path in reports.items():
            target = private / "gates" / (gate + ".json")
            snapshot_input(path, target, 16 * 1024**2)
            report_paths[gate] = target
        publication_stage("release-gates")
        plan = prepare_publication(
            root=root,
            repository=repository,
            ref=ref,
            event_sha=event_sha,
            reviewed_commit=reviewed_commit,
            manifest_path=asset_paths["release-manifest.json"],
            bundle=asset_paths[bundle.name],
            indexes=index_paths,
            source_assets={name: asset_paths[name] for name in source_assets},
            reports=report_paths,
            verifier=verifier,
        )
        publication_stage("workflow-context")
        context = WorkflowContext.from_environment(plan, environment)
        publication_stage("native-smoke")
        configs = tested_configurations(
            plan, report_paths["final-image-smoke"], verifier
        )
        publication_stage("final-oci")
        for target, path in archive_paths.items():
            component, arch = target.split("-")
            actual = inspect_archive(
                path,
                platform="linux/" + arch,
                repository=repository,
                version=plan.binding.version,
                commit=plan.binding.commit,
                tested_config=configs[target],
                component=component,
            )
            require(
                actual["manifest_digest"]
                == plan.manifest["images"][component]["platform_digests"][
                    "linux/" + arch
                ],
                "Final OCI archive is not the authenticated reviewed child",
            )
        if attestor is None:
            publication_stage("attestor-initialization")
            from github_release_attestor import WorkflowAttestor as OfficialAttestor

            signer_cache = signer_scope.enter_context(
                tempfile.TemporaryDirectory(prefix="publication-signer-", dir=state)
            )
            attestor = OfficialAttestor(
                plan.binding,
                token=environment["GH_TOKEN"],
                environment=environment,
                verifier=verifier,
                private_output=private / "readbacks",
                private_action_cache=Path(signer_cache),
            )
        publication_stage("transport-initialization")
        adapter = transport_factory(
            plan,
            state,
            context,
            github_token=environment["GH_TOKEN"],
            inspection_token=environment["PSST_IMMUTABLE_INSPECTION_TOKEN"],
            actor=environment.get("GITHUB_ACTOR", ""),
            journal_checkpoint=None,
            initialize_packages=initialize_packages,
        )
        publication_stage("workflow-api-preflight")
        adapter.verify_workflow()
        publication_stage("package-preflight")
        adapter.package_preflight()
        publication_stage("image-transport-preflight")
        prepared_images = signer_scope.enter_context(
            adapter.prepare_images(archive_paths, index_paths, tested_configs=configs)
        )
        publication_stage("snapshot-durability")
        exclusive_report(private / "snapshot-binding.json", plan.record())
        # File fsync does not persist the containing directory entries. Make
        # the complete retained snapshot reachable before any remote mutation.
        for subdir in ("assets", "indexes", "archives", "gates", "readbacks"):
            sync_directory(private / subdir)
        sync_directory(private)
        sync_directory(state)
        # Once the publication lease is entered, an interrupted operation may
        # have mutated remote state. Preserve exact inputs for manual inspection.
        publication_stage("publication-lease")
        verify_gates(
            {"final-image-scanners": report_paths["final-image-scanners"]},
            {"final-image-scanners"},
            plan.binding,
            verifier,
        )
        snapshots.retain = True
        with reserve_draft(plan, adapter) as reservation:
            publication_stage("registry-publication")
            adapter.push_images(prepared_images)
            publication_stage("package-visibility")
            adapter.wait_for_public_packages()
            publication_stage("registry-readback")
            registry = adapter.inspect_pair()
            anonymous = adapter.anonymous_pull()
            provenance = None

            def attest_subjects():
                nonlocal provenance
                provenance = attestor.attest_subjects(
                    plan, asset_paths, private / "readbacks"
                )
                require(
                    isinstance(provenance, Path)
                    and provenance.parent == private / "readbacks"
                    and provenance.is_file()
                    and not provenance.is_symlink(),
                    "Invalid signed provenance report path",
                )
                verify_gates(
                    {"provenance": provenance}, {"provenance"}, plan.binding, verifier
                )
                return {"report_sha256": source_digest(provenance)}

            publication_stage("provenance-publication")
            adapter.journal.mutate(
                "attest-reviewed-subjects",
                {"subjects": dict(plan.binding.subjects)},
                attest_subjects,
            )
            publication_stage("release-assets")
            adapter.upload_assets(reservation, asset_paths)
            assets = adapter.inspect_assets(reservation)
            readback_reports = {"provenance": provenance}
            for gate, details in (
                ("registry-readback", registry),
                ("anonymous-pull", anonymous),
                ("asset-readback", assets),
            ):
                path = private / "readbacks" / (gate + ".json")
                exclusive_report(
                    path,
                    {
                        "schema_version": 1,
                        "gate": gate,
                        "binding_digest": plan.binding.digest,
                        "passed": True,
                        "details": details,
                    },
                )
                digest = source_digest(path)

                def sign_readback(gate=gate, path=path, digest=digest):
                    attestor.sign_report(plan.binding, path)
                    require(
                        source_digest(path) == digest,
                        "Readback report changed during signing",
                    )
                    verify_gates({gate: path}, {gate}, plan.binding, verifier)
                    return {"report_sha256": digest}

                adapter.journal.mutate(
                    "attest-" + gate, {"report_sha256": digest}, sign_readback
                )
                readback_reports[gate] = path
            verify_gates(readback_reports, set(READBACK_GATES), plan.binding, verifier)
            # The accepted risk may expire while awaiting package visibility or
            # remote readbacks. Recheck before exposing immutable version tags.
            verify_gates(
                {"final-image-scanners": report_paths["final-image-scanners"]},
                {"final-image-scanners"},
                plan.binding,
                verifier,
            )
            publication_stage("version-tags")
            adapter.create_version_tags(index_paths)
            publication_stage("immutable-publication")
            verify_gates(
                {"final-image-scanners": report_paths["final-image-scanners"]},
                {"final-image-scanners"},
                plan.binding,
                verifier,
            )
            release = adapter.publish(reservation, readback_reports, verifier)
            publication_stage("public-readback")
            public = adapter.verify_public(reservation)
            result = {
                "schema_version": 1,
                "kind": "container-publication-receipt",
                "repository": repository,
                "version": plan.binding.version,
                "commit": plan.binding.commit,
                "binding_digest": plan.binding.digest,
                "release_id": release["release_id"],
                "immutable": release["immutable"],
                "journal_sha256": source_digest(
                    state / (plan.binding.version + ".jsonl")
                ),
                "public_readback": public,
            }
        snapshots.retain = False
        return result


def paths(value: object, label: str) -> dict[str, Path]:
    require(
        isinstance(value, dict)
        and all(
            isinstance(name, str) and isinstance(path, str)
            for name, path in value.items()
        ),
        "Malformed " + label + " path map",
    )
    result = {name: Path(path) for name, path in value.items()}
    require(
        all(path.is_absolute() for path in result.values()),
        "Publication paths must be absolute",
    )
    return result


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--inputs",
        type=Path,
        required=True,
        help="Exact prepared paths and authenticated gate reports JSON",
    )
    parser.add_argument(
        "--root", type=Path, required=True, help="Reviewed tagged Git checkout"
    )
    parser.add_argument("--reviewed-commit", required=True)
    parser.add_argument(
        "--state",
        type=Path,
        required=True,
        help="Private runner-local publication journal directory",
    )
    args = parser.parse_args(argv)
    publication_stage("cli-inputs")
    environment = {key: os.environ[key] for key in WORKFLOW_ENV if key in os.environ}
    inputs = fields(
        read_json(read_bounded_file(args.inputs)),
        {"manifest", "bundle", "indexes", "archives", "source_assets", "reports"},
        "prepared publication inputs",
    )
    singles = paths(
        {name: inputs[name] for name in ("manifest", "bundle")}, "release assets"
    )
    root = args.root
    require(
        root.is_absolute() and root.is_dir() and not root.is_symlink(),
        "Publication requires an absolute regular checkout",
    )
    require(args.state.is_absolute(), "Publication state path must be absolute")
    ref = environment.get("GITHUB_REF", "")
    require(ref.startswith("refs/tags/"), "Publication requires a version tag event")
    version = ref.removeprefix("refs/tags/")
    matches(version, VERSION, "Publication requires a stable version tag")
    private_directory(args.state)
    receipt_path = args.state / (version + "-publication-receipt.json")
    require(
        not receipt_path.exists() and not receipt_path.is_symlink(),
        "Publication receipt exists; inspect recovery",
    )
    result = publish(
        root=root,
        repository=environment.get("GITHUB_REPOSITORY", ""),
        ref=ref,
        event_sha=environment.get("GITHUB_SHA", ""),
        reviewed_commit=args.reviewed_commit,
        manifest=singles["manifest"],
        bundle=singles["bundle"],
        indexes=paths(inputs["indexes"], "indexes"),
        archives=paths(inputs["archives"], "archives"),
        source_assets=paths(inputs["source_assets"], "sources"),
        reports=paths(inputs["reports"], "reports"),
        state=args.state,
        environment=environment,
    )
    exclusive_report(receipt_path, result)
    print(
        "Published immutable release "
        + version
        + "; verified anonymous readback receipt retained."
    )


def run_command(argv: list[str] | None = None) -> None:
    try:
        main(argv)
    except (Exception, KeyboardInterrupt) as error:
        print("Publication failure category: " + failure_category(error), flush=True)
        raise SystemExit(
            "Publication stopped; inspect retained journals before any further action."
        ) from None


if __name__ == "__main__":
    run_command()
