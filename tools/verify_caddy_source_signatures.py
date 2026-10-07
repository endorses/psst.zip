#!/usr/bin/env python3
"""Verify retained official Caddy source/checksum signatures without publishing.

Legacy Sigstore verification remains online: Fulcio/SCT trust and Rekor checks
must succeed. This emits a separate, collection-bound evidence report; it never
clears the collector's remaining legal/source review or authenticates Alpine APKs.
"""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

from release_artifacts import (
    COMMIT,
    DIGEST,
    InvalidRelease,
    json_bytes,
    matches,
    require,
)

COSIGN_VERSION = "v2.6.5"
COSIGN_COMMIT = "3e82f50a2839855693aacf7b3d0e7e2f30774cb4"
# Official release digests independently matched against cosign_checksums.txt.
COSIGN_SHA256 = {
    "linux/amd64": "c3b4f5410e608af03a5eb0aaac84a4313d8da131248e08ff1759ac70c79d1644",
    "linux/arm64": "426193b4c5da4d4d643e822f48fe0cc8a476ca1782a272704831f5a0cef716d7",
}
ISSUER = "https://token.actions.githubusercontent.com"
WORKFLOW = "https://github.com/caddyserver/caddy/.github/workflows/release.yml"
REKOR = "https://rekor.sigstore.dev"
VERSION = re.compile(r"v[0-9]+\.[0-9]+\.[0-9]+\Z")
NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,199}\Z")
SUM_LINE = re.compile(r"([0-9a-f]{128})  ([A-Za-z0-9][A-Za-z0-9_.-]{0,199})\Z")
LIMIT = 512 * 1024 * 1024


def file_hash(path: Path, algorithm: str = "sha256") -> str:
    require(
        path.is_file() and not path.is_symlink(), "Expected a regular retained file"
    )
    require(path.stat().st_size <= LIMIT, "Retained file exceeds verification bounds")
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, algorithm).hexdigest()


def run_verifier(args: list[str]) -> subprocess.CompletedProcess:
    # Do not inherit credentials, alternative trust roots, proxy overrides or
    # COSIGN_* environment options. Public trust metadata is kept in memory.
    result = subprocess.run(
        args,
        env={"PATH": os.defpath, "SIGSTORE_NO_CACHE": "1"},
        capture_output=True,
        timeout=55,
    )
    require(
        len(result.stdout) + len(result.stderr) <= 1024 * 1024,
        "Cosign output exceeds verification bounds",
    )
    return result


def verify_tool(cosign: Path) -> dict:
    checksum = file_hash(cosign)
    platforms = [name for name, value in COSIGN_SHA256.items() if value == checksum]
    require(
        len(platforms) == 1, "Cosign executable differs from pinned official release"
    )
    result = run_verifier([str(cosign), "version", "--json"])
    require(result.returncode == 0, "Cosign version inspection failed")
    info = json.loads(result.stdout)
    require(
        info.get("gitVersion") == COSIGN_VERSION
        and info.get("gitCommit") == COSIGN_COMMIT
        and info.get("gitTreeState") == "clean"
        and info.get("platform") == platforms[0],
        "Cosign build identity differs",
    )
    return {
        "version": COSIGN_VERSION,
        "commit": COSIGN_COMMIT,
        "platform": platforms[0],
        "sha256": checksum,
    }


def verification_arguments(
    cosign: Path,
    version: str,
    revision: str,
    artifact: Path,
    certificate: Path,
    signature: Path,
) -> list[str]:
    matches(version, VERSION, "Invalid Caddy release version")
    matches(revision, COMMIT, "Invalid expected Caddy source revision")
    return [
        str(cosign),
        "--timeout",
        "45s",
        "verify-blob",
        "--certificate-identity",
        WORKFLOW + "@refs/tags/" + version,
        "--certificate-oidc-issuer",
        ISSUER,
        "--certificate-github-workflow-sha",
        revision,
        "--certificate-github-workflow-ref",
        "refs/tags/" + version,
        "--certificate-github-workflow-repository",
        "caddyserver/caddy",
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
        "--new-bundle-format=false",
        "--certificate",
        str(certificate),
        "--signature",
        str(signature),
        str(artifact),
    ]


def checksum_entries(data: bytes) -> dict[str, str]:
    require(len(data) <= 1024 * 1024, "Signed checksum list exceeds bounds")
    entries = {}
    for line in data.decode("ascii").splitlines():
        parsed = SUM_LINE.fullmatch(line)
        require(parsed is not None, "Unsupported signed Caddy checksum line")
        checksum, name = parsed.groups()
        require(name not in entries, "Duplicate signed Caddy checksum entry")
        entries[name] = checksum
    require(bool(entries), "Signed Caddy checksum list is empty")
    return entries


