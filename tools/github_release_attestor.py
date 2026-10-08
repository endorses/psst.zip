#!/usr/bin/env python3
"""Hosted-only official GitHub action bridge; independent verification is mandatory.

This is a signing/uploading adapter, not a local signer. It is only instantiated
by the checked publication driver inside the exact hosted tag workflow. Subprocess
diagnostics and Actions commands are deliberately withheld from runner logs.
"""

from __future__ import annotations

import base64
import hashlib
import http.client
import os
import platform
import re
import selectors
import signal
import stat
import subprocess
import tempfile
import time
from pathlib import Path
from types import SimpleNamespace
from urllib.parse import urlsplit

from github_release_evidence import (
    GhEvidenceVerifier,
    PREDICATE,
    bounded_verify,
    verification_arguments,
    verified_subject,
)
from github_release_transport import WorkflowContext, private_directory
from publish_container_release import (
    Binding,
    GATES,
    READBACK_GATES,
    PublicationPlan,
    SOURCE_NAME,
    image_subjects,
    sha256,
    source_digest,
    validate_plan,
)
from release_artifacts import (
    DIGEST,
    InvalidRelease,
    json_bytes,
    matches,
    read_bounded_file,
    read_json,
    require,
)

ACTION_COMMIT = "1e69f48acb82d1966a394da916b4c1698aa569d6"
ACTION_FILES = {
    "action.yml": (
        4048,
        "9e4a1b808433f9ec87120b534e11fc35469a039bdbdc62b019441444c9ad0449",
    ),
    "package.json": (
        2757,
        "221767f43cc74afbd71dc9531af4e8e7494d94f3530acfbd749e09105c7d2c8d",
    ),
    "dist/index.js": (
        4868977,
        "3ca89e06ffcb09ff97e9b1633575865cad0b23310ac82176d72990d7554836b5",
    ),
}
CONTEXT_KEYS = (
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
)
ACTION_TIMEOUT = 180
MAX_ACTION_OUTPUT = 4 * 1024**2


def limited_file(path: Path, limit: int) -> bytes:
    require(
        path.is_file() and not path.is_symlink(), "Signing input is not a regular file"
    )
    require(
        0 < path.stat().st_size <= limit, "Signing input exceeds bounds or is empty"
    )
    with path.open("rb") as stream:
        content = stream.read(limit + 1)
    require(0 < len(content) <= limit, "Signing input exceeds bounds or is empty")
    return content


def official_bytes(name: str) -> bytes:
    """Fetch only pinned public source bytes; no proxy, redirects or credentials."""
    size, checksum = ACTION_FILES[name]
    connection = http.client.HTTPSConnection("raw.githubusercontent.com", timeout=30)
    try:
        connection.request("GET", f"/actions/attest/{ACTION_COMMIT}/{name}")
        response = connection.getresponse()
        require(response.status == 200, "Official attestor source download failed")
        content = response.read(size + 1)
        require(
            len(content) == size and hashlib.sha256(content).hexdigest() == checksum,
            "Official attestor source hash differs",
        )
        return content
    except (OSError, http.client.HTTPException):
        raise InvalidRelease("Official attestor source download failed") from None
    finally:
        connection.close()


def checked_action(directory: Path) -> Path:
    private_directory(directory)
    for name, (size, checksum) in ACTION_FILES.items():
        path = directory / name
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        require(not path.parent.is_symlink(), "Official action directory is linked")
        if not path.exists():
            with path.open("xb") as stream:
                stream.write(official_bytes(name))
            path.chmod(0o400)
        require(
            path.is_file() and not path.is_symlink() and path.stat().st_size == size,
            "Official action source file is unsafe",
        )
        require(
            source_digest(path) == "sha256:" + checksum,
            "Official attestor source hash differs",
        )
    return directory / "dist/index.js"


