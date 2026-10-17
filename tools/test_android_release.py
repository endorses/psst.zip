"""Small release-boundary regressions; no Gradle, sleeps, network or secrets."""

import contextlib
import io
import json
import os
import struct
import subprocess
import tempfile
import unittest
import warnings
import zipfile
from pathlib import Path
from unittest.mock import patch

import android_release as release


class AndroidSourceRevisionTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="psst-source-revision-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.paths = sorted(release.SCANNER_WRAPPER_DELETIONS) + [
            "android/app/src/main/Main.kt",
            "android/gradle/wrapper/gradle-wrapper.properties",
            "shared/src/commonMain/Shared.kt",
            "LICENSE",
            "tools/android_release.py",
        ]
        for name in self.paths:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("tracked fixture\n")
        self.git("init", "-q")
        self.git("add", ".")
        self.git(
            "-c",
            "user.name=Source Test",
            "-c",
            "user.email=source-test@example.invalid",
            "-c",
            "commit.gpgsign=false",
            "commit",
            "-qm",
            "fixture",
        )
        self.revision = self.git("rev-parse", "HEAD").strip()
        root_patch = patch.object(release, "ROOT", self.root)
        root_patch.start()
        self.addCleanup(root_patch.stop)

    def git(self, *arguments):
        return subprocess.run(
            ["git", *arguments],
            cwd=self.root,
            capture_output=True,
            text=True,
            check=True,
        ).stdout

    def test_pristine_and_exact_unstaged_scanner_deletions_retain_head(self):
        self.assertEqual(release.source_revision(), self.revision)
        # Each deletion, and the cumulative six-file scanner cleanup, is allowed.
        for name in sorted(release.SCANNER_WRAPPER_DELETIONS):
            with self.subTest(path=name):
                (self.root / name).unlink()
                self.assertEqual(release.source_revision(), self.revision)

    def test_modified_or_deleted_source_and_helper_have_no_identity(self):
        for name in self.paths:
            for action in ("modify", "delete"):
                with self.subTest(path=name, action=action):
                    self.git("reset", "--hard", "HEAD")
                    path = self.root / name
                    if action == "modify":
                        path.write_text("changed\n")
                    else:
                        path.unlink()
                    expected = (
                        self.revision
                        if action == "delete"
                        and name in release.SCANNER_WRAPPER_DELETIONS
                        else ""
                    )
                    self.assertEqual(release.source_revision(), expected)

    def test_staged_deletions_modifications_and_renames_have_no_identity(self):
        for name in sorted(release.SCANNER_WRAPPER_DELETIONS):
            for action in ("delete", "modify", "rename"):
                with self.subTest(path=name, action=action):
                    self.git("reset", "--hard", "HEAD")
                    path = self.root / name
                    if action == "delete":
                        self.git("rm", name)
                    elif action == "modify":
                        path.write_text("changed\n")
                        self.git("add", name)
                    else:
                        self.git("mv", name, name + ".renamed")
                    self.assertEqual(release.source_revision(), "")

    def test_untracked_mobile_source_and_unstaged_rename_have_no_identity(self):
        for name in ("android/untracked\nsource.kt", "shared/untracked.kt"):
            with self.subTest(path=name):
                path = self.root / name
                path.write_text("untracked\n")
                self.assertEqual(release.source_revision(), "")
                path.unlink()
        (self.root / "android/gradlew").rename(self.root / "android/gradlew.renamed")
        self.assertEqual(release.source_revision(), "")

    def test_unrelated_files_do_not_dirty_mobile_build_inputs(self):
        (self.root / "notes.txt").write_text("unrelated\n")
        self.assertEqual(release.source_revision(), self.revision)

    def test_cli_returns_only_revision_or_empty_for_missing_git_metadata(self):
        for expected in (self.revision, ""):
            with self.subTest(expected=expected):
                output = io.StringIO()
                with (
                    patch("sys.argv", ["android_release.py", "source-revision"]),
                    contextlib.redirect_stdout(output),
                ):
                    release.main()
                self.assertEqual(output.getvalue(), expected + "\n")
                if expected:
                    (self.root / ".git").rename(self.root / ".git-unavailable")