def verify(collection: Path, cosign: Path) -> dict:
    require(not collection.is_symlink(), "Symlinked collection directory rejected")
    inventory_path = collection / "caddy-source-inventory.json"
    inventory_checksum = file_hash(inventory_path)
    require(
        inventory_path.stat().st_size <= 8 * 1024 * 1024,
        "Caddy inventory exceeds bounds",
    )
    inventory = json.loads(inventory_path.read_bytes())
    require(inventory.get("schema_version") == 1, "Unsupported Caddy collection schema")
    require(
        inventory.get("review_required") is True,
        "Caddy collection review state differs",
    )
    version = matches(
        inventory.get("version"), VERSION, "Invalid Caddy collection version"
    )
    revision = matches(
        inventory.get("source_revision"), COMMIT, "Caddy source revision missing"
    )
    matches(
        inventory.get("image_id"), DIGEST, "Caddy collection image identity missing"
    )
    retained = {}
    for entry in inventory["sources"]:
        name = matches(entry.get("file"), NAME, "Unsafe Caddy retained asset name")
        require(name not in retained, "Duplicate retained Caddy source asset")
        retained[name] = entry
    short = version[1:]
    archive = f"caddy_{short}_buildable-artifact.tar.gz"
    checksums = f"caddy_{short}_checksums.txt"
    binaries = [
        name
        for name in retained
        if name
        in {f"caddy_{short}_linux_amd64.tar.gz", f"caddy_{short}_linux_arm64.tar.gz"}
    ]
    require(
        len(binaries) == 1, "Missing or ambiguous retained Caddy executable archive"
    )
    names = [
        archive,
        archive + ".sig",
        archive.removesuffix(".tar.gz") + ".pem",
        checksums,
        checksums + ".sig",
        checksums + ".pem",
        binaries[0],
    ]
    for name in names:
        require(name in retained, "Required signed Caddy source asset missing")
        entry = retained[name]
        require(
            entry.get("url")
            == f"https://github.com/caddyserver/caddy/releases/download/{version}/{name}",
            "Retained Caddy release asset URL differs",
        )
        require(
            file_hash(collection / name) == entry.get("sha256"),
            "Retained Caddy source checksum differs",
        )
    tool = verify_tool(cosign)
    verifications = []
    for name, certificate in (
        (archive, archive.removesuffix(".tar.gz") + ".pem"),
        (checksums, checksums + ".pem"),
    ):
        args = verification_arguments(
            cosign,
            version,
            revision,
            collection / name,
            collection / certificate,
            collection / (name + ".sig"),
        )
        result = run_verifier(args)
        require(
            result.returncode == 0,
            f"Official Caddy upstream signature verification failed: {name}",
        )
        verifications.append(
            {
                "artifact": name,
                "sha256": retained[name]["sha256"],
                "certificate_sha256": retained[certificate]["sha256"],
                "signature_sha256": retained[name + ".sig"]["sha256"],
                "exit_code": result.returncode,
                "stdout": result.stdout.decode("utf-8", errors="replace"),
                "stderr": result.stderr.decode("utf-8", errors="replace"),
                "arguments": args[1 : len(args) - 5],
            }
        )
    signed = checksum_entries((collection / checksums).read_bytes())
    bindings = []
    for name in (archive, binaries[0]):
        digest = file_hash(collection / name, "sha512")
        require(
            signed.get(name) == digest,
            "Retained artifact differs from signed Caddy SHA512 list",
        )
        bindings.append({"file": name, "sha512": digest})
    # Recheck all covered bytes and the inventory after verifier subprocesses.
    require(
        file_hash(inventory_path) == inventory_checksum,
        "Caddy collection changed during verification",
    )
    for name in names:
        require(
            file_hash(collection / name) == retained[name]["sha256"],
            "Caddy asset changed during verification",
        )
    return {
        "schema_version": 1,
        "verified_at": datetime.now(timezone.utc).isoformat(),
        "collection_inventory_sha256": inventory_checksum,
        "image_id": inventory["image_id"],
        "version": version,
        "source_revision": revision,
        "verifier": tool,
        "signer_identity": WORKFLOW + "@refs/tags/" + version,
        "oidc_issuer": ISSUER,
        "rekor_url": REKOR,
        "legacy_sigstore_signatures_verified": True,
        "certificate_transparency_verification_required": True,
        "rekor_verification_required": True,
        "verifications": verifications,
        "signed_sha512_bindings": bindings,
        "apk_upstream_signatures_verified": False,
        "other_source_archive_signatures_verified": False,
        "review_required": True,
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--collection", required=True, type=Path)
    parser.add_argument(
        "--cosign",
        required=True,
        type=Path,
        help="Pinned official v2.6.5 Linux executable",
    )
    parser.add_argument(
        "--output",
        required=True,
        type=Path,
        help="New independent verification evidence file",
    )
    args = parser.parse_args()
    try:
        require(not args.output.exists(), "Signature evidence output already exists")
        result = verify(args.collection.resolve(), args.cosign.resolve())
        with args.output.open("xb") as output:
            output.write(json_bytes(result))
        print(
            json.dumps(
                {
                    "verified_artifacts": len(result["verifications"]),
                    "review_required": True,
                }
            )
        )
    except (
        InvalidRelease,
        OSError,
        ValueError,
        KeyError,
        TypeError,
        subprocess.TimeoutExpired,
    ) as error:
        parser.exit(1, f"Caddy signature verification rejected: {error}\n")


if __name__ == "__main__":
    main()
