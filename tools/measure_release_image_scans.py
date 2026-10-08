"""Measure exact native OCI images using authenticated Trivy, without approval.

The caller supplies retained official release assets. By default the verified
scanner downloads its own official database; a retained DB is measurement-only.
Every input is copied into private temporary storage; only verified tools run.
Returned raw reports and measurements still need trusted workflow authentication
and independent, image/binary/source/advisory-bound finding review. This module
cannot produce a final-image-scanners success gate, even for zero findings.
"""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import os
from pathlib import Path
import platform as host_platform
import shutil
import stat
import sys
import tarfile
import tempfile

from assemble_release_oci import MAX_ARCHIVE, MAX_BLOBS, StrictTarInfo, inspect_archive
from generate_release_gate_reports import NativeSourceContext, timestamp
from github_release_transport import MAX_JSON, command
from publish_container_release import sha256, source_digest
from release_artifacts import (
    DIGEST,
    InvalidRelease,
    fields,
    json_bytes,
    matches,
    read_json,
    require,
)
from verify_caddy_source_signatures import COSIGN_COMMIT, COSIGN_SHA256, COSIGN_VERSION

VERSION = "0.75.0"
SOURCE_COMMIT = "591e9799316a602e703f0b484f6c6d7b234ec8f3"
IDENTITY = (
    "https://github.com/aquasecurity/trivy/.github/workflows/reusable-release.yaml@refs/tags/v"
    + VERSION
)
ISSUER = "https://token.actions.githubusercontent.com"
REKOR = "https://rekor.sigstore.dev"
ASSETS = {
    "linux/amd64": {
        "archive": "c6e65abddb348e25f10549df887045629cf28cc72453cd1c63acb717316b3f3f",
        "bundle": "3565a49a9d857a977b18cb402323cb71ed806b735730c02a757c46133ddc12c0",
        "binary": "93f9da8e4ba5e0c1c76d8234ed2494cf9afb0a96fd21953e424bb795f3299b8e",
    },
    "linux/arm64": {
        "archive": "a1ee9f6ffb7d112b64ff726a2a0717c21175c1114361391f4a132956751a13b3",
        "bundle": "5c09172152cc2d24837962e0dee2f2b0ff9dd4ffb2c2a93aff437e9c6f5f0052",
        # ARM binary identity is derived only AFTER official bundle verification.
    },
}
TARGETS = {"backend": "app/server", "web": "usr/bin/caddy"}
SEVERITIES = {"UNKNOWN", "LOW", "MEDIUM", "HIGH", "CRITICAL"}
MAX_TOOL = 512 * 1024**2
MAX_DB = 2 * 1024**3
DATABASE_REPOSITORY = "ghcr.io/aquasecurity/trivy-db:2"


def native_platform() -> str:
    machine = host_platform.machine()
    require(host_platform.system() == "Linux", "Native scans require Linux")
    require(machine in {"x86_64", "aarch64", "arm64"}, "Unsupported scanner host")
    return "linux/amd64" if machine == "x86_64" else "linux/arm64"


