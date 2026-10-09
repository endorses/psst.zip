"""Private migration boundaries; these do not replace actual Android update tests."""

import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import android_release as release
import android_signing_bridge as bridge


class SigningBridgeTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="psst-bridge-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.old = "a" * 64
        self.new = "b" * 64

    def lineage(self):
        result = ""
        for index, certificate in enumerate((self.old, self.new), start=1):
            result += (
                f"Signer #{index} in lineage certificate DN: public test identity\n"
            )
            result += f"Signer #{index} in lineage certificate SHA-256 digest: {certificate}\n"
            for name, flag in bridge.CAPABILITIES.items():
                result += f"Has {name} capability : {str(flag).lower()}\n"
        return result

    def test_lineage_requires_exact_order_and_restrictive_capabilities(self):
        value = self.lineage()
        bridge.verify_lineage(value, self.old, self.new)
        for changed in (
            value.replace(self.old, self.new),
            value.replace(
                "Has rollback capability : false", "Has rollback capability : true"
            ),
            value.replace(
                "Has installed data capability : true",
                "Has installed data capability : false",
            ),
            value.replace(
                "Has permission capability : true", "Has permission capability : false"
            ),
        ):
            with self.assertRaises(release.InvalidRelease):
                bridge.verify_lineage(changed, self.old, self.new)

    def test_private_output_cannot_be_checkout_public_directory_or_ci(self):
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(
                bridge.private_output(self.root / "migration"), self.root / "migration"
            )
            for path in (
                release.ROOT / "private-migration",
                self.root / "artifacts" / "migration",
            ):
                with self.assertRaises(release.InvalidRelease):
                    bridge.private_output(path)
        with (
            patch.dict(os.environ, {"GITHUB_ACTIONS": "true"}, clear=True),
            self.assertRaises(release.InvalidRelease),
        ):
            bridge.private_output(self.root / "migration")

    def test_android12_and_earlier_fail_before_reading_private_material(self):
        with (
            patch.object(bridge, "signer_arguments") as credentials,
            self.assertRaises(release.InvalidRelease),
        ):
            bridge.prepare_bridge(
                self.root / "old.apk",
                self.root / "unsigned.apk",
                self.root,
                self.root / "version",
                "a" * 40,
                self.old,
                self.new,
                32,
                1,
                self.root / "migration",
            )
        credentials.assert_not_called()

    def test_reused_bridge_version_fails_before_credentials(self):
        with (
            patch.object(bridge, "verify_old_apk", return_value={"versionCode": 1}),
            patch.object(release, "verify_apk", return_value={"versionCode": 2}),
            patch.object(bridge, "signer_arguments") as credentials,
            self.assertRaises(release.InvalidRelease),
        ):
            bridge.prepare_bridge(
                self.root / "old.apk",
                self.root / "unsigned.apk",
                self.root,
                self.root / "version",
                "a" * 40,
                self.old,
                self.new,
                36,
                2,
                self.root / "migration",
            )
        credentials.assert_not_called()
        self.assertFalse((self.root / "migration").exists())

    def test_bridge_inputs_fail_closed_and_passwords_are_environment_references(self):
        with (
            patch.dict(os.environ, {}, clear=True),
            self.assertRaises(release.InvalidRelease),
        ):
            bridge.signer_arguments("ANDROID_DEBUG")
        keystore = self.root / "test.keystore"
        keystore.write_bytes(b"disposable non-key fixture")
        environment = {
            "ANDROID_DEBUG_KEYSTORE": str(keystore),
            "ANDROID_DEBUG_KEY_ALIAS": "test",
            "ANDROID_DEBUG_STORE_PASSWORD": "do-not-log-this-fixture",
            "ANDROID_DEBUG_KEY_PASSWORD": "nor-this-fixture",
        }
        with patch.dict(os.environ, environment, clear=True):
            arguments = bridge.signer_arguments("ANDROID_DEBUG")
            self.assertIn("env:ANDROID_DEBUG_STORE_PASSWORD", arguments)
            self.assertIn("env:ANDROID_DEBUG_KEY_PASSWORD", arguments)
            self.assertNotIn(environment["ANDROID_DEBUG_STORE_PASSWORD"], arguments)
            self.assertNotIn(environment["ANDROID_DEBUG_KEY_PASSWORD"], arguments)
        capabilities = bridge.capability_arguments()
        self.assertEqual(
            capabilities[capabilities.index("--set-rollback") + 1], "false"
        )
        self.assertEqual(
            capabilities[capabilities.index("--set-permission") + 1], "true"
        )


if __name__ == "__main__":
    unittest.main()
