#!/usr/bin/env python3
"""Authenticate release gate reports with GitHub's artifact attestation verifier.

This adapter reads evidence; it cannot sign, upload, or publish anything. Report
content is trusted only after verification of a private snapshot against the
exact reviewed repository, workflow commit, version ref, and hosted issuer.
"""

from __future__ import annotations

import os
from pathlib import Path
import selectors
import shutil
import signal
import subprocess
import tempfile
import time

from publish_container_release import (
    Binding,
    GATES,
    READBACK_GATES,
    VerifiedEvidence,
    WORKFLOW,
    sha256,
)
from release_artifacts import (
    COMMIT,
    VERSION,
    InvalidRelease,
    MAX_BUNDLE_BYTES,
    fields,
    matches,
    read_bounded_file,
    read_json,
    repository_name,
    require,
)

ISSUER = "https://token.actions.githubusercontent.com"
PREDICATE = "https://slsa.dev/provenance/v1"
MAX_OUTPUT = 4 * 1024 * 1024
TIMEOUT = 55


def bounded_verify(args: list[str], environment: dict[str, str]) -> bytes:
    """Drain both pipes with a combined bound, suppressing credential diagnostics."""
    process = subprocess.Popen(
        args,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=environment,
        start_new_session=True,
    )
    deadline, count = time.monotonic() + TIMEOUT, 0
    output = bytearray()
    try:
        with selectors.DefaultSelector() as poll:
            poll.register(process.stdout, selectors.EVENT_READ, True)
            poll.register(process.stderr, selectors.EVENT_READ, False)
            while poll.get_map():
                remaining = deadline - time.monotonic()
                require(remaining > 0, "GitHub evidence verification timed out")
                for selected, _ in poll.select(min(remaining, 1)):
                    block = os.read(selected.fileobj.fileno(), 65536)
                    if not block:
                        poll.unregister(selected.fileobj)
                        continue
                    count += len(block)
                    require(
                        count <= MAX_OUTPUT, "GitHub verifier output exceeds bounds"
                    )
                    if selected.data:
                        output.extend(block)
            require(
                process.wait(timeout=max(0.01, deadline - time.monotonic())) == 0,
                "GitHub report attestation verification failed",
            )
    except subprocess.TimeoutExpired:
        raise InvalidRelease("GitHub evidence verification timed out") from None
    finally:
        # Helpers may still hold pipes open after the parent has exited. Always
        # reap this private group, including when the parent already completed.
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()
        process.stdout.close()
        process.stderr.close()
    return bytes(output)


def verification_arguments(gh: Path, report: Path, binding: Binding) -> list[str]:
    repository = repository_name(binding.repository)
    matches(binding.commit, COMMIT, "Invalid evidence source commit")
    matches(binding.version, VERSION, "Invalid evidence version")
    ref = "refs/tags/" + binding.version
    return [
        str(gh),
        "attestation",
        "verify",
        str(report),
        "--hostname",
        "github.com",
        "--repo",
        repository,
        "--signer-digest",
        binding.commit,
        "--source-digest",
        binding.commit,
        "--source-ref",
        ref,
        "--cert-identity",
        "https://github.com/" + repository + "/" + WORKFLOW + "@" + ref,
        "--cert-oidc-issuer",
        ISSUER,
        "--deny-self-hosted-runners",
        "--predicate-type",
        PREDICATE,
        "--format",
        "json",
        "--limit",
        "30",
    ]


