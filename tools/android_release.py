#!/usr/bin/env python3
"""Validate, sign and package Android releases; never generate keys or publish.

Signing credentials are read only by sign-apk from ANDROID_RELEASE_* environment
variables. Keystores must be files outside the source checkout. Diagnostics never
include subprocess output or credential values. Ordinary validation needs no key.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import tempfile
import tomllib
import zipfile
from pathlib import Path, PurePosixPath

ROOT = Path(__file__).resolve().parents[1]
REVISION = re.compile(r"[a-f0-9]{40}\Z")
VERSION = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\Z")
PACKAGE = "zip.psst.android"
ABIS = {"armeabi-v7a", "arm64-v8a", "x86", "x86_64"}
MAX_APK = 1024**3
MAX_MEMBER = 256 * 1024**2


class InvalidRelease(ValueError):
    """A safe, fixed diagnostic without caller secrets."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise InvalidRelease(message)


def command(args: list[str], *, timeout: int = 120) -> str:
    result = subprocess.run(
        args, cwd=ROOT, capture_output=True, timeout=timeout, check=False
    )
    require(result.returncode == 0, "Required release command failed")
    return result.stdout.decode("utf-8", errors="strict")


def write_json(path: Path, value: dict) -> None:
    require(not path.exists(), "Output already exists")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def version_metadata(path: Path) -> dict:
    values = {}
    for line in path.read_text().splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        name, separator, value = line.partition("=")
        name = name.strip()
        require(bool(separator) and name not in values, "Invalid version properties")
        values[name] = value.strip()
    require(
        set(values) == {"versionName", "versionCode"}, "Unexpected version properties"
    )
    require(
        bool(VERSION.fullmatch(values["versionName"])), "Invalid release version name"
    )
    require(
        bool(re.fullmatch(r"[1-9][0-9]*", values["versionCode"])),
        "Invalid version code",
    )
    code = int(values["versionCode"])
    require(code <= 2_100_000_000, "Version code exceeds Android limit")
    return {"versionName": values["versionName"], "versionCode": code}


def validate_source(
    tag: str, expected_commit: str, main_ref: str, version_file: Path
) -> dict:
    require(bool(REVISION.fullmatch(expected_commit)), "Invalid source revision")
    version = version_metadata(version_file)
    require(tag == "android-v" + version["versionName"], "Tag and version disagree")
    resolved = command(
        ["git", "rev-parse", "--verify", "refs/tags/" + tag + "^{commit}"]
    ).strip()
    require(resolved == expected_commit, "Tag does not identify selected source")
    require(
        command(["git", "rev-parse", "HEAD"]).strip() == resolved,
        "Checkout differs from selected source",
    )
    command(["git", "merge-base", "--is-ancestor", resolved, main_ref])
    require(
        not command(["git", "status", "--porcelain", "--untracked-files=no"]),
        "Tracked source is modified",
    )
    require(
        not command(
            [
                "git",
                "ls-files",
                "--others",
                "--exclude-standard",
                "--",
                "android",
                "shared",
            ]
        ),
        "Untracked mobile source cannot be assigned a release identity",
    )
    require(
        command(["git", "show", resolved + ":android/release-version.properties"])
        == version_file.read_text(),
        "Version file differs from selected source",
    )
    return {"schema_version": 1, "tag": tag, "sourceRevision": resolved, **version}


def certificate_digest(value: str) -> str:
    normalized = value.replace(":", "").lower()
    require(
        bool(re.fullmatch(r"[a-f0-9]{64}", normalized)),
        "Invalid public signer fingerprint",
    )
    return normalized