def snapshot(source: Path, destination: Path, limit: int) -> str:
    """Copy a bounded regular file without following its final path symlink."""
    fd = os.open(source, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as stream:
        info = os.fstat(stream.fileno())
        require(
            stat.S_ISREG(info.st_mode) and 0 < info.st_size <= limit,
            "Invalid scanner input file",
        )
        checksum, count = hashlib.sha256(), 0
        with destination.open("xb") as output:
            os.chmod(destination, 0o400)
            while content := stream.read(1024**2):
                count += len(content)
                require(count <= limit, "Scanner input exceeds bounds")
                checksum.update(content)
                output.write(content)
        require(count == info.st_size, "Scanner input changed during snapshot")
        require(
            source_digest(source) == "sha256:" + checksum.hexdigest(),
            "Scanner input changed during snapshot",
        )
    return checksum.hexdigest()


def environment(root: Path) -> dict[str, str]:
    # No inherited auth, trust roots, TRIVY_*, plugin, proxy or Go environment.
    return {
        "PATH": os.defpath,
        "HOME": str(root),
        "TMPDIR": str(root),
        "XDG_CONFIG_HOME": str(root / "config"),
        "XDG_CACHE_HOME": str(root / "cache"),
        "SIGSTORE_NO_CACHE": "1",
    }


def authenticate_tool(
    root: Path,
    *,
    platform: str,
    archive: Path,
    bundle: Path,
    cosign: Path,
    execute=command,
) -> tuple[Path, dict]:
    """Authenticate fixed official assets before extracting/executing Trivy."""
    require(platform in ASSETS, "Unsupported native scanner platform")
    env = environment(root)
    verifier = root / "cosign"
    verifier_hash = snapshot(cosign, verifier, MAX_TOOL)
    require(
        verifier_hash == COSIGN_SHA256[platform],
        "Cosign differs from pinned native official executable",
    )
    os.chmod(verifier, 0o500)
    version = read_json(
        execute([str(verifier), "version", "--json"], environment=env, timeout=55)
    )
    require(
        isinstance(version, dict)
        and version.get("gitVersion") == COSIGN_VERSION
        and version.get("gitCommit") == COSIGN_COMMIT
        and version.get("gitTreeState") == "clean"
        and version.get("platform") == platform,
        "Unexpected Cosign build identity",
    )
    retained_archive, retained_bundle = (
        root / "trivy.tar.gz",
        root / "trivy.sigstore.json",
    )
    archive_hash = snapshot(archive, retained_archive, MAX_TOOL)
    bundle_hash = snapshot(bundle, retained_bundle, 1024**2)
    require(
        archive_hash == ASSETS[platform]["archive"]
        and bundle_hash == ASSETS[platform]["bundle"],
        "Trivy official release asset differs from pinned bytes",
    )
    args = [
        str(verifier),
        "--timeout",
        "45s",
        "verify-blob",
        "--certificate-identity",
        IDENTITY,
        "--certificate-oidc-issuer",
        ISSUER,
        "--certificate-github-workflow-sha",
        SOURCE_COMMIT,
        "--certificate-github-workflow-ref",
        "refs/tags/v" + VERSION,
        "--certificate-github-workflow-repository",
        "aquasecurity/trivy",
        "--certificate-github-workflow-trigger",
        "push",
        "--certificate-github-workflow-name",
        "Release",
        "--rekor-url",
        REKOR,
        "--insecure-ignore-tlog=false",
        "--insecure-ignore-sct=false",
        "--private-infrastructure=false",
        "--offline=false",
        "--new-bundle-format=true",
        "--bundle",
        str(retained_bundle),
        str(retained_archive),
    ]
    execute(args, environment=env, timeout=55)
    require(
        source_digest(retained_archive) == "sha256:" + archive_hash
        and source_digest(retained_bundle) == "sha256:" + bundle_hash
        and source_digest(verifier) == "sha256:" + verifier_hash,
        "Tool inputs changed during verification",
    )
    executable, seen, expanded = root / "trivy", set(), 0
    with tarfile.open(retained_archive, "r:gz", tarinfo=StrictTarInfo) as package:
        for member in package:
            require(
                member.name not in seen and len(seen) < 200,
                "Duplicate/excess scanner archive entries",
            )
            seen.add(member.name)
            require(
                member.isdir() or member.isfile(), "Unsupported scanner archive entry"
            )
            require(
                0 <= member.size <= MAX_TOOL, "Scanner archive member exceeds bounds"
            )
            expanded += member.size
            require(expanded <= MAX_TOOL, "Scanner archive expands beyond bounds")
            if member.name != "trivy":
                continue
            require(member.isfile(), "Scanner executable is not a regular member")
            source = package.extractfile(member)
            require(source is not None, "Missing scanner executable")
            with executable.open("xb") as output:
                shutil.copyfileobj(source, output, 1024**2)
    require(executable.is_file(), "Scanner release lacks executable")
    executable_hash = source_digest(executable)[7:]
    if "binary" in ASSETS[platform]:
        require(
            executable_hash == ASSETS[platform]["binary"],
            "Unexpected Trivy executable checksum",
        )
    with executable.open("rb") as stream:
        header = stream.read(20)
    require(
        len(header) == 20
        and header[:6] == b"\x7fELF\x02\x01"
        and int.from_bytes(header[18:20], "little")
        == (62 if platform == "linux/amd64" else 183),
        "Trivy executable architecture differs",
    )
    os.chmod(executable, 0o500)
    info = read_json(
        execute(
            [
                str(executable),
                "--version",
                "--format",
                "json",
                "--config",
                str(root / "empty.yaml"),
            ],
            environment=env,
            timeout=55,
        )
    )
    require(
        isinstance(info, dict) and info.get("Version") == VERSION,
        "Unexpected Trivy version",
    )
    return executable, {
        "name": "Trivy",
        "version": VERSION,
        "source_commit": SOURCE_COMMIT,
        "platform": platform,
        "sha256": "sha256:" + executable_hash,
        "archive_sha256": "sha256:" + archive_hash,
        "bundle_sha256": "sha256:" + bundle_hash,
        "certificate_identity": IDENTITY,
        "oidc_issuer": ISSUER,
        "verification": "online-cosign-bundle-sct-rekor",
        "cosign": {
            "version": COSIGN_VERSION,
            "commit": COSIGN_COMMIT,
            "sha256": "sha256:" + verifier_hash,
        },
    }


def layout_snapshot(archive: Path, root: Path) -> dict[str, str]:
    """Copy the already validated OCI layout, never untar image layer contents."""
    root.mkdir(mode=0o700)
    inventory, seen, expanded = {}, set(), 0
    with tarfile.open(archive, "r:", tarinfo=StrictTarInfo) as contents:
        for member in contents:
            require(
                member.name not in seen and len(seen) < MAX_BLOBS + 8,
                "OCI snapshot contains duplicate/excess members",
            )
            seen.add(member.name)
            require(
                0 <= member.size <= MAX_ARCHIVE, "OCI snapshot member exceeds bounds"
            )
            expanded += member.size
            require(expanded <= MAX_ARCHIVE, "OCI snapshot expands beyond bounds")
            name = member.name.removeprefix("./").rstrip("/")
            require(
                name in {"oci-layout", "index.json", "blobs", "blobs/sha256"}
                or (
                    name.startswith("blobs/sha256/")
                    and len(name) == 77
                    and all(c in "0123456789abcdef" for c in name[13:])
                ),
                "Invalid private OCI layout path",
            )
            if member.isdir():
                continue
            require(
                member.isfile() and name not in inventory,
                "Invalid private OCI layout member",
            )
            destination = root / name
            destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            stream = contents.extractfile(member)
            require(stream is not None, "Missing OCI member")
            with destination.open("xb") as output:
                shutil.copyfileobj(stream, output, 1024**2)
            os.chmod(destination, 0o400)
            inventory[name] = source_digest(destination)
    return inventory


def unchanged_layout(root: Path, expected: dict[str, str]) -> None:
    actual = {}
    for path in root.rglob("*"):
        require(not path.is_symlink(), "Scanner modified OCI layout entry type")
        if path.is_file():
            actual[str(path.relative_to(root))] = source_digest(path)
        else:
            require(path.is_dir(), "Scanner added unsupported OCI layout entry")
    require(actual == expected, "Scanner modified the private OCI layout")


def checked_report(raw: bytes, *, platform: str, component: str, config: str) -> dict:
    require(0 < len(raw) <= MAX_JSON, "Scanner JSON exceeds bounds")
    value = read_json(raw)
    require(
        isinstance(value, dict)
        and value.get("SchemaVersion") == 2
        and value.get("ArtifactType") == "container_image"
        and value.get("Trivy", {}).get("Version") == VERSION,
        "Unexpected scanner report identity",
    )
    metadata = value.get("Metadata", {})
    require(
        isinstance(metadata, dict) and metadata.get("ImageID") == config,
        "Scanner did not scan the exact tested image configuration",
    )
    require(
        metadata.get("ImageConfig", {}).get("os") == "linux"
        and metadata["ImageConfig"].get("architecture") == platform.split("/")[1]
        and metadata.get("OS", {}).get("Family") == "alpine",
        "Scanner image platform/runtime differs",
    )
    timestamp(value.get("CreatedAt"))
    results = value.get("Results")
    require(
        isinstance(results, list) and 0 < len(results) <= 128,
        "Missing scanner package inventory",
    )
    os_seen, executable_seen = False, False
    for result in results:
        require(
            isinstance(result, dict)
            and result.get("Class") in {"os-pkgs", "lang-pkgs"}
            and isinstance(result.get("Target"), str)
            and isinstance(result.get("Packages"), list)
            and result["Packages"],
            "Incomplete scanner result/package inventory",
        )
        if result["Class"] == "os-pkgs":
            require(result.get("Type") == "alpine", "Unexpected OS package scanner")
            os_seen = True
        if (
            result["Class"] == "lang-pkgs"
            and result.get("Type") == "gobinary"
            and result["Target"] == TARGETS[component]
        ):
            executable_seen = True
        findings = result.get("Vulnerabilities", [])
        require(isinstance(findings, list), "Malformed scanner findings")
        for finding in findings:
            require(
                isinstance(finding, dict)
                and all(
                    isinstance(finding.get(key), str) and finding[key]
                    for key in (
                        "VulnerabilityID",
                        "PkgName",
                        "InstalledVersion",
                        "Status",
                    )
                )
                and finding.get("Severity") in SEVERITIES,
                "Incomplete scanner finding",
            )
        require(
            not result.get("ExperimentalModifiedFindings"),
            "Scanner unexpectedly suppressed findings",
        )
    require(
        os_seen and executable_seen,
        "Scanner omitted OS packages or the application binary",
    )
    return value


def measure_image_scan(
    context: NativeSourceContext,
    *,
    component: str,
    archive: Path,
    tested_config: str,
    tool_archive: Path,
    tool_bundle: Path,
    cosign: Path,
    database: Path | None = None,
    execute=command,
) -> tuple[dict, bytes]:
    """Run one complete native measurement and return its record and raw JSON.

    No caller-supplied disposition or scanner command is accepted. Exit-code zero
    means scanning completed; all findings remain unresolved for publication.
    The workflow must retain/sign the returned bytes before any later review.
    """
    source = context.checked()
    platform = context.platform
    require(
        platform == native_platform(),
        "Scanner measurement requires actual native execution",
    )
    require(component in TARGETS, "Unknown scanner component")
    matches(tested_config, DIGEST, "Invalid tested configuration digest")

    def inspect():
        return inspect_archive(
            archive,
            platform=platform,
            repository=context.repository,
            version=context.version,
            commit=context.commit,
            tested_config=tested_config,
            component=component,
        )

    image = inspect()
    target = component + "-" + platform.split("/")[1]
    with tempfile.TemporaryDirectory(prefix="psst-image-scan-") as temporary:
        root = Path(temporary)
        (root / "empty.yaml").write_text("{}\n")
        executable, tool = authenticate_tool(
            root,
            platform=platform,
            archive=tool_archive,
            bundle=tool_bundle,
            cosign=cosign,
            execute=execute,
        )
        cache = root / "cache"
        db = cache / "db"
        db.mkdir(parents=True, mode=0o700)
        if database is None:
            download_started = datetime.now(timezone.utc).isoformat()
            execute(
                [
                    str(executable),
                    "image",
                    "--config",
                    str(root / "empty.yaml"),
                    "--cache-dir",
                    str(cache),
                    "--db-repository",
                    DATABASE_REPOSITORY,
                    "--download-db-only",
                    "--timeout",
                    "5m",
                ],
                environment=environment(root),
                timeout=330,
            )
            db_hash = source_digest(db / "trivy.db")[7:]
            require(
                0 < (db / "trivy.db").stat().st_size <= MAX_DB,
                "Downloaded database exceeds bounds",
            )
            metadata_hash = source_digest(db / "metadata.json")[7:]
            require(
                (db / "metadata.json").stat().st_size <= 1024**2,
                "Downloaded database metadata exceeds bounds",
            )
            acquisition = {
                "kind": "owned-authenticated-trivy-download",
                "repository": DATABASE_REPOSITORY,
                "scanner_sha256": tool["sha256"],
                "started_at": download_started,
                "completed_at": datetime.now(timezone.utc).isoformat(),
                "database_sha256": "sha256:" + db_hash,
                "metadata_sha256": "sha256:" + metadata_hash,
            }
        else:
            db_hash = snapshot(database / "trivy.db", db / "trivy.db", MAX_DB)
            metadata_hash = snapshot(
                database / "metadata.json", db / "metadata.json", 1024**2
            )
            acquisition = {
                "kind": "retained-unapproved-snapshot",
                "authenticated_acquisition_required": True,
            }
        metadata = read_json((db / "metadata.json").read_bytes())
        fields(
            metadata,
            {"Version", "UpdatedAt", "NextUpdate", "DownloadedAt"},
            "actual vulnerability database metadata",
        )
        require(
            isinstance(metadata, dict)
            and type(metadata.get("Version")) is int
            and metadata["Version"] == 2,
            "Unexpected vulnerability database schema",
        )
        for key in ("UpdatedAt", "NextUpdate", "DownloadedAt"):
            timestamp(metadata.get(key))
        layout = root / "layout"
        inventory = layout_snapshot(archive, layout)
        require(inspect() == image, "OCI input changed during layout snapshot")
        started = datetime.now(timezone.utc).isoformat()
        args = [
            str(executable),
            "image",
            "--config",
            str(root / "empty.yaml"),
            "--cache-dir",
            str(cache),
            "--input",
            str(layout),
            "--scanners",
            "vuln",
            "--format",
            "json",
            "--list-all-pkgs=true",
            "--severity",
            "UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL",
            "--ignore-unfixed=false",
            "--ignorefile=",
            "--ignore-policy=",
            "--offline-scan",
            "--skip-db-update",
            "--skip-java-db-update",
            "--skip-vex-repo-update",
            "--show-suppressed",
            "--exit-code",
            "0",
            "--timeout",
            "10m",
        ]
        raw = execute(args, environment=environment(root), timeout=660)
        report = checked_report(
            raw, platform=platform, component=component, config=tested_config
        )
        unchanged_layout(layout, inventory)
        require(inspect() == image, "OCI input changed during scan")
        require(
            source_digest(executable) == tool["sha256"]
            and source_digest(db / "trivy.db") == "sha256:" + db_hash
            and source_digest(db / "metadata.json") == "sha256:" + metadata_hash,
            "Scanner tool/database changed during scan",
        )
        findings = [
            {
                "target": result["Target"],
                "class": result["Class"],
                "finding_sha256": sha256(json_bytes(finding)),
                "finding": finding,
            }
            for result in report["Results"]
            for finding in result.get("Vulnerabilities", [])
        ]
        return {
            "schema_version": 1,
            "kind": "native-image-scanner-measurement",
            "source": source,
            "execution": "native",
            "target": target,
            "image": image,
            "scanner": tool,
            "database": {
                "sha256": "sha256:" + db_hash,
                "metadata_sha256": "sha256:" + metadata_hash,
                "acquisition": acquisition,
                **metadata,
            },
            "started_at": started,
            "scanned_at": report["CreatedAt"],
            "completed_at": datetime.now(timezone.utc).isoformat(),
            "status": "complete",
            "exit_code": 0,
            "raw_report_sha256": sha256(raw),
            "findings": findings,
            "publication_authorized": False,
            "findings_review_required": True,
            "final_image_scanners_gate_pending": True,
        }, raw


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("repository", "version", "commit", "tested-config"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--platform", required=True, choices=tuple(ASSETS))
    parser.add_argument("--component", required=True, choices=tuple(TARGETS))
    for name in (
        "archive",
        "tool-archive",
        "tool-bundle",
        "cosign",
        "output",
    ):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument(
        "--database",
        type=Path,
        help="Retained measurement-only snapshot; default downloads the official DB with the authenticated scanner",
    )
    args = parser.parse_args(argv)
    output = args.output
    require(
        not output.exists() and not output.is_symlink(), "Scanner output already exists"
    )
    require(
        output.parent.is_dir() and not output.parent.is_symlink(),
        "Scanner output parent must be an existing real directory",
    )
    context = NativeSourceContext(
        args.repository, args.version, args.commit, args.platform
    )
    measurement, raw = measure_image_scan(
        context,
        component=args.component,
        archive=args.archive,
        tested_config=args.tested_config,
        tool_archive=args.tool_archive,
        tool_bundle=args.tool_bundle,
        cosign=args.cosign,
        database=args.database,
    )
    # No report exists before completed measurement; reserve without overwrite.
    # Each complete file is linked into the reserved directory atomically, with
    # measurement published last. Any write failure removes only our new output.
    output.mkdir(mode=0o700)
    try:
        with tempfile.TemporaryDirectory(
            prefix=".psst-scanner-output-", dir=output.parent
        ) as temporary:
            staging = Path(temporary)
            for name, content in (
                ("scan.json", raw),
                ("measurement.json", json_bytes(measurement)),
            ):
                source = staging / name
                with source.open("xb") as stream:
                    os.chmod(source, 0o400)
                    stream.write(content)
                    stream.flush()
                    os.fsync(stream.fileno())
                os.link(source, output / name)
    except BaseException:
        shutil.rmtree(output)
        raise


if __name__ == "__main__":
    try:
        main()
    except (InvalidRelease, OSError, ValueError, tarfile.TarError) as exc:
        print(f"Image scanner measurement failed: {exc}", file=sys.stderr)
        sys.exit(1)
