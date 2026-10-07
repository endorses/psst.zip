#!/usr/bin/env python3
"""Bounded GitHub/GHCR publication transport; requires an authenticated plan.

No standalone publish CLI is provided. The trusted release workflow supplies the
verified plan, current hosted-run identity and evidence verifier. Tests inject
HTTP/command fixtures; this module must not invent approval reports.
"""

from __future__ import annotations

import base64
import fcntl
import hashlib
import http.client
import os
import re
import signal
import socket
import stat
import subprocess
import tempfile
import threading
import time
from collections.abc import Callable, Iterator
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import quote, urlencode, urlsplit

from assemble_release_oci import inspect_archive
from publish_container_release import (
    WORKFLOW,
    EvidenceVerifier,
    PublicationPlan,
    ready_release_request,
    sha256,
    source_digest,
    validate_plan,
    validate_registry_index,
)
from release_artifacts import (
    COMMIT,
    DIGEST,
    InvalidRelease,
    fields,
    json_bytes,
    matches,
    read_bounded_file,
    read_json,
    require,
    validate_manifest,
)

GROUP = "container-release-publication"
API_VERSION = "2026-03-10"
MAX_JSON = 16 * 1024**2
MAX_ASSET = 2 * 1024**3
NUMBER = re.compile(r"[1-9][0-9]{0,19}\Z")
ALLOWED_HOSTS = {
    "api.github.com",
    "uploads.github.com",
    "ghcr.io",
    "release-assets.githubusercontent.com",
    "objects.githubusercontent.com",
}


class TransportError(InvalidRelease):
    """Fail-closed transport error without server payloads or credentials."""


@dataclass(frozen=True)
class Response:
    status: int
    headers: dict[str, str]
    body: bytes = b""
    digest: str | None = None
    size: int = 0


class HTTPS:
    """Fixed HTTPS hosts, bounded bodies/time, no ambient proxy or redirects."""

    def request(
        self,
        method: str,
        url: str,
        *,
        headers: dict | None = None,
        body: bytes | Path | None = None,
        limit: int = MAX_JSON,
        destination: Path | None = None,
    ) -> Response:
        parsed = urlsplit(url)
        require(
            parsed.scheme == "https"
            and parsed.hostname in ALLOWED_HOSTS
            and parsed.port in {None, 443}
            and parsed.username is None
            and not parsed.fragment,
            "Untrusted transport URL",
        )
        require(
            method in {"GET", "HEAD", "POST", "PUT", "PATCH"}, "Unsupported HTTP method"
        )
        connection = http.client.HTTPSConnection(parsed.hostname, timeout=30)
        started = time.monotonic()
        deadline = 1200 if isinstance(body, Path) or destination else 180

        def abort():
            if connection.sock is not None:
                try:
                    connection.sock.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
            connection.close()

        watchdog = threading.Timer(deadline, abort)
        watchdog.daemon = True
        watchdog.start()
        stream = None
        output = None
        try:
            request_headers = dict(headers or {})
            if isinstance(body, Path):
                require(
                    body.is_file() and not body.is_symlink(),
                    "Upload is not a regular file",
                )
                require(0 < body.stat().st_size <= MAX_ASSET, "Invalid upload size")
                stream = body.open("rb")
                request_headers["Content-Length"] = str(body.stat().st_size)
                payload = stream
            else:
                payload = body
            connection.request(
                method,
                parsed.path + ("?" + parsed.query if parsed.query else ""),
                payload,
                request_headers,
            )
            response = connection.getresponse()
            result_headers = {
                name.lower(): value for name, value in response.getheaders()
            }
            length = result_headers.get("content-length")
            require(
                length is None or (length.isdecimal() and int(length) <= limit),
                "HTTP response exceeds size limit",
            )
            chunks = []
            checksum = hashlib.sha256()
            size = 0
            if destination is not None and response.status == 200:
                output = destination.open("xb")
            while method != "HEAD":
                require(
                    time.monotonic() - started <= deadline,
                    "HTTP operation exceeded deadline",
                )
                chunk = response.read1(min(1024 * 1024, limit - size + 1))
                if not chunk:
                    break
                size += len(chunk)
                require(size <= limit, "HTTP response exceeds size limit")
                checksum.update(chunk)
                if output is not None:
                    output.write(chunk)
                else:
                    chunks.append(chunk)
            if output is not None:
                output.flush()
                os.fsync(output.fileno())
            return Response(
                response.status,
                result_headers,
                b"".join(chunks),
                "sha256:" + checksum.hexdigest() if output is not None else None,
                size,
            )
        except InvalidRelease:
            raise
        except (OSError, http.client.HTTPException, ValueError):
            raise TransportError(
                "HTTPS transport failed; reconcile uncertain mutations"
            ) from None
        finally:
            watchdog.cancel()
            if stream is not None:
                stream.close()
            if output is not None:
                output.close()
            connection.close()


