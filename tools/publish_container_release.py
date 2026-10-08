#!/usr/bin/env python3
"""Fail-closed publication preparation; no GitHub or registry transport is installed.

The verifier and reservation adapter are trusted dependencies, not JSON input.
This module never pushes images, uploads files, publishes a release, or uses tokens.
"""

from __future__ import annotations

import argparse
import hashlib
import re
from collections.abc import Iterator
from contextlib import AbstractContextManager, contextmanager
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Protocol

from prepare_release_candidate import IMAGE_TYPES, INDEX_TYPES, validated_tag
from release_artifacts import (
    COMMIT,
    DIGEST,
    PLATFORMS,
    VERSION,
    InvalidRelease,
    create_output,
    fields,
    git,
    json_bytes,
    matches,
    read_bounded_file,
    read_json,
    repository_name,
    require,
    validate_bundle,
    validate_manifest,
)

WORKFLOW = ".github/workflows/release.yml"
GATES = frozenset(
    {
        "source-ci",
        "source-scanners",
        "final-image-scanners",
        "final-image-smoke",
        "runtime-notices",
        "corresponding-source",
        "distribution-review",
        "upgrade-recovery",
    }
)
READBACK_GATES = frozenset(
    {"registry-readback", "anonymous-pull", "asset-readback", "provenance"}
)
SOURCE_NAME = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9._-]{0,159}\Z")
SOURCE_REVIEW_POLICY = "tools/container-distribution-policy.json"
SOURCE_COVERAGE = {
    component
    + "-"
    + arch: frozenset(
        {"application", "runtime"}
        | (
            {"backend-modules"}
            if component == "backend"
            else {"browser-packages", "browser-generators"}
        )
    )
    for component in ("backend", "web")
    for arch in ("amd64", "arm64")
}
RECOVERY_CHECKS = (
    "historical-upgrade",
    "same-version-reapply",
    "pause-preservation",
    "post-migration-startup-failure",
    "matching-checkpoint-isolated-restore",
)


def sha256(content: bytes) -> str:
    return "sha256:" + hashlib.sha256(content).hexdigest()


def source_digest(path: Path) -> str:
    require(
        path.is_file() and not path.is_symlink(), "Source asset must be a regular file"
    )
    require(0 < path.stat().st_size <= 2 * 1024**3, "Missing or oversized source asset")
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return "sha256:" + digest.hexdigest()


def image_subjects(manifest: dict) -> dict[str, str]:
    result = {}
    for component, record in manifest["images"].items():
        repository, digest = record["index"].split("@")
        result[f"{component}-index"] = "oci://" + repository + "@" + digest
        for platform in PLATFORMS:
            result[f"{component}-{platform.split('/')[1]}"] = (
                "oci://" + repository + "@" + record["platform_digests"][platform]
            )
    require(
        len({subject.split("@")[1] for subject in result.values()}) == 6,
        "Image pair has overlapping subjects",
    )
    return result


def validate_registry_index(raw: bytes, record: dict) -> None:
    """Bind the actual registry bytes, not an inspect formatter's digest claim."""
    expected = record["index"].split("@")[1]
    require(sha256(raw) == expected, "Registry index bytes have the wrong digest")
    value = read_json(raw)
    require(isinstance(value, dict), "Registry index must be an object")
    require(
        type(value.get("schemaVersion")) is int and value["schemaVersion"] == 2,
        "Invalid registry index schema",
    )
    require(
        isinstance(value.get("mediaType"), str) and value["mediaType"] in INDEX_TYPES,
        "Registry object is not an index",
    )
    descriptors = value.get("manifests")
    require(isinstance(descriptors, list) and descriptors, "Empty registry index")
    children = {}
    seen = set()
    attestations = []
    for descriptor in descriptors:
        require(isinstance(descriptor, dict), "Invalid registry descriptor")
        require(
            isinstance(descriptor.get("mediaType"), str)
            and descriptor["mediaType"] in IMAGE_TYPES,
            "Registry child is not an image manifest",
        )
        digest = matches(descriptor.get("digest"), DIGEST, "Invalid child digest")
        require(digest not in seen and digest != expected, "Duplicate registry child")
        seen.add(digest)
        require(
            type(descriptor.get("size")) is int and descriptor["size"] > 0,
            "Invalid registry descriptor size",
        )
        platform = descriptor.get("platform")
        require(isinstance(platform, dict), "Registry child lacks a platform")
        name = f"{platform.get('os')}/{platform.get('architecture')}"
        if name in PLATFORMS:
            require(name not in children, "Duplicate registry architecture")
            require(
                isinstance(platform.get("variant", ""), str)
                and platform.get("variant", "")
                in ({"", "v8"} if name == "linux/arm64" else {""}),
                "Unsupported registry architecture variant",
            )
            children[name] = digest
        else:
            # Buildx may include attached attestations, never another runnable OS.
            annotations = descriptor.get("annotations", {})
            require(
                name == "unknown/unknown"
                and isinstance(annotations, dict)
                and annotations.get("vnd.docker.reference.type")
                == "attestation-manifest",
                "Unexpected registry platform or unbound attachment",
            )
            attestations.append(annotations.get("vnd.docker.reference.digest"))
    require(children == record["platform_digests"], "Registry child map differs")
    require(
        all(item in children.values() for item in attestations),
        "Registry attestation references another image",
    )