def hosted_node() -> tuple[Path, str]:
    """Use the runner's Node24 runtime, never a caller-selected signer/PATH."""
    candidates = sorted(
        Path("/home/runner/runners").glob("*/externals/node24/bin/node")
    )
    candidates += [Path("/opt/actions-runner/externals/node24/bin/node")]
    require(len(candidates) <= 17, "Hosted Node24 runtime inventory exceeds bounds")
    for node in candidates:
        if not node.is_file() or node.is_symlink():
            continue
        metadata = node.stat()
        require(
            metadata.st_uid in {0, os.geteuid()}
            and stat.S_IMODE(metadata.st_mode) & 0o022 == 0,
            "Hosted Node24 runtime has unsafe ownership or permissions",
        )
        try:
            result = subprocess.run(
                [
                    str(node),
                    "-p",
                    "JSON.stringify([process.version,process.platform,process.arch])",
                ],
                env={"PATH": os.defpath, "LANG": "C"},
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL,
                timeout=10,
                check=False,
            )
        except (OSError, subprocess.TimeoutExpired):
            raise InvalidRelease("Hosted Node24 runtime check failed") from None
        require(
            result.returncode == 0 and len(result.stdout) <= 256,
            "Hosted Node24 runtime check failed",
        )
        facts = read_json(result.stdout)
        expected_arch = {"x86_64": "x64", "aarch64": "arm64"}.get(platform.machine())
        require(
            isinstance(facts, list)
            and len(facts) == 3
            and isinstance(facts[0], str)
            and re.fullmatch(r"v24\.[0-9]+\.[0-9]+", facts[0])
            and facts[1:] == ["linux", expected_arch],
            "Official action requires the native hosted Node24 runtime",
        )
        return node, facts[0]
    raise InvalidRelease("Official hosted runner Node24 runtime is unavailable")


def run_action(node: Path, action: Path, environment: dict[str, str]) -> None:
    """Drain but never log stdout/stderr, including embedded Actions commands."""
    process = subprocess.Popen(
        [str(node), "--max-http-header-size=32768", str(action)],
        cwd=action.parent.parent,
        env=environment,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        start_new_session=True,
    )
    deadline, count = time.monotonic() + ACTION_TIMEOUT, 0
    try:
        with selectors.DefaultSelector() as poll:
            poll.register(process.stdout, selectors.EVENT_READ)
            poll.register(process.stderr, selectors.EVENT_READ)
            while poll.get_map():
                remaining = deadline - time.monotonic()
                require(remaining > 0, "Official GitHub attestation action timed out")
                for selected, _ in poll.select(min(remaining, 1)):
                    block = os.read(selected.fileobj.fileno(), 65536)
                    if not block:
                        poll.unregister(selected.fileobj)
                    count += len(block)
                    require(
                        count <= MAX_ACTION_OUTPUT,
                        "Official action output exceeds bounds",
                    )
            require(
                process.wait(timeout=max(0.01, deadline - time.monotonic())) == 0,
                "Official GitHub attestation action failed; remote state is uncertain",
            )
    except subprocess.TimeoutExpired:
        raise InvalidRelease("Official GitHub attestation action timed out") from None
    finally:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()
        process.stdout.close()
        process.stderr.close()


def bundle_output(output: Path, temporary: Path) -> Path:
    try:
        lines = limited_file(output, 16384).decode("utf-8").splitlines()
    except UnicodeError:
        raise InvalidRelease("Malformed official action output") from None
    values = {}
    while lines:
        header = lines.pop(0)
        match = re.fullmatch(
            r"(bundle-path|attestation-id|attestation-url)<<([a-zA-Z0-9_-]{1,100})",
            header,
        )
        require(match is not None, "Malformed official action output")
        name, delimiter = match.groups()
        require(
            name not in values and len(lines) >= 2,
            "Duplicate or truncated action output",
        )
        value, closing = lines.pop(0), lines.pop(0)
        require(
            closing == delimiter and 0 < len(value) <= 2048,
            "Malformed action output value",
        )
        values[name] = value
    require("bundle-path" in values, "Official action produced no bundle")
    path = Path(values["bundle-path"])
    require(
        path.is_absolute()
        and path.is_relative_to(temporary)
        and ".." not in path.parts,
        "Official bundle escaped private invocation",
    )
    current = path
    while current != temporary:
        require(not current.is_symlink(), "Official bundle path is linked")
        current = current.parent
    require(path.is_file(), "Official action produced no complete bundle")
    return path


