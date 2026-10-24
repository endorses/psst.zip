"""Authenticate release gate reports with GitHub's artifact attestation verifier.

This adapter reads evidence; it cannot sign, upload, or publish anything. Report
content is trusted only after verification of a private snapshot against the
exact reviewed repository, workflow commit, version ref, and hosted issuer.
"""

from __future__ import annotations

import os
import selectors
import shutil
import signal
import subprocess
import tempfile
import time
from pathlib import Path

from publish_container_release import (
    GATES,
    WORKFLOW,
    Binding,
    VerifiedEvidence,
    sha256,
    source_digest,
)
from release_artifacts import (
    COMMIT,
    MAX_BUNDLE_BYTES,
    VERSION,
    InvalidRelease,
    fields,
    json_bytes,
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
MAX_AUTHENTICATION_CACHE = 64


def checked_invocation(run_id: int | None, run_attempt: int | None) -> tuple | None:
    require(
        (run_id is None and run_attempt is None)
        or (
            type(run_id) is int
            and run_id > 0
            and type(run_attempt) is int
            and run_attempt > 0
        ),
        "Verification requires both positive workflow run ID and attempt",
    )
    return None if run_id is None else (run_id, run_attempt)


def matching_invocation(statement: dict, binding: Binding, invocation: tuple) -> bool:
    """Constrain provenance only after the CLI verified its certificate/signature."""
    predicate = statement.get("predicate")
    if not isinstance(predicate, dict):
        return False
    build, run = predicate.get("buildDefinition"), predicate.get("runDetails")
    if not isinstance(build, dict) or not isinstance(run, dict):
        return False
    external, internal = (
        build.get("externalParameters"),
        build.get("internalParameters"),
    )
    builder, metadata = run.get("builder"), run.get("metadata")
    if not all(
        isinstance(row, dict) for row in (external, internal, builder, metadata)
    ):
        return False
    github = internal.get("github")
    if not isinstance(github, dict):
        return False
    repository = "https://github.com/" + binding.repository
    ref = "refs/tags/" + binding.version
    return (
        statement.get("_type") == "https://in-toto.io/Statement/v1"
        and build.get("buildType") == "https://actions.github.io/buildtypes/workflow/v1"
        and external.get("workflow")
        == {"repository": repository, "ref": ref, "path": WORKFLOW}
        and build.get("resolvedDependencies")
        == [
            {
                "uri": "git+" + repository + "@" + ref,
                "digest": {"gitCommit": binding.commit},
            }
        ]
        and github.get("event_name") == "push"
        and github.get("runner_environment") == "github-hosted"
        and builder.get("id") == repository + "/" + WORKFLOW + "@" + ref
        and metadata.get("invocationId")
        == repository + f"/actions/runs/{invocation[0]}/attempts/{invocation[1]}"
    )


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


def verified_subject(
    output: bytes,
    digest: str,
    *,
    binding: Binding | None = None,
    run_id: int | None = None,
    run_attempt: int | None = None,
) -> None:
    """Reject empty or malformed successful output, even if the CLI exits zero.

    Certificate policies are enforced by the CLI flags above. A workflow can
    control predicate contents, so those contents never substitute for signer
    identity or hosted-runner verification.
    """
    invocation = checked_invocation(run_id, run_attempt)
    require(
        invocation is None or binding is not None,
        "Workflow scope lacks release identity",
    )
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
            if subject["digest"].get("sha256") == digest.removeprefix("sha256:") and (
                invocation is None
                or matching_invocation(statement, binding, invocation)
            ):
                found = True
    require(
        found, "Verified attestation does not cover the report bytes and workflow scope"
    )


class GhEvidenceVerifier:
    """Production verifier; unsigned caller JSON never produces a receipt.

    The audited release workflow must produce reports from completed checks and
    sign them. This verifies their origin and release binding, not the truth of
    arbitrary statements emitted by compromised reviewed workflow code.
    """

    def __init__(
        self,
        *,
        token: str | None = None,
        gh: Path | None = None,
        run_id: int | None = None,
        run_attempt: int | None = None,
    ):
        self.invocation = checked_invocation(run_id, run_attempt)
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
        self._authenticated: dict[tuple, None] = {}

    def verify(self, gate: str, report: Path, binding: Binding) -> VerifiedEvidence:
        require(gate in GATES, "Unknown release evidence gate")
        content = read_bounded_file(report)
        value = read_json(content)
        if isinstance(value, dict) and value.get("kind") == "container-release-result":
            from release_result import checked_result

            result = checked_result(value, binding, self.invocation)
            require(gate in result["checks"], "Release result lacks required check")
            self.authenticate(content, binding)
            return VerifiedEvidence(
                gate, binding.digest, sha256(content), True, result["checks"][gate]
            )
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
        digest = sha256(content)
        key = (binding.digest, digest, self.invocation)
        if key in self._authenticated:
            return
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
            run_id, run_attempt = self.invocation or (None, None)
            verified_subject(
                output,
                digest,
                binding=binding,
                run_id=run_id,
                run_attempt=run_attempt,
            )
        # Store only successful verification of these exact immutable bytes.
        # Keep this instance-local and bounded; never persist a trust decision.
        if len(self._authenticated) >= MAX_AUTHENTICATION_CACHE:
            self._authenticated.pop(next(iter(self._authenticated)))
        self._authenticated[key] = None


def verify_final_subjects(
    plan, assets: dict[str, Path], verifier: GhEvidenceVerifier
) -> dict:
    """Authenticate the single standard statement covering every final subject.

    The manifest lookup verifies the signature/certificate once. Every exact
    name/digest pair must occur in that same verified statement, including the
    index and child digests sharing an OCI repository name. Registry bytes are
    independently measured by the transport; retained files are rehashed here.
    """
    require(
        set(assets) == set(dict(plan.assets)), "Final provenance asset coverage differs"
    )
    expected = set()
    manifest_name, manifest_digest = (
        dict(plan.binding.subjects)["manifest"].removeprefix("file:").split("@")
    )
    for _, subject in plan.binding.subjects:
        name, digest = subject.split("@")
        expected.add(
            (
                name.removeprefix("file:").removeprefix("oci://"),
                digest.removeprefix("sha256:"),
            )
        )
        if name.startswith("file:"):
            require(
                source_digest(assets[name.removeprefix("file:")]) == digest,
                "Final attested file changed",
            )
    with tempfile.TemporaryDirectory(prefix="psst-final-provenance-") as temporary:
        environment = {
            "PATH": os.defpath,
            "HOME": temporary,
            "LANG": "C",
            "GH_CONFIG_DIR": str(Path(temporary) / "gh-config"),
            "GH_PROMPT_DISABLED": "1",
        }
        if verifier.token:
            environment["GH_TOKEN"] = verifier.token
        output = bounded_verify(
            verification_arguments(verifier.gh, assets[manifest_name], plan.binding),
            environment,
        )
        run_id, attempt = verifier.invocation or (None, None)
        verified_subject(
            output,
            manifest_digest,
            binding=plan.binding,
            run_id=run_id,
            run_attempt=attempt,
        )
        complete = False
        for candidate in read_json(output):
            try:
                verified_subject(
                    json_bytes([candidate]),
                    manifest_digest,
                    binding=plan.binding,
                    run_id=run_id,
                    run_attempt=attempt,
                )
            except InvalidRelease:
                continue
            subjects = candidate["verificationResult"]["statement"]["subject"]
            pairs = {
                (item["name"], item["digest"].get("sha256"))
                for item in subjects
                if isinstance(item.get("name"), str)
            }
            if pairs == expected and len(pairs) == len(subjects):
                complete = True
                break
        require(
            complete, "Verified final statement lacks the exact complete subject set"
        )
        for name, digest in plan.assets:
            require(
                source_digest(assets[name]) == digest,
                "Final attested file changed during verification",
            )
    return {"subjects": dict(plan.binding.subjects)}