class AndroidCommandDiagnosticsTest(unittest.TestCase):
    def test_failures_distinguish_alignment_signing_and_verification_without_output(
        self,
    ):
        canary = "private-canary-alias-path-password"
        for executable, action, operation in (
            ("zipalign", "-P", "apk-align"),
            ("apksigner", "sign", "apk-sign"),
            ("apksigner", "verify", "apk-verify"),
        ):
            arguments = [f"/{canary}/{executable}", action, canary]
            result = subprocess.CompletedProcess(
                arguments, 7, stdout=canary.encode(), stderr=canary.encode()
            )
            with (
                self.subTest(operation=operation),
                patch.object(release.subprocess, "run", return_value=result),
                self.assertRaises(release.InvalidRelease) as rejected,
            ):
                release.command(arguments)
            self.assertEqual(
                str(rejected.exception),
                f"Android release command failed: {operation} (exit 7)"
                " [category=unclassified]",
            )
            self.assertNotIn(canary, str(rejected.exception))

    def test_signing_classifications_only_emit_fixed_categories(self):
        canary = b"private-canary-password-and-path"
        for marker, expected in (
            (
                b"Keystore was tampered with, or password was incorrect",
                "store-password-or-format",
            ),
            (
                b"keystore password was incorrect: UnrecoverableKeyException",
                "store-password-or-format",
            ),
            (b"Keystore does not contain key with alias", "alias-or-private-key"),
            (b"UnrecoverableKeyException: Cannot recover key", "key-password"),
            (b"java.security.NoSuchAlgorithmException", "java-provider-or-algorithm"),
            (b"Refusing to sign debuggable APK", "apk-policy"),
            (b"Permission denied", "permission-or-disk"),
            (b"No space left on device", "permission-or-disk"),
            (b"Disk quota exceeded", "permission-or-disk"),
            (b"unknown failure", "unclassified"),
        ):
            arguments = ["apksigner", "sign"]
            result = subprocess.CompletedProcess(
                arguments, 1, stdout=canary, stderr=marker + b": " + canary
            )
            with (
                self.subTest(expected=expected, marker=marker),
                patch.object(release.subprocess, "run", return_value=result),
                self.assertRaises(release.InvalidRelease) as rejected,
            ):
                release.command(arguments)
            self.assertEqual(
                str(rejected.exception),
                "Android release command failed: apk-sign (exit 1)"
                f" [category={expected}]",
            )
            self.assertNotIn(canary.decode(), str(rejected.exception))
        self.assertEqual(
            release.command_failure_category(
                b"x" * 65536 + b"Permission denied", signing=True
            ),
            "unclassified",
        )

    def test_timeout_and_launch_errors_suppress_exception_details(self):
        canary = "private-canary-password-and-path"
        for error, expected in (
            (
                subprocess.TimeoutExpired([canary], 17, output=canary, stderr=canary),
                "Android release command timed out: apk-sign",
            ),
            (
                FileNotFoundError(2, canary, canary),
                "Android release command could not start: apk-sign [category=unclassified]",
            ),
            (
                PermissionError(13, canary, canary),
                "Android release command could not start: apk-sign [category=permission-or-disk]",
            ),
        ):
            with (
                self.subTest(error=type(error).__name__),
                patch.object(release.subprocess, "run", side_effect=error),
                self.assertRaises(release.InvalidRelease) as rejected,
            ):
                release.command(["apksigner", "sign", canary])
            self.assertEqual(str(rejected.exception), expected)
            self.assertTrue(rejected.exception.__suppress_context__)

    def test_failed_sign_removes_partial_output_and_keeps_passwords_in_environment(
        self,
    ):
        with tempfile.TemporaryDirectory(prefix="psst-android-sign-test-") as temporary:
            root = Path(temporary)
            unsigned = root / "unsigned.apk"
            unsigned.write_bytes(b"disposable non-APK fixture")
            keystore = root / "private.keystore"
            keystore.write_bytes(b"disposable non-key fixture")
            output = root / "signed.apk"
            canary = "private-canary-password"
            environment = {
                "ANDROID_RELEASE_KEYSTORE": str(keystore),
                "ANDROID_RELEASE_KEY_ALIAS": "private-canary-alias",
                "ANDROID_RELEASE_STORE_PASSWORD": canary,
                "ANDROID_RELEASE_KEY_PASSWORD": canary,
            }

            def execute(arguments, **kwargs):
                if Path(arguments[0]).name == "apksigner":
                    output.write_bytes(b"partial signed output")
                    self.assertIn("env:ANDROID_RELEASE_STORE_PASSWORD", arguments)
                    self.assertIn("env:ANDROID_RELEASE_KEY_PASSWORD", arguments)
                    self.assertNotIn(canary, arguments)
                    return subprocess.CompletedProcess(
                        arguments, 1, stdout=canary.encode(), stderr=canary.encode()
                    )
                return subprocess.CompletedProcess(arguments, 0, stdout=b"", stderr=b"")

            with (
                patch.dict(os.environ, environment, clear=True),
                patch.object(release.subprocess, "run", side_effect=execute) as run,
                self.assertRaisesRegex(release.InvalidRelease, "apk-sign"),
            ):
                release.sign_apk(unsigned, output, root)
            self.assertEqual(run.call_count, 2)
            self.assertFalse(output.exists())


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
        signed_block(0x70E1C89F, b"private SDK 37 rotated signer")
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
        with self.assertRaises(release.InvalidRelease):
            release.inspect_apk_assets(
                self.apk, self.identity, self.revision, device_fixture=True
            )
        self.make_apk(identity={**self.identity, "sourceRevision": "b" * 40})
        with self.assertRaises(release.InvalidRelease):
            release.inspect_apk_assets(self.apk, self.identity, self.revision)
        self.make_apk(
            identity={
                **self.identity,
                "sourceRevision": self.revision,
                "deviceFixture": True,
            }
        )
        with self.assertRaises(release.InvalidRelease):
            release.inspect_apk_assets(self.apk, self.identity, self.revision)
        release.inspect_apk_assets(
            self.apk, self.identity, self.revision, device_fixture=True
        )
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
            "Verified using v2 scheme (APK Signature Scheme v2): true\nNumber of signers: 1\nSigner #1 certificate SHA-256 digest: "
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

    def test_sdk37_signer_labels_keep_single_identity_and_rotation_boundaries(self):
        digest = "b" * 64
        for label in ("Signer #1", "V2 Signer:", "V3.0 Signer:"):
            value = (
                f"Number of signers: 1\n{label} certificate SHA-256 digest: {digest}\n"
            )
            release.verify_signer_output(value, digest)
        repeated = (
            f"Number of signers: 1\nV2 Signer: certificate SHA-256 digest: {digest}\n"
            f"V3.0 Signer: certificate SHA-256 digest: {digest}\n"
            f"Source Stamp Signer: certificate SHA-256 digest: {'c' * 64}\n"
        )
        release.verify_signer_output(repeated, digest)
        rotated = (
            "Number of signers: 1\n"
            f"V3.1 Signer: (minSdkVersion=33, maxSdkVersion=2147483647) certificate SHA-256 digest: {digest}\n"
            f"V3.0 Signer: (minSdkVersion=28, maxSdkVersion=32) certificate SHA-256 digest: {'a' * 64}\n"
        )
        release.verify_signer_output(rotated, digest, device_api=36)
        for value, api in (
            (rotated, None),
            (rotated, 32),
            (rotated, 27),
            (repeated.replace("Number of signers: 1", "Number of signers: 2"), None),
            (repeated + f"V2 Signer #2: certificate SHA-256 digest: {digest}\n", None),
            (repeated + "V2 Signer: certificate SHA-256 digest: invalid\n", None),
            (repeated.replace("V2 Signer:", "Unknown Signer:"), None),
            (rotated.replace("maxSdkVersion=32", "maxSdkVersion=36"), 36),
        ):
            with (
                self.subTest(value=value, api=api),
                self.assertRaises(release.InvalidRelease),
            ):
                release.verify_signer_output(value, digest, device_api=api)

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