def check_bundle(
    content: bytes, name: str, digest: str, binding: Binding, context: dict[str, str]
) -> None:
    """Structural check only; certificates/signatures require independent gh verify."""
    bundle = read_json(content)
    require(isinstance(bundle, dict), "Malformed official bundle")
    envelope = bundle.get("dsseEnvelope")
    require(
        isinstance(envelope, dict)
        and envelope.get("payloadType") == "application/vnd.in-toto+json",
        "Missing official DSSE envelope",
    )
    try:
        payload = base64.b64decode(envelope.get("payload", ""), validate=True)
    except (ValueError, TypeError):
        raise InvalidRelease("Malformed official DSSE payload") from None
    statement = read_json(payload)
    require(
        isinstance(statement, dict)
        and statement.get("_type") == "https://in-toto.io/Statement/v1"
        and statement.get("predicateType") == PREDICATE
        and statement.get("subject")
        == [{"name": name, "digest": {"sha256": digest.removeprefix("sha256:")}}],
        "Official bundle subject or predicate differs",
    )
    predicate = statement.get("predicate")
    require(isinstance(predicate, dict), "Missing official provenance predicate")
    build = predicate.get("buildDefinition")
    require(isinstance(build, dict), "Missing official build definition")
    external = build.get("externalParameters")
    require(isinstance(external, dict), "Missing official workflow parameters")
    workflow = external.get("workflow")
    require(
        build.get("buildType") == "https://actions.github.io/buildtypes/workflow/v1"
        and workflow
        == {
            "repository": "https://github.com/" + binding.repository,
            "ref": "refs/tags/" + binding.version,
            "path": ".github/workflows/release.yml",
        }
        and build.get("resolvedDependencies")
        == [
            {
                "uri": "git+https://github.com/"
                + binding.repository
                + "@refs/tags/"
                + binding.version,
                "digest": {"gitCommit": binding.commit},
            }
        ],
        "Official provenance does not match the exact source/workflow",
    )
    internal = build.get("internalParameters")
    require(
        isinstance(internal, dict) and isinstance(internal.get("github"), dict),
        "Missing official runner parameters",
    )
    require(
        internal["github"].get("event_name") == "push"
        and internal["github"].get("runner_environment") == "github-hosted",
        "Official provenance runner/event differs",
    )
    run = predicate.get("runDetails")
    require(
        isinstance(run, dict)
        and isinstance(run.get("builder"), dict)
        and isinstance(run.get("metadata"), dict),
        "Missing official invocation metadata",
    )
    require(
        run["builder"].get("id")
        == "https://github.com/" + context["GITHUB_WORKFLOW_REF"]
        and run["metadata"].get("invocationId")
        == "https://github.com/"
        + binding.repository
        + "/actions/runs/"
        + context["GITHUB_RUN_ID"]
        + "/attempts/"
        + context["GITHUB_RUN_ATTEMPT"],
        "Official provenance invocation differs",
    )