def verify_signer_output(
    value: str, expected: str, *, device_api: int | None = None
) -> None:
    """Accept SDK 37 labels without confusing rotation with multi-signing.

    Verbose apksigner output reports the logical signer count separately from
    certificate records. Rotated APKs can print old and current certificates with
    SDK ranges; only the private bridge verifier supplies a selected API. Public
    releases require every printed APK signer to have the approved key. Source
    stamps are independent of the APK signing identity.
    """
    expected = certificate_digest(expected)
    require(
        re.findall(r"^Number of signers: ([0-9]+)$", value, re.MULTILINE) == ["1"],
        "APK must have exactly one verified signer",
    )
    records = re.findall(
        r"^(.+) certificate SHA-256 digest: ([^\r\n]+)$", value, re.MULTILINE
    )
    observed = []
    for label, digest in records:
        if label in {"Source Stamp Signer", "Source Stamp Signer:"}:
            continue
        identity = re.fullmatch(
            r"(?:V(?:1|2|3\.[012]) )?Signer(?: #([1-9][0-9]{0,9}))?:?"
            r"(?: \(minSdkVersion=([1-9][0-9]{0,9})(?: \(dev release=true\))?, "
            r"maxSdkVersion=([1-9][0-9]{0,9})\))?",
            label,
        )
        require(identity is not None, "Unrecognized APK signing certificate record")
        number, minimum, maximum = identity.groups()
        require(number in {None, "1"}, "APK must have exactly one verified signer")
        digest = certificate_digest(digest)
        if minimum is not None:
            minimum, maximum = int(minimum), int(maximum)
            require(minimum <= maximum, "Invalid APK signer SDK range")
            if device_api is not None and not minimum <= device_api <= maximum:
                continue
        observed.append(digest)
    require(
        bool(observed) and set(observed) == {expected},
        "APK signer differs from approved signer",
    )


def parse_badging(value: str, *, allow_debuggable: bool = False) -> dict:
    package = re.search(
        r"^package: name='([^']+)' versionCode='([0-9]+)' versionName='([^']+)'",
        value,
        re.MULTILINE,
    )
    minimum = re.search(
        r"^(?:minSdkVersion|sdkVersion):'([0-9]+)'$", value, re.MULTILINE
    )
    target = re.search(r"^targetSdkVersion:'([0-9]+)'$", value, re.MULTILINE)
    require(bool(package and minimum and target), "APK manifest metadata is incomplete")
    require(
        allow_debuggable
        or not re.search(r"^application-debuggable(?:\s|$)", value, re.MULTILINE),
        "APK is debuggable",
    )
    return {
        "applicationId": package[1],
        "versionCode": int(package[2]),
        "versionName": package[3],
        "minSdk": int(minimum[1]),
        "targetSdk": int(target[1]),
    }


def verify_elf(data: bytes, abi: str | None = None) -> None:
    require(
        data[:4] == b"\x7fELF" and len(data) >= 64, "Native library is not an ELF file"
    )
    require(data[4] in (1, 2) and data[5] in (1, 2), "Unsupported ELF format")
    order = "<" if data[5] == 1 else ">"
    wide = data[4] == 2
    if abi is not None:
        machines = {
            "armeabi-v7a": (False, 40),
            "arm64-v8a": (True, 183),
            "x86": (False, 3),
            "x86_64": (True, 62),
        }
        require(
            (wide, struct.unpack_from(order + "H", data, 18)[0]) == machines[abi],
            "Native ELF architecture differs from ABI",
        )
    offset = struct.unpack_from(
        order + ("Q" if wide else "I"), data, 32 if wide else 28
    )[0]
    entry_size, count = struct.unpack_from(order + "HH", data, 54 if wide else 42)
    require(
        0 < count < 4096 and entry_size >= (56 if wide else 32),
        "Invalid ELF program headers",
    )
    require(offset + entry_size * count <= len(data), "Truncated ELF program headers")
    loads = 0
    for index in range(count):
        start = offset + index * entry_size
        if struct.unpack_from(order + "I", data, start)[0] != 1:
            continue
        loads += 1
        if wide:
            file_offset, address = struct.unpack_from(order + "QQ", data, start + 8)
            alignment = struct.unpack_from(order + "Q", data, start + 48)[0]
        else:
            file_offset, address = struct.unpack_from(order + "II", data, start + 4)
            alignment = struct.unpack_from(order + "I", data, start + 28)[0]
        require(
            alignment >= 16384 and alignment & (alignment - 1) == 0,
            "Native library lacks 16 KB ELF alignment",
        )
        require(
            file_offset % 16384 == address % 16384,
            "Native library load offsets lack 16 KB alignment",
        )
    require(loads > 0, "Native library has no load segments")