def checked_checkout(root: Path, repository: str, commit: str) -> None:
    """Check checkout bytes/identity; this does not require or authorize a tag."""
    repository = repository_name(repository)
    matches(commit, COMMIT, "Invalid reviewed commit")
    origin = git(root, "remote", "get-url", "origin").decode().strip()
    require(
        origin
        in {
            f"https://github.com/{repository}.git",
            f"git@github.com:{repository}.git",
            f"https://github.com/{repository}",
            f"git@github.com:{repository}",
        },
        "Checkout origin differs from the trusted repository",
    )
    require(
        git(root, "rev-parse", "HEAD").decode().strip() == commit,
        "Checkout is not the reviewed commit",
    )
    require(
        git(root, "diff", "--name-only", commit, "--") == b"",
        "Tracked checkout differs from the reviewed commit",
    )


def bind_reviewed_source(
    root: Path, repository: str, ref: str, event_sha: str, reviewed_commit: str
) -> tuple[str, str]:
    repository = repository_name(repository)
    matches(reviewed_commit, COMMIT, "Invalid reviewed commit")
    version, commit = validated_tag(root, ref, event_sha)
    require(commit == reviewed_commit, "Tag is not the reviewed source commit")
    checked_checkout(root, repository, commit)
    return version, commit


@dataclass(frozen=True)
class Binding:
    repository: str
    version: str
    commit: str
    subjects: tuple[tuple[str, str], ...]

    @property
    def digest(self) -> str:
        return sha256(
            json_bytes(
                {
                    "repository": self.repository,
                    "ref": "refs/tags/" + self.version,
                    "commit": self.commit,
                    "workflow": WORKFLOW,
                    "subjects": dict(self.subjects),
                }
            )
        )


@dataclass(frozen=True)
class VerifiedEvidence:
    """Returned only after the injected verifier authenticates the report."""

    gate: str
    binding_digest: str
    report_digest: str
    passed: bool
    details: dict


class EvidenceVerifier(Protocol):
    def verify(self, gate: str, report: Path, binding: Binding) -> VerifiedEvidence:
        """Verify trusted issuer/reviewer, source, workflow/ref and subject bytes."""


