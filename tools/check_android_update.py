#!/usr/bin/env python3
"""Exercise one real-storage update fixture on an explicitly disposable emulator.

Supply already-built debug/release APKs and their matching instrumentation APKs.
This creates only disposable signing keys, never uses operator signing credentials,
never clears/uninstalls the target app, and never contacts a production server.
The caller owns emulator lifecycle; its AVD name must start psst-release-fixture-.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import secrets
import subprocess
import sys
import tempfile
from contextlib import contextmanager
from pathlib import Path

import android_release as release
import android_signing_bridge as bridge

TEST_PACKAGE = release.PACKAGE + ".test"
TEST_COMPONENT = TEST_PACKAGE + "/zip.psst.android.fixture.ReleaseUpdateInstrumentation"
TEST_METHOD = "zip.psst.android.data.ReleaseUpdateFixture#run"


@contextmanager
def environment(values: dict[str, str]):
    previous = {key: os.environ.get(key) for key in values}
    os.environ.update(values)
    try:
        yield
    finally:
        for key, value in previous.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value


class DisposableEmulator:
    def __init__(self, adb: Path, serial: str):
        release.require(
            bool(re.fullmatch(r"emulator-[0-9]+", serial)),
            "Only an explicitly selected emulator is supported",
        )
        self.prefix = [str(adb), "-s", serial]

    def run(self, *arguments: str, timeout: int = 120) -> str:
        return release.command([*self.prefix, *arguments], timeout=timeout)

    def validate(self) -> int:
        release.require(
            self.run("get-state").strip() == "device",
            "Disposable emulator is not connected",
        )
        release.require(
            self.run("shell", "getprop", "ro.kernel.qemu").strip() == "1",
            "Target is not an emulator",
        )
        release.require(
            self.run("shell", "getprop", "ro.hardware").strip()
            in {"ranchu", "goldfish"},
            "Unsupported emulator hardware",
        )
        name = self.run("emu", "avd", "name").splitlines()[0].strip()
        release.require(
            name.startswith("psst-release-fixture-"),
            "AVD is not owned by this disposable release fixture",
        )
        version = self.run("shell", "getprop", "ro.build.version.sdk").strip()
        release.require(
            version.isdigit() and int(version) >= 33,
            "Signing transition requires an Android 13+ emulator",
        )
        packages = self.run("shell", "pm", "list", "packages").splitlines()
        release.require(
            "package:" + release.PACKAGE not in packages
            and "package:" + TEST_PACKAGE not in packages,
            "Fixture refuses an emulator containing an existing app installation",
        )
        return int(version)

    def install(self, apk: Path, *, update: bool = False) -> None:
        arguments = ["install"]
        if update:
            arguments.append("-r")
        value = self.run(*arguments, str(apk))
        release.require(
            bool(re.search(r"^Success$", value, re.MULTILINE)),
            "Disposable APK install failed",
        )

    def fixture(self, phase: str) -> None:
        value = self.run(
            "shell",
            "am",
            "instrument",
            "-w",
            "-r",
            "-e",
            "class",
            TEST_METHOD,
            "-e",
            "disposableUpdate",
            "true",
            "-e",
            "updatePhase",
            phase,
            TEST_COMPONENT,
        )
        release.require(
            bool(re.search(r"\bOK \(1 test\)", value))
            and bool(re.search(r"^INSTRUMENTATION_CODE: -1\s*$", value, re.MULTILINE))
            and not any(
                marker in value
                for marker in (
                    "FAILURES!!!",
                    "INSTRUMENTATION_FAILED",
                    "INSTRUMENTATION_ABORTED",
                    "shortMsg=",
                )
            ),
            "Actual installed-storage update fixture failed",
        )

    def replace_test(self, apk: Path) -> None:
        # Only replace the disposable instrumentation package. Never the target.
        release.require(
            self.run("uninstall", TEST_PACKAGE).strip() == "Success",
            "Disposable instrumentation removal failed",
        )
        self.install(apk)


def disposable_key(keytool: Path, directory: Path, name: str) -> tuple[Path, str]:
    keystore = directory / (name + ".p12")
    certificate = directory / (name + ".der")
    release.command(
        [
            str(keytool),
            "-genkeypair",
            "-alias",
            "fixture",
            "-keystore",
            str(keystore),
            "-storetype",
            "PKCS12",
            "-keyalg",
            "RSA",
            "-keysize",
            "2048",
            "-validity",
            "3",
            "-dname",
            "CN=Disposable release update fixture",
            "-storepass:env",
            "PSST_FIXTURE_PASSWORD",
            "-keypass:env",
            "PSST_FIXTURE_PASSWORD",
            "-noprompt",
        ]
    )
    release.command(
        [
            str(keytool),
            "-exportcert",
            "-alias",
            "fixture",
            "-keystore",
            str(keystore),
            "-storepass:env",
            "PSST_FIXTURE_PASSWORD",
            "-file",
            str(certificate),
        ]
    )
    os.chmod(keystore, 0o600)
    return keystore, hashlib.sha256(certificate.read_bytes()).hexdigest()


def sign_fixture(
    apk: Path, output: Path, sdk_tools: Path, keystore: Path, *, debuggable: bool
) -> None:
    aligned = output.with_suffix(".aligned.apk")
    try:
        release.command(
            [str(sdk_tools / "zipalign"), "-P", "16", "-f", "4", str(apk), str(aligned)]
        )
        release.command(
            [
                str(sdk_tools / "apksigner"),
                "sign",
                "--ks",
                str(keystore),
                "--ks-key-alias",
                "fixture",
                "--ks-pass",
                "env:PSST_FIXTURE_PASSWORD",
                "--key-pass",
                "env:PSST_FIXTURE_PASSWORD",
                "--v4-signing-enabled",
                "false",
                "--debuggable-apk-permitted",
                str(debuggable).lower(),
                "--out",
                str(output),
                str(aligned),
            ]
        )
    finally:
        aligned.unlink(missing_ok=True)


def fixture_version(directory: Path, code: int, name: str) -> Path:
    path = directory / f"version-{code}.properties"
    path.write_text(f"versionName={name}\nversionCode={code}\n")
    return path


def run_update(args: argparse.Namespace) -> None:
    release.require(
        os.environ.get("GITHUB_ACTIONS", "").lower() != "true",
        "Private signing transition is a local disposable fixture",
    )
    release.require(not args.output.exists(), "Device evidence output already exists")
    emulator = DisposableEmulator(args.adb, args.serial)
    api = emulator.validate()
    apks = [args.seed_apk, args.bridge_unsigned, args.next_unsigned]
    manifests = [
        release.parse_badging(
            release.command(
                [str(args.sdk_tools / "aapt2"), "dump", "badging", str(apk)]
            ),
            allow_debuggable=index == 0,
        )
        for index, apk in enumerate(apks)
    ]
    release.require(
        all(value["applicationId"] == release.PACKAGE for value in manifests),
        "Fixture target package differs",
    )
    codes = [value["versionCode"] for value in manifests]
    release.require(
        codes[0] < codes[1] < codes[2],
        "Fixture must use increasing debug, bridge and production version codes",
    )
    release.require(
        len({value["versionName"] for value in manifests}) == 1,
        "Fixture version names differ",
    )
    for apk, manifest in zip(apks, manifests, strict=True):
        release.inspect_apk_assets(
            apk,
            {key: manifest[key] for key in ("versionName", "versionCode")},
            args.revision,
            device_fixture=True,
        )
    with tempfile.TemporaryDirectory(prefix="psst-release-update-") as temporary:
        directory = Path(temporary)
        with environment({"PSST_FIXTURE_PASSWORD": secrets.token_urlsafe(36)}):
            old_key, old_fingerprint = disposable_key(
                args.keytool, directory, "disposable-debug"
            )
            new_key, new_fingerprint = disposable_key(
                args.keytool, directory, "disposable-production"
            )
            seed = directory / "seed.apk"
            seed_test = directory / "seed-test.apk"
            sign_fixture(args.seed_apk, seed, args.sdk_tools, old_key, debuggable=True)
            sign_fixture(
                args.seed_test_apk, seed_test, args.sdk_tools, old_key, debuggable=True
            )
            values = {}
            for prefix, keystore in (
                ("ANDROID_DEBUG", old_key),
                ("ANDROID_RELEASE", new_key),
            ):
                values.update(
                    {
                        prefix + "_KEYSTORE": str(keystore),
                        prefix + "_KEY_ALIAS": "fixture",
                        prefix + "_STORE_PASSWORD": os.environ["PSST_FIXTURE_PASSWORD"],
                        prefix + "_KEY_PASSWORD": os.environ["PSST_FIXTURE_PASSWORD"],
                    }
                )
            with environment(values):
                private = directory / "migration"
                bridge.prepare_bridge(
                    seed,
                    args.bridge_unsigned,
                    args.sdk_tools,
                    fixture_version(directory, codes[1], manifests[1]["versionName"]),
                    args.revision,
                    old_fingerprint,
                    new_fingerprint,
                    api,
                    codes[0],
                    private,
                    device_fixture=True,
                )
                production = directory / "production-only.apk"
                release.sign_apk(args.next_unsigned, production, args.sdk_tools)
                release.verify_apk(
                    production,
                    args.sdk_tools,
                    new_fingerprint,
                    fixture_version(directory, codes[2], manifests[2]["versionName"]),
                    args.revision,
                    device_fixture=True,
                )
            bridge_test = directory / "bridge-test.apk"
            next_test = directory / "next-test.apk"
            sign_fixture(
                args.bridge_test_apk,
                bridge_test,
                args.sdk_tools,
                new_key,
                debuggable=True,
            )
            sign_fixture(
                args.next_test_apk, next_test, args.sdk_tools, new_key, debuggable=True
            )
            emulator.install(seed)
            emulator.install(seed_test)
            emulator.fixture("seed")
            emulator.run("shell", "am", "force-stop", release.PACKAGE)
            emulator.install(private / "private-debug-to-production.apk", update=True)
            emulator.replace_test(bridge_test)
            emulator.fixture("verify")
            emulator.run("shell", "am", "force-stop", release.PACKAGE)
            emulator.install(production, update=True)
            emulator.replace_test(next_test)
            emulator.fixture("verify")
            release.write_json(
                args.output,
                {
                    "schema_version": 1,
                    "sourceRevision": args.revision,
                    "deviceApi": api,
                    "disposableEmulator": True,
                    "disposableSigningKeys": True,
                    "fixtureModifiedApks": True,
                    "applicationId": release.PACKAGE,
                    "versionCodes": codes,
                    "debugToProductionBridgePassed": True,
                    "laterProductionOnlyUpdatePassed": True,
                    "preservedState": [
                        "application UID",
                        "Keystore session",
                        "Room Send history and file decryption",
                        "Receive-v2 private key and HPKE/AES decryption",
                        "guest history and encrypted capability",
                    ],
                    "operatorInstallationVerified": False,
                    "networkPairingShareQrVerified": False,
                },
            )


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in (
        "seed-apk",
        "bridge-unsigned",
        "next-unsigned",
        "seed-test-apk",
        "bridge-test-apk",
        "next-test-apk",
        "sdk-tools",
        "adb",
        "keytool",
        "output",
    ):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--serial", required=True)
    parser.add_argument("--revision", required=True)
    args = parser.parse_args()
    try:
        run_update(args)
    except release.InvalidRelease as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1)
    except (OSError, ValueError, subprocess.SubprocessError):
        print(
            "Disposable Android update check failed; preservation is unverified.",
            file=sys.stderr,
        )
        raise SystemExit(1)


if __name__ == "__main__":
    main()