def inspect_apk_assets(apk: Path, expected: dict, revision: str) -> dict:
    require(
        apk.is_file() and not apk.is_symlink() and 0 < apk.stat().st_size <= MAX_APK,
        "Invalid APK file",
    )
    with zipfile.ZipFile(apk) as archive:
        entries = archive.infolist()
        require(len(entries) <= 50000, "APK contains too many entries")
        names = set()
        abis = set()
        libraries = []
        total = 0
        for entry in entries:
            name = PurePosixPath(entry.filename)
            require(
                not name.is_absolute()
                and ".." not in name.parts
                and "\\" not in entry.filename
                and entry.filename not in names
                and not entry.flag_bits & 1
                and (entry.external_attr >> 16) & 0o170000 != 0o120000,
                "APK contains an unsafe or duplicate entry",
            )
            names.add(entry.filename)
            total += entry.file_size
            require(
                entry.file_size <= MAX_MEMBER and total <= 4 * MAX_APK,
                "APK expanded size exceeds limit",
            )
            if entry.filename.startswith("lib/") and entry.filename.endswith(".so"):
                require(
                    len(name.parts) == 3 and name.parts[1] in ABIS,
                    "Unsupported packaged native ABI",
                )
                abis.add(name.parts[1])
                verify_elf(archive.read(entry), name.parts[1])
                libraries.append(entry.filename)
        require("assets/psst-release.json" in names, "APK lacks release identity")
        require(
            archive.getinfo("assets/psst-release.json").file_size <= 65536,
            "Release identity exceeds size limit",
        )
        identity = json.loads(archive.read("assets/psst-release.json"))
        require(
            identity == {**expected, "sourceRevision": revision},
            "Packaged source/version identity mismatch",
        )
        notices = {}
        for name in (
            "AGPL-3.0-only.txt",
            "THIRD_PARTY_NOTICES.txt",
            "dependency-inventory.json",
        ):
            source = ROOT / "android/app/src/main/assets/licenses" / name
            packaged = "assets/licenses/" + name
            require(packaged in names, "APK lacks required legal resources")
            data = archive.read(packaged)
            require(
                data == source.read_bytes(),
                "Packaged legal resources differ from selected source",
            )
            notices[name] = hashlib.sha256(data).hexdigest()
        return {
            "nativeAbis": sorted(abis),
            "nativeLibraries": sorted(libraries),
            "legalSha256": notices,
        }


def require_unsigned(apk: Path) -> None:
    with zipfile.ZipFile(apk) as archive:
        require(
            not any(
                re.fullmatch(r"META-INF/[^/]+\.(?:SF|RSA|DSA|EC)", name, re.IGNORECASE)
                for name in archive.namelist()
            ),
            "Unsigned APK already contains JAR signatures",
        )
        central_directory = archive.start_dir
    if central_directory >= 24:
        with apk.open("rb") as stream:
            stream.seek(central_directory - 16)
            require(
                stream.read(16) != b"APK Sig Block 42",
                "Unsigned APK already contains a signing block",
            )


def length_prefixed(data: bytes, offset: int = 0) -> tuple[bytes, int]:
    require(offset + 4 <= len(data), "Truncated signing attribute")
    size = struct.unpack_from("<I", data, offset)[0]
    end = offset + 4 + size
    require(end <= len(data), "Truncated signing attribute")
    return data[offset + 4 : end], end