def source_review_details(
    details: dict, binding: Binding, *, distribution: bool
) -> None:
    """Check authenticated producer facts, never turn an approval flag into evidence.

    The producer must replay archives, read the exact Git policy bytes/blob, and
    authenticate real GitHub protected-environment approval and its authorized
    reviewer. Bounded names/hashes here are not reviewer authorization. This
    boundary additionally rejects partial or stale coverage even for signed reports.
    """
    fields(
        details,
        {"schema_version", "source_subjects", "images", "policy"}
        | ({"coverage_digest", "review"} if distribution else {"coverage"}),
        "complete source/distribution review details",
    )
    require(
        type(details["schema_version"]) is int and details["schema_version"] == 1,
        "Unsupported source review detail schema",
    )
    subjects = dict(binding.subjects)
    sources = {
        name: subject
        for name, subject in subjects.items()
        if name.startswith("source:")
    }
    require(
        bool(sources) and details["source_subjects"] == sources,
        "Source review does not cover every exact source subject",
    )
    images = fields(
        details["images"], set(SOURCE_COVERAGE), "all four reviewed final images"
    )
    for name, record in images.items():
        fields(record, {"subject", "notice_inventory_digest"}, "reviewed image notices")
        require(
            record["subject"] == subjects.get(name),
            "Source review covers another final image",
        )
        matches(
            record["notice_inventory_digest"],
            DIGEST,
            "Missing exact final image notice inventory digest",
        )
    policy = fields(
        details["policy"],
        {"path", "source_commit", "record_digest", "git_blob"},
        "reviewed committed distribution policy",
    )
    require(
        policy["path"] == SOURCE_REVIEW_POLICY
        and policy["source_commit"] == binding.commit,
        "Distribution policy is not from the reviewed source commit",
    )
    matches(policy["record_digest"], DIGEST, "Missing committed policy record digest")
    matches(policy["git_blob"], COMMIT, "Missing committed policy Git blob")
    if distribution:
        matches(
            details["coverage_digest"],
            DIGEST,
            "Missing complete source coverage digest",
        )
        review = fields(
            details["review"],
            {
                "decision",
                "reviewer",
                "record_digest",
                "source_gate_report_digest",
                "reviewed_subjects",
            },
            "authenticated authorized distribution review",
        )
        require(
            review["decision"] == "approved"
            and isinstance(review["reviewer"], str)
            and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.@/-]{0,159}", review["reviewer"])
            is not None
            and review["reviewed_subjects"] == subjects,
            "Distribution review is pending or does not cover the exact final subjects",
        )
        matches(
            review["record_digest"],
            DIGEST,
            "Missing authenticated distribution review record digest",
        )
        matches(
            review["source_gate_report_digest"],
            DIGEST,
            "Missing corresponding-source report binding",
        )
    else:
        coverage = fields(
            details["coverage"],
            set(SOURCE_COVERAGE),
            "source coverage for all final images",
        )
        for image, categories in SOURCE_COVERAGE.items():
            entries = fields(
                coverage[image],
                set(categories),
                "exact final source coverage categories",
            )
            for evidence in entries.values():
                fields(
                    evidence,
                    {"status", "evidence_digest"},
                    "completed source coverage evidence",
                )
                require(
                    evidence["status"] == "complete",
                    "Preferred-form source coverage remains incomplete",
                )
                matches(
                    evidence["evidence_digest"],
                    DIGEST,
                    "Missing independently checked source coverage evidence",
                )


def recovery_review_details(details: dict, binding: Binding) -> None:
    """Require complete native recovery scope, distinct from public/provider proof."""
    require(
        isinstance(details, dict)
        and type(details.get("schema_version")) is int
        and details["schema_version"] == 1
        and details.get("checks") == list(RECOVERY_CHECKS),
        "Recovery report lacks complete experiment coverage",
    )
    modes = fields(details.get("execution"), set(PLATFORMS), "recovery execution")
    require(
        all(mode == "native" for mode in modes.values()),
        "Recovery requires both native architectures",
    )
    records = fields(
        details.get("native_measurements"), set(PLATFORMS), "recovery measurements"
    )
    for record in records.values():
        fields(record, {"record_digest", "completed_at"}, "completed native recovery")
        matches(record["record_digest"], DIGEST, "Missing recovery measurement digest")
        completed = record["completed_at"]
        require(
            isinstance(completed, str) and 0 < len(completed) <= 64,
            "Missing recovery completion time",
        )
        try:
            parsed = datetime.fromisoformat(completed.replace("Z", "+00:00"))
        except ValueError:
            raise InvalidRelease("Invalid recovery completion time") from None
        require(parsed.tzinfo is not None, "Recovery completion lacks timezone")
    subjects = dict(binding.subjects)
    for name in ("manifest", "bundle"):
        require(
            subjects.get(name, "").endswith("@" + str(details.get(name + "_sha256")))
            and subjects[name].startswith("file:"),
            "Recovery ran another prepared manifest or bundle",
        )
        matches(details[name + "_sha256"], DIGEST, "Invalid recovery input digest")
    require(
        all(
            details.get(key) is False
            for key in (
                "public_provenance_verified",
                "off_host_provider_verified",
                "browser_mobile_flows_verified",
            )
        ),
        "Native recovery cannot claim public delivery or independent provider proof",
    )


