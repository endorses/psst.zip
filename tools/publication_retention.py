#!/usr/bin/env python3
"""Private, write-once publication recovery records through AWS CLI v2.

The endpoint must implement conditional PutObject, full-object SHA256 checksums,
SSE-S3, HeadObject checksum reads, GetPublicAccessBlock and GetBucketPolicyStatus.
Unsupported or denied privacy APIs fail closed. This adapter does not configure a
bucket, promise retention against administrator deletion, or resume transactions.
Credentials and CLI configuration live only in a separate private temporary
folder. Original payload hashes come from the driver's immutable snapshots.
"""

from __future__ import annotations

import base64
import hashlib
import os
from pathlib import Path, PurePosixPath
import re
import stat
import tempfile
from urllib.parse import urlsplit

from github_release_transport import WorkflowContext, command
from publish_container_release import Binding, GATES, WORKFLOW
from release_artifacts import (
    COMMIT,
    DIGEST,
    VERSION,
    InvalidRelease,
    fields,
    json_bytes,
    matches,
    read_json,
    read_bounded_file,
    repository_name,
    require,
)

MAX_INPUT = 4 * 1024**3
MAX_RECORD = 16 * 1024**2
PREFIX = "PSST_PUBLICATION_S3_"
BLOCK_FIELDS = {
    "BlockPublicAcls",
    "IgnorePublicAcls",
    "BlockPublicPolicy",
    "RestrictPublicBuckets",
}


def validate_configuration(environment) -> dict[str, str]:
    """Validate explicit configuration without copying inputs or running a tool."""
    config = {}
    for name in ("ENDPOINT", "BUCKET", "REGION", "ACCESS_KEY_ID", "SECRET_ACCESS_KEY"):
        value = environment.get(PREFIX + name)
        require(
            isinstance(value, str)
            and bool(value)
            and len(value) <= 2048
            and all(33 <= ord(char) <= 126 for char in value),
            "Missing or unsafe publication retention configuration",
        )
        config[name] = value
    token = environment.get(PREFIX + "SESSION_TOKEN")
    if token is not None:
        require(
            isinstance(token, str)
            and 0 < len(token) <= 2048
            and all(33 <= ord(char) <= 126 for char in token),
            "Invalid publication retention session token",
        )
        config["SESSION_TOKEN"] = token
    try:
        endpoint = urlsplit(config["ENDPOINT"])
        safe_endpoint = (
            endpoint.scheme == "https"
            and endpoint.hostname
            and endpoint.port in {None, 443}
            and endpoint.username is None
            and endpoint.password is None
            and endpoint.path in {"", "/"}
            and not endpoint.query
            and not endpoint.fragment
        )
    except ValueError:
        safe_endpoint = False
    require(safe_endpoint, "Retention endpoint must be a fixed HTTPS origin")
    require(
        re.fullmatch(r"[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]", config["BUCKET"])
        and ".." not in config["BUCKET"],
        "Invalid retention bucket",
    )
    require(
        re.fullmatch(r"[a-z0-9][a-z0-9-]{0,62}", config["REGION"]),
        "Invalid retention region",
    )
    require(
        all(
            "[" not in value and "]" not in value
            for name, value in config.items()
            if name in {"ACCESS_KEY_ID", "SECRET_ACCESS_KEY", "SESSION_TOKEN"}
        ),
        "Invalid retention credential encoding",
    )
    return config