def reject_public_lineage(apk: Path) -> None:
    """Reject rotation authority not printed by verify --print-certs.

    apksigner verifies cryptography separately. This bounded parser inspects the
    official APK v3 signed-data attribute layout only to disallow proof-of-rotation.
    Private migration APKs must never pass the public publication verifier.
    """
    with zipfile.ZipFile(apk) as archive:
        central_directory = archive.start_dir
    with apk.open("rb") as stream:
        require(central_directory >= 24, "APK lacks a signing block")
        stream.seek(central_directory - 24)
        footer = stream.read(24)
        require(footer[8:] == b"APK Sig Block 42", "APK lacks a signing block")
        size = struct.unpack_from("<Q", footer)[0]
        require(
            24 <= size <= 16 * 1024**2 and size + 8 <= central_directory,
            "Invalid signing block size",
        )
        stream.seek(central_directory - size - 8)
        block = stream.read(size + 8)
    require(struct.unpack_from("<Q", block)[0] == size, "Signing block sizes disagree")
    offset = 8
    seen = set()
    while offset < len(block) - 24:
        require(offset + 8 <= len(block) - 24, "Truncated signing block entry")
        length = struct.unpack_from("<Q", block, offset)[0]
        end = offset + 8 + length
        require(length >= 4 and end <= len(block) - 24, "Invalid signing block entry")
        block_id = struct.unpack_from("<I", block, offset + 8)[0]
        require(block_id not in seen, "Duplicate signing block")
        seen.add(block_id)
        require(
            block_id not in {0x1B93AD61, 0x70E1C89F},
            "Rotated private APK cannot be published",
        )
        if block_id == 0xF05368C0:
            value = block[offset + 12 : end]
            signers, consumed = length_prefixed(value)
            require(consumed == len(value), "Invalid v3 signing block")
            position = 0
            while position < len(signers):
                signer, position = length_prefixed(signers, position)
                signed_data, _ = length_prefixed(signer)
                _, current = length_prefixed(signed_data)
                _, current = length_prefixed(signed_data, current)
                require(current + 8 <= len(signed_data), "Truncated v3 SDK range")
                attributes, current = length_prefixed(signed_data, current + 8)
                require(current == len(signed_data), "Invalid v3 signed data")
                attribute_offset = 0
                while attribute_offset < len(attributes):
                    attribute, attribute_offset = length_prefixed(
                        attributes, attribute_offset
                    )
                    require(len(attribute) >= 4, "Invalid v3 signing attribute")
                    require(
                        struct.unpack_from("<I", attribute)[0] != 0x3BA06F8C,
                        "Private debug lineage cannot be published",
                    )
        offset = end
    require(offset == len(block) - 24, "Invalid signing block boundary")


def verify_apk(
    apk: Path, sdk_tools: Path, signer: str | None, version_file: Path, revision: str
) -> dict:
    require(bool(REVISION.fullmatch(revision)), "Invalid source revision")
    expected = version_metadata(version_file)
    assets = inspect_apk_assets(apk, expected, revision)
    badging = parse_badging(
        command([str(sdk_tools / "aapt2"), "dump", "badging", str(apk)])
    )
    versions = tomllib.loads((ROOT / "android/gradle/libs.versions.toml").read_text())[
        "versions"
    ]
    require(
        badging
        == {
            "applicationId": PACKAGE,
            **expected,
            "minSdk": int(versions["android-minSdk"]),
            "targetSdk": int(versions["android-targetSdk"]),
        },
        "Packaged manifest differs from release configuration",
    )
    command([str(sdk_tools / "zipalign"), "-c", "-P", "16", "4", str(apk)])
    if signer is None:
        require_unsigned(apk)
    if signer is not None:
        signer = certificate_digest(signer)
        signatures = command(
            [
                str(sdk_tools / "apksigner"),
                "verify",
                "--verbose",
                "--print-certs",
                str(apk),
            ]
        )
        verify_signer_output(signatures, signer)
        require(
            bool(
                re.search(
                    r"^Verified using v2 scheme .*: true$", signatures, re.MULTILINE
                )
            ),
            "APK lacks verified v2 signature",
        )
        reject_public_lineage(apk)
    with apk.open("rb") as stream:
        digest = hashlib.file_digest(stream, "sha256").hexdigest()
    return {
        "schema_version": 1,
        "sourceRevision": revision,
        **badging,
        **assets,
        "signed": signer is not None,
        "signerSha256": signer,
        "apkSha256": digest,
        "apkSize": apk.stat().st_size,
    }