def verify_gates(
    reports: dict[str, Path],
    gates: frozenset[str],
    binding: Binding,
    verifier: EvidenceVerifier,
) -> dict[str, str]:
    fields(reports, set(gates), "required verification reports")
    result, source_details = {}, None
    for gate in sorted(gates):
        report = read_bounded_file(reports[gate])
        receipt = verifier.verify(gate, reports[gate], binding)
        require(isinstance(receipt, VerifiedEvidence), "Verifier returned no receipt")
        require(
            receipt.gate == gate
            and receipt.binding_digest == binding.digest
            and receipt.report_digest == sha256(report)
            and receipt.passed is True,
            "Evidence is failed, stale, substituted or bound to another release",
        )
        require(isinstance(receipt.details, dict), "Evidence details are missing")
        if gate in {"source-scanners", "final-image-scanners"}:
            scans = receipt.details.get("scans")
            require(isinstance(scans, list) and scans, "Scanner evidence is absent")
            required = (
                {"backend-source", "web-source"}
                if gate == "source-scanners"
                else {
                    key
                    for key, _ in binding.subjects
                    if key.endswith(("-amd64", "-arm64"))
                }
            )
            require(
                all(
                    isinstance(scan, dict) and isinstance(scan.get("target"), str)
                    for scan in scans
                )
                and {scan["target"] for scan in scans} == required,
                "Scanner evidence lacks exact source or final-image coverage",
            )
            require(len(scans) == len(required), "Duplicate scanner evidence")
            for scan in scans:
                expected_subject = (
                    "git:" + binding.repository + "@" + binding.commit
                    if gate == "source-scanners"
                    else dict(binding.subjects)[scan["target"]]
                )
                require(
                    scan.get("subject") == expected_subject,
                    "Scanner examined another source/image",
                )
                require(
                    scan.get("status") == "complete"
                    and type(scan.get("exit_code")) is int
                    and scan["exit_code"] == 0
                    and all(
                        isinstance(scan.get(key), str) and scan[key].strip()
                        for key in ("scanner", "version", "database", "scanned_at")
                    ),
                    "Scanner failed or lacks tool/database/date evidence",
                )
                findings = scan.get("findings")
                require(
                    isinstance(findings, list), "Scanner findings were not recorded"
                )
                for finding in findings:
                    require(
                        isinstance(finding, dict)
                        and isinstance(finding.get("disposition"), str)
                        and finding["disposition"] in {"fixed", "not-applicable"}
                        and isinstance(finding.get("reason"), str)
                        and bool(finding["reason"].strip()),
                        "Unresolved scanner finding",
                    )
        elif gate == "final-image-smoke":
            modes = fields(
                receipt.details.get("execution"), set(PLATFORMS), "smoke execution"
            )
            require(
                all(mode == "native" for mode in modes.values()),
                "Publication requires native smoke on both architectures",
            )
        elif gate == "source-ci":
            require(
                receipt.details.get("jobs")
                == {name: "success" for name in ("security", "backend", "web")},
                "Exact source CI did not pass security/backend/web jobs",
            )
        elif gate == "upgrade-recovery":
            recovery_review_details(receipt.details, binding)
        elif gate == "provenance":
            require(
                receipt.details.get("subjects") == dict(binding.subjects),
                "Provenance does not cover every updater and source subject",
            )
        elif gate == "corresponding-source":
            source_review_details(receipt.details, binding, distribution=False)
            source_details = receipt.details
        elif gate == "distribution-review":
            source_review_details(receipt.details, binding, distribution=True)
            require(
                source_details is not None and "corresponding-source" in result,
                "Distribution review requires the verified corresponding-source gate",
            )
            require(
                all(
                    receipt.details[name] == source_details[name]
                    for name in ("source_subjects", "images", "policy")
                )
                and receipt.details["coverage_digest"]
                == sha256(json_bytes(source_details["coverage"]))
                and receipt.details["review"]["source_gate_report_digest"]
                == result["corresponding-source"],
                "Distribution review differs from completed source/notice/policy evidence",
            )
        result[gate] = receipt.report_digest
    return result


