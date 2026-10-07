#!/usr/bin/env python3
"""Root-owned psst.zip release updater; install with its trusted artifact parser.

The restricted SSH interface accepts only `update vMAJOR.MINOR.PATCH` and `status`.
A candidate remains loopback-only until a protected verification hook or a local
administrator verifies the authenticated flows. Normal server startup is an
irreversible migration boundary, durably recorded before Docker is called.
"""

from __future__ import annotations

import argparse
from contextlib import closing, contextmanager
from datetime import datetime, timezone
import fcntl
import getpass
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import selectors
import shutil
import socket
import sqlite3
import stat
import subprocess
import sys
import tarfile
import time
import uuid

PARSER_PATH = Path(__file__).resolve().parents[1] / "tools/release_artifacts.py"
if os.geteuid() == 0:
    # Privileged imports must themselves be inside the installed root-owned tree.
    # The entry point must be launched with Python -I by its protected wrapper.
    for _code in (Path(__file__).absolute(), PARSER_PATH):
        for _component in (_code, *_code.parents):
            _metadata = _component.lstat()
            if (
                stat.S_ISLNK(_metadata.st_mode)
                or _metadata.st_uid != 0
                or _metadata.st_mode & 0o022
            ):
                raise SystemExit(
                    "refusing privileged import outside the root-owned helper tree"
                )
_spec = importlib.util.spec_from_file_location("psst_release_artifacts", PARSER_PATH)
_release = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_release)

CONFIG_PATH = Path("/etc/psst.zip/deployment.json")
STATE_PATH = Path("/var/lib/psst.zip-deploy")
BACKUP_PATH = Path("/var/backups/psst.zip")
ID = re.compile(r"[0-9]{8}T[0-9]{6}Z-[0-9a-f]{12}\Z")
NAME = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}\Z")
DOMAIN = re.compile(r"[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?\Z")
FLOW_CHECKS = frozenset(
    {
        "existing_account_and_session",
        "administrator_second_factor",
        "persisted_settings_and_quotas",
        "existing_encrypted_download",
        "new_upload_and_receive",
        "expiry_budget_and_revocation",
        "cleanup_and_restart",
        "client_authenticated_decryption",
    }
)
RESTORE_CHECKS = frozenset(
    {
        "post_checkpoint_changes_reviewed",
        "restored_sessions_links_and_factors_reconciled",
        "independent_traffic_allowances_reconciled",
    }
)
TERMINAL = {"completed", "failed-safe", "abandoned"}
MUTATING = {
    "migration-starting",
    "candidate-running",
    "awaiting-verification",
    "verified",
    "activating",
    "failed-closed",
    "restore-starting",
    "restored-awaiting-verification",
}