def verified_subject(output: bytes, digest: str) -> None:
    """Reject empty or malformed successful output, even if the CLI exits zero.

    Certificate policies are enforced by the CLI flags above. A workflow can
    control predicate contents, so those contents never substitute for signer
    identity or hosted-runner verification.
    """
    results = read_json(output)
    require(
        isinstance(results, list) and 0 < len(results) <= 30,
        "GitHub verifier returned no bounded attestation results",
    )
    found = False
    for result in results:
        require(isinstance(result, dict), "Invalid attestation result")
        verified = result.get("verificationResult")
        require(isinstance(verified, dict), "Missing verified attestation")
        signature = verified.get("signature")
        require(
            isinstance(signature, dict)
            and isinstance(signature.get("certificate"), dict)
            and bool(signature["certificate"]),
            "Verified attestation lacks a certificate",
        )
        timestamps = verified.get("verifiedTimestamps")
        require(
            isinstance(timestamps, list) and timestamps,
            "Verified attestation lacks a witnessed timestamp",
        )
        statement = verified.get("statement")
        require(
            isinstance(statement, dict) and statement.get("predicateType") == PREDICATE,
            "Unexpected verified predicate",
        )
        subjects = statement.get("subject")
        require(isinstance(subjects, list) and subjects, "Missing attested subject")
        for subject in subjects:
            require(
                isinstance(subject, dict) and isinstance(subject.get("digest"), dict),
                "Invalid verified subject",
            )
            if subject["digest"].get("sha256") == digest.removeprefix("sha256:"):
                found = True
    require(found, "Verified attestation does not cover the report bytes")


class GhEvidenceVerifier:
    """Production verifier; unsigned caller JSON never produces a receipt.

    The audited release workflow must produce reports from completed checks and
    sign them. This verifies their origin and release binding, not the truth of
    arbitrary statements emitted by compromised reviewed workflow code.
    """

    def __init__(self, *, token: str | None = None, gh: Path | None = None):
        executable = gh or Path(shutil.which("gh") or "/usr/bin/gh")
        require(
            executable.is_absolute() and executable.is_file(),
            "Install the trusted GitHub CLI attestation verifier",
        )
        require(
            token is None or (isinstance(token, str) and token and "\n" not in token),
            "Invalid GitHub verification token",
        )
        self.gh = executable
        self.token = token

    def verify(self, gate: str, report: Path, binding: Binding) -> VerifiedEvidence:
        require(gate in GATES | READBACK_GATES, "Unknown release evidence gate")
        content = read_bounded_file(report)
        record = fields(
            read_json(content),
            {"schema_version", "gate", "binding_digest", "passed", "details"},
            "signed release evidence",
        )
        require(
            type(record["schema_version"]) is int and record["schema_version"] == 1,
            "Unsupported release evidence schema",
        )
        require(
            record["gate"] == gate
            and record["binding_digest"] == binding.digest
            and record["passed"] is True
            and isinstance(record["details"], dict),
            "Release report is failed or bound to another gate/release",
        )
        self.authenticate(content, binding)
        return VerifiedEvidence(
            gate, binding.digest, sha256(content), True, record["details"]
        )

    def authenticate(self, content: bytes, binding: Binding) -> None:
        """Authenticate bounded measurement bytes; caller validates their schema.

        Native matrix artifacts use the same exact release signer/source policy.
        This method verifies origin only and never issues a gate approval receipt.
        """
        require(
            isinstance(content, bytes) and 0 < len(content) <= MAX_BUNDLE_BYTES,
            "Attested measurement bytes exceed bounds or are empty",
        )
        # Verify the same bytes that were parsed. A substituted caller file or
        # changes made during the CLI call cannot become an authenticated report.
        with tempfile.TemporaryDirectory(prefix="psst-evidence-") as temporary:
            root = Path(temporary)
            snapshot = root / "report.json"
            snapshot.write_bytes(content)
            snapshot.chmod(0o400)
            environment = {
                "PATH": os.defpath,
                "HOME": temporary,
                "GH_CONFIG_DIR": str(root / "gh-config"),
                "GH_PROMPT_DISABLED": "1",
                "LANG": "C",
            }
            if self.token is not None:
                environment["GH_TOKEN"] = self.token
            output = bounded_verify(
                verification_arguments(self.gh, snapshot, binding), environment
            )
            require(
                read_bounded_file(snapshot) == content,
                "Verification snapshot was changed",
            )
            verified_subject(output, sha256(content))