@dataclass(frozen=True)
class PublicationPlan:
    binding: Binding
    manifest: dict
    manifest_record_digest: str
    updater_subjects: tuple[tuple[str, str], ...]
    assets: tuple[tuple[str, str], ...]
    evidence: tuple[tuple[str, str], ...]

    def record(self) -> dict:
        validate_plan(self)
        return {
            "schema_version": 1,
            "kind": "publication-preparation",
            "binding": self.binding.digest,
            "repository": self.binding.repository,
            "version": self.binding.version,
            "source_commit": self.binding.commit,
            "signer_workflow": WORKFLOW,
            "updater_subjects": dict(self.updater_subjects),
            "source_assets": {
                name: digest
                for name, digest in self.assets
                if name
                not in {"release-manifest.json", self.manifest["bundle"]["name"]}
            },
            "evidence": dict(self.evidence),
            "publication_authorized": False,
            "limitations": [
                "No transport or authenticated live publication was exercised."
            ],
        }


def validate_plan(plan: PublicationPlan) -> None:
    require(
        sha256(json_bytes(plan.manifest)) == plan.manifest_record_digest,
        "Prepared manifest was changed after verification",
    )
    require(
        set(dict(plan.evidence)) == GATES,
        "Preparation lacks required verification gates",
    )


@dataclass(frozen=True)
class PublicationInputs:
    """Exact validated artifacts, before gate verification; never authorization."""

    binding: Binding
    manifest: dict
    manifest_record_digest: str
    updater_subjects: tuple[tuple[str, str], ...]
    assets: tuple[tuple[str, str], ...]


def prepare_inputs(
    *,
    root: Path,
    repository: str,
    ref: str,
    event_sha: str,
    reviewed_commit: str,
    manifest_path: Path,
    bundle: Path,
    indexes: dict[str, Path],
    source_assets: dict[str, Path],
) -> PublicationInputs:
    version, commit = bind_reviewed_source(
        root, repository, ref, event_sha, reviewed_commit
    )
    return measure_prepared_inputs(
        repository=repository,
        version=version,
        commit=commit,
        manifest_path=manifest_path,
        bundle=bundle,
        indexes=indexes,
        source_assets=source_assets,
    )


def measure_prepared_inputs(
    *,
    repository: str,
    version: str,
    commit: str,
    manifest_path: Path,
    bundle: Path,
    indexes: dict[str, Path],
    source_assets: dict[str, Path],
) -> PublicationInputs:
    """Measure exact prepared bytes only; no tag, source CI or publication trust.

    Planned-version assembly may use this structural helper, but publication must
    enter through prepare_inputs and its mandatory reviewed-tag binding.
    """
    repository = repository_name(repository)
    matches(version, VERSION, "Invalid prepared version")
    matches(commit, COMMIT, "Invalid prepared source commit")
    raw = read_bounded_file(manifest_path)
    manifest = validate_manifest(read_json(raw), repository)
    require(
        manifest["version"] == version
        and manifest["source"]["commit"] == commit
        and manifest["payload_profile"] == "deployment-ready",
        "Manifest is not the selected deployment-ready release",
    )
    validate_bundle(manifest, bundle)
    fields(indexes, {"backend", "web"}, "registry indexes")
    for component, index_path in indexes.items():
        validate_registry_index(
            read_bounded_file(index_path), manifest["images"][component]
        )
    subjects = image_subjects(manifest)
    subjects["manifest"] = "file:release-manifest.json@" + sha256(raw)
    subjects["bundle"] = (
        "file:" + bundle.name + "@sha256:" + manifest["bundle"]["sha256"]
    )
    updater_subjects = tuple(sorted(subjects.items()))
    require(len(updater_subjects) == 8, "Updater requires eight provenance subjects")
    require(
        isinstance(source_assets, dict) and source_assets,
        "Corresponding-source assets are missing",
    )
    assets = {
        "release-manifest.json": sha256(raw),
        bundle.name: "sha256:" + manifest["bundle"]["sha256"],
    }
    for name, path in source_assets.items():
        matches(name, SOURCE_NAME, "Unsafe corresponding-source asset name")
        require(
            name not in assets and name == path.name,
            "Duplicate or renamed source asset",
        )
        digest = source_digest(path)
        assets[name] = digest
        subjects["source:" + name] = "file:" + name + "@" + digest
    binding = Binding(repository, version, commit, tuple(sorted(subjects.items())))
    return PublicationInputs(
        binding,
        manifest,
        sha256(json_bytes(manifest)),
        updater_subjects,
        tuple(sorted(assets.items())),
    )