def command(
    args: list[str], *, environment: dict[str, str], timeout: int = 1200
) -> bytes:
    """Run fixed argv with bounded captured output; never echo output or secrets."""
    with tempfile.TemporaryFile() as output:
        child = subprocess.Popen(
            args,
            stdout=output,
            stderr=subprocess.DEVNULL,
            env=environment,
            start_new_session=True,
        )
        try:
            started = time.monotonic()
            while child.poll() is None:
                require(
                    time.monotonic() - started <= timeout, "Image command timed out"
                )
                require(
                    os.fstat(output.fileno()).st_size <= MAX_JSON,
                    "Image command output exceeds limit",
                )
                time.sleep(0.05)
            require(
                child.returncode == 0,
                "Image command failed; reconcile uncertain mutation",
            )
            require(
                os.fstat(output.fileno()).st_size <= MAX_JSON,
                "Image command output exceeds limit",
            )
            output.seek(0)
            return output.read(MAX_JSON + 1)
        finally:
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait()


@dataclass(frozen=True)
class WorkflowContext:
    run_id: int
    attempt: int

    @classmethod
    def from_environment(cls, plan: PublicationPlan, environment: dict | None = None):
        env = os.environ if environment is None else environment
        expected = {
            "GITHUB_ACTIONS": "true",
            "RUNNER_ENVIRONMENT": "github-hosted",
            "GITHUB_REPOSITORY": plan.binding.repository,
            "GITHUB_JOB": "publish",
            "GITHUB_REF": "refs/tags/" + plan.binding.version,
            "GITHUB_SHA": plan.binding.commit,
            "GITHUB_EVENT_NAME": "push",
            "GITHUB_WORKFLOW_REF": plan.binding.repository
            + "/"
            + WORKFLOW
            + "@refs/tags/"
            + plan.binding.version,
            "GITHUB_WORKFLOW_SHA": plan.binding.commit,
        }
        require(
            all(env.get(key) == value for key, value in expected.items()),
            "Publication requires the exact trusted hosted tag workflow",
        )
        return cls(
            int(matches(env.get("GITHUB_RUN_ID"), NUMBER, "Invalid workflow run ID")),
            int(
                matches(
                    env.get("GITHUB_RUN_ATTEMPT"), NUMBER, "Invalid workflow attempt"
                )
            ),
        )


def private_directory(path: Path) -> None:
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    require(not path.is_symlink(), "State directory is a symlink")
    metadata = path.stat()
    require(
        metadata.st_uid == os.geteuid() and stat.S_IMODE(metadata.st_mode) & 0o077 == 0,
        "Publication state must be owned privately by this runner",
    )


def sync_directory(path: Path) -> None:
    descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


class Journal:
    """Exclusive durable transaction. Interrupted intents are never replayed."""

    def __init__(self, path: Path, plan: PublicationPlan, context: WorkflowContext):
        self.path = path
        self.binding = plan.binding.digest
        self.sequence = 0
        self.operations = set()
        descriptor = os.open(
            path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600
        )
        self.stream = os.fdopen(descriptor, "wb")
        self.append(
            "begin",
            "transaction",
            {
                "repository": plan.binding.repository,
                "version": plan.binding.version,
                "commit": plan.binding.commit,
                "run_id": context.run_id,
                "attempt": context.attempt,
            },
        )
        sync_directory(path.parent)

    def append(self, phase: str, operation: str, details: dict) -> None:
        self.sequence += 1
        self.stream.write(
            json_bytes(
                {
                    "sequence": self.sequence,
                    "binding": self.binding,
                    "phase": phase,
                    "operation": operation,
                    "details": details,
                }
            ).replace(b"\n", b" ")
            + b"\n"
        )
        self.stream.flush()
        os.fsync(self.stream.fileno())

    def mutate(self, operation: str, details: dict, action: Callable):
        require(
            operation not in self.operations,
            "Mutation was already attempted; inspect recovery instead",
        )
        self.operations.add(operation)
        self.append("intent", operation, details)
        try:
            value = action()
        except BaseException:
            self.append("uncertain", operation, details)
            raise
        receipt = value if isinstance(value, dict) else {}
        self.append("complete", operation, {**details, **receipt})
        return value

    def close(self):
        self.stream.close()