class WorkflowAttestor:
    def __init__(
        self,
        binding: Binding,
        *,
        token: str,
        environment: dict[str, str],
        verifier: GhEvidenceVerifier,
        private_output: Path,
        private_action_cache: Path | None = None,
    ):
        WorkflowContext.from_environment(SimpleNamespace(binding=binding), environment)
        require(
            isinstance(token, str)
            and token
            and len(token) <= 4096
            and not any(c in token for c in "\r\n\0"),
            "Invalid GitHub signing token",
        )
        require(
            environment.get("GITHUB_SERVER_URL") == "https://github.com"
            and environment.get("GITHUB_API_URL") == "https://api.github.com",
            "Signing requires official GitHub endpoints",
        )
        require(
            platform.system() == "Linux"
            and platform.machine() in {"x86_64", "aarch64"},
            "Signing requires a native supported Linux runner",
        )
        url = environment.get("ACTIONS_ID_TOKEN_REQUEST_URL", "")
        try:
            parsed = urlsplit(url)
            port = parsed.port
        except ValueError:
            raise InvalidRelease("Untrusted GitHub OIDC request endpoint") from None
        require(
            0 < len(url) <= 4096
            and not any(c in url for c in "\r\n\0")
            and parsed.scheme == "https"
            and parsed.hostname is not None
            and parsed.hostname.endswith(".actions.githubusercontent.com")
            and port in {None, 443}
            and parsed.username is None
            and parsed.password is None
            and not parsed.fragment,
            "Untrusted GitHub OIDC request endpoint",
        )
        oidc_token = environment.get("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
        require(
            0 < len(oidc_token) <= 16384 and not any(c in oidc_token for c in "\r\n\0"),
            "Hosted OIDC token capability is missing",
        )
        require(
            private_output.is_absolute()
            and not any(c in str(private_output) for c in "*?[]\r\n\0"),
            "Signing output path must be an absolute literal path",
        )
        private_directory(private_output)
        self.root = private_output
        self.binding, self.verifier = binding, verifier
        self.environment = {key: environment[key] for key in CONTEXT_KEYS}
        self.environment.update(
            {
                "GITHUB_SERVER_URL": "https://github.com",
                "GITHUB_API_URL": "https://api.github.com",
                "ACTIONS_ID_TOKEN_REQUEST_URL": url,
                "ACTIONS_ID_TOKEN_REQUEST_TOKEN": oidc_token,
            }
        )
        self.token = token
        event = limited_file(
            Path(environment.get("GITHUB_EVENT_PATH", "")), 2 * 1024**2
        )
        record = read_json(event)
        require(
            isinstance(record, dict)
            and isinstance(record.get("repository"), dict)
            and record["repository"].get("visibility") == "public"
            and record["repository"].get("full_name") == binding.repository
            and record.get("after") == binding.commit
            and record.get("ref") == "refs/tags/" + binding.version,
            "Signing requires the exact public repository push event",
        )
        self.event = event
        self.node, self.node_version = hosted_node()
        self.node_digest = source_digest(self.node)
        action_cache = private_action_cache or self.root / "official-attestor"
        require(
            action_cache.is_absolute()
            and not any(c in str(action_cache) for c in "*?[]\r\n\0"),
            "Official action cache must be an absolute literal path",
        )
        self.action = checked_action(action_cache)

    def _attest(self, name: str, digest: str, path: Path | None = None) -> str:
        matches(digest, DIGEST, "Invalid signing digest")
        require(
            source_digest(self.node) == self.node_digest,
            "Hosted Node24 runtime changed",
        )
        checked_action(self.action.parent.parent)
        with tempfile.TemporaryDirectory(prefix="sign-", dir=self.root) as temporary:
            directory = Path(temporary)
            event = directory / "event.json"
            event.write_bytes(self.event)
            event.chmod(0o400)
            output = directory / "output"
            output.touch(mode=0o600)
            environment = dict(self.environment)
            environment.update(
                {
                    "PATH": os.defpath,
                    "LANG": "C",
                    "HOME": str(directory),
                    "RUNNER_TEMP": str(directory),
                    "GITHUB_EVENT_PATH": str(event),
                    "GITHUB_OUTPUT": str(output),
                    "GITHUB_WORKSPACE": str(directory),
                    "INPUT_GITHUB-TOKEN": self.token,
                    "INPUT_SUBJECT-NAME": name,
                    "INPUT_SUBJECT-PATH": str(path) if path else "",
                    "INPUT_SUBJECT-DIGEST": "" if path else digest,
                }
            )
            for boolean in (
                "PUSH-TO-REGISTRY",
                "CREATE-STORAGE-RECORD",
                "SHOW-SUMMARY",
                "PRIVATE-SIGNING",
            ):
                environment["INPUT_" + boolean] = "false"
            run_action(self.node, self.action, environment)
            require(
                source_digest(self.node) == self.node_digest,
                "Hosted Node24 runtime changed",
            )
            checked_action(self.action.parent.parent)
            bundle_path = bundle_output(output, directory)
            bundle = limited_file(bundle_path, 2 * 1024**2)
            check_bundle(bundle, name, digest, self.binding, self.environment)
            args = verification_arguments(
                self.verifier.gh, path or Path("unused"), self.binding
            )
            if path is None:
                args[3] = "oci://" + name + "@" + digest
            args.extend(["--bundle", str(bundle_path)])
            with tempfile.TemporaryDirectory(
                prefix="verify-image-" if path is None else "verify-file-",
                dir=self.root,
            ) as verification:
                verified_subject(
                    bounded_verify(
                        args, self._verification_environment(Path(verification))
                    ),
                    digest,
                )
            require(
                limited_file(bundle_path, 2 * 1024**2) == bundle,
                "Official bundle changed during verification",
            )
            require(
                limited_file(event, 2 * 1024**2) == self.event,
                "Signing event snapshot was changed",
            )
            return sha256(bundle)

    def _file(self, name: str, path: Path, expected: str) -> str:
        require(SOURCE_NAME.fullmatch(name) is not None, "Unsafe signing asset name")
        require(source_digest(path) == expected, "Signing asset has changed")
        with tempfile.TemporaryDirectory(prefix="subject-", dir=self.root) as temporary:
            snapshot = Path(temporary) / name
            original = path.stat()
            count = 0
            with path.open("rb") as source, snapshot.open("xb") as destination:
                while block := source.read(1024**2):
                    count += len(block)
                    require(
                        count <= original.st_size, "Signing asset grew during snapshot"
                    )
                    destination.write(block)
            snapshot.chmod(0o400)
            require(
                count == original.st_size
                and source_digest(snapshot) == expected
                and source_digest(path) == expected,
                "Signing asset changed during snapshot",
            )
            bundle_digest = self._attest(name, expected, snapshot)
            args = verification_arguments(self.verifier.gh, snapshot, self.binding)
            verified_subject(
                bounded_verify(args, self._verification_environment(Path(temporary))),
                expected,
            )
            require(
                source_digest(snapshot) == expected and source_digest(path) == expected,
                "Signing asset changed during verification",
            )
            return bundle_digest

    def _verification_environment(self, directory: Path) -> dict[str, str]:
        result = {
            "PATH": os.defpath,
            "LANG": "C",
            "HOME": str(directory),
            "GH_CONFIG_DIR": str(directory / "gh-config"),
            "GH_PROMPT_DISABLED": "1",
        }
        if self.verifier.token:
            result["GH_TOKEN"] = self.verifier.token
        return result

    def attest_subjects(
        self, plan: PublicationPlan, files: dict[str, Path], private_output: Path
    ) -> Path:
        validate_plan(plan)
        require(
            plan.binding == self.binding
            and private_output.resolve() == self.root.resolve(),
            "Signing plan/output differs from checked context",
        )
        assets = dict(plan.assets)
        require(
            len(assets) == len(plan.assets)
            and len(dict(self.binding.subjects)) == len(self.binding.subjects),
            "Duplicate signing subjects/assets",
        )
        actual_images = {
            key: value
            for key, value in self.binding.subjects
            if value.startswith("oci://")
        }
        require(
            actual_images == image_subjects(plan.manifest),
            "Signing image subjects differ from the exact manifest graph",
        )
        require(set(files) == set(assets), "Signing requires every exact release asset")
        for name, digest in assets.items():
            require(
                source_digest(files[name]) == digest,
                "Release asset differs before signing",
            )
        proofs = {}
        file_subjects = set()
        for key, subject in self.binding.subjects:
            if subject.startswith("file:"):
                name, digest = subject.removeprefix("file:").split("@")
                require(
                    name in assets and assets[name] == digest,
                    "File subject differs from release asset",
                )
                file_subjects.add(name)
                proofs[key] = self._file(name, files[name], digest)
            else:
                require(
                    subject in actual_images.values(),
                    "Image signing subject differs from the manifest",
                )
                name, digest = subject.removeprefix("oci://").split("@")
                proofs[key] = self._attest(name, digest)
                args = verification_arguments(
                    self.verifier.gh, Path("unused"), self.binding
                )
                args[3] = subject
                with tempfile.TemporaryDirectory(
                    prefix="verify-image-", dir=self.root
                ) as temporary:
                    verified_subject(
                        bounded_verify(
                            args, self._verification_environment(Path(temporary))
                        ),
                        digest,
                    )
        require(
            file_subjects == set(assets) and len(proofs) == len(self.binding.subjects),
            "Provenance coverage is incomplete",
        )
        report = self.root / "provenance.json"
        with report.open("xb") as stream:
            stream.write(
                json_bytes(
                    {
                        "schema_version": 1,
                        "gate": "provenance",
                        "binding_digest": self.binding.digest,
                        "passed": True,
                        "details": {
                            "subjects": dict(self.binding.subjects),
                            "verified_bundle_sha256": proofs,
                            "official_action_commit": ACTION_COMMIT,
                            "node_version": self.node_version,
                            "node_sha256": self.node_digest,
                        },
                    }
                )
            )
        report.chmod(0o400)
        self.sign_report(self.binding, report)
        self.verifier.verify("provenance", report, self.binding)
        return report

    def sign_report(self, binding: Binding, report: Path) -> None:
        require(binding == self.binding, "Signing report binding differs")
        content = read_bounded_file(report)
        record = read_json(content)
        require(
            isinstance(record, dict)
            and set(record)
            == {"schema_version", "gate", "binding_digest", "passed", "details"}
            and type(record["schema_version"]) is int
            and record["schema_version"] == 1
            and record["gate"] in GATES | READBACK_GATES
            and record["binding_digest"] == binding.digest
            and record["passed"] is True
            and isinstance(record["details"], dict),
            "Signing report is not an exact completed bound gate",
        )
        self._file(report.name, report, sha256(content))
        require(read_bounded_file(report) == content, "Signing report changed")
        self.verifier.authenticate(content, binding)