def prepare_publication(
    *,
    root: Path,
    repository: str,
    ref: str,
    event_sha: str,
    reviewed_commit: str,
    manifest_path: Path,
    bundle: Path,
    indexes: dict[str, Path],
    source_assets: dict[str, Path],
    reports: dict[str, Path],
    verifier: EvidenceVerifier,
) -> PublicationPlan:
    inputs = prepare_inputs(
        root=root,
        repository=repository,
        ref=ref,
        event_sha=event_sha,
        reviewed_commit=reviewed_commit,
        manifest_path=manifest_path,
        bundle=bundle,
        indexes=indexes,
        source_assets=source_assets,
    )
    evidence = verify_gates(reports, GATES, inputs.binding, verifier)
    return PublicationPlan(
        inputs.binding,
        inputs.manifest,
        inputs.manifest_record_digest,
        inputs.updater_subjects,
        inputs.assets,
        tuple(sorted(evidence.items())),
    )


class ReservationAdapter(Protocol):
    """Future transport must hold one repository-wide lease through publication.

    API/registry errors, pagination gaps and unauthorized lookups must raise;
    none may be represented as absent. No implementation is supplied here.
    """

    def serialized(self, repository: str) -> AbstractContextManager: ...
    def snapshot(self, plan: PublicationPlan) -> dict: ...
    def create_draft(self, parameters: dict) -> dict: ...


@contextmanager
def reserve_draft(plan: PublicationPlan, adapter: ReservationAdapter) -> Iterator[dict]:
    """Hold the repository lease until all caller operations leave this context.

    Failed or interrupted operations leave the draft for explicit recovery.
    No existing draft, uploaded asset or version tag is automatically adopted.
    """
    validate_plan(plan)
    with adapter.serialized(plan.binding.repository):
        snapshot = fields(
            adapter.snapshot(plan),
            {"releases", "version_tags", "tag_commit", "immutable_releases"},
            "reservation snapshot",
        )
        require(snapshot["tag_commit"] == plan.binding.commit, "Remote tag moved")
        require(
            snapshot["immutable_releases"] is True, "Immutable releases are not enabled"
        )
        require(
            snapshot["releases"] == [],
            "Version already has a release; recovery required",
        )
        require(
            snapshot["version_tags"] == {"backend": None, "web": None},
            "Version already has registry tags; recovery required",
        )
        draft = adapter.create_draft(
            {
                "tag_name": plan.binding.version,
                "target_commitish": plan.binding.commit,
                "draft": True,
                "prerelease": False,
                "make_latest": "false",
            }
        )
        require(
            isinstance(draft, dict)
            and type(draft.get("id")) is int
            and draft["id"] > 0
            and draft.get("tag_name") == plan.binding.version
            and draft.get("draft") is True
            and draft.get("prerelease") is False
            and draft.get("assets") == [],
            "Reservation did not create a fresh empty version draft",
        )
        yield {
            "release_id": draft["id"],
            "binding": plan.binding.digest,
            "ready": False,
        }


def publication_order(plan: PublicationPlan) -> list[str]:
    """Readiness boundary: version tags may be partial; draft stays unadvertised."""
    validate_plan(plan)
    return [
        "reserve-exclusive-empty-draft-under-held-lease",
        "push-four-reviewed-children-by-digest",
        "push-two-reviewed-indexes-by-digest",
        "verify-registry-index-child-maps-and-anonymous-paired-pulls",
        "attest-eight-updater-subjects-and-corresponding-source-assets",
        "upload-corresponding-source-assets-without-replacement",
        "upload-deployment-bundle-without-replacement",
        "upload-detached-manifest-without-replacement",
        "verify-downloaded-assets-and-all-required-provenance",
        "create-absent-backend-and-web-version-tags-without-overwrite",
        "verify-both-version-tags-and-recheck-remote-source-tag",
        "publish-complete-immutable-draft-with-make-latest-false",
        "verify-public-release-assets-and-anonymous-paired-pulls",
        "advance-convenience-tags-only-after-public-readback",
    ]