class UpdateError(RuntimeError):
    """Failure with a deliberately non-secret operator message."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise UpdateError(message)


def protected(
    path: Path,
    *,
    directory: bool = False,
    executable: bool = False,
    private: bool = True,
) -> None:
    """Reject symlink components and files writable by unprivileged identities."""
    require(path.is_absolute(), "protected path must be absolute")
    for item in [path, *path.parents]:
        info = item.lstat()
        require(not stat.S_ISLNK(info.st_mode), "protected path contains a symlink")
        require(
            info.st_uid == 0 and not info.st_mode & 0o022,
            "protected path is not root-owned",
        )
    info = path.stat()
    require(
        stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode),
        "invalid protected file type",
    )
    if not directory and private:
        require(
            not info.st_mode & 0o077 or executable,
            "protected configuration must have mode 0600",
        )
    if executable:
        require(bool(info.st_mode & 0o100), "verification hook is not executable")


def private_directory(path: Path) -> None:
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    protected(path, directory=True)
    require(
        not path.stat().st_mode & 0o077,
        "deployment state directory must have mode 0700",
    )


def atomic_json(path: Path, value: dict) -> None:
    """Publish only a complete protected record, then sync its parent directory."""
    temporary = path.with_name(path.name + ".new-" + uuid.uuid4().hex)
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, "wb") as output:
            output.write(_release.json_bytes(value))
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
        parent = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(parent)
        finally:
            os.close(parent)
    finally:
        temporary.unlink(missing_ok=True)


def load_json(path: Path) -> dict:
    result = _release.read_json(_release.read_bounded_file(path))
    require(isinstance(result, dict), "expected JSON object")
    return result


def fingerprint(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def validate_config(value: dict) -> dict:
    fields = {
        "repository",
        "signer_workflow",
        "installation",
        "project",
        "environment",
        "compose_files",
        "operator_files",
        "release_overrides",
        "external_proxy",
        "domain",
        "db_path",
        "volume_names",
        "disk_reserve_bytes",
        "image_reserve_bytes",
        "verification_hook",
        "checkpoint_hook",
        "github_token_file",
        "retention_count",
    }
    require(
        set(value) == fields, "deployment configuration has missing or unknown fields"
    )
    _release.repository_name(value["repository"])
    require(
        value["signer_workflow"]
        == value["repository"] + "/.github/workflows/release.yml",
        "unexpected signer workflow",
    )
    root = Path(value["installation"])
    require(
        root.is_absolute()
        and str(root) == value["installation"]
        and ".." not in root.parts,
        "invalid installation directory",
    )
    require(
        isinstance(value["project"], str) and NAME.fullmatch(value["project"]),
        "invalid Compose project",
    )
    require(
        isinstance(value["domain"], str)
        and DOMAIN.fullmatch(value["domain"])
        and "." in value["domain"]
        and ".." not in value["domain"],
        "invalid public hostname",
    )
    require(type(value["external_proxy"]) is bool, "external_proxy must be boolean")
    require(
        isinstance(value["db_path"], str)
        and value["db_path"].startswith("/app/data/")
        and str(PurePosixPath(value["db_path"])) == value["db_path"]
        and re.fullmatch(r"[a-zA-Z0-9_./-]+", value["db_path"]) is not None
        and ".." not in PurePosixPath(value["db_path"]).parts,
        "database must reside in the backend volume",
    )
    for key in ("compose_files", "operator_files", "release_overrides"):
        require(
            isinstance(value[key], list)
            and all(isinstance(x, str) for x in value[key]),
            "invalid deployment file list",
        )
        require(len(set(value[key])) == len(value[key]), "duplicate deployment file")
    require(bool(value["compose_files"]), "current Compose files are required")
    for name in [
        value["environment"],
        *value["compose_files"],
        *value["operator_files"],
        *value["release_overrides"],
    ]:
        require(
            isinstance(name, str)
            and name
            and not PurePosixPath(name).is_absolute()
            and ".." not in PurePosixPath(name).parts
            and "\\" not in name,
            "deployment paths must stay inside the installation",
        )
    volumes = value["volume_names"]
    expected = {"backend:/app/data", "caddy:/data", "caddy:/config"}
    if value["external_proxy"]:
        expected |= {"external-proxy:/data", "external-proxy:/config"}
    require(
        isinstance(volumes, dict)
        and set(volumes) == expected
        and all(isinstance(x, str) and NAME.fullmatch(x) for x in volumes.values())
        and len(set(volumes.values())) == len(volumes),
        "explicit distinct physical volumes are required",
    )
    for key, minimum in (
        ("disk_reserve_bytes", 1024**3),
        ("image_reserve_bytes", 1024**3),
        ("retention_count", 2),
    ):
        require(
            type(value[key]) is int
            and minimum <= value[key] <= (1024**5 if key.endswith("bytes") else 100),
            "invalid deployment capacity/retention policy",
        )
    for key in ("verification_hook", "checkpoint_hook", "github_token_file"):
        require(
            value[key] is None
            or isinstance(value[key], str)
            and Path(value[key]).is_absolute(),
            "invalid protected hook/token path",
        )
    return value


def semver(value: str) -> str:
    require(
        isinstance(value, str) and _release.VERSION.fullmatch(value),
        "version must be vMAJOR.MINOR.PATCH",
    )
    return value


def ssh_command(command: str) -> list[str]:
    if command == "status":
        return ["status"]
    match = re.fullmatch(
        r"update (v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))", command
    )
    require(match is not None, "restricted SSH accepts only update VERSION or status")
    return ["update", match.group(1)]


@contextmanager
def deployment_lock(root: Path):
    path = root / "deployment.lock"
    fd = os.open(path, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        require(
            os.fstat(fd).st_uid == 0 and not os.fstat(fd).st_mode & 0o077,
            "unsafe deployment lock",
        )
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise UpdateError("another deployment owns the host lock") from None
        yield
    finally:
        os.close(fd)


def verify_archive(path: Path, *, image: bool = False) -> None:
    """Read every member, rejecting truncated or unsafe checkpoint archives."""
    count = 0
    with tarfile.open(path, "r:") as archive:
        seen = set()
        for member in archive:
            parts = PurePosixPath(member.name).parts
            require(
                not member.name.startswith("/")
                and ".." not in parts
                and "\\" not in member.name,
                "unsafe checkpoint archive path",
            )
            require(member.name not in seen, "duplicate checkpoint archive path")
            seen.add(member.name)
            require(
                member.isdir() or member.isfile(),
                "unsupported checkpoint entry; preserve it for operator review",
            )
            # Caddy state roots can be sticky; setgid directories preserve group
            # inheritance. Neither grants execution privilege like setuid or a
            # privileged file bit. Preserve these legitimate directory modes.
            require(
                not member.mode & (0o4000 if member.isdir() else 0o7000),
                "unsafe checkpoint entry permissions",
            )
            if member.isfile():
                stream = archive.extractfile(member)
                read = 0
                for block in iter(lambda: stream.read(1024 * 1024), b""):
                    read += len(block)
                require(read == member.size, "truncated checkpoint archive")
            count += 1
    require(count > 0, "empty checkpoint archive")


def require_flow_report(report: dict, restore: bool) -> None:
    required = FLOW_CHECKS | (RESTORE_CHECKS if restore else set())
    require(
        isinstance(report, dict)
        and set(report) == {"checks", "observed_at", "version", "source_commit"},
        "invalid authenticated verification report",
    )
    require(
        isinstance(report["checks"], dict) and set(report["checks"]) == required,
        "missing authenticated flow/security verification",
    )
    for observation in report["checks"].values():
        require(
            isinstance(observation, str) and 12 <= len(observation) <= 1000,
            "verification requires an evidence description, not a success flag",
        )
    require(isinstance(report["observed_at"], str), "verification time missing")
    stamp = datetime.fromisoformat(report["observed_at"].replace("Z", "+00:00"))
    require(
        stamp.tzinfo is not None
        and abs((datetime.now(timezone.utc) - stamp).total_seconds()) < 600,
        "stale verification report",
    )


def persisted_public_settings(value: dict) -> dict:
    """Separate the documented protocol capability from persisted operator policy."""
    result = json.loads(json.dumps(value))
    if "history_sync_version" in result:
        capability = result.pop("history_sync_version")
        require(
            type(capability) is int and capability >= 0,
            "invalid history synchronization capability",
        )
    return result


def reconciled_restore_settings(baseline: dict, observed: dict) -> dict:
    """Allow only conservative budget reductions at the authenticated restore gate."""
    actual = json.loads(json.dumps(observed))
    require(
        actual.pop("public_transfers_paused", None) is True,
        "restored verification must remain paused",
    )
    actual, baseline = persisted_public_settings(actual), persisted_public_settings(
        baseline
    )
    if actual == baseline:
        return actual
    original, changed = dict(baseline), dict(actual)
    old_policy, new_policy = original.pop("traffic_policy", None), changed.pop(
        "traffic_policy", None
    )
    require(
        original == changed
        and isinstance(old_policy, dict)
        and isinstance(new_policy, dict),
        "restore reconciliation changed unrelated operator settings",
    )
    before, after = dict(old_policy), dict(new_policy)
    for field in ("server_budget_bytes", "default_account_budget_bytes"):
        old, new = before.pop(field, None), after.pop(field, None)
        require(
            type(old) is int and type(new) is int and 0 < new <= old,
            "restore reconciliation may only decrease positive traffic budgets",
        )
    require(
        before == after, "restore reconciliation changed traffic enforcement policy"
    )
    require(
        new_policy["default_account_budget_bytes"] <= new_policy["server_budget_bytes"],
        "restored account budget exceeds server budget",
    )
    return actual


class Host:
    """Concrete Docker/GitHub operations. Tests replace this boundary, never live checks."""

    def __init__(self, config: dict):
        self.config = config
        self.root = Path(config["installation"])
        self.bound_inputs = []
        self.env = {
            "PATH": "/usr/sbin:/usr/bin:/sbin:/bin",
            "HOME": "/root",
            "LANG": "C.UTF-8",
        }

    def run(
        self,
        *args: str,
        input_data: bytes | None = None,
        output: Path | None = None,
        gh: bool = False,
        timeout: int = 900,
    ) -> bytes:
        environment = self.env.copy()
        if gh and self.config["github_token_file"]:
            token = Path(self.config["github_token_file"])
            protected(token)
            environment["GH_TOKEN"] = token.read_text().strip()
        sink = output.open("xb") if output else subprocess.PIPE
        try:
            if output:
                os.chmod(output, 0o600)
            result = subprocess.run(
                list(args),
                input=input_data,
                stdout=sink,
                stderr=subprocess.PIPE,
                env=environment,
                timeout=timeout,
            )
            require(
                result.returncode == 0,
                f"command failed ({args[0]} {args[1] if len(args) > 1 else ''}); output withheld",
            )
            if output:
                sink.flush()
                os.fsync(sink.fileno())
                return b""
            return result.stdout
        finally:
            if output:
                sink.close()

    def compose(self, file: Path, *args: str) -> bytes:
        return self.run(
            "docker", "compose", "-p", self.config["project"], "-f", str(file), *args
        )

    def inspect(self, *ids: str) -> list[dict]:
        return json.loads(self.run("docker", "inspect", *ids)) if ids else []

    def current(self) -> dict:
        active = STATE_PATH / "active.json"
        if active.exists():
            protected(active)
            value = load_json(active)["compose"]
            approved = set(self.protected_inputs())
            for service in value["services"].values():
                for mount in service.get("volumes", []):
                    if mount["type"] == "bind":
                        path = Path(mount["source"])
                        require(
                            path in approved
                            or path.is_relative_to(STATE_PATH / "transactions")
                            or path.is_relative_to(BACKUP_PATH),
                            "managed configuration bind escaped protected state",
                        )
                        protected(path, private=False)
                        if path not in approved:
                            self.bound_inputs.append(path)
            return value
        args = [
            "docker",
            "compose",
            "-p",
            self.config["project"],
            "--env-file",
            str(self.root / self.config["environment"]),
        ]
        for name in self.config["compose_files"]:
            args += ["-f", str(self.root / name)]
        return json.loads(self.run(*args, "config", "--format", "json"))

    def verify_identity(self, artifact: str, manifest: dict) -> None:
        repository = _release.repository_name(self.config["repository"])
        workflow = self.config["signer_workflow"]
        require(
            workflow == repository + "/.github/workflows/release.yml",
            "unexpected signer workflow",
        )
        commit = _release.matches(
            manifest["source"]["commit"], _release.COMMIT, "invalid attestation commit"
        )
        version = _release.matches(
            manifest["version"], _release.VERSION, "invalid attestation version"
        )
        ref = "refs/tags/" + version
        self.run(
            "gh",
            "attestation",
            "verify",
            artifact,
            "--hostname",
            "github.com",
            "--repo",
            repository,
            "--cert-identity",
            "https://github.com/" + workflow + "@" + ref,
            "--signer-digest",
            commit,
            "--source-digest",
            commit,
            "--source-ref",
            ref,
            "--deny-self-hosted-runners",
            "--cert-oidc-issuer",
            "https://token.actions.githubusercontent.com",
            "--predicate-type",
            "https://slsa.dev/provenance/v1",
            gh=True,
        )

    def download(self, version: str, target: Path) -> dict:
        # gh receives only a strict version, fixed repository and fixed asset names.
        release = json.loads(
            self.run(
                "gh",
                "api",
                "repos/" + self.config["repository"] + "/releases/tags/" + version,
                gh=True,
            )
        )
        require(
            release.get("draft") is False
            and release.get("prerelease") is False
            and release.get("tag_name") == version,
            "only a ready stable release can be deployed",
        )
        bundle_name = f"psst.zip-deployment-{version}.tar.gz"
        for name in ("release-manifest.json", bundle_name):
            assets = [
                asset
                for asset in release.get("assets", [])
                if asset.get("name") == name
            ]
            require(
                len(assets) == 1
                and type(assets[0].get("id")) is int
                and type(assets[0].get("size")) is int
                and 0 < assets[0]["size"] <= _release.MAX_BUNDLE_BYTES,
                "missing, duplicate or oversized release asset",
            )
            self.download_asset(assets[0]["id"], target / name)
        manifest = _release.validate_manifest(
            load_json(target / "release-manifest.json"), self.config["repository"]
        )
        require(
            manifest["version"] == version
            and manifest["payload_profile"] == "deployment-ready",
            "release is not a deployment-ready selected version",
        )
        # Parsing is bounded/unprivileged data handling. Nothing from the bundle
        # is imported or executed before every identity is authenticated.
        self.verify_identity(str(target / "release-manifest.json"), manifest)
        self.verify_identity(str(target / bundle_name), manifest)
        _release.validate_bundle(manifest, target / bundle_name)
        for component in manifest["images"].values():
            index = component["index"]
            self.verify_identity("oci://" + index, manifest)
            raw = json.loads(
                self.run("docker", "buildx", "imagetools", "inspect", index, "--raw")
            )
            platform_map = {}
            for item in raw.get("manifests", []):
                platform = item.get("platform", {})
                name = platform.get("os", "") + "/" + platform.get("architecture", "")
                if name in _release.PLATFORMS:
                    require(
                        name not in platform_map,
                        "duplicate advertised image architecture",
                    )
                    platform_map[name] = item["digest"]
            require(
                platform_map == component["platform_digests"],
                "registry children do not match authenticated manifest",
            )
            for child in component["platform_digests"].values():
                self.verify_identity(
                    "oci://" + index.split("@")[0] + "@" + child, manifest
                )
        contents = _release.expand_bundle(
            _release.read_bounded_file(target / bundle_name)
        )
        unpacked = target / "bundle"
        unpacked.mkdir(mode=0o700)
        with tarfile.open(fileobj=io.BytesIO(contents), mode="r:") as archive:
            for member in archive:
                destination = unpacked / member.name
                destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                with destination.open("xb") as stream:
                    stream.write(archive.extractfile(member).read())
                os.chmod(destination, 0o644)
        return manifest

    def download_asset(self, identity: int, destination: Path) -> None:
        environment = self.env.copy()
        if self.config["github_token_file"]:
            token = Path(self.config["github_token_file"])
            protected(token)
            environment["GH_TOKEN"] = token.read_text().strip()
        process = subprocess.Popen(
            [
                "gh",
                "api",
                "repos/"
                + self.config["repository"]
                + "/releases/assets/"
                + str(identity),
                "--header",
                "Accept: application/octet-stream",
            ],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            env=environment,
        )
        selector = selectors.DefaultSelector()
        selector.register(process.stdout, selectors.EVENT_READ)
        deadline, total = time.monotonic() + 900, 0
        try:
            with destination.open("xb") as stream:
                os.chmod(destination, 0o600)
                while True:
                    require(
                        time.monotonic() < deadline, "release asset download timed out"
                    )
                    ready = selector.select(timeout=1)
                    if not ready:
                        continue
                    block = os.read(process.stdout.fileno(), 64 * 1024)
                    if not block:
                        break
                    total += len(block)
                    require(
                        total <= _release.MAX_BUNDLE_BYTES,
                        "downloaded release asset exceeds size bound",
                    )
                    stream.write(block)
                require(
                    process.wait(timeout=15) == 0 and total > 0,
                    "release asset download failed",
                )
                stream.flush()
                os.fsync(stream.fileno())
        finally:
            selector.close()
            if process.poll() is None:
                process.kill()
            process.wait()
            process.stdout.close()

    def protected_inputs(self) -> list[Path]:
        protected(self.root, directory=True)
        names = {
            self.config["environment"],
            *self.config["compose_files"],
            *self.config["operator_files"],
            *self.config["release_overrides"],
        }
        paths = [self.root / name for name in sorted(names)] + self.bound_inputs
        for path in paths:
            protected(
                path,
                private=path == self.root / self.config["environment"]
                or path.name.startswith(".env")
                or path.name.endswith(".env"),
            )
        for name in ("verification_hook", "checkpoint_hook"):
            if self.config[name]:
                protected(Path(self.config[name]), executable=True)
        return paths

    def candidate(self, target: Path, manifest: dict) -> dict:
        overrides = target / "release-images.env"
        # This file contains no password. The operator's original environment is
        # read first; explicit identities below select its existing storage.
        names = self.config["volume_names"]
        values = {
            "BACKEND_IMAGE": manifest["images"]["backend"]["index"],
            "WEB_IMAGE": manifest["images"]["web"]["index"],
            "COMPOSE_PROJECT_NAME": self.config["project"],
            "DB_PATH": self.config["db_path"],
            "PSST_DOMAIN": self.config["domain"],
            "BACKEND_DATA_VOLUME": names["backend:/app/data"],
            "CADDY_DATA_VOLUME": names["caddy:/data"],
            "CADDY_CONFIG_VOLUME": names["caddy:/config"],
            "ADMIN_USERNAME": "",
            "ADMIN_PASSWORD": "",
        }
        if self.config["external_proxy"]:
            values.update(
                EXTERNAL_PROXY_DATA_VOLUME=names["external-proxy:/data"],
                EXTERNAL_PROXY_CONFIG_VOLUME=names["external-proxy:/config"],
            )
        overrides.write_text(
            "".join(f"{key}={value}\n" for key, value in values.items())
        )
        os.chmod(overrides, 0o600)
        args = [
            "docker",
            "compose",
            "-p",
            self.config["project"],
            "--env-file",
            str(self.root / self.config["environment"]),
            "--env-file",
            str(overrides),
            "-f",
            str(target / "bundle/deploy/compose.release.yml"),
        ]
        if self.config["external_proxy"]:
            args += [
                "-f",
                str(target / "bundle/deploy/external-proxy.release.compose.yml"),
            ]
        for name in self.config["release_overrides"]:
            args += ["-f", str(self.root / name)]
        value = json.loads(self.run(*args, "config", "--format", "json"))
        for service in value["services"].values():
            service.pop("build", None)
            service["pull_policy"] = "never"
        return value

    def preflight(
        self, current: dict, candidate: dict, manifest: dict, checkpoint: Path
    ) -> dict:
        require(
            self.config["checkpoint_hook"] is not None,
            "configure and exercise encrypted off-host checkpoint protection before production updates",
        )
        docker = (
            self.run("docker", "version", "--format", "{{.Server.Version}}")
            .decode()
            .strip()
        )
        compose = (
            self.run("docker", "compose", "version", "--short")
            .decode()
            .strip()
            .lstrip("v")
        )
        for value, minimum in (
            (docker, manifest["requirements"]["docker"]),
            (compose, manifest["requirements"]["compose"]),
        ):
            match = re.match(r"(\d+)\.(\d+)\.(\d+)", value)
            require(
                match
                and tuple(map(int, match.groups()))
                >= tuple(map(int, minimum.split("."))),
                "Docker/Compose does not meet release requirements",
            )
        info = json.loads(self.run("docker", "info", "--format", "{{json .}}"))
        architecture = {
            "x86_64": "amd64",
            "aarch64": "arm64",
            "amd64": "amd64",
            "arm64": "arm64",
        }.get(info.get("Architecture"))
        require(
            info.get("OSType") == "linux" and architecture is not None,
            "unsupported daemon platform",
        )
        require(
            set(current["services"])
            == set(candidate["services"])
            == (
                {"backend", "caddy", "external-proxy"}
                if self.config["external_proxy"]
                else {"backend", "caddy"}
            ),
            "unexpected deployment services",
        )
        ids = (
            self.run(
                "docker",
                "ps",
                "-aq",
                "--filter",
                "label=com.docker.compose.project=" + self.config["project"],
            )
            .decode()
            .split()
        )
        actual = self.inspect(*ids)
        services = {
            item["Config"]["Labels"].get("com.docker.compose.service"): item
            for item in actual
        }
        require(
            len(services) == len(actual) and set(services) == set(current["services"]),
            "ambiguous or missing current containers",
        )
        volumes = {}
        for key, expected in self.config["volume_names"].items():
            service_name, destination = key.split(":", 1)
            container = services[service_name]
            require(
                container["State"]["Running"]
                and not container["State"].get("Restarting"),
                "current deployment is not healthy/running",
            )
            mounts = [x for x in container["Mounts"] if x["Destination"] == destination]
            require(
                len(mounts) == 1
                and mounts[0]["Type"] == "volume"
                and mounts[0]["Name"] == expected
                and mounts[0]["RW"],
                "actual volume mapping differs from explicit adoption",
            )
            volume = json.loads(self.run("docker", "volume", "inspect", expected))[0]
            require(
                volume["Driver"] == "local"
                and not volume.get("Options")
                and Path(volume["Mountpoint"]).is_dir(),
                "only existing local named volumes are supported",
            )
            # Exact adoption grants permission to retain a legacy name, but it
            # does not grant permission to consume another Compose project's volume.
            owner = (volume.get("Labels") or {}).get("com.docker.compose.project")
            require(
                owner in {None, self.config["project"]},
                "volume belongs to another Compose project",
            )
            volumes[expected] = {
                "mountpoint": volume["Mountpoint"],
                "image": container["Image"],
                "service": service_name,
                "destination": destination,
            }
        for name, container in services.items():
            expected = current["services"][name].get("ports", [])
            actual_ports = container["HostConfig"].get("PortBindings") or {}
            expected_keys = {
                str(port["target"]) + "/" + port.get("protocol", "tcp")
                for port in expected
            }
            require(
                set(actual_ports) == expected_keys,
                "actual published ports differ from adopted Compose",
            )
            for port in expected:
                bindings = actual_ports[
                    str(port["target"]) + "/" + port.get("protocol", "tcp")
                ]
                require(
                    all(
                        str(item["HostPort"]) == str(port["published"])
                        for item in bindings
                    ),
                    "actual public port differs",
                )
                expected_host = port.get("host_ip", "0.0.0.0")
                require(
                    all(
                        item.get("HostIp")
                        in {
                            expected_host,
                            "" if expected_host == "0.0.0.0" else expected_host,
                            "::" if expected_host == "0.0.0.0" else expected_host,
                        }
                        for item in bindings
                    ),
                    "actual ingress interface differs",
                )
        all_ids = self.run("docker", "ps", "-aq").decode().split()
        for container in self.inspect(*all_ids):
            if container["Id"] not in {x["Id"] for x in actual}:
                require(
                    not any(
                        x.get("Name") in volumes and x.get("RW")
                        for x in container.get("Mounts", [])
                    ),
                    "another container can write installation storage",
                )
        require(
            current.get("networks", {}) == candidate.get("networks", {}),
            "candidate changes operator network configuration",
        )
        for name, service in candidate["services"].items():
            prior = current["services"][name]
            require(
                "user" not in service and "user" not in prior,
                "service user overrides need a reviewed ownership adaptation",
            )
            expected_image = manifest["images"][
                "backend" if name == "backend" else "web"
            ]["index"]
            require(
                service["image"] == expected_image,
                "operator override changes the authenticated image pair",
            )
            require(
                service.get("networks", {}) == prior.get("networks", {}),
                "candidate changes operator network membership/addresses",
            )
            require(
                not service.get("privileged")
                and not service.get("cap_add")
                and not service.get("devices")
                and service.get("network_mode") not in {"host", "service:backend"}
                and service.get("pid") != "host",
                "candidate enables unsafe host privileges",
            )
            require(
                service.get("read_only") is True
                and "ALL" in service.get("cap_drop", [])
                and any(
                    x.replace(":", "=") == "no-new-privileges=true"
                    for x in service.get("security_opt", [])
                ),
                "candidate loses container hardening",
            )
            previous_env = prior.get("environment", {})
            actual_env = dict(
                x.split("=", 1) for x in services[name]["Config"]["Env"] if "=" in x
            )
            for key, value in previous_env.items():
                require(
                    str(actual_env.get(key, "")) == str(value or ""),
                    "current Compose environment differs from running deployment",
                )
                if key in {"ADMIN_USERNAME", "ADMIN_PASSWORD"}:
                    require(
                        not value,
                        "remove existing bootstrap credentials before adoption",
                    )
                else:
                    require(
                        str(service.get("environment", {}).get(key, ""))
                        == str(value or ""),
                        "candidate drops or changes an operator setting; review an explicit release override",
                    )
            for key in ("pids_limit", "mem_limit", "cpus", "tmpfs", "logging"):
                require(
                    service.get(key) == prior.get(key),
                    "candidate changes operator resource/logging limits",
                )
            require(
                self.bindings(current, name)
                == self.bindings(candidate, name)
                == {
                    key.split(":", 1)[1]: volume
                    for key, volume in self.config["volume_names"].items()
                    if key.startswith(name + ":")
                },
                "candidate/current volume identities differ from adopted storage",
            )
            require(
                service.get("ports", []) == prior.get("ports", []),
                "candidate changes public ports",
            )
            # A source-era Caddyfile must be deliberately adopted through a
            # protected release override or replaced by the bundled policy.
            approved = set(self.protected_inputs())
            for mount in services[name]["Mounts"]:
                if mount["Type"] == "bind":
                    require(
                        Path(mount["Source"]) in approved and not mount["RW"],
                        "unrecorded/writable operator bind mount",
                    )
            for mount in service.get("volumes", []):
                if mount["type"] == "bind":
                    source = Path(mount["source"])
                    require(
                        mount.get("read_only") is True
                        and (
                            source in approved
                            or source.is_relative_to(
                                STATE_PATH / "transactions" / checkpoint.name / "bundle"
                            )
                        ),
                        "untrusted candidate bind mount",
                    )
        backend_env = candidate["services"]["backend"]["environment"]
        require(
            backend_env.get("DB_PATH") == self.config["db_path"]
            and backend_env.get("STORAGE_PATH") == "/app/data/files"
            and not backend_env.get("ADMIN_USERNAME")
            and not backend_env.get("ADMIN_PASSWORD"),
            "candidate database/storage/bootstrap mismatch",
        )
        require(
            backend_env.get("AUTH_ALLOW_INSECURE_HTTP") == "false"
            and backend_env.get("PUBLIC_URL") == "https://" + self.config["domain"],
            "production requires the adopted HTTPS public URL",
        )
        db = Path(
            volumes[self.config["volume_names"]["backend:/app/data"]]["mountpoint"]
        ) / self.config["db_path"].removeprefix("/app/data/")
        require(
            db.is_file()
            and not db.is_symlink()
            and db.stat().st_size > 0
            and not db.stat().st_mode & 0o077,
            "existing private database is missing or unsafe",
        )
        status_config = json.loads(json.dumps(current))
        status_config["services"]["backend"]["image"] = services["backend"]["Image"]
        self.incident(status_config, "incident-status")
        total = sum(
            self.directory_size(Path(x["mountpoint"])) for x in volumes.values()
        )
        image_ids = sorted({x["Image"] for x in actual})
        image_bytes = sum(
            json.loads(self.run("docker", "image", "inspect", image))[0]["Size"]
            for image in image_ids
        )
        require(
            shutil.disk_usage(checkpoint.parent).free
            >= 2 * total + image_bytes + self.config["disk_reserve_bytes"],
            "insufficient space for a complete stopped checkpoint and isolated restore",
        )
        require(
            shutil.disk_usage(info["DockerRootDir"]).free
            >= self.config["image_reserve_bytes"]
            + self.config["disk_reserve_bytes"]
            + total,
            "insufficient Docker image/restore headroom",
        )
        baseline = self.https(current, "/api/v1/config", public=True)
        baseline.pop("public_transfers_paused", None)
        initialized = self.https(current, "/api/v1/auth/status", public=True)
        require(
            initialized.get("setup_required") is False,
            "existing administrator has not been initialized",
        )
        return {
            "volumes": volumes,
            "containers": {name: item["Id"] for name, item in services.items()},
            "images": {name: item["Image"] for name, item in services.items()},
            "platform": "linux/" + architecture,
            "baseline_config": persisted_public_settings(baseline),
            "database": str(db),
        }

    @staticmethod
    def directory_size(root: Path) -> int:
        total = 0
        for parent, directories, files in os.walk(root, followlinks=False):
            for name in [*directories, *files]:
                path = Path(parent) / name
                require(
                    not path.is_symlink(),
                    "storage has an unsupported symlink; review before deployment",
                )
            for name in files:
                total += (Path(parent) / name).stat().st_size
        return total

    @staticmethod
    def bindings(compose: dict, service: str) -> dict:
        result = {}
        for mount in compose["services"][service].get("volumes", []):
            if mount["type"] == "volume":
                result[mount["target"]] = compose["volumes"][mount["source"]]["name"]
        return result

    def original_health(self, compose: dict, adopted: dict) -> None:
        for attempt in range(40):
            try:
                self.https(compose, "/api/v1/health")
                require(
                    self.https(compose, "/api/v1/auth/status").get("setup_required")
                    is False,
                    "previous account initialization was lost",
                )
                settings = self.https(compose, "/api/v1/config")
                settings.pop("public_transfers_paused", None)
                require(
                    persisted_public_settings(settings)
                    == persisted_public_settings(adopted["baseline_config"]),
                    "previous operator settings changed during recovery",
                )
                return
            except UpdateError:
                if attempt == 39:
                    raise UpdateError(
                        "previous deployment did not recover; keep traffic closed"
                    ) from None
                time.sleep(2)

    def pull(self, manifest: dict, platform: str) -> None:
        for component in manifest["images"].values():
            image = component["index"]
            self.run("docker", "pull", "--platform", platform, image)
            metadata = json.loads(self.run("docker", "image", "inspect", image))[0]
            labels = metadata["Config"].get("Labels", {})
            require(
                metadata["Architecture"] == platform.split("/")[1]
                and metadata["Os"] == "linux"
                and metadata["Config"].get("User") not in {"", "0", "root"},
                "pulled image has invalid architecture/user",
            )
            require(
                labels.get("org.opencontainers.image.version") == manifest["version"]
                and labels.get("org.opencontainers.image.revision")
                == manifest["source"]["commit"]
                and labels.get("org.opencontainers.image.source")
                == "https://github.com/" + self.config["repository"]
                and labels.get("org.opencontainers.image.licenses") == "AGPL-3.0-only",
                "pulled image metadata does not match the authenticated release",
            )

    def validate_caddy(self, candidate_path: Path) -> None:
        compose = load_json(candidate_path)
        for name in ("caddy", "external-proxy"):
            if name not in compose["services"]:
                continue
            service = compose["services"][name]
            environment = candidate_path.parent / (name + ".validate.env")
            values = service.get("environment", {})
            require(
                all("\n" not in str(value) for value in values.values()),
                "invalid Caddy validation environment",
            )
            environment.write_text(
                "".join(f"{key}={value}\n" for key, value in values.items())
            )
            os.chmod(environment, 0o600)
            args = [
                "docker",
                "run",
                "--rm",
                "--network",
                "none",
                "--read-only",
                "--cap-drop",
                "ALL",
                "--security-opt",
                "no-new-privileges",
                "--entrypoint",
                "caddy",
                "--env-file",
                str(environment),
            ]
            for directory in ("/data", "/config", "/tmp"):
                args += ["--tmpfs", directory + ":rw,noexec,nosuid,nodev,mode=1777"]
            for mount in service.get("volumes", []):
                if mount["type"] == "bind":
                    require(
                        mount.get("read_only") is True,
                        "Caddy configuration must be read-only",
                    )
                    args += [
                        "--mount",
                        "type=bind,src="
                        + mount["source"]
                        + ",dst="
                        + mount["target"]
                        + ",readonly",
                    ]
            try:
                self.run(
                    *args,
                    service["image"],
                    "validate",
                    "--config",
                    "/etc/caddy/Caddyfile",
                    "--adapter",
                    "caddyfile",
                )
            finally:
                environment.unlink(missing_ok=True)

    def ownership(self, manifest: dict, adopted: dict) -> None:
        for component, service in (("backend", "backend"), ("web", "caddy")):
            old = adopted["images"][service]
            new = manifest["images"][component]["index"]
            identities = []
            for image in (old, new):
                identities.append(
                    self.run(
                        "docker",
                        "run",
                        "--rm",
                        "--network",
                        "none",
                        "--read-only",
                        "--cap-drop",
                        "ALL",
                        "--security-opt",
                        "no-new-privileges",
                        "--entrypoint",
                        "id",
                        image,
                        "-u",
                    ).strip()
                )
            require(
                identities[0] == identities[1]
                and identities[1].isdigit()
                and identities[1] != b"0",
                "image user IDs differ; ownership migration requires separate reviewed work",
            )
        database = Path(adopted["database"])
        require(
            database.stat().st_uid
            == int(
                self.run(
                    "docker",
                    "run",
                    "--rm",
                    "--network",
                    "none",
                    "--read-only",
                    "--cap-drop",
                    "ALL",
                    "--entrypoint",
                    "id",
                    manifest["images"]["backend"]["index"],
                    "-u",
                )
            ),
            "database ownership is incompatible with selected image",
        )

    def incident(self, compose: dict, action: str) -> bool:
        backend = compose["services"]["backend"]
        bindings = self.bindings(compose, "backend")
        output = self.run(
            "docker",
            "run",
            "--rm",
            "--network",
            "none",
            "--read-only",
            "--cap-drop",
            "ALL",
            "--security-opt",
            "no-new-privileges",
            "--entrypoint",
            "/app/server",
            "--mount",
            "type=volume,src=" + bindings["/app/data"] + ",dst=/app/data",
            "--env",
            "DB_PATH=" + backend["environment"]["DB_PATH"],
            backend["image"],
            action,
        )
        result = json.loads(output)
        require(
            type(result.get("public_transfers_paused")) is bool,
            "incident CLI returned an invalid state",
        )
        if action in {"pause", "resume"}:
            require(
                result["public_transfers_paused"] == (action == "pause"),
                "incident CLI did not persist the requested state",
            )
        return result["public_transfers_paused"]

    def stopped(self, compose_path: Path, volumes: dict) -> None:
        self.compose(compose_path, "stop", "--timeout", "60")
        ids = self.run("docker", "ps", "-q").decode().split()
        for container in self.inspect(*ids):
            require(
                not any(
                    x.get("Name") in volumes and x.get("RW")
                    for x in container.get("Mounts", [])
                ),
                "a writer survived shutdown",
            )

    @staticmethod
    def free_ports(compose: dict) -> None:
        seen = set()
        for service in compose["services"].values():
            for port in service.get("ports", []):
                number = int(port["published"])
                require(
                    1 <= number <= 65535
                    and number not in seen
                    and port.get("protocol", "tcp") == "tcp",
                    "duplicate or unsupported deployment port",
                )
                seen.add(number)
                with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
                    listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
                    try:
                        listener.bind(("127.0.0.1", number))
                    except OSError:
                        raise UpdateError(
                            "deployment port is still occupied after shutdown"
                        ) from None

    def backup(
        self, current: dict, adopted: dict, checkpoint: Path, transaction: dict
    ) -> None:
        checkpoint.mkdir(mode=0o700)
        records = {}
        for number, (volume, facts) in enumerate(sorted(adopted["volumes"].items())):
            name = f"volume-{number}.tar"
            destination = checkpoint / name
            self.run(
                "docker",
                "run",
                "--rm",
                "--network",
                "none",
                "--read-only",
                "--cap-drop",
                "ALL",
                "--security-opt",
                "no-new-privileges",
                "--entrypoint",
                "tar",
                "--mount",
                f"type=volume,src={volume},dst=/snapshot,readonly",
                facts["image"],
                "-C",
                "/snapshot",
                "-cpf",
                "-",
                ".",
                output=destination,
            )
            verify_archive(destination)
            records[name] = {
                "sha256": fingerprint(destination),
                "kind": "volume",
                "volume": volume,
                **facts,
            }
        for number, image in enumerate(sorted(set(adopted["images"].values()))):
            destination = checkpoint / f"image-{number}.tar"
            self.run("docker", "image", "save", image, output=destination)
            verify_archive(destination, image=True)
            records[destination.name] = {
                "sha256": fingerprint(destination),
                "kind": "image",
                "image": image,
            }
            self.run(
                "docker",
                "image",
                "tag",
                image,
                "psst-checkpoint-" + transaction["id"].lower() + f":{number}",
            )
        configs = checkpoint / "configuration"
        configs.mkdir(mode=0o700)
        for path in self.protected_inputs():
            relative = (
                path.relative_to(self.root)
                if path.is_relative_to(self.root)
                else Path("managed") / str(len(records)) / path.name
            )
            copy = configs / relative
            copy.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            shutil.copy2(path, copy)
            with copy.open("rb") as stream:
                os.fsync(stream.fileno())
            records["configuration/" + str(relative)] = {
                "sha256": fingerprint(copy),
                "kind": "configuration",
                "source": str(path),
            }
        atomic_json(checkpoint / "current.compose.json", current)
        records["current.compose.json"] = {
            "sha256": fingerprint(checkpoint / "current.compose.json"),
            "kind": "configuration",
        }
        atomic_json(
            checkpoint / "checkpoint.json",
            {
                "schema_version": 1,
                "transaction": transaction["id"],
                "created_at": datetime.now(timezone.utc).isoformat(),
                "prior_pause": transaction["prior_pause"],
                "images": adopted["images"],
                "records": records,
            },
        )
        self.verify_backup(checkpoint, require_off_host=False)
        self.sqlite_integrity(Path(adopted["database"]))
        self.export_checkpoint(checkpoint)
        self.verify_backup(checkpoint)

    def export_checkpoint(self, checkpoint: Path) -> None:
        hook = self.config["checkpoint_hook"]
        require(
            hook is not None, "encrypted off-host checkpoint protection is required"
        )
        protected(Path(hook), executable=True)
        report = _release.read_json(self.run(hook, "--checkpoint", str(checkpoint)))
        require(
            isinstance(report, dict)
            and set(report)
            == {
                "checkpoint_sha256",
                "encrypted_off_host_receipt",
                "restore_exercise",
                "verified_at",
            },
            "invalid protected backup receipt",
        )
        require(
            report["checkpoint_sha256"] == fingerprint(checkpoint / "checkpoint.json"),
            "off-host receipt does not cover this stopped checkpoint",
        )
        require(
            isinstance(report["verified_at"], str),
            "off-host receipt timestamp is invalid",
        )
        for name in ("encrypted_off_host_receipt", "restore_exercise"):
            require(
                isinstance(report[name], str) and 12 <= len(report[name]) <= 1000,
                "off-host encryption/restore evidence is required",
            )
        stamp = datetime.fromisoformat(report["verified_at"].replace("Z", "+00:00"))
        require(
            stamp.tzinfo is not None
            and abs((datetime.now(timezone.utc) - stamp).total_seconds()) < 900,
            "off-host checkpoint receipt is stale",
        )
        atomic_json(checkpoint / "off-host-receipt.json", report)

    @staticmethod
    def sqlite_integrity(database: Path) -> None:
        # No schema migrations, no creation, no journal deletion, no repair.
        with closing(
            sqlite3.connect(database.as_uri() + "?mode=ro", uri=True)
        ) as connection:
            require(
                connection.execute("PRAGMA integrity_check").fetchall() == [("ok",)],
                "stopped database integrity check failed",
            )
            require(
                connection.execute("PRAGMA foreign_key_check").fetchall() == [],
                "stopped database foreign-key check failed",
            )

    @staticmethod
    def verify_backup(checkpoint: Path, *, require_off_host: bool = True) -> dict:
        protected(checkpoint, directory=True)
        protected(checkpoint / "checkpoint.json")
        value = load_json(checkpoint / "checkpoint.json")
        require(
            value.get("schema_version") == 1
            and ID.fullmatch(value.get("transaction", ""))
            and value["transaction"] == checkpoint.name,
            "invalid checkpoint record",
        )
        require(bool(value.get("records")), "checkpoint has no retained artifacts")
        for name, record in value["records"].items():
            require(
                not PurePosixPath(name).is_absolute()
                and ".." not in PurePosixPath(name).parts,
                "unsafe checkpoint record",
            )
            path = checkpoint / name
            protected(path, private=record["kind"] != "configuration")
            require(
                fingerprint(path) == record["sha256"],
                "checkpoint checksum failed; preserve it and investigate",
            )
            if record["kind"] in {"image", "volume"}:
                verify_archive(path, image=record["kind"] == "image")
        require(
            any(x["kind"] == "image" for x in value["records"].values())
            and any(x["kind"] == "volume" for x in value["records"].values()),
            "checkpoint is missing images or storage",
        )
        if require_off_host:
            receipt = checkpoint / "off-host-receipt.json"
            protected(receipt)
            evidence = load_json(receipt)
            require(
                evidence.get("checkpoint_sha256")
                == fingerprint(checkpoint / "checkpoint.json"),
                "off-host receipt does not authenticate the selected checkpoint",
            )
        return value

    def https(
        self,
        compose: dict,
        path: str,
        *,
        public: bool = False,
        cookie: str | None = None,
        raw: bool = False,
    ) -> dict | bytes:
        proxy = compose["services"][
            "external-proxy" if self.config["external_proxy"] else "caddy"
        ]
        ports = [
            item
            for item in proxy["ports"]
            if item["target"] == 8443 and item.get("protocol", "tcp") == "tcp"
        ]
        require(len(ports) == 1, "expected a single HTTPS ingress")
        port = str(ports[0]["published"])
        args = [
            "curl",
            "--fail",
            "--silent",
            "--show-error",
            "--max-time",
            "15",
            "--proto",
            "=https",
            "--noproxy",
            "*",
            "--resolve",
            self.config["domain"] + ":" + port + ":127.0.0.1",
            "https://" + self.config["domain"] + ":" + port + path,
        ]
        # A secret never appears in command arguments or deployment logs.
        if cookie:
            require(
                "\n" not in cookie
                and "\r" not in cookie
                and '"' not in cookie
                and "\\" not in cookie,
                "invalid private cookie",
            )
            args += ["--config", "-"]
        data = self.run(
            *args,
            input_data=(f'header = "Cookie: {cookie}"\n'.encode() if cookie else None),
        )
        return data if raw else json.loads(data)

    def runtime_checks(self, compose: dict) -> None:
        ids = (
            self.run(
                "docker",
                "ps",
                "-aq",
                "--filter",
                "label=com.docker.compose.project=" + self.config["project"],
            )
            .decode()
            .split()
        )
        containers = self.inspect(*ids)
        actual = {
            item["Config"]["Labels"].get("com.docker.compose.service"): item
            for item in containers
        }
        require(
            len(actual) == len(containers) and set(actual) == set(compose["services"]),
            "candidate has unexpected containers",
        )
        for name, service in compose["services"].items():
            container = actual[name]
            limits = container["HostConfig"]
            require(
                container["State"]["Running"]
                and not container["State"].get("Restarting")
                and container["Config"]["Image"] == service["image"],
                "candidate runtime image/service mismatch",
            )
            require(
                limits.get("ReadonlyRootfs")
                and "ALL" in (limits.get("CapDrop") or [])
                and not limits.get("CapAdd")
                and not limits.get("Privileged")
                and any(
                    item
                    in {
                        "no-new-privileges",
                        "no-new-privileges=true",
                        "no-new-privileges:true",
                    }
                    for item in limits.get("SecurityOpt", [])
                ),
                "candidate runtime lost hardening",
            )
            physical = self.bindings(compose, name)
            mounted = {
                item["Destination"]: item["Name"]
                for item in container["Mounts"]
                if item["Type"] == "volume"
            }
            require(mounted == physical, "candidate runtime uses different storage")
            expected_ports = service.get("ports", [])
            ports = limits.get("PortBindings") or {}
            require(
                set(ports)
                == {
                    str(item["target"]) + "/" + item.get("protocol", "tcp")
                    for item in expected_ports
                },
                "candidate published unexpected ports",
            )
            for item in expected_ports:
                bindings = ports[
                    str(item["target"]) + "/" + item.get("protocol", "tcp")
                ]
                host = item.get("host_ip", "0.0.0.0")
                require(
                    all(
                        str(binding["HostPort"]) == str(item["published"])
                        and binding.get("HostIp")
                        in ({host} if host == "127.0.0.1" else {host, "", "::"})
                        for binding in bindings
                    ),
                    "candidate listener does not match isolated/public policy",
                )
            for logical, network in service.get("networks", {}).items():
                if network and network.get("ipv4_address"):
                    physical_name = compose["networks"][logical]["name"]
                    require(
                        container["NetworkSettings"]["Networks"]
                        .get(physical_name, {})
                        .get("IPAddress")
                        == network["ipv4_address"],
                        "candidate proxy network address differs from trust policy",
                    )

    def automatic_checks(self, private: dict, transaction: dict) -> None:
        self.runtime_checks(private)
        for attempt in range(40):
            try:
                self.https(private, "/api/v1/health")
                break
            except (UpdateError, json.JSONDecodeError):
                if attempt == 39:
                    raise UpdateError(
                        "candidate HTTPS/API health did not become ready"
                    ) from None
                time.sleep(2)
        require(
            self.https(private, "/api/v1/auth/status").get("setup_required") is False,
            "candidate lost initialized account state",
        )
        settings = self.https(private, "/api/v1/config")
        require(
            settings.pop("public_transfers_paused", None) is True,
            "candidate is not transfer-paused",
        )
        require(
            persisted_public_settings(settings)
            == persisted_public_settings(transaction["adopted"]["baseline_config"]),
            "candidate changed persisted public operator settings",
        )
        html = self.https(private, "/", raw=True).decode()
        require("psst-web" in html, "compiled website marker missing")
        scripts = re.findall(r'(?:src|href)="([^" ]+\.js)"', html)
        require(bool(scripts), "compiled website scripts missing")
        for script in scripts[:3]:
            require(
                script.startswith("/") and not script.startswith("//"),
                "invalid compiled asset locator",
            )
            require(
                len(self.https(private, script, raw=True)) > 10,
                "compiled asset unavailable",
            )
        manifest = transaction["manifest"]
        if not transaction.get("restoring"):
            release = self.https(private, "/licenses/release.json")
            require(
                release.get("version") == manifest["version"]
                and release.get("revision") == manifest["source"]["commit"],
                "served source/release metadata mismatch",
            )
        require(
            self.incident(private, "incident-status"),
            "candidate must remain transfer-paused until verification",
        )

    def authenticated_checks(self, private: dict, cookie: str) -> None:
        identity = self.https(private, "/api/v1/auth/me", cookie=cookie)
        require(
            identity.get("role") == "admin"
            or identity.get("user", {}).get("role") == "admin",
            "verification requires a current administrator session",
        )
        for name in ("storage", "counter", "orphan"):
            status = self.https(
                private, "/api/v1/admin/" + name + "-checks", cookie=cookie
            )
            require(
                status.get("state") == "checked"
                and status.get("scan_pending") is False
                and bool(status.get("last_scan_completed_at"))
                and not status.get("scan_error_code"),
                "storage/security reconciliation is not complete",
            )
            require(
                not any(
                    status.get(key, 0)
                    for key in (
                        "issue_count",
                        "busy_count",
                        "unavailable_count",
                        "failed_count",
                        "pending_count",
                        "pending_directories",
                        "pending_candidates",
                        "unsupported_count",
                        "saturated",
                        "unstable",
                    )
                ),
                "storage/security reconciliation reported unresolved state",
            )

    def verification_hook(
        self, private_path: Path, transaction_path: Path
    ) -> dict | None:
        hook = self.config["verification_hook"]
        if hook is None:
            return None
        protected(Path(hook), executable=True)
        # Trusted local code can read private transaction/configuration and make
        # real authenticated probes. Nonzero exit or malformed evidence fails closed.
        return json.loads(
            self.run(
                hook,
                "--transaction",
                str(transaction_path),
                "--compose",
                str(private_path),
            )
        )

    def restore(self, checkpoint: Path, target: Path, transaction: dict) -> dict:
        backup = self.verify_backup(checkpoint)
        current = load_json(checkpoint / "current.compose.json")
        restored_names = {}
        for record in backup["records"].values():
            if record["kind"] == "image":
                file = next(
                    name for name, item in backup["records"].items() if item is record
                )
                self.run("docker", "image", "load", "--input", str(checkpoint / file))
                self.run("docker", "image", "inspect", record["image"])
        for name, record in backup["records"].items():
            if record["kind"] != "volume":
                continue
            volume = (
                "psst-restore-" + transaction["id"] + "-" + str(len(restored_names))
            )
            existing = (
                self.run(
                    "docker", "volume", "ls", "-q", "--filter", "name=^" + volume + "$"
                )
                .decode()
                .strip()
            )
            require(
                not existing,
                "restore destination already exists; never overwrite a partial restore",
            )
            self.run(
                "docker",
                "volume",
                "create",
                "--label",
                "zip.psst.restore=" + transaction["id"],
                volume,
            )
            facts = json.loads(self.run("docker", "volume", "inspect", volume))[0]
            mount = Path(facts["Mountpoint"])
            require(not any(mount.iterdir()), "restore destination is not empty")
            self.run(
                "tar",
                "--numeric-owner",
                "-C",
                str(mount),
                "-xpf",
                str(checkpoint / name),
            )
            restored_names[record["volume"]] = volume
        for volume in current["volumes"].values():
            require(
                volume["name"] in restored_names,
                "checkpoint does not cover every current state volume",
            )
            volume["name"] = restored_names[volume["name"]]
        for service, image in backup["images"].items():
            current["services"][service]["image"] = image
            current["services"][service].pop("build", None)
            for mount in current["services"][service].get("volumes", []):
                if mount["type"] == "bind":
                    source = Path(mount["source"])
                    copies = [
                        checkpoint / name
                        for name, entry in backup["records"].items()
                        if entry.get("source") == str(source)
                    ]
                    require(len(copies) == 1, "unprotected restore configuration bind")
                    copy = copies[0]
                    protected(copy, private=False)
                    mount["source"] = str(copy)
        current["services"]["backend"]["environment"]["ADMIN_USERNAME"] = ""
        current["services"]["backend"]["environment"]["ADMIN_PASSWORD"] = ""
        db_volume = restored_names[self.config["volume_names"]["backend:/app/data"]]
        db_root = Path(
            json.loads(self.run("docker", "volume", "inspect", db_volume))[0][
                "Mountpoint"
            ]
        )
        self.sqlite_integrity(
            db_root / self.config["db_path"].removeprefix("/app/data/")
        )
        self.incident(current, "pause")  # Matching old CLI, before normal startup.
        transaction["restored_volumes"] = restored_names
        return current


def isolated(compose: dict) -> dict:
    result = json.loads(json.dumps(compose))
    for service in result["services"].values():
        service["restart"] = "no"
        for port in service.get("ports", []):
            require(
                port.get("protocol", "tcp") == "tcp",
                "only reviewed TCP ingress is supported",
            )
            port["host_ip"] = "127.0.0.1"
    require(
        not result["services"]["backend"].get("ports"),
        "backend may not have published ports",
    )
    return result


class Updater:
    def __init__(
        self,
        config: dict,
        host: Host,
        state: Path = STATE_PATH,
        backups: Path = BACKUP_PATH,
    ):
        self.config, self.host, self.state, self.backups = config, host, state, backups
        self.record = state / "transaction.json"

    def read(self) -> dict | None:
        return load_json(self.record) if self.record.exists() else None

    def write(self, transaction: dict, phase: str) -> None:
        transaction["phase"] = phase
        transaction["updated_at"] = datetime.now(timezone.utc).isoformat()
        atomic_json(self.record, transaction)
        target = self.state / "transactions" / transaction["id"]
        atomic_json(target / "transaction.json", transaction)

    def new(self, version: str) -> tuple[dict, Path]:
        previous = self.read()
        require(
            previous is None or previous["phase"] in TERMINAL,
            "unresolved transaction; inspect status and use explicit recovery",
        )
        identity = (
            datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ-")
            + uuid.uuid4().hex[:12]
        )
        target = self.state / "transactions" / identity
        target.mkdir(parents=True, mode=0o700)
        transaction = {
            "schema_version": 1,
            "id": identity,
            "version": version,
            "phase": "preparing",
            "checkpoint": str(self.backups / identity),
            "previous_version": (
                load_json(self.state / "active.json")["version"]
                if (self.state / "active.json").exists()
                else (
                    previous.get("active_version", previous.get("previous_version"))
                    if previous
                    else None
                )
            ),
            "prior_pause": None,
            "mutation_started": False,
            "restoring": False,
        }
        self.write(transaction, "preparing")
        return transaction, target

    def update(self, version: str) -> dict:
        version = semver(version)
        transaction, target = self.new(version)
        stopped = False
        try:
            self.host.protected_inputs()
            transaction["manifest"] = self.host.download(version, target)
            current = self.host.current()
            candidate = self.host.candidate(target, transaction["manifest"])
            atomic_json(target / "previous.compose.json", current)
            atomic_json(target / "public.compose.json", candidate)
            atomic_json(target / "private.compose.json", isolated(candidate))
            transaction["adopted"] = self.host.preflight(
                current,
                candidate,
                transaction["manifest"],
                Path(transaction["checkpoint"]),
            )
            for name, image in transaction["adopted"]["images"].items():
                current["services"][name]["image"] = image
                current["services"][name].pop("build", None)
            atomic_json(target / "previous.compose.json", current)
            self.host.pull(transaction["manifest"], transaction["adopted"]["platform"])
            self.host.ownership(transaction["manifest"], transaction["adopted"])
            self.host.validate_caddy(target / "private.compose.json")
            transaction["adopted"] = self.host.preflight(
                current,
                candidate,
                transaction["manifest"],
                Path(transaction["checkpoint"]),
            )
            transaction["prior_pause"] = self.host.incident(current, "incident-status")
            self.write(transaction, "pausing")
            self.host.incident(current, "pause")
            self.write(transaction, "stopping")
            stopped = True  # A partially failed stop also requires safe recovery.
            self.host.stopped(
                target / "previous.compose.json", transaction["adopted"]["volumes"]
            )
            self.host.free_ports(candidate)
            self.write(transaction, "backing-up")
            self.host.backup(
                current,
                transaction["adopted"],
                Path(transaction["checkpoint"]),
                transaction,
            )
            # Must reach stable storage BEFORE the first normal candidate startup.
            transaction["mutation_started"] = True
            self.write(transaction, "migration-starting")
            self.host.compose(
                target / "private.compose.json",
                "up",
                "-d",
                "--no-build",
                "--pull",
                "never",
            )
            self.write(transaction, "candidate-running")
            self.host.automatic_checks(isolated(candidate), transaction)
            self.write(transaction, "awaiting-verification")
            evidence = self.host.verification_hook(
                target / "private.compose.json", target / "transaction.json"
            )
            if evidence is not None:
                self.verify_evidence(transaction, evidence)
                self.activate(transaction)
            return transaction
        except BaseException:
            # KeyboardInterrupt/connection loss is handled like command failure.
            if transaction["mutation_started"]:
                try:
                    self.host.compose(
                        target / "private.compose.json", "stop", "--timeout", "60"
                    )
                finally:
                    self.write(transaction, "failed-closed")
            else:
                try:
                    if stopped:
                        self.host.compose(
                            target / "previous.compose.json",
                            "up",
                            "-d",
                            "--no-build",
                            "--pull",
                            "never",
                        )
                    if stopped:
                        self.host.original_health(
                            load_json(target / "previous.compose.json"),
                            transaction["adopted"],
                        )
                    if transaction["prior_pause"] is not None:
                        self.host.incident(
                            load_json(target / "previous.compose.json"),
                            "pause" if transaction["prior_pause"] else "resume",
                        )
                except BaseException:
                    try:
                        self.host.compose(
                            target / "previous.compose.json", "stop", "--timeout", "60"
                        )
                    finally:
                        self.write(transaction, "failed-closed")
                    raise UpdateError(
                        "old deployment recovery failed; inspect protected transaction, keep traffic closed"
                    ) from None
                self.write(transaction, "failed-safe")
            raise

    def verify_evidence(self, transaction: dict, report: dict) -> None:
        require_flow_report(report, transaction.get("restoring", False))
        expected = transaction.get(
            "verification_identity",
            {
                "version": transaction["version"],
                "source_commit": transaction["manifest"]["source"]["commit"],
            },
        )
        require(
            report["version"] == expected["version"]
            and report["source_commit"] == expected["source_commit"],
            "verification report describes another candidate",
        )
        if transaction.get("restoring"):
            target = self.state / "transactions" / transaction["id"]
            observed = self.host.https(
                load_json(target / "private.compose.json"), "/api/v1/config"
            )
            reconciled = reconciled_restore_settings(
                transaction["adopted"]["baseline_config"], observed
            )
            transaction.setdefault(
                "checkpoint_baseline_config", transaction["adopted"]["baseline_config"]
            )
            transaction["adopted"]["baseline_config"] = reconciled
        transaction["verification"] = report
        self.write(transaction, "verified")

    def activate(self, transaction: dict) -> None:
        require(
            transaction["phase"] == "verified",
            "activation requires completed candidate and security verification",
        )
        require_flow_report(
            transaction["verification"], transaction.get("restoring", False)
        )
        target = self.state / "transactions" / transaction["id"]
        public = load_json(target / "public.compose.json")
        private = load_json(target / "private.compose.json")
        try:
            self.host.automatic_checks(private, transaction)
            self.write(transaction, "activating")
            # Recreate only proxies to change binding. Never start an old backend.
            proxies = [name for name in public["services"] if name != "backend"]
            self.host.compose(
                target / "public.compose.json",
                "up",
                "-d",
                "--no-deps",
                "--no-build",
                "--pull",
                "never",
                *proxies,
            )
            self.host.automatic_checks(public, transaction)
            self.host.incident(
                public, "pause" if transaction["prior_pause"] else "resume"
            )
            for item in (
                self.host.run(
                    "docker",
                    "ps",
                    "-q",
                    "--filter",
                    "label=com.docker.compose.project=" + self.config["project"],
                )
                .decode()
                .split()
            ):
                self.host.run("docker", "update", "--restart", "unless-stopped", item)
            transaction["active_version"] = transaction.get(
                "verification_identity", {}
            ).get("version", transaction["version"])
            atomic_json(
                self.state / "active.json",
                {
                    "transaction": transaction["id"],
                    "version": transaction["active_version"],
                    "compose": public,
                },
            )
            self.write(transaction, "completed")
        except BaseException:
            try:
                self.host.compose(
                    target / "private.compose.json", "stop", "--timeout", "60"
                )
            finally:
                self.write(transaction, "failed-closed")
            raise

    def restore(self) -> dict:
        transaction = self.read()
        require(
            transaction is not None
            and transaction["phase"] in MUTATING | {"completed"},
            "no mutation checkpoint available for restore",
        )
        require(
            not transaction.get("restoring"),
            "restore already attempted; preserve partial copy and inspect it",
        )
        target = self.state / "transactions" / transaction["id"]
        checkpoint = Path(transaction["checkpoint"])
        self.host.verify_backup(checkpoint)
        self.host.compose(target / "private.compose.json", "stop", "--timeout", "60")
        transaction["restoring"] = True
        self.write(transaction, "restore-starting")
        try:
            restored = self.host.restore(checkpoint, target, transaction)
            # The baseline settings remain the stopped checkpoint's settings;
            # its matching image pair is restored onto new separate volumes.
            atomic_json(target / "public.compose.json", restored)
            atomic_json(target / "private.compose.json", isolated(restored))
            transaction["verification_identity"] = {
                "version": transaction["previous_version"] or "source-installation",
                "source_commit": "checkpoint:" + transaction["id"],
            }
            self.write(transaction, "restore-starting")
            self.host.compose(
                target / "private.compose.json",
                "up",
                "-d",
                "--no-build",
                "--pull",
                "never",
            )
            self.host.automatic_checks(isolated(restored), transaction)
            self.write(transaction, "restored-awaiting-verification")
            evidence = self.host.verification_hook(
                target / "private.compose.json", target / "transaction.json"
            )
            if evidence is not None:
                self.verify_evidence(transaction, evidence)
                self.activate(transaction)
            return transaction
        except BaseException:
            try:
                self.host.compose(
                    target / "private.compose.json", "stop", "--timeout", "60"
                )
            finally:
                self.write(transaction, "failed-closed")
            raise

    def local_verify(self) -> None:
        transaction = self.read()
        require(
            transaction
            and transaction["phase"]
            in {"awaiting-verification", "restored-awaiting-verification", "verified"},
            "no isolated candidate awaiting verification",
        )
        require(
            sys.stdin.isatty(),
            "local verification requires an interactive administrator terminal",
        )
        target = self.state / "transactions" / transaction["id"]
        private = load_json(target / "private.compose.json")
        self.host.automatic_checks(private, transaction)
        cookie = getpass.getpass(
            "Current candidate administrator Cookie header (kept only in memory): "
        )
        self.host.authenticated_checks(private, cookie)
        print(
            "Verify the isolated candidate using a browser tunnel and retained client keys.\nKeep public routing closed. Full flow tests temporarily resume only the isolated\ninstance; pause again before completing this gate. Evidence is protected, not logged."
        )
        checks = FLOW_CHECKS | (RESTORE_CHECKS if transaction["restoring"] else set())
        report = {
            "checks": {},
            "observed_at": datetime.now(timezone.utc).isoformat(),
            **transaction.get(
                "verification_identity",
                {
                    "version": transaction["version"],
                    "source_commit": transaction["manifest"]["source"]["commit"],
                },
            ),
        }
        for check in sorted(checks):
            report["checks"][check] = input(
                check + " — describe observed evidence (no secrets): "
            ).strip()
        self.host.automatic_checks(private, transaction)
        self.host.authenticated_checks(private, cookie)
        del cookie
        report["observed_at"] = datetime.now(timezone.utc).isoformat()
        self.verify_evidence(transaction, report)
        self.activate(transaction)

    def recover_safe(self) -> None:
        transaction = self.read()
        require(
            transaction
            and not transaction["mutation_started"]
            and not transaction.get("restoring"),
            "automatic old-binary recovery is forbidden after candidate startup",
        )
        target = self.state / "transactions" / transaction["id"]
        if transaction["phase"] == "preparing":
            self.write(transaction, "abandoned")
            return
        previous = load_json(target / "previous.compose.json")
        try:
            self.host.incident(previous, "pause")
            self.host.compose(
                target / "previous.compose.json",
                "up",
                "-d",
                "--no-build",
                "--pull",
                "never",
            )
            self.host.original_health(previous, transaction["adopted"])
            if transaction["prior_pause"] is not None:
                self.host.incident(
                    previous, "pause" if transaction["prior_pause"] else "resume"
                )
            self.write(transaction, "failed-safe")
        except BaseException:
            try:
                self.host.compose(
                    target / "previous.compose.json", "stop", "--timeout", "60"
                )
            finally:
                self.write(transaction, "failed-closed")
            raise

    def retention(self) -> None:
        """Explicit local retention; preserve current and last known-good checkpoints."""
        current = self.read()
        require(
            current and current["phase"] == "completed",
            "retention requires a completed deployment",
        )
        transactions = []
        for directory in (self.state / "transactions").iterdir():
            if (
                ID.fullmatch(directory.name)
                and (directory / "transaction.json").is_file()
            ):
                record = load_json(directory / "transaction.json")
                if record["phase"] == "completed" and not record.get("restoring"):
                    transactions.append(record)
        transactions.sort(key=lambda record: record["id"], reverse=True)
        keep = {
            current["id"],
            *(x["id"] for x in transactions[: self.config["retention_count"]]),
        }
        for record in transactions[self.config["retention_count"] :]:
            if record["id"] in keep:
                continue
            checkpoint = self.backups / record["id"]
            if checkpoint.exists():
                self.host.verify_backup(checkpoint)
                shutil.rmtree(checkpoint)
        # Partial/failed checkpoints and original/restored volumes are never pruned.


def display(record: dict | None) -> int:
    if record:
        print(
            json.dumps(
                {
                    key: record.get(key)
                    for key in (
                        "id",
                        "version",
                        "active_version",
                        "previous_version",
                        "phase",
                        "updated_at",
                    )
                },
                sort_keys=True,
            )
        )
        return (
            20
            if record["phase"]
            in {"awaiting-verification", "restored-awaiting-verification"}
            else 0
        )
    print('{"phase":"no-transaction"}')
    return 0


def main(argv: list[str] | None = None) -> int:
    os.umask(0o077)
    args = list(sys.argv[1:] if argv is None else argv)
    if args == ["ssh"]:
        args = ssh_command(os.environ.get("SSH_ORIGINAL_COMMAND", ""))
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "command",
        choices=("update", "status", "verify", "restore", "recover-safe", "retention"),
    )
    parser.add_argument("version", nargs="?")
    parsed = parser.parse_args(args)
    require(os.geteuid() == 0, "run the installed root-owned deployment helper")
    protected(CONFIG_PATH)
    protected(Path(__file__).resolve(), executable=True)
    protected(PARSER_PATH, private=False)
    config = validate_config(load_json(CONFIG_PATH))
    active = STATE_PATH / "active.json"
    if active.exists():
        protected(active)
        adopted = load_json(active)["compose"]
        config["volume_names"] = {
            service + ":" + target: volume
            for service in adopted["services"]
            for target, volume in Host.bindings(adopted, service).items()
        }
        validate_config(config)
    private_directory(STATE_PATH)
    private_directory(BACKUP_PATH)
    private_directory(STATE_PATH / "transactions")
    updater = Updater(config, Host(config))
    if parsed.command == "status":
        require(parsed.version is None, "status accepts no extra arguments")
        return display(updater.read())
    with deployment_lock(STATE_PATH):
        if parsed.command == "update":
            require(parsed.version is not None, "update requires a release version")
            record = updater.update(parsed.version)
        else:
            require(
                parsed.version is None, "this command accepts no release/path argument"
            )
            if parsed.command == "verify":
                updater.local_verify()
            elif parsed.command == "restore":
                updater.restore()
            elif parsed.command == "recover-safe":
                updater.recover_safe()
            elif parsed.command == "retention":
                updater.retention()
            record = updater.read()
        return display(record)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as error:
        # Raw external output and configuration values must never reach CI logs.
        message = (
            str(error)
            if isinstance(error, UpdateError)
            else "deployment operation failed; inspect protected state locally"
        )
        print(message, file=sys.stderr)
        sys.exit(1)
