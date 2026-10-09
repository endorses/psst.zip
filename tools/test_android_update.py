"""Guard the disposable update runner from touching phones or existing app data."""

import os
import unittest
from pathlib import Path
from unittest.mock import patch

import android_release as release
import check_android_update as update


class DisposableUpdateTest(unittest.TestCase):
    def test_physical_device_serial_is_refused_without_any_adb_command(self):
        with (
            patch.object(release, "command") as command,
            self.assertRaises(release.InvalidRelease),
        ):
            update.DisposableEmulator(Path("adb"), "real-phone-serial")
        command.assert_not_called()

    def test_existing_target_or_unowned_avd_is_refused(self):
        emulator = update.DisposableEmulator(Path("adb"), "emulator-5554")
        for outputs in (
            ["device", "1", "ranchu", "Medium_Phone\nOK"],
            [
                "device",
                "1",
                "ranchu",
                "psst-release-fixture-test\nOK",
                "36",
                "package:zip.psst.android\n",
            ],
        ):
            with (
                patch.object(emulator, "run", side_effect=outputs),
                self.assertRaises(release.InvalidRelease),
            ):
                emulator.validate()
        with patch.object(
            emulator,
            "run",
            side_effect=[
                "device",
                "1",
                "ranchu",
                "psst-release-fixture-test\nOK",
                "36",
                "package:android\n",
            ],
        ):
            self.assertEqual(emulator.validate(), 36)

    def test_instrumentation_failure_cannot_be_reported_as_preservation(self):
        emulator = update.DisposableEmulator(Path("adb"), "emulator-5554")
        for result in (
            "FAILURES!!!",
            "OK (1 test)\nINSTRUMENTATION_FAILED: crash",
            "INSTRUMENTATION_CODE: 0",
            "OK (1 test)\nINSTRUMENTATION_CODE: 0",
            "OK (1 test)",
        ):
            with (
                patch.object(emulator, "run", return_value=result),
                self.assertRaises(release.InvalidRelease),
            ):
                emulator.fixture("verify")
        with patch.object(
            emulator, "run", return_value="OK (1 test)\nINSTRUMENTATION_CODE: -1"
        ):
            emulator.fixture("verify")

    def test_disposable_password_environment_is_restored_after_failure(self):
        with patch.dict(
            os.environ, {"PSST_FIXTURE_PASSWORD": "previous-fixture"}, clear=True
        ):
            with (
                self.assertRaises(RuntimeError),
                update.environment(
                    {
                        "PSST_FIXTURE_PASSWORD": "temporary-fixture",
                        "NEW_FIXTURE_VALUE": "temporary",
                    }
                ),
            ):
                raise RuntimeError("disposable signing failed")
            self.assertEqual(os.environ["PSST_FIXTURE_PASSWORD"], "previous-fixture")
            self.assertNotIn("NEW_FIXTURE_VALUE", os.environ)


if __name__ == "__main__":
    unittest.main()
