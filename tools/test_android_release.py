"""Small release-boundary regressions; no Gradle, sleeps, network or secrets."""

import json
import os
import struct
import tempfile
import unittest
import warnings
import zipfile
from pathlib import Path
from unittest.mock import patch

import android_release as release


class AndroidReleaseTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="psst-android-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.version = self.root / "version.properties"
        self.version.write_text("versionName=0.1.0\nversionCode=2\n")
        self.identity = {"versionName": "0.1.0", "versionCode": 2}
        self.revision = "a" * 40
        self.apk = self.root / "release.apk"

    def make_apk(self, extras=(), identity=None):
        with zipfile.ZipFile(self.apk, "w") as archive:
            archive.writestr(
                "assets/psst-release.json",
                json.dumps(
                    identity or {**self.identity, "sourceRevision": self.revision}
                ),
            )
            for name in (
                "AGPL-3.0-only.txt",
                "THIRD_PARTY_NOTICES.txt",
                "dependency-inventory.json",
            ):
                archive.writestr(
                    "assets/licenses/" + name,
                    (
                        release.ROOT / "android/app/src/main/assets/licenses" / name
                    ).read_bytes(),
                )
            for name, data in extras:
                with warnings.catch_warnings():
                    warnings.simplefilter("ignore", UserWarning)
                    archive.writestr(name, data)

    def test_public_apk_rejects_private_rotation_history(self):
        def length(value):
            return struct.pack("<I", len(value)) + value

        def signed_block(identifier, value):
            self.make_apk()
            with zipfile.ZipFile(self.apk) as archive:
                central = archive.start_dir
            original = self.apk.read_bytes()
            pair = struct.pack("<QI", len(value) + 4, identifier) + value
            size = len(pair) + 24
            block = (
                struct.pack("<Q", size)
                + pair
                + struct.pack("<Q", size)
                + b"APK Sig Block 42"
            )
            raw = bytearray(original[:central] + block + original[central:])
            eocd = raw.rfind(b"PK\x05\x06")
            struct.pack_into("<I", raw, eocd + 16, central + len(block))
            self.apk.write_bytes(raw)

        def v3(attributes):
            signed_data = (
                length(b"")
                + length(b"")
                + struct.pack("<II", 28, 2147483647)
                + length(attributes)
            )
            return length(length(length(signed_data)))

        signed_block(0xF05368C0, v3(b""))
        release.reject_public_lineage(self.apk)
        with self.assertRaises(release.InvalidRelease):
            release.require_unsigned(self.apk)
        signed_block(
            0xF05368C0, v3(length(struct.pack("<I", 0x3BA06F8C) + b"private lineage"))
        )
        with self.assertRaises(release.InvalidRelease):
            release.reject_public_lineage(self.apk)
        signed_block(0x1B93AD61, b"private rotated signer")
        with self.assertRaises(release.InvalidRelease):
            release.reject_public_lineage(self.apk)

    def test_unsigned_preview_rejects_existing_apk_and_jar_signatures(self):
        self.make_apk()
        release.require_unsigned(self.apk)
        self.make_apk(extras=[("META-INF/TEST.RSA", b"public signature fixture")])
        with self.assertRaises(release.InvalidRelease):
            release.require_unsigned(self.apk)

    def test_timing_reports_cannot_include_credentials_or_markup(self):
        path = self.root / "timing.jsonl"
        good = {"task": ":app:minifyReleaseWithR8", "duration_ms": 25, "success": True}
        path.write_text(json.dumps(good) + "\n")
        report = release.report_timings(path)
        self.assertEqual(report["groups"]["R8 shrinking"]["duration_ms"], 25)
        for record in (
            dict(good, task=":task\n| injected summary |"),
            dict(good, password="not-public-metadata"),
            dict(good, duration_ms=-1),
        ):
            path.write_text(json.dumps(record) + "\n")
            with self.assertRaises(release.InvalidRelease):
                release.report_timings(path)

    def test_versions_reject_ambiguous_and_out_of_range_properties(self):
        self.assertEqual(release.version_metadata(self.version), self.identity)
        for text in (
            "versionName=0.1.0\nversionCode=2\n versionCode=3\n",
            "versionName=0.1.0\nversionCode=2100000001\n",
            "versionName=0.1.0\nversionCode=02\n",
        ):
            self.version.write_text(text)
            with self.assertRaises(release.InvalidRelease):
                release.version_metadata(self.version)

    def test_badging_rejects_debuggable_and_incomplete_manifests(self):
        badging = "package: name='zip.psst.android' versionCode='2' versionName='0.1.0'\nminSdkVersion:'26'\ntargetSdkVersion:'36'\n"
        self.assertEqual(release.parse_badging(badging)["versionCode"], 2)
        for value in (
            badging + "application-debuggable\n",
            badging.replace("minSdkVersion:'26'", ""),
        ):
            with self.assertRaises(release.InvalidRelease):
                release.parse_badging(value)

    def test_apk_requires_exact_packaged_source_and_legal_bytes(self):
        self.make_apk()
        report = release.inspect_apk_assets(self.apk, self.identity, self.revision)
        self.assertEqual(report["nativeAbis"], [])
        self.assertEqual(len(report["legalSha256"]), 3)
        self.make_apk(identity={**self.identity, "sourceRevision": "b" * 40})
        with self.assertRaises(release.InvalidRelease):
            release.inspect_apk_assets(self.apk, self.identity, self.revision)
        self.make_apk(extras=[("assets/licenses/THIRD_PARTY_NOTICES.txt", b"changed")])
        with self.assertRaises(release.InvalidRelease):
            release.inspect_apk_assets(self.apk, self.identity, self.revision)

    def test_unsafe_archive_path_cannot_pass_verification(self):
        self.make_apk(extras=[("../credentials", b"irrelevant")])
        with self.assertRaises(release.InvalidRelease):
            release.inspect_apk_assets(self.apk, self.identity, self.revision)

    def test_actual_elf_load_alignment_and_abi_are_checked(self):
        data = bytearray(128)
        data[:6] = b"\x7fELF\x02\x01"
        struct.pack_into("<H", data, 18, 183)
        struct.pack_into("<Q", data, 32, 64)
        struct.pack_into("<HH", data, 54, 56, 1)
        struct.pack_into("<I", data, 64, 1)
        struct.pack_into("<QQ", data, 72, 0, 0)
        struct.pack_into("<Q", data, 112, 16384)
        release.verify_elf(data, "arm64-v8a")
        with self.assertRaises(release.InvalidRelease):
            release.verify_elf(data, "x86_64")
        struct.pack_into("<Q", data, 112, 4096)
        with self.assertRaises(release.InvalidRelease):
            release.verify_elf(data)
        struct.pack_into("<Q", data, 112, 16384)
        struct.pack_into("<Q", data, 72, 4096)
        with self.assertRaises(release.InvalidRelease):
            release.verify_elf(data)

    def test_signature_mismatch_or_multiple_signers_rejected(self):
        self.make_apk()
        versions = release.tomllib.loads(
            (release.ROOT / "android/gradle/libs.versions.toml").read_text()
        )["versions"]
        badging = f"package: name='zip.psst.android' versionCode='2' versionName='0.1.0'\nminSdkVersion:'{versions['android-minSdk']}'\ntargetSdkVersion:'{versions['android-targetSdk']}'\n"
        good = (
            "Verified using v2 scheme (APK Signature Scheme v2): true\nSigner #1 certificate SHA-256 digest: "
            + "b" * 64
            + "\n"
        )
        for signature in (
            good.replace("b" * 64, "c" * 64),
            good + "Signer #2 certificate SHA-256 digest: " + "b" * 64 + "\n",
            good.replace(": true", ": false"),
        ):
            with (
                patch.object(release, "command", side_effect=[badging, "", signature]),
                self.assertRaises(release.InvalidRelease),
            ):
                release.verify_apk(
                    self.apk, self.root, "b" * 64, self.version, self.revision
                )
        with (
            patch.object(release, "command", side_effect=[badging, "", good]),
            patch.object(release, "reject_public_lineage"),
        ):
            self.assertTrue(
                release.verify_apk(
                    self.apk, self.root, "b" * 64, self.version, self.revision
                )["signed"]
            )

    def test_existing_release_or_version_downgrade_is_rejected(self):
        for records in (
            [{"tag_name": "android-v0.1.0", "body": "Android versionCode: 2"}],
            [{"tag_name": "android-v0.0.9", "body": "Android versionCode: 2"}],
            [{"tag_name": "android-v0.0.9", "body": "untracked code"}],
        ):
            with self.assertRaises(release.InvalidRelease):
                release.check_publication(records, "android-v0.1.0", self.identity)
        release.check_publication(
            [
                {"tag_name": "v0.1.6"},
                {"tag_name": "android-v0.0.9", "body": "Android versionCode: 1"},
            ],
            "android-v0.1.0",
            self.identity,
        )

    def test_tag_checkout_and_main_ancestry_are_bound(self):
        outputs = [self.revision, self.revision, "", "", "", self.version.read_text()]
        with patch.object(release, "command", side_effect=outputs):
            self.assertEqual(
                release.validate_source(
                    "android-v0.1.0", self.revision, "origin/main", self.version
                )["sourceRevision"],
                self.revision,
            )
        with (
            patch.object(release, "command", return_value="b" * 40),
            self.assertRaises(release.InvalidRelease),
        ):
            release.validate_source(
                "android-v0.1.0", self.revision, "origin/main", self.version
            )

    def test_missing_secrets_never_fall_back_to_debug_signing(self):
        with (
            patch.dict(os.environ, {}, clear=True),
            patch.object(release, "command") as command,
        ):
            with self.assertRaises(release.InvalidRelease):
                release.sign_apk(self.apk, self.root / "signed.apk", self.root)
            command.assert_not_called()

    def test_package_rejects_bytes_changed_after_verification(self):
        self.make_apk()
        report = self.root / "verification.json"
        report.write_text(
            json.dumps(
                {
                    "signed": True,
                    "sourceRevision": self.revision,
                    "versionName": "0.1.0",
                    "apkSha256": "b" * 64,
                    "apkSize": self.apk.stat().st_size,
                }
            )
        )
        with (
            patch.object(release, "command", return_value=self.revision),
            self.assertRaises(release.InvalidRelease),
        ):
            release.package_release(
                self.apk,
                report,
                "android-v0.1.0",
                self.revision,
                self.root / "publication",
            )
        self.assertFalse((self.root / "publication").exists())


if __name__ == "__main__":
    unittest.main()