class GitHubReleaseTransport:
    def __init__(
        self,
        plan: PublicationPlan,
        state_dir: Path,
        context: WorkflowContext,
        *,
        github_token: str,
        inspection_token: str | None = None,
        actor: str,
        http: HTTPS | None = None,
        execute: Callable = command,
    ):
        validate_plan(plan)
        validate_manifest(plan.manifest, plan.binding.repository)
        require(
            isinstance(github_token, str)
            and bool(github_token)
            and "\n" not in github_token
            and "\r" not in github_token,
            "Missing or invalid workflow credential",
        )
        require(
            inspection_token is None
            or (
                isinstance(inspection_token, str)
                and bool(inspection_token)
                and "\n" not in inspection_token
                and "\r" not in inspection_token
            ),
            "Invalid inspection credential",
        )
        require(
            isinstance(actor, str)
            and re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9-]{0,38}(?:\[bot\])?", actor)
            is not None,
            "Invalid registry actor",
        )
        self.plan, self.context, self.state_dir = plan, context, state_dir
        self.token, self.inspection_token, self.actor = (
            github_token,
            inspection_token,
            actor,
        )
        self.http, self.execute = http or HTTPS(), execute
        self.base = "https://api.github.com/repos/" + plan.binding.repository
        self.held = False
        self.journal = None
        self.temporary = None
        self.release_id = None
        self.images_pushed = False
        self.assets_uploaded = False
        self.tags_created = False
        self.published = False
        self.public_verified = False
        self.registry_tokens = {}

    def github(
        self,
        method: str,
        path: str,
        *,
        body: dict | None = None,
        inspection: bool = False,
        expected: int = 200,
    ) -> object:
        token = (
            self.inspection_token
            if inspection and self.inspection_token
            else self.token
        )
        response = self.http.request(
            method,
            "https://api.github.com/" + path,
            headers={
                "Authorization": "Bearer " + token,
                "Accept": "application/vnd.github+json",
                "X-GitHub-Api-Version": API_VERSION,
                "Content-Type": "application/json",
                "User-Agent": "psst.zip-release",
            },
            body=json_bytes(body) if body is not None else None,
        )
        require(
            response.status == expected,
            f"GitHub API failed (HTTP {response.status}); state is not absence",
        )
        return read_json(response.body)

    def repo_api(self, suffix: str) -> str:
        return "repos/" + self.plan.binding.repository + "/" + suffix

    def list_all(self, suffix: str) -> list[dict]:
        values = []
        for page in range(1, 101):
            batch = self.github(
                "GET", self.repo_api(suffix) + f"?per_page=100&page={page}"
            )
            require(
                isinstance(batch, list)
                and all(isinstance(item, dict) for item in batch),
                "Malformed or incomplete API page",
            )
            values.extend(batch)
            if len(batch) < 100:
                return values
        raise TransportError("API pagination exceeded bound; absence is unknown")

    def tag_commit(self) -> str:
        value = self.github(
            "GET", self.repo_api("git/ref/tags/" + self.plan.binding.version)
        )
        require(
            isinstance(value, dict)
            and value.get("ref") == "refs/tags/" + self.plan.binding.version,
            "Remote tag identity differs",
        )
        item = value.get("object")
        for _ in range(5):
            require(isinstance(item, dict), "Malformed tag target")
            oid = matches(item.get("sha"), COMMIT, "Invalid remote tag target")
            if item.get("type") == "commit":
                return oid
            require(item.get("type") == "tag", "Remote tag is not a commit/tag")
            value = self.github("GET", self.repo_api("git/tags/" + oid))
            require(isinstance(value, dict), "Malformed annotated tag")
            item = value.get("object")
        raise TransportError("Annotated tag nesting exceeds bound")

    def immutable(self) -> bool:
        value = self.github("GET", self.repo_api("immutable-releases"), inspection=True)
        require(
            isinstance(value, dict) and value.get("enabled") is True,
            "Immutable release policy is not enabled",
        )
        return True

    def verify_workflow(self) -> None:
        value = self.github(
            "GET",
            self.repo_api(
                f"actions/runs/{self.context.run_id}/attempts/{self.context.attempt}"
            ),
        )
        require(
            isinstance(value, dict)
            and value.get("id") == self.context.run_id
            and value.get("run_attempt") == self.context.attempt
            and value.get("status") == "in_progress"
            and value.get("event") == "push"
            and value.get("head_sha") == self.plan.binding.commit
            and value.get("path") == WORKFLOW
            and value.get("repository", {}).get("full_name")
            == self.plan.binding.repository
            and value.get("head_repository", {}).get("full_name")
            == self.plan.binding.repository,
            "Current run is not the selected active trusted publication workflow",
        )
        value = self.github(
            "GET",
            self.repo_api("contents/" + WORKFLOW) + "?ref=" + self.plan.binding.commit,
        )
        require(
            isinstance(value, dict) and value.get("encoding") == "base64",
            "Workflow source could not be inspected",
        )
        try:
            require(
                isinstance(value.get("content"), str), "Workflow contents are missing"
            )
            contents = base64.b64decode(
                re.sub(r"\s", "", value["content"]), validate=True
            ).decode()
        except (KeyError, ValueError, UnicodeError) as error:
            raise TransportError("Malformed trusted workflow contents") from error
        require(len(contents.encode()) <= MAX_JSON, "Workflow source exceeds bound")
        # Require the literal reviewed repository-wide workflow-level group.
        # Unsupported YAML styles fail instead of guessing expression behavior.
        block = re.search(
            r"(?m)^concurrency:\s*\n((?:[ \t]+[^\n]*\n|\s*\n)+)", contents
        )
        require(
            block is not None
            and len(re.findall(r"(?m)^concurrency:", contents)) == 1
            and re.search(r"(?m)^  group: [\"']?" + GROUP + r"[\"']?\s*$", block[1])
            is not None
            and re.search(r"(?m)^  cancel-in-progress: false\s*$", block[1]) is not None
            and re.search(r"(?m)^  publish:\s*$", contents) is not None,
            "Trusted workflow lacks the required global serialization group/publish job",
        )

    @contextmanager
    def serialized(self, repository: str) -> Iterator[None]:
        require(
            repository == self.plan.binding.repository and not self.held,
            "Invalid or nested publication lease",
        )
        private_directory(self.state_dir)
        lock_path = self.state_dir / "publication.lock"
        descriptor = os.open(lock_path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        try:
            require(
                os.fstat(descriptor).st_uid == os.geteuid()
                and stat.S_IMODE(os.fstat(descriptor).st_mode) & 0o077 == 0,
                "Unsafe local publication lock",
            )
            try:
                fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError as error:
                raise TransportError(
                    "Another local publication holds the lease"
                ) from error
            self.verify_workflow()
            self.journal = Journal(
                self.state_dir / (self.plan.binding.version + ".jsonl"),
                self.plan,
                self.context,
            )
            self.temporary = tempfile.TemporaryDirectory(
                prefix="transport-", dir=self.state_dir
            )
            self.held = True
            try:
                yield
            except BaseException:
                self.journal.append(
                    "stopped",
                    "transaction",
                    {"ready": False, "automatic_resume": False},
                )
                raise
        finally:
            self.held = False
            if self.journal is not None:
                self.journal.close()
            if self.temporary is not None:
                self.temporary.cleanup()
            self.registry_tokens.clear()
            os.close(descriptor)

    def require_lease(self) -> None:
        validate_plan(self.plan)
        require(
            self.held and self.journal is not None and self.temporary is not None,
            "Publishing mutation requires the held trusted workflow lease",
        )

    def repository(self, component: str) -> str:
        require(component in {"backend", "web"}, "Unexpected image component")
        return (
            self.plan.manifest["images"][component]["index"]
            .split("@")[0]
            .removeprefix("ghcr.io/")
        )

    def registry(
        self,
        method: str,
        component: str,
        reference: str,
        *,
        anonymous: bool = False,
        body: bytes | None = None,
    ) -> Response:
        require(
            reference == self.plan.binding.version
            or DIGEST.fullmatch(reference) is not None,
            "Unsafe registry reference",
        )
        repository = self.repository(component)
        key = (repository, anonymous)
        cached = self.registry_tokens.get(key)
        if cached is None or cached[1] <= time.monotonic():
            credentials = (
                {}
                if anonymous
                else {
                    "Authorization": "Basic "
                    + base64.b64encode(
                        (self.actor + ":" + self.token).encode()
                    ).decode()
                }
            )
            scope = f"repository:{repository}:" + ("pull" if anonymous else "pull,push")
            response = self.http.request(
                "GET",
                "https://ghcr.io/token?"
                + urlencode({"service": "ghcr.io", "scope": scope}),
                headers=credentials,
            )
            require(
                response.status == 200,
                "Registry token request failed; absence is unknown",
            )
            token = read_json(response.body)
            require(
                isinstance(token, dict)
                and isinstance(token.get("token"), str)
                and bool(token["token"])
                and "\n" not in token["token"]
                and "\r" not in token["token"],
                "Registry did not grant a valid token",
            )
            expires = token.get("expires_in", 60)
            require(
                type(expires) is int and 1 <= expires <= 86400,
                "Invalid registry token lifetime",
            )
            self.registry_tokens[key] = (
                token["token"],
                time.monotonic() + max(0, expires - 30),
            )
        headers = {
            "Authorization": "Bearer " + self.registry_tokens[key][0],
            "Accept": "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json",
        }
        if body is not None:
            value = read_json(body)
            require(
                isinstance(value, dict) and isinstance(value.get("mediaType"), str),
                "Missing manifest media type",
            )
            headers["Content-Type"] = value["mediaType"]
        return self.http.request(
            method,
            f"https://ghcr.io/v2/{repository}/manifests/{quote(reference, safe=':')}",
            headers=headers,
            body=body,
        )

    def absent_or_digest(
        self, component: str, reference: str, *, anonymous: bool = False
    ) -> str | None:
        response = self.registry("GET", component, reference, anonymous=anonymous)
        if response.status == 404:
            value = read_json(response.body)
            require(
                isinstance(value, dict)
                and isinstance(value.get("errors"), list)
                and value["errors"]
                and all(
                    isinstance(item, dict)
                    and item.get("code") in {"MANIFEST_UNKNOWN", "NAME_UNKNOWN"}
                    for item in value["errors"]
                ),
                "Registry absence is not authenticated/structured",
            )
            return None
        require(
            response.status == 200,
            f"Registry inspection failed (HTTP {response.status}); absence is unknown",
        )
        digest = sha256(response.body)
        require(
            response.headers.get("docker-content-digest") == digest,
            "Registry manifest digest header differs",
        )
        return digest

    def snapshot(self, plan: PublicationPlan) -> dict:
        self.require_lease()
        require(plan.binding == self.plan.binding, "Snapshot belongs to another plan")
        return {
            "releases": [
                item
                for item in self.list_all("releases")
                if item.get("tag_name") == self.plan.binding.version
            ],
            "version_tags": {
                component: self.absent_or_digest(component, self.plan.binding.version)
                for component in ("backend", "web")
            },
            "tag_commit": self.tag_commit(),
            "immutable_releases": self.immutable(),
        }

    def create_draft(self, parameters: dict) -> dict:
        self.require_lease()
        expected = {
            "tag_name": self.plan.binding.version,
            "target_commitish": self.plan.binding.commit,
            "draft": True,
            "prerelease": False,
            "make_latest": "false",
        }
        require(parameters == expected, "Unsafe draft request")

        def action():
            require(
                self.tag_commit() == self.plan.binding.commit, "Remote source tag moved"
            )
            require(
                not any(
                    item.get("tag_name") == self.plan.binding.version
                    for item in self.list_all("releases")
                ),
                "Version draft appeared before reservation",
            )
            value = self.github(
                "POST", self.repo_api("releases"), body=parameters, expected=201
            )
            require(
                isinstance(value, dict)
                and type(value.get("id")) is int
                and value["id"] > 0
                and value.get("draft") is True
                and value.get("assets") == []
                and value.get("tag_name") == self.plan.binding.version,
                "Unexpected draft reservation response",
            )
            self.release_id = value["id"]
            return {"release_id": self.release_id}

        self.journal.mutate("reserve-draft", {"tag": self.plan.binding.version}, action)
        return self.github("GET", self.repo_api(f"releases/{self.release_id}"))

    def authfile(self) -> Path:
        self.require_lease()
        path = Path(self.temporary.name) / "registry-auth.json"
        if not path.exists():
            with path.open("x") as stream:
                os.chmod(path, 0o600)
                stream.write(
                    json_bytes(
                        {
                            "auths": {
                                "ghcr.io": {
                                    "auth": base64.b64encode(
                                        (self.actor + ":" + self.token).encode()
                                    ).decode()
                                }
                            }
                        }
                    ).decode()
                )
        return path

    def command_environment(self) -> dict:
        return {
            key: value
            for key, value in os.environ.items()
            if key
            in {"PATH", "LANG", "LC_ALL", "HOME", "SSL_CERT_FILE", "SSL_CERT_DIR"}
        }

    def push_images(
        self,
        staged_oci_archives: dict[str, Path],
        index_files: dict[str, Path],
        *,
        tested_configs: dict[str, str],
    ) -> None:
        self.require_lease()
        require(
            self.release_id is not None and not self.images_pushed,
            "Images require a fresh draft and have not already been pushed",
        )
        subjects = dict(self.plan.updater_subjects)
        keys = {
            component + "-" + arch
            for component in ("backend", "web")
            for arch in ("amd64", "arm64")
        }
        fields(staged_oci_archives, keys, "staged final images")
        fields(tested_configs, keys, "exact smoke-tested configuration identities")
        fields(index_files, {"backend", "web"}, "staged indexes")
        # Validate every staged input before the first registry write.
        staged = {}
        for key, archive in sorted(staged_oci_archives.items()):
            require(
                ":" not in str(archive)
                and archive.is_file()
                and not archive.is_symlink(),
                "Unsafe OCI archive input",
            )
            component, architecture = key.split("-")
            correspondence = inspect_archive(
                archive,
                platform="linux/" + architecture,
                repository=self.plan.binding.repository,
                version=self.plan.binding.version,
                commit=self.plan.binding.commit,
                tested_config=tested_configs[key],
                component=component,
            )
            archive_digest = correspondence["archive_digest"]
            raw = self.execute(
                ["skopeo", "inspect", "--raw", "oci-archive:" + str(archive.resolve())],
                environment=self.command_environment(),
                timeout=180,
            )
            expected = subjects[key].split("@")[1]
            require(
                sha256(raw) == expected
                and correspondence["manifest_digest"] == expected,
                "Staged OCI archive is not the reviewed final child",
            )
            staged[key] = (archive, archive_digest, expected)
        for component, path in index_files.items():
            validate_registry_index(
                read_bounded_file(path), self.plan.manifest["images"][component]
            )
        for key, (archive, archive_digest, expected) in staged.items():
            component = key.split("-")[0]

            def push(
                archive=archive,
                archive_digest=archive_digest,
                expected=expected,
                component=component,
            ):
                require(
                    source_digest(archive) == archive_digest,
                    "Staged image changed before push",
                )
                require(
                    self.absent_or_digest(component, expected) is None,
                    "Existing registry child is not adopted automatically",
                )
                self.execute(
                    [
                        "skopeo",
                        "copy",
                        "--preserve-digests",
                        "--dest-authfile",
                        str(self.authfile()),
                        "--retry-times",
                        "0",
                        "oci-archive:" + str(archive.resolve()),
                        "docker://ghcr.io/"
                        + self.repository(component)
                        + "@"
                        + expected,
                    ],
                    environment=self.command_environment(),
                )
                require(
                    self.absent_or_digest(component, expected) == expected,
                    "Pushed child readback differs",
                )
                return {"digest": expected}

            self.journal.mutate(
                "push-" + key,
                {
                    "archive_digest": archive_digest,
                    "expected": expected,
                    "tested_config": tested_configs[key],
                },
                push,
            )
        for component, path in sorted(index_files.items()):
            raw = read_bounded_file(path)
            validate_registry_index(raw, self.plan.manifest["images"][component])
            expected = self.plan.manifest["images"][component]["index"].split("@")[1]

            def push_index(component=component, raw=raw, expected=expected):
                require(
                    self.absent_or_digest(component, expected) is None,
                    "Existing registry index is not adopted automatically",
                )
                response = self.registry("PUT", component, expected, body=raw)
                require(response.status == 201, "Registry index push failed")
                require(
                    self.absent_or_digest(component, expected) == expected,
                    "Pushed index readback differs",
                )
                return {"digest": expected}

            self.journal.mutate(
                "push-" + component + "-index", {"expected": expected}, push_index
            )
        self.images_pushed = True

    def inspect_pair(self, *, anonymous: bool = False) -> dict:
        records = {}
        for component, image in self.plan.manifest["images"].items():
            expected = image["index"].split("@")[1]
            response = self.registry("GET", component, expected, anonymous=anonymous)
            require(
                response.status == 200
                and response.headers.get("docker-content-digest") == expected,
                "Registry pair is unavailable or changed",
            )
            validate_registry_index(response.body, image)
            records[component + "-index"] = expected
            for platform, digest in image["platform_digests"].items():
                response = self.registry("GET", component, digest, anonymous=anonymous)
                require(
                    response.status == 200
                    and sha256(response.body) == digest
                    and response.headers.get("docker-content-digest") == digest,
                    "Registry child readback differs",
                )
                records[component + "-" + platform.split("/")[1]] = digest
        return {
            "binding": self.plan.binding.digest,
            "anonymous": anonymous,
            "subjects": records,
        }

    def package_visibility(self) -> dict:
        owner = self.plan.binding.repository.split("/")[0]
        owner_record = self.github("GET", "users/" + owner)
        require(
            isinstance(owner_record, dict)
            and owner_record.get("type") in {"User", "Organization"},
            "Unknown package owner",
        )
        prefix = "orgs" if owner_record["type"] == "Organization" else "users"
        records = {}
        for component in ("backend", "web"):
            value = self.github(
                "GET", f"{prefix}/{owner}/packages/container/psst-zip-{component}"
            )
            require(
                isinstance(value, dict)
                and value.get("visibility") == "public"
                and value.get("package_type") == "container"
                and value.get("name") == "psst-zip-" + component
                and value.get("repository", {}).get("full_name")
                == self.plan.binding.repository,
                "Package is not public and linked to the selected repository",
            )
            records[component] = {
                "visibility": value["visibility"],
                "repository": value["repository"]["full_name"],
            }
        return records

    def upload_assets(self, reservation: dict, files: dict[str, Path]) -> None:
        self.require_lease()
        require(
            self.images_pushed
            and not self.assets_uploaded
            and reservation.get("release_id") == self.release_id
            and reservation.get("binding") == self.plan.binding.digest,
            "Asset upload requires this complete image reservation",
        )
        fields(files, set(dict(self.plan.assets)), "release upload files")
        for name, path in files.items():
            require(
                path.name == name
                and source_digest(path) == dict(self.plan.assets)[name],
                "Upload asset differs from reviewed bytes",
            )
        bundle_name = self.plan.manifest["bundle"]["name"]
        ordered = sorted(
            name for name in files if name not in {bundle_name, "release-manifest.json"}
        ) + [bundle_name, "release-manifest.json"]
        for name in ordered:
            path = files[name]

            def upload(name=name, path=path):
                require(
                    not any(
                        asset.get("name") == name
                        for asset in self.list_all(f"releases/{self.release_id}/assets")
                    ),
                    "Existing upload is never replaced/adopted",
                )
                require(
                    source_digest(path) == dict(self.plan.assets)[name],
                    "Upload changed before mutation",
                )
                response = self.http.request(
                    "POST",
                    "https://uploads.github.com/repos/"
                    + self.plan.binding.repository
                    + f"/releases/{self.release_id}/assets?"
                    + urlencode({"name": name}),
                    headers={
                        "Authorization": "Bearer " + self.token,
                        "Content-Type": "application/octet-stream",
                        "Accept": "application/vnd.github+json",
                        "X-GitHub-Api-Version": API_VERSION,
                        "User-Agent": "psst.zip-release",
                    },
                    body=path,
                )
                require(
                    response.status == 201,
                    "Asset upload failed; partial starter assets require reconciliation",
                )
                asset = read_json(response.body)
                require(
                    isinstance(asset, dict)
                    and type(asset.get("id")) is int
                    and asset["id"] > 0
                    and asset.get("name") == name
                    and asset.get("state") == "uploaded"
                    and asset.get("digest") == dict(self.plan.assets)[name]
                    and asset.get("size") == path.stat().st_size,
                    "Uploaded asset response differs",
                )
                return {
                    "asset_id": asset["id"],
                    "digest": asset["digest"],
                    "size": asset["size"],
                }

            self.journal.mutate(
                "upload-" + name,
                {"name": name, "digest": dict(self.plan.assets)[name]},
                upload,
            )
        self.assets_uploaded = True

    def download_asset(
        self, asset: dict, destination: Path, *, anonymous: bool = False
    ) -> dict:
        require(
            type(asset.get("id")) is int
            and asset["id"] > 0
            and type(asset.get("size")) is int
            and 0 < asset["size"] <= MAX_ASSET,
            "Invalid asset download descriptor",
        )
        headers = {
            "Accept": "application/octet-stream",
            "User-Agent": "psst.zip-release",
        }
        if not anonymous:
            headers["Authorization"] = "Bearer " + self.token
        url = self.base + f"/releases/assets/{asset['id']}"
        for _ in range(4):
            response = self.http.request(
                "GET",
                url,
                headers=headers,
                limit=asset["size"],
                destination=destination,
            )
            if response.status in {301, 302, 303, 307, 308}:
                url = response.headers.get("location", "")
                parsed = urlsplit(url)
                require(
                    parsed.scheme == "https"
                    and parsed.hostname
                    in {
                        "release-assets.githubusercontent.com",
                        "objects.githubusercontent.com",
                    }
                    and parsed.port in {None, 443}
                    and parsed.username is None
                    and not parsed.fragment,
                    "Untrusted asset redirect",
                )
                headers.pop("Authorization", None)
                continue
            require(
                response.status == 200
                and response.size == asset["size"]
                and response.digest == asset.get("digest"),
                "Downloaded asset checksum/size differs",
            )
            return {
                "name": asset["name"],
                "digest": response.digest,
                "size": response.size,
            }
        raise TransportError("Asset redirect limit exceeded")

    def inspect_assets(self, reservation: dict, *, anonymous: bool = False) -> dict:
        require(
            reservation.get("release_id") == self.release_id, "Wrong reserved release"
        )
        assets = self.list_all(f"releases/{self.release_id}/assets")
        require(
            len(assets) == len(self.plan.assets), "Incomplete or extra release assets"
        )
        expected = dict(self.plan.assets)
        seen = set()
        records = []
        with tempfile.TemporaryDirectory(
            prefix="readback-", dir=self.state_dir
        ) as folder:
            for asset in assets:
                name = asset.get("name")
                require(
                    isinstance(name, str)
                    and name in expected
                    and name not in seen
                    and asset.get("state") == "uploaded"
                    and asset.get("digest") == expected[name],
                    "Release asset map differs",
                )
                seen.add(name)
                records.append(
                    self.download_asset(asset, Path(folder) / name, anonymous=anonymous)
                )
        return {
            "binding": self.plan.binding.digest,
            "anonymous": anonymous,
            "assets": records,
        }

    def create_version_tags(self, index_files: dict[str, Path]) -> None:
        self.require_lease()
        require(
            self.images_pushed and self.assets_uploaded and not self.tags_created,
            "Version tags require complete draft assets/images",
        )
        self.package_visibility()
        self.inspect_pair(anonymous=True)
        fields(index_files, {"backend", "web"}, "version index files")
        for component, path in sorted(index_files.items()):
            raw = read_bounded_file(path)
            validate_registry_index(raw, self.plan.manifest["images"][component])
            expected = sha256(raw)

            def tag(component=component, raw=raw, expected=expected):
                require(
                    self.tag_commit() == self.plan.binding.commit, "Source tag moved"
                )
                require(
                    self.absent_or_digest(component, self.plan.binding.version) is None,
                    "Version tag already exists; never overwrite",
                )
                response = self.registry(
                    "PUT", component, self.plan.binding.version, body=raw
                )
                require(
                    response.status == 201
                    and self.absent_or_digest(component, self.plan.binding.version)
                    == expected,
                    "Version tag creation/readback failed",
                )
                return {"digest": expected}

            self.journal.mutate(
                "tag-" + component,
                {"version": self.plan.binding.version, "expected": expected},
                tag,
            )
        self.tags_created = True

    def ready_snapshot(self, reservation: dict) -> dict:
        self.require_lease()
        require(
            reservation.get("release_id") == self.release_id and self.tags_created,
            "Wrong or incomplete readiness reservation",
        )
        value = self.github("GET", self.repo_api(f"releases/{self.release_id}"))
        require(isinstance(value, dict), "Malformed reserved release")
        value["assets"] = self.list_all(f"releases/{self.release_id}/assets")
        return {
            "release": value,
            "version_tags": {
                component: self.absent_or_digest(component, self.plan.binding.version)
                for component in ("backend", "web")
            },
            "tag_commit": self.tag_commit(),
            "immutable_releases": self.immutable(),
        }

    def publish(
        self, reservation: dict, reports: dict[str, Path], verifier: EvidenceVerifier
    ) -> dict:
        self.require_lease()
        require(
            self.tags_created and not self.published,
            "Publication requires complete version pair",
        )
        request = ready_release_request(
            self.plan, reservation, self.ready_snapshot(reservation), reports, verifier
        )
        expected = {
            "release_id": self.release_id,
            "draft": False,
            "prerelease": False,
            "make_latest": "false",
        }
        require(request == expected, "Unsafe ready release request")

        def publish():
            require(
                self.tag_commit() == self.plan.binding.commit and self.immutable(),
                "Remote identity/policy changed before publication",
            )
            value = self.github(
                "PATCH",
                self.repo_api(f"releases/{self.release_id}"),
                body={
                    key: value for key, value in request.items() if key != "release_id"
                },
            )
            require(
                isinstance(value, dict)
                and value.get("id") == self.release_id
                and value.get("draft") is False
                and value.get("prerelease") is False
                and value.get("immutable") is True
                and value.get("tag_name") == self.plan.binding.version,
                "Published release did not become immutable and complete",
            )
            return {"release_id": self.release_id, "immutable": True}

        value = self.journal.mutate(
            "publish-release", {"release_id": self.release_id}, publish
        )
        self.published = True
        return value

    def verify_public(self, reservation: dict) -> dict:
        self.require_lease()
        require(
            self.published and not self.public_verified,
            "Public readback requires the completed published release",
        )
        self.package_visibility()
        response = self.http.request(
            "GET",
            self.base + f"/releases/{self.release_id}",
            headers={
                "Accept": "application/vnd.github+json",
                "X-GitHub-Api-Version": API_VERSION,
                "User-Agent": "psst.zip-release",
            },
        )
        require(response.status == 200, "Published release is not anonymously visible")
        release = read_json(response.body)
        require(
            isinstance(release, dict)
            and release.get("id") == self.release_id
            and release.get("draft") is False
            and release.get("immutable") is True
            and release.get("tag_name") == self.plan.binding.version,
            "Public release identity differs",
        )
        for component, record in self.plan.manifest["images"].items():
            require(
                self.absent_or_digest(
                    component, self.plan.binding.version, anonymous=True
                )
                == record["index"].split("@")[1],
                "Anonymous version tag differs",
            )
        result = {
            "registry": self.inspect_pair(anonymous=True),
            "pull": self.anonymous_pull(),
            "assets": self.inspect_assets(reservation, anonymous=True),
        }
        self.journal.append(
            "complete",
            "public-readback",
            {"release_id": self.release_id, "binding": self.plan.binding.digest},
        )
        self.public_verified = True
        return result

    def anonymous_pull(self) -> dict:
        self.require_lease()
        self.package_visibility()
        observation = self.inspect_pair(anonymous=True)
        verified = []
        with tempfile.TemporaryDirectory(
            prefix="anonymous-pull-", dir=self.state_dir
        ) as folder:
            empty_auth = Path(folder) / "auth.json"
            empty_auth.write_bytes(json_bytes({"auths": {}}))
            for component, record in self.plan.manifest["images"].items():
                for platform, digest in record["platform_digests"].items():
                    target = Path(folder) / (component + "-" + platform.split("/")[1])
                    self.execute(
                        [
                            "skopeo",
                            "copy",
                            "--preserve-digests",
                            "--src-no-creds",
                            "--authfile",
                            str(empty_auth),
                            "--retry-times",
                            "0",
                            "docker://ghcr.io/"
                            + self.repository(component)
                            + "@"
                            + digest,
                            "oci:" + str(target),
                        ],
                        environment=self.command_environment(),
                    )
                    raw = self.execute(
                        ["skopeo", "inspect", "--raw", "oci:" + str(target)],
                        environment=self.command_environment(),
                        timeout=180,
                    )
                    require(sha256(raw) == digest, "Anonymous image content differs")
                    verified.append(component + "-" + platform.split("/")[1])
        return {
            **observation,
            "complete_child_pulls": sorted(verified),
            "credentials_used": False,
        }

    def reconcile(self) -> dict:
        """Read-only remote incident inspection; never restores a mutation right."""
        journal_path = self.state_dir / (self.plan.binding.version + ".jsonl")
        local = {"present": False, "records": [], "unresolved_intents": []}
        try:
            descriptor = os.open(journal_path, os.O_RDONLY | os.O_NOFOLLOW)
        except FileNotFoundError:
            descriptor = None
        if descriptor is not None:
            with os.fdopen(descriptor, "rb") as stream:
                metadata = os.fstat(stream.fileno())
                require(
                    stat.S_ISREG(metadata.st_mode)
                    and metadata.st_uid == os.getuid()
                    and stat.S_IMODE(metadata.st_mode) & 0o077 == 0,
                    "Unsafe recovery journal ownership or permissions",
                )
                raw = stream.read(MAX_JSON + 1)
            require(
                0 < len(raw) <= MAX_JSON and raw.endswith(b"\n"),
                "Incomplete or oversized recovery journal; preserve for incident review",
            )
            records = []
            pending = set()
            attempted = set()
            for sequence, line in enumerate(raw.splitlines(), 1):
                record = read_json(line)
                fields(
                    record,
                    {"sequence", "binding", "phase", "operation", "details"},
                    "durable mutation receipt",
                )
                require(
                    type(record["sequence"]) is int
                    and record["sequence"] == sequence
                    and record["binding"] == self.plan.binding.digest
                    and isinstance(record["details"], dict)
                    and isinstance(record["operation"], str)
                    and record["phase"]
                    in {"begin", "intent", "complete", "uncertain", "stopped"},
                    "Recovery journal differs from reviewed release or is malformed",
                )
                phase, operation = record["phase"], record["operation"]
                if sequence == 1:
                    require(
                        phase == "begin" and operation == "transaction",
                        "Missing journal transaction identity",
                    )
                else:
                    require(phase != "begin", "Repeated journal transaction identity")
                if phase == "intent":
                    require(operation not in attempted, "Repeated journal mutation")
                    attempted.add(operation)
                    pending.add(operation)
                elif (
                    phase in {"complete", "uncertain"}
                    and operation != "public-readback"
                ):
                    require(
                        operation in pending,
                        "Receipt has no matching unresolved intent",
                    )
                    if phase == "complete":
                        pending.remove(operation)
                records.append(record)
            local = {
                "present": True,
                "records": records,
                "unresolved_intents": sorted(pending),
            }
        releases = [
            item
            for item in self.list_all("releases")
            if item.get("tag_name") == self.plan.binding.version
        ]
        for value in releases:
            require(
                type(value.get("id")) is int and value["id"] > 0,
                "Unknown partial release identity",
            )
            value["assets"] = self.list_all(f"releases/{value['id']}/assets")
        observations = {}
        for component, record in self.plan.manifest["images"].items():
            references = [
                record["index"].split("@")[1],
                *record["platform_digests"].values(),
                self.plan.binding.version,
            ]
            observations[component] = {
                reference: self.absent_or_digest(component, reference)
                for reference in references
            }
        return {
            "binding": self.plan.binding.digest,
            "local_journal": local,
            "releases": releases,
            "registry": observations,
            "tag_commit": self.tag_commit(),
            "automatic_resume_allowed": False,
            "automatic_cleanup_allowed": False,
            "ready_advertisement_allowed": False,
        }
