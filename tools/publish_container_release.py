#!/usr/bin/env python3
"""Read-only release evidence compatibility for the Python recovery experiment.

The Go publisher owns preparation, reservation, mutation and final readback.
Retained v1 evidence validators support historical authenticated recovery inputs.
"""

from __future__ import annotations

import hashlib
import re
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Protocol

from prepare_release_candidate import IMAGE_TYPES, INDEX_TYPES
from release_artifacts import (
    COMMIT,
    DIGEST,
    PLATFORMS,
    InvalidRelease,
    fields,
    json_bytes,
    matches,
    read_bounded_file,
    read_json,
    require,
)
from temporary_caddy_acceptance import committed_acceptance, verify_finding

WORKFLOW = ".github/workflows/release.yml"
GATES = frozenset(
    {
        "source-ci",
        "source-scanners",
        "final-image-scanners",
        "final-image-smoke",
        "runtime-notices",
        "corresponding-source",
        "upgrade-recovery",
    }
)
READBACK_GATES = frozenset(
    {"registry-readback", "anonymous-pull", "asset-readback", "provenance"}
)
SOURCE_NAME = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9._-]{0,159}\Z")
SOURCE_REVIEW_POLICY = "tools/container-distribution-policy.json"
SOURCE_COVERAGE = {
    component + "-" + arch: frozenset(
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


def source_review_details(details: dict, binding: Binding) -> None:
    """Check complete source, image notice and exact committed policy facts."""
    fields(
        details,
        {"schema_version", "source_subjects", "images", "policy", "coverage"},
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
            parsed = datetime.fromisoformat(completed)
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
    result, source_details, caddy_acceptance = {}, None, None
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
                    if (
                        isinstance(finding, dict)
                        and finding.get("disposition") == "temporarily-accepted"
                    ):
                        require(
                            gate == "final-image-scanners",
                            "Source findings cannot use Caddy acceptance",
                        )
                        if caddy_acceptance is None:
                            caddy_acceptance = committed_acceptance(binding)
                        node, policy_fact = caddy_acceptance
                        require(
                            receipt.details.get("temporary_caddy_exception_policy")
                            == policy_fact,
                            "Scanner acceptance is not bound to the committed policy",
                        )
                        verify_finding(
                            finding, binding, scan["target"], node, policy_fact
                        )
                        continue
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
        elif gate == "corresponding-source":
            source_review_details(receipt.details, binding)
            source_details = receipt.details
        result[gate] = receipt.report_digest
    if caddy_acceptance is not None and source_details is not None:
        require(
            caddy_acceptance[1] == source_details["policy"],
            "Caddy acceptance differs from reviewed source policy",
        )
    return result