def ready_release_request(
    plan: PublicationPlan,
    reservation: dict,
    snapshot: dict,
    reports: dict[str, Path],
    verifier: EvidenceVerifier,
) -> dict:
    validate_plan(plan)
    fields(reservation, {"release_id", "binding", "ready"}, "reservation receipt")
    require(
        reservation["binding"] == plan.binding.digest and reservation["ready"] is False,
        "Reservation belongs to another publication",
    )
    require(
        type(reservation["release_id"]) is int and reservation["release_id"] > 0,
        "Invalid reserved draft ID",
    )
    fields(
        snapshot,
        {"release", "version_tags", "tag_commit", "immutable_releases"},
        "ready snapshot",
    )
    require(
        snapshot["tag_commit"] == plan.binding.commit
        and snapshot["immutable_releases"] is True,
        "Remote source or immutable policy changed",
    )
    release = snapshot["release"]
    require(
        isinstance(release, dict)
        and release.get("id") == reservation["release_id"]
        and release.get("draft") is True
        and release.get("prerelease") is False
        and release.get("tag_name") == plan.binding.version,
        "Reserved draft is absent, changed or already published",
    )
    assets = release.get("assets")
    require(
        isinstance(assets, list) and len(assets) == len(plan.assets),
        "Draft has missing or extra assets",
    )
    found = {}
    for asset in assets:
        require(
            isinstance(asset, dict) and isinstance(asset.get("name"), str),
            "Invalid draft asset",
        )
        require(asset["name"] not in found, "Duplicate draft asset")
        require(
            asset.get("state") == "uploaded"
            and type(asset.get("size")) is int
            and asset["size"] > 0,
            "Incomplete draft upload",
        )
        found[asset["name"]] = asset.get("digest")
    require(found == dict(plan.assets), "Draft asset digests differ")
    require(
        snapshot["version_tags"]
        == {
            name: record["index"].split("@")[1]
            for name, record in plan.manifest["images"].items()
        },
        "Version tag pair is incomplete or differs",
    )
    verify_gates(reports, READBACK_GATES, plan.binding, verifier)
    return {
        "release_id": reservation["release_id"],
        "draft": False,
        "prerelease": False,
        "make_latest": "false",
    }


def recovery_report(plan: PublicationPlan, snapshot: dict) -> dict:
    """Explicit incident record, never deletion, overwrite, resume or promotion."""
    validate_plan(plan)
    fields(
        snapshot,
        {"releases", "version_tags", "tag_commit", "immutable_releases"},
        "recovery snapshot",
    )
    require(isinstance(snapshot["releases"], list), "Unknown release state")
    tags = fields(snapshot["version_tags"], {"backend", "web"}, "recovery version tags")
    for value in tags.values():
        require(
            value is None or (isinstance(value, str) and DIGEST.fullmatch(value)),
            "Unknown registry tag state",
        )
    return {
        "binding": plan.binding.digest,
        "ready": False,
        "automatic_resume_allowed": False,
        "automatic_cleanup_allowed": False,
        "snapshot": snapshot,
        "instructions": [
            "Keep the release draft and convenience tags unchanged; preserve run receipts and uploaded assets.",
            "Authenticate every existing asset and registry subject against the original binding before an operator chooses recovery.",
            "Do not replace existing assets/version tags or reuse this reservation automatically. A new reviewed version is the default recovery path.",
            "If already published, handle it as a published-release incident; do not rewrite immutable history.",
        ],
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--repository", default="endorses/psst.zip")
    parser.add_argument("--backend-index", type=Path, required=True)
    parser.add_argument("--web-index", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        manifest = validate_manifest(
            read_json(read_bounded_file(args.manifest)), args.repository
        )
        for component in ("backend", "web"):
            validate_registry_index(
                read_bounded_file(getattr(args, component + "_index")),
                manifest["images"][component],
            )
        subjects = image_subjects(manifest)
        subjects.update(
            {
                "manifest": "file:release-manifest.json@"
                + sha256(read_bounded_file(args.manifest)),
                "bundle": "file:"
                + manifest["bundle"]["name"]
                + "@sha256:"
                + manifest["bundle"]["sha256"],
            }
        )
        create_output(
            args.output,
            json_bytes(
                {
                    "kind": "publication-inspection",
                    "publication_authorized": False,
                    "updater_subjects": subjects,
                    "required_gates": sorted(GATES),
                }
            ),
        )
    except (InvalidRelease, OSError, ValueError) as error:
        parser.exit(1, f"Publication preparation rejected: {error}\n")


if __name__ == "__main__":
    main()