def sign_apk(unsigned: Path, output: Path, sdk_tools: Path) -> None:
    required = (
        "ANDROID_RELEASE_KEYSTORE",
        "ANDROID_RELEASE_KEY_ALIAS",
        "ANDROID_RELEASE_STORE_PASSWORD",
        "ANDROID_RELEASE_KEY_PASSWORD",
    )
    require(
        all(os.environ.get(name) for name in required),
        "Release signing credentials are incomplete",
    )
    keystore = Path(os.environ[required[0]]).resolve()
    require(
        keystore.is_file() and not keystore.is_relative_to(ROOT),
        "Signing keystore must be outside source checkout",
    )
    require(not output.exists(), "Signed output already exists")
    require(
        unsigned.is_file() and unsigned.resolve() != output.resolve(),
        "Invalid unsigned APK input",
    )
    output.parent.mkdir(parents=True, exist_ok=True)
    try:
        with tempfile.TemporaryDirectory(prefix="psst-android-sign-") as temporary:
            aligned = Path(temporary) / "aligned.apk"
            command(
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
            command(
                [
                    str(sdk_tools / "apksigner"),
                    "sign",
                    "--ks",
                    str(keystore),
                    "--ks-key-alias",
                    os.environ[required[1]],
                    "--ks-pass",
                    "env:ANDROID_RELEASE_STORE_PASSWORD",
                    "--key-pass",
                    "env:ANDROID_RELEASE_KEY_PASSWORD",
                    "--v4-signing-enabled",
                    "false",
                    "--debuggable-apk-permitted",
                    "false",
                    "--out",
                    str(output),
                    str(aligned),
                ]
            )
    except BaseException:
        output.unlink(missing_ok=True)
        raise


def check_publication(releases: list, tag: str, version: dict) -> None:
    require(isinstance(releases, list), "Invalid release listing")
    require(tag == "android-v" + version["versionName"], "Tag and version disagree")
    for release in releases:
        require(isinstance(release, dict), "Invalid release record")
        require(
            release.get("tag_name") != tag,
            "Android release already exists; refusing replacement",
        )
        if not str(release.get("tag_name", "")).startswith("android-v"):
            continue
        body = release.get("body") or ""
        codes = re.findall(r"^Android versionCode: ([0-9]+)$", body, re.MULTILINE)
        require(
            len(codes) == 1, "Existing Android release lacks trustworthy version code"
        )
        require(
            int(codes[0]) < version["versionCode"],
            "Android version code is reused or decreases",
        )


def package_release(
    apk: Path, verification_file: Path, tag: str, revision: str, output: Path
) -> None:
    report = json.loads(verification_file.read_text())
    require(
        report.get("signed") is True and report.get("sourceRevision") == revision,
        "Only verified signed APKs can be packaged",
    )
    require(bool(REVISION.fullmatch(revision)), "Invalid source revision")
    require(
        tag == "android-v" + report.get("versionName", ""),
        "Package tag/version mismatch",
    )
    require(
        command(["git", "rev-parse", "HEAD"]).strip() == revision,
        "Source archive checkout mismatch",
    )
    require(not output.exists(), "Publication directory already exists")
    with apk.open("rb") as stream:
        require(
            hashlib.file_digest(stream, "sha256").hexdigest()
            == report.get("apkSha256"),
            "APK changed after verification",
        )
    require(
        apk.stat().st_size == report.get("apkSize"),
        "APK size changed after verification",
    )
    output.mkdir(parents=True)
    apk_name = f"psst.zip-{report['versionName']}.apk"
    shutil.copyfile(apk, output / apk_name)
    write_json(output / "android-release.json", {**report, "tag": tag, "apk": apk_name})
    command(
        [
            "git",
            "archive",
            "--format=tar.gz",
            "--prefix=psst.zip/",
            "--output=" + str((output / "source.tar.gz").resolve()),
            revision,
        ]
    )
    legal = ROOT / "android/app/src/main/assets/licenses"
    with tarfile.open(output / "third-party-notices.tar.gz", "w:gz") as archive:
        for path in sorted(legal.rglob("*")):
            require(not path.is_symlink(), "Legal resource contains a symbolic link")
            if path.is_file():
                archive.add(
                    path,
                    arcname="licenses/" + path.relative_to(legal).as_posix(),
                    recursive=False,
                )
    instructions = (
        f"# psst.zip Android {report['versionName']}\n\n"
        f"Android versionCode: {report['versionCode']}\n\n"
        f"Source: `{revision}` (`{tag}`).\n\n"
        f"Signing certificate SHA256: `{report['signerSha256']}`.\n\n"
        "Install this APK only after verifying its checksum and signing certificate. "
        "Updates require the same package and compatible signer. Do not uninstall an "
        "existing debug installation to bypass a signing mismatch: uninstalling can "
        "destroy its private encryption keys and history. Complete the documented "
        "safe transition first. A normal same-signer update preserves app data.\n\n"
        "Build corresponding source with the checked-in Gradle wrapper: "
        "`PSST_ANDROID_UNSIGNED_RELEASE=true ./android/gradlew -p android :app:assembleRelease :app:lintRelease --no-daemon`. "
        "Use the SDK/JDK versions from the tagged Android release workflow. "
        "The unsigned output must be zipaligned and signed with your own private key; "
        "the publisher's key is intentionally not included.\n\n"
        "The source archive includes project source and build inputs. Packaged "
        "third-party notices and their source references are in `third-party-notices.tar.gz`. "
        "This release does not claim independent reproducible-build verification "
        "or availability in Google Play or F-Droid.\n"
    )
    (output / "INSTALL.md").write_text(instructions)
    lines = []
    for path in sorted(output.rglob("*")):
        if path.is_file():
            with path.open("rb") as stream:
                digest = hashlib.file_digest(stream, "sha256").hexdigest()
            lines.append(f"{digest}  {path.relative_to(output).as_posix()}\n")
    (output / "SHA256SUMS").write_text("".join(lines))


def report_timings(path: Path) -> dict:
    require(
        path.is_file() and not path.is_symlink() and path.stat().st_size <= 4 * 1024**2,
        "Task timing input exceeds bounds",
    )
    groups = {
        name: {"duration_ms": 0, "tasks": 0, "failures": 0}
        for name in (
            "R8 shrinking",
            "Lint",
            "Source tests",
            "Compile and assembly",
            "Other tasks",
        )
    }
    count = 0
    with path.open() as stream:
        for line in stream:
            require(len(line) <= 1024, "Task timing record exceeds bounds")
            record = json.loads(line)
            require(
                isinstance(record, dict)
                and set(record) == {"task", "duration_ms", "success"},
                "Unexpected task timing fields",
            )
            require(
                isinstance(record["task"], str)
                and bool(re.fullmatch(r":[A-Za-z0-9_.:-]{1,240}", record["task"])),
                "Invalid public task timing name",
            )
            require(
                type(record["duration_ms"]) is int
                and 0 <= record["duration_ms"] <= 24 * 60 * 60 * 1000,
                "Invalid task timing duration",
            )
            require(type(record["success"]) is bool, "Invalid task timing result")
            task = record["task"].rsplit(":", 1)[-1].lower()
            if "r8" in task or "minify" in task:
                group = "R8 shrinking"
            elif "lint" in task:
                group = "Lint"
            elif task.startswith("test") or "unittest" in task:
                group = "Source tests"
            elif any(
                word in task
                for word in (
                    "compile",
                    "assemble",
                    "package",
                    "dex",
                    "merge",
                    "process",
                    "generate",
                    "ksp",
                )
            ):
                group = "Compile and assembly"
            else:
                group = "Other tasks"
            groups[group]["duration_ms"] += record["duration_ms"]
            groups[group]["tasks"] += 1
            groups[group]["failures"] += not record["success"]
            count += 1
            require(count <= 10000, "Task timing input contains too many records")
    require(count > 0, "No task timing evidence was recorded")
    return {
        "schema_version": 1,
        "measurement": "sum of observed task durations; parallel tasks can overlap",
        "task_count": count,
        "groups": groups,
    }


def timing_summary(report: dict) -> str:
    lines = [
        "\nAndroid Gradle task timings (task sums can overlap; total build wall time is reported separately):\n\n",
        "| Work | Observed task seconds | Tasks | Failures |\n",
        "| --- | ---: | ---: | ---: |\n",
    ]
    for name, group in report["groups"].items():
        lines.append(
            f"| {name} | {group['duration_ms'] / 1000:.3f} | {group['tasks']} | {group['failures']} |\n"
        )
    return "".join(lines)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    source = sub.add_parser("validate-source")
    source.add_argument("--tag", required=True)
    source.add_argument("--expected-commit", required=True)
    source.add_argument("--main-ref", default="origin/main")
    source.add_argument(
        "--version-file", type=Path, default=ROOT / "android/release-version.properties"
    )
    source.add_argument("--output", type=Path, required=True)
    verify = sub.add_parser("verify-apk")
    verify.add_argument("--apk", type=Path, required=True)
    verify.add_argument("--sdk-tools", type=Path, required=True)
    signature = verify.add_mutually_exclusive_group(required=True)
    signature.add_argument("--signer-sha256")
    signature.add_argument("--unsigned", action="store_true")
    verify.add_argument(
        "--version-file", type=Path, default=ROOT / "android/release-version.properties"
    )
    verify.add_argument("--revision", required=True)
    verify.add_argument("--output", type=Path, required=True)
    sign = sub.add_parser("sign-apk")
    sign.add_argument("--unsigned", type=Path, required=True)
    sign.add_argument("--output", type=Path, required=True)
    sign.add_argument("--sdk-tools", type=Path, required=True)
    package = sub.add_parser("package")
    package.add_argument("--apk", type=Path, required=True)
    package.add_argument("--verification", type=Path, required=True)
    package.add_argument("--tag", required=True)
    package.add_argument("--revision", required=True)
    package.add_argument("--output-dir", type=Path, required=True)
    publication = sub.add_parser("check-publication")
    publication.add_argument("--releases-json", type=Path, required=True)
    publication.add_argument("--tag", required=True)
    publication.add_argument(
        "--version-file", type=Path, default=ROOT / "android/release-version.properties"
    )
    timings = sub.add_parser("report-timings")
    timings.add_argument("--input", type=Path, required=True)
    timings.add_argument("--summary", type=Path, required=True)
    timings.add_argument("--output", type=Path)
    args = parser.parse_args()
    try:
        if args.command == "validate-source":
            write_json(
                args.output,
                validate_source(
                    args.tag, args.expected_commit, args.main_ref, args.version_file
                ),
            )
        elif args.command == "verify-apk":
            write_json(
                args.output,
                verify_apk(
                    args.apk,
                    args.sdk_tools,
                    args.signer_sha256,
                    args.version_file,
                    args.revision,
                ),
            )
        elif args.command == "sign-apk":
            sign_apk(args.unsigned, args.output, args.sdk_tools)
        elif args.command == "package":
            package_release(
                args.apk, args.verification, args.tag, args.revision, args.output_dir
            )
        elif args.command == "report-timings":
            report = report_timings(args.input)
            with args.summary.open("a") as stream:
                stream.write(timing_summary(report))
            if args.output:
                write_json(args.output, report)
        else:
            check_publication(
                json.loads(args.releases_json.read_text()),
                args.tag,
                version_metadata(args.version_file),
            )
    except InvalidRelease as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1)
    except (
        OSError,
        ValueError,
        subprocess.SubprocessError,
        zipfile.BadZipFile,
        struct.error,
    ):
        # Exceptions may contain arbitrary tool output or paths: do not echo them.
        print(
            "Android release validation failed; no artifact is approved for publication.",
            file=sys.stderr,
        )
        raise SystemExit(1)


if __name__ == "__main__":
    main()