class S3PublicationRetention:
    """One exact workflow attempt; failures never trigger retries or overwrites."""

    def __init__(
        self,
        binding: Binding,
        context: WorkflowContext,
        *,
        environment,
        execute=command,
    ):
        repository_name(binding.repository)
        matches(binding.version, VERSION, "Invalid retention version")
        matches(binding.commit, COMMIT, "Invalid retention commit")
        require(
            type(context.run_id) is int
            and context.run_id > 0
            and type(context.attempt) is int
            and context.attempt > 0,
            "Invalid retention run identity",
        )
        self.binding, self.context, self.execute = binding, context, execute
        self.config = validate_configuration(environment)
        self.prefix = (
            f"publication/{binding.repository}/{binding.version}/"
            f"run-{context.run_id}/attempt-{context.attempt}/"
        )
        self.temporary = None
        self.entered = False
        self.sequence = 0
        self.previous_journal = b""
        self.inputs_persisted = False
        self.inputs_attempted = False
        self.finished = False
        self.used_keys = set()

    def __enter__(self):
        require(not self.entered, "Retention context was already entered")
        self.entered = True
        self.temporary = tempfile.TemporaryDirectory(
            prefix="psst-publication-s3-", dir="/tmp"
        )
        try:
            private = Path(self.temporary.name)
            self.env = {
                "PATH": "/usr/local/bin:/usr/bin:/bin",
                "LANG": "C.UTF-8",
                "AWS_CONFIG_FILE": str(private / "config"),
                "AWS_SHARED_CREDENTIALS_FILE": str(private / "credentials"),
                "AWS_EC2_METADATA_DISABLED": "true",
                "AWS_MAX_ATTEMPTS": "1",
                "AWS_RETRY_MODE": "standard",
                "AWS_PAGER": "",
                "AWS_CLI_AUTO_PROMPT": "off",
            }
            credentials = (
                "[default]\naws_access_key_id = "
                + self.config["ACCESS_KEY_ID"]
                + "\naws_secret_access_key = "
                + self.config["SECRET_ACCESS_KEY"]
                + "\n"
            )
            if "SESSION_TOKEN" in self.config:
                credentials += (
                    "aws_session_token = " + self.config["SESSION_TOKEN"] + "\n"
                )
            self._private_file(private / "credentials", credentials.encode())
            self._private_file(
                private / "config",
                (
                    "[default]\nregion = "
                    + self.config["REGION"]
                    + "\nretry_mode = standard\nmax_attempts = 1\ns3 =\n    addressing_style = path\n"
                ).encode(),
            )
            version = self._execute(["aws", "--version"], timeout=30)
            require(
                re.match(rb"aws-cli/2\.[0-9]+\.[0-9]+(?:\s|$)", version) is not None,
                "Publication retention requires installed AWS CLI v2",
            )
            self._privacy()
            return self
        except BaseException:
            self.__exit__(None, None, None)
            raise

    def __exit__(self, *_):
        if self.temporary is not None:
            self.temporary.cleanup()
            self.temporary = None
        self.config.clear()
        self.env = {}

    @staticmethod
    def _private_file(path, content):
        descriptor = os.open(
            path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600
        )
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())

    def _execute(self, args, *, timeout=1200):
        require(self.temporary is not None, "Retention context is not active")
        try:
            result = self.execute(args, environment=self.env, timeout=timeout)
            require(
                isinstance(result, bytes) and len(result) <= MAX_RECORD,
                "Invalid retention command response",
            )
            return result
        except Exception:
            # Command diagnostics can contain signed URLs or credential material.
            raise InvalidRelease(
                "Publication retention operation failed; reconcile retained state"
            ) from None

    def _api(self, operation, *arguments, timeout=1200):
        raw = self._execute(
            [
                "aws",
                "s3api",
                operation,
                "--bucket",
                self.config["BUCKET"],
                "--endpoint-url",
                self.config["ENDPOINT"],
                "--region",
                self.config["REGION"],
                "--output",
                "json",
                "--no-cli-pager",
                "--no-cli-auto-prompt",
                "--cli-connect-timeout",
                "30",
                "--cli-read-timeout",
                "60",
                *arguments,
            ],
            timeout=timeout,
        )
        try:
            value = read_json(raw)
            require(isinstance(value, dict), "Retention response must be an object")
            return value
        except Exception:
            raise InvalidRelease("Malformed publication retention response") from None

    def _privacy(self):
        block = self._api("get-public-access-block", timeout=60).get(
            "PublicAccessBlockConfiguration"
        )
        require(
            isinstance(block, dict)
            and all(block.get(name) is True for name in BLOCK_FIELDS),
            "Retention bucket must block all public policies and ACLs",
        )
        policy = self._api("get-bucket-policy-status", timeout=60).get("PolicyStatus")
        require(
            isinstance(policy, dict) and policy.get("IsPublic") is False,
            "Retention bucket must have an authenticated nonpublic policy",
        )

    @staticmethod
    def _regular(root, name, maximum):
        require(
            isinstance(name, str)
            and 0 < len(name) <= 512
            and str(PurePosixPath(name)) == name
            and not name.startswith("/")
            and all(part not in {"", ".", ".."} for part in PurePosixPath(name).parts)
            and re.fullmatch(r"[A-Za-z0-9_.@/-]+", name),
            "Unsafe retention input path",
        )
        require(
            root.is_absolute() and root.is_dir() and not root.is_symlink(),
            "Retention inputs require an absolute regular directory",
        )
        root_stat = root.stat()
        require(
            root_stat.st_uid == os.geteuid()
            and stat.S_IMODE(root_stat.st_mode) & 0o077 == 0,
            "Retention input directory must be private",
        )
        require(
            all(not ancestor.is_symlink() for ancestor in root.parents),
            "Retention input directory has a symlink ancestor",
        )
        path = root
        for part in PurePosixPath(name).parts:
            path = path / part
            require(not path.is_symlink(), "Retention input has a symlink")
        before = path.stat()
        require(
            stat.S_ISREG(before.st_mode)
            and before.st_uid == os.geteuid()
            and stat.S_IMODE(before.st_mode) & 0o077 == 0
            and 0 < before.st_size <= maximum,
            "Retention input must be a bounded private regular file",
        )
        return path, before

    @staticmethod
    def _unchanged(path, before):
        after = path.stat()
        require(
            not path.is_symlink()
            and (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns)
            == (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns),
            "Retention input changed during upload",
        )

    def _put(self, relative, path, digest, size):
        require(not self.finished, "Retention transaction already finished")
        key = self.prefix + relative
        require(key not in self.used_keys, "Retention object was already attempted")
        self.used_keys.add(key)
        checksum = base64.b64encode(
            bytes.fromhex(matches(digest, DIGEST, "Invalid retention checksum")[7:])
        ).decode()
        _, before = self._regular(path.parent, path.name, MAX_INPUT)
        require(
            before.st_size == size, "Retention size differs from snapshot inventory"
        )
        self._api(
            "put-object",
            "--key",
            key,
            "--body",
            str(path),
            "--if-none-match",
            "*",
            "--checksum-sha256",
            checksum,
            "--server-side-encryption",
            "AES256",
        )
        head = self._api(
            "head-object", "--key", key, "--checksum-mode", "ENABLED", timeout=60
        )
        require(
            type(head.get("ContentLength")) is int
            and head["ContentLength"] == size
            and head.get("ChecksumSHA256") == checksum
            and head.get("ServerSideEncryption") == "AES256",
            "Retained object checksum, size or encryption readback differs",
        )
        self._unchanged(path, before)

    def _record(self, relative, value):
        content = json_bytes(value)
        require(0 < len(content) <= MAX_RECORD, "Retention record exceeds bounds")
        path = Path(self.temporary.name) / (
            "record-" + str(len(self.used_keys)) + ".json"
        )
        self._private_file(path, content)
        self._put(
            relative,
            path,
            "sha256:" + hashlib.sha256(content).hexdigest(),
            len(content),
        )
        path.unlink()

    def persist_inputs(self, snapshot_root: Path, inventory: dict):
        require(
            not self.inputs_attempted and self.sequence == 0,
            "Retention inputs were already attempted",
        )
        require(
            isinstance(inventory, dict)
            and 1 <= len(inventory) <= 256
            and "snapshot-binding.json" in inventory,
            "Retention snapshot inventory is incomplete",
        )
        checked = {}
        for name, metadata in inventory.items():
            path, before = self._regular(snapshot_root, name, MAX_INPUT)
            require(
                name == "snapshot-binding.json"
                or PurePosixPath(name).parts[0]
                in {"assets", "indexes", "archives", "gates"},
                "Unexpected retained snapshot category",
            )
            metadata = fields(metadata, {"sha256", "size"}, "retention inventory entry")
            matches(metadata["sha256"], DIGEST, "Invalid snapshot checksum")
            require(
                type(metadata["size"]) is int and 0 < metadata["size"] <= MAX_INPUT,
                "Invalid snapshot size",
            )
            require(
                before.st_size == metadata["size"],
                "Snapshot size differs from inventory",
            )
            checked[name] = (path, metadata)
        actual = set()
        for path in snapshot_root.rglob("*"):
            require(not path.is_symlink(), "Snapshot tree contains a symlink")
            require(
                path.is_file() or path.is_dir(), "Snapshot tree contains a special file"
            )
            if path.is_file():
                actual.add(path.relative_to(snapshot_root).as_posix())
        require(actual == set(inventory), "Snapshot inventory omits files")
        binding_path = checked["snapshot-binding.json"][0]
        require(
            binding_path.stat().st_size <= MAX_RECORD, "Snapshot binding exceeds bounds"
        )
        content = read_bounded_file(binding_path)
        require(
            "sha256:" + hashlib.sha256(content).hexdigest()
            == inventory["snapshot-binding.json"]["sha256"],
            "Snapshot binding checksum differs",
        )
        record = read_json(content)
        require(
            isinstance(record, dict)
            and record.get("schema_version") == 1
            and record.get("kind") == "publication-preparation"
            and record.get("binding") == self.binding.digest
            and record.get("repository") == self.binding.repository
            and record.get("version") == self.binding.version
            and record.get("source_commit") == self.binding.commit
            and record.get("signer_workflow") == WORKFLOW
            and record.get("publication_authorized") is False,
            "Snapshot binding differs from exact publication",
        )
        evidence = fields(record.get("evidence"), GATES, "retained gate evidence")
        for digest in evidence.values():
            matches(digest, DIGEST, "Invalid retained evidence checksum")
        require(
            isinstance(record.get("updater_subjects"), dict)
            and isinstance(record.get("source_assets"), dict),
            "Retained subject inventories must be objects",
        )
        subjects = dict(record["updater_subjects"])
        for name, digest in record["source_assets"].items():
            require(isinstance(name, str), "Invalid retained source name")
            matches(digest, DIGEST, "Invalid retained source digest")
            subjects["source:" + name] = "file:" + name + "@" + digest
        require(
            subjects == dict(self.binding.subjects),
            "Retained subjects differ from publication",
        )
        # Set before uploads so failures cannot silently retry this packet.
        self.inputs_attempted = True
        for name in [
            "snapshot-binding.json",
            *sorted(set(inventory) - {"snapshot-binding.json"}),
        ]:
            path, metadata = checked[name]
            self._put("inputs/" + name, path, metadata["sha256"], metadata["size"])
        self._record(
            "snapshot-inventory.json",
            {
                "schema_version": 1,
                "kind": "publication-retention-inventory",
                "binding": self.binding.digest,
                "run_id": self.context.run_id,
                "run_attempt": self.context.attempt,
                "files": inventory,
            },
        )
        self._privacy()
        self.inputs_persisted = True

    def checkpoint(self, journal_path: Path, sequence: int):
        require(
            self.inputs_persisted
            and type(sequence) is int
            and sequence == self.sequence + 1,
            "Publication checkpoint sequence differs",
        )
        path, _ = self._regular(journal_path.parent, journal_path.name, MAX_RECORD)
        raw = read_bounded_file(path)
        require(
            raw.endswith(b"\n") and raw.startswith(self.previous_journal),
            "Publication journal is truncated or changed",
        )
        lines = raw.splitlines()
        records = [read_json(line) for line in lines]
        require(
            len(records) == sequence
            and all(
                isinstance(record, dict)
                and record.get("binding") == self.binding.digest
                and record.get("sequence") == index
                for index, record in enumerate(records, 1)
            ),
            "Publication journal binding or sequence differs",
        )
        # Check the provider posture before a mutation intent can be acknowledged.
        if sequence == 1 or records[-1].get("phase") == "intent":
            self._privacy()
        snapshot = Path(self.temporary.name) / f"checkpoint-{sequence:08d}.jsonl"
        self._private_file(snapshot, raw)
        self._put(
            f"journal/{sequence:08d}.jsonl",
            snapshot,
            "sha256:" + hashlib.sha256(raw).hexdigest(),
            len(raw),
        )
        self.sequence = sequence
        self.previous_journal = raw
        snapshot.unlink()

    def finish(self, receipt: dict):
        require(
            self.inputs_persisted
            and self.sequence > 0
            and isinstance(receipt, dict)
            and receipt.get("schema_version") == 1
            and receipt.get("kind") == "container-publication-receipt"
            and receipt.get("immutable") is True
            and receipt.get("binding_digest") == self.binding.digest
            and receipt.get("repository") == self.binding.repository
            and receipt.get("version") == self.binding.version
            and receipt.get("commit") == self.binding.commit
            and receipt.get("journal_sha256")
            == "sha256:" + hashlib.sha256(self.previous_journal).hexdigest(),
            "Publication receipt differs from retained transaction",
        )
        self._privacy()
        content = json_bytes(receipt)
        require(
            all(
                value.encode() not in content
                for name, value in self.config.items()
                if name in {"ACCESS_KEY_ID", "SECRET_ACCESS_KEY", "SESSION_TOKEN"}
            ),
            "Publication receipt contains credential material",
        )
        self._record("publication-receipt.json", receipt)
        self.finished = True
