#!/usr/bin/env python3
"""Prepare a private Android 13+ debug-to-production signing bridge locally.

No keys are generated and no installation/publication occurs. The caller supplies
both private keystores outside the checkout and passwords through environment
variables. This is an evaluation tool: it cannot prove installed-data preservation
without the actual device and a subsequent production-only signed update.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

import android_release as release

CAPABILITIES = {
    "installed data": True,
    "shared UID": False,
    # AndroidX declares a package-scoped signature permission. PackageManager
    # requires PERMISSION continuity to let the rotated app retain ownership.
    "permission": True,
    "rollback": False,
    "auth": False,
}
PRIVATE_NAMES = {"artifacts", "publication", "public", "dist", "releases", "release"}


def signer_arguments(prefix: str) -> list[str]:
    names = [
        prefix + suffix
        for suffix in ("_KEYSTORE", "_KEY_ALIAS", "_STORE_PASSWORD", "_KEY_PASSWORD")
    ]
    release.require(
        all(os.environ.get(name) for name in names),
        "Private bridge signing inputs are incomplete",
    )
    path = Path(os.environ[names[0]]).resolve()
    release.require(
        path.is_file() and not path.is_relative_to(release.ROOT),
        "Private bridge keystore must be outside checkout",
    )
    return [
        "--ks",
        str(path),
        "--ks-key-alias",
        os.environ[names[1]],
        "--ks-pass",
        "env:" + names[2],
        "--key-pass",
        "env:" + names[3],
    ]


def capability_arguments() -> list[str]:
    return [
        "--set-installed-data",
        "true",
        "--set-shared-uid",
        "false",
        "--set-permission",
        "true",
        "--set-rollback",
        "false",
        "--set-auth",
        "false",
    ]


def verify_lineage(value: str, old_fingerprint: str, new_fingerprint: str) -> None:
    sections = re.split(
        r"(?=^Signer #[0-9]+ in lineage certificate DN:)", value, flags=re.MULTILINE
    )
    records = [section for section in sections if section.startswith("Signer #")]
    release.require(
        len(records) == 2,
        "Private bridge lineage must contain exactly two certificates",
    )
    for index, (record, expected) in enumerate(
        zip(records, (old_fingerprint, new_fingerprint), strict=True), start=1
    ):
        certificates = re.findall(
            r"^Signer #([0-9]+) in lineage certificate SHA-256 digest: ([a-fA-F0-9:]+)$",
            record,
            re.MULTILINE,
        )
        release.require(
            len(certificates) == 1
            and certificates[0][0] == str(index)
            and release.certificate_digest(certificates[0][1]) == expected,
            "Private bridge lineage certificate order differs",
        )
        for name, allowed in CAPABILITIES.items():
            matches = re.findall(
                r"^Has " + re.escape(name) + r" capability\s*:\s*(true|false)$",
                record,
                re.MULTILINE,
            )
            release.require(
                matches == [str(allowed).lower()],
                "Private bridge lineage capabilities differ",
            )


def verify_old_apk(old_apk: Path, sdk_tools: Path, installed_fingerprint: str) -> dict:
    release.require(
        old_apk.is_file() and not old_apk.is_symlink(), "Invalid old APK input"
    )
    signatures = release.command(
        [
            str(sdk_tools / "apksigner"),
            "verify",
            "--verbose",
            "--print-certs",
            str(old_apk),
        ]
    )
    release.verify_signer_output(signatures, installed_fingerprint)
    release.reject_public_lineage(old_apk)
    manifest = release.parse_badging(
        release.command([str(sdk_tools / "aapt2"), "dump", "badging", str(old_apk)]),
        allow_debuggable=True,
    )
    release.require(
        manifest["applicationId"] == release.PACKAGE and manifest["versionCode"] > 0,
        "Old APK does not match current Android package",
    )
    return manifest


def private_output(output: Path) -> Path:
    release.require(
        os.environ.get("GITHUB_ACTIONS", "").lower() != "true",
        "Private bridge must never run in GitHub Actions",
    )
    resolved = output.resolve()
    release.require(
        not resolved.is_relative_to(release.ROOT),
        "Private bridge output must be outside checkout",
    )
    release.require(
        not (PRIVATE_NAMES & {part.lower() for part in resolved.parts}),
        "Private bridge output cannot use a public artifact directory",
    )
    release.require(
        not output.exists() and not output.is_symlink(),
        "Private bridge output already exists",
    )
    return resolved


def prepare_bridge(
    old_apk: Path,
    unsigned: Path,
    sdk_tools: Path,
    version_file: Path,
    revision: str,
    installed_fingerprint: str,
    production_fingerprint: str,
    device_api: int,
    minimum_version_code: int,
    output: Path,
) -> None:
    release.require(
        device_api >= 33, "Private bridge evaluation requires Android 13 or newer"
    )
    release.require(
        minimum_version_code >= 1, "Recorded installed/private version code is required"
    )
    old_fingerprint = release.certificate_digest(installed_fingerprint)
    new_fingerprint = release.certificate_digest(production_fingerprint)
    release.require(
        old_fingerprint != new_fingerprint,
        "Production signer must differ from debug signer",
    )
    output = private_output(output)
    old = verify_old_apk(old_apk, sdk_tools, old_fingerprint)
    candidate = release.verify_apk(unsigned, sdk_tools, None, version_file, revision)
    release.require(
        candidate["versionCode"] > max(old["versionCode"], minimum_version_code),
        "Private bridge version code must exceed every installed/private version",
    )
    old_arguments = signer_arguments("ANDROID_DEBUG")
    new_arguments = signer_arguments("ANDROID_RELEASE")
    # Create only after all read-only/version/signer/input checks have passed.
    output.mkdir(parents=True, mode=0o700)
    os.chmod(output, 0o700)
    lineage = output / "private-debug-to-production.lineage"
    signed = output / "private-debug-to-production.apk"
    aligned = output / "aligned-unsigned.apk"
    try:
        release.command(
            [
                str(sdk_tools / "apksigner"),
                "rotate",
                "--out",
                str(lineage),
                "--old-signer",
                *old_arguments,
                *capability_arguments(),
                "--new-signer",
                *new_arguments,
                *capability_arguments(),
            ]
        )
        verify_lineage(
            release.command(
                [
                    str(sdk_tools / "apksigner"),
                    "lineage",
                    "--in",
                    str(lineage),
                    "--print-certs",
                    "--verbose",
                ]
            ),
            old_fingerprint,
            new_fingerprint,
        )
        release.command(
            [
                str(sdk_tools / "zipalign"),
                "-P",
                "16",
                "-f",
                "4",
                str(unsigned),
                str(aligned),
            ]
        )
        release.command(
            [
                str(sdk_tools / "apksigner"),
                "sign",
                *old_arguments,
                "--next-signer",
                *new_arguments,
                "--lineage",
                str(lineage),
                "--rotation-min-sdk-version",
                "33",
                "--v4-signing-enabled",
                "false",
                "--debuggable-apk-permitted",
                "false",
                "--out",
                str(signed),
                str(aligned),
            ]
        )
        signatures = release.command(
            [
                str(sdk_tools / "apksigner"),
                "verify",
                "--min-sdk-version",
                str(device_api),
                "--max-sdk-version",
                str(device_api),
                "--verbose",
                "--print-certs",
                str(signed),
            ]
        )
        release.verify_signer_output(signatures, new_fingerprint, device_api=device_api)
        verify_lineage(
            release.command(
                [
                    str(sdk_tools / "apksigner"),
                    "lineage",
                    "--in",
                    str(signed),
                    "--print-certs",
                    "--verbose",
                ]
            ),
            old_fingerprint,
            new_fingerprint,
        )
        release.inspect_apk_assets(
            signed, release.version_metadata(version_file), revision
        )
        release.command(
            [str(sdk_tools / "zipalign"), "-c", "-P", "16", "4", str(signed)]
        )
        with signed.open("rb") as stream:
            checksum = hashlib.file_digest(stream, "sha256").hexdigest()
        release.write_json(
            output / "private-bridge.json",
            {
                "schema_version": 1,
                "privateOnly": True,
                "sourceRevision": revision,
                "applicationId": release.PACKAGE,
                "versionCode": candidate["versionCode"],
                "oldVersionCode": old["versionCode"],
                "deviceApi": device_api,
                "rotationMinimumApi": 33,
                "installedSignerSha256": old_fingerprint,
                "productionSignerSha256": new_fingerprint,
                "apkSha256": checksum,
                "oldSignerCapabilities": CAPABILITIES,
                "operatorPermissionTrustReviewRequired": True,
                "devicePreservationVerified": False,
                "subsequentProductionOnlyUpdateVerified": False,
            },
        )
        (output / "PRIVATE-INSTRUCTIONS.txt").write_text(
            "This APK and lineage are private migration artifacts. Never publish them "
            "or upload them to GitHub. Do not uninstall the old app. This artifact is "
            "intended only for the recorded Android 13+ device and certificate. "
            "Before any real-device transition, the operator must review and accept "
            "historical debug-key trust for installed data and signature permissions. "
            "Permission continuity is required to retain ownership of AndroidX's "
            "package-scoped signature permission; it also retains permission trust "
            "for the historical signer. Shared UID, rollback and auth trust are disabled. "
            "Installing a later production-only APK does not prove that Android has "
            "forgotten this historical permission trust. "
            "Verify a disposable signing transition first, then verify the real "
            "in-place update preserves client-held keys, accounts and history. "
            "A subsequent production-only APK with a higher version code must "
            "preserve that state before treating the transition as complete. "
            "Preparation or apksigner verification is not device evidence.\n"
        )
        for path in output.iterdir():
            os.chmod(path, 0o600)
    except BaseException:
        shutil.rmtree(output)
        raise
    finally:
        aligned.unlink(missing_ok=True)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--old-apk", type=Path, required=True)
    parser.add_argument("--unsigned", type=Path, required=True)
    parser.add_argument("--sdk-tools", type=Path, required=True)
    parser.add_argument(
        "--version-file",
        type=Path,
        default=release.ROOT / "android/release-version.properties",
    )
    parser.add_argument("--revision", required=True)
    parser.add_argument("--installed-signer-sha256", required=True)
    parser.add_argument("--production-signer-sha256", required=True)
    parser.add_argument("--device-api", type=int, required=True)
    parser.add_argument("--minimum-version-code", type=int, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    try:
        prepare_bridge(
            args.old_apk,
            args.unsigned,
            args.sdk_tools,
            args.version_file,
            args.revision,
            args.installed_signer_sha256,
            args.production_signer_sha256,
            args.device_api,
            args.minimum_version_code,
            args.output_dir,
        )
    except release.InvalidRelease as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1)
    except (OSError, ValueError, subprocess.SubprocessError):
        print(
            "Private signing bridge preparation failed; no device preservation is verified.",
            file=sys.stderr,
        )
        raise SystemExit(1)


if __name__ == "__main__":
    main()
