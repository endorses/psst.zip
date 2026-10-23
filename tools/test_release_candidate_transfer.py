"""The remaining Python transfer surface delegates to the bounded Go reader."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import prepare_candidate_transfer as transfer
from generate_release_gate_reports import NativeSourceContext
from release_artifacts import InvalidRelease, json_bytes


class CandidateTransferAdapters(unittest.TestCase):
    def setUp(self):
        self.context = NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", "a" * 40, "linux/amd64"
        )

    def test_collect_and_verify_keep_exact_source_and_path_arguments(self):
        with tempfile.TemporaryDirectory() as temporary:
            private = Path(temporary) / "private"
            output = Path(temporary) / "transfer"
            record = {
                "schema_version": 1,
                "publication_authorized": False,
                "measurement_authentication_required": True,
            }
            with patch.object(
                transfer, "_invoke", return_value=json_bytes(record)
            ) as invoke:
                self.assertEqual(
                    transfer.stage_native(self.context, private, output, "version-tag"),
                    record,
                )
                self.assertEqual(invoke.call_args.args[1:3], ("transfer", "collect"))
                self.assertEqual(
                    invoke.call_args.args[-4:],
                    ("--private", str(private), "--output", str(output)),
                )
                self.assertEqual(
                    transfer.verify_native(self.context, output, "version-tag"), record
                )
                self.assertEqual(invoke.call_args.args[1:3], ("transfer", "verify"))
                self.assertEqual(
                    invoke.call_args.args[-2:], ("--transfer", str(output))
                )
                self.assertIn(self.context.commit, invoke.call_args.args)

    def test_historical_browser_and_unknown_kind_require_explicit_archived_helpers(
        self,
    ):
        with patch.object(transfer, "_invoke") as invoke:
            with self.assertRaises(InvalidRelease):
                transfer.verify_native(
                    self.context,
                    Path("unused"),
                    "version-tag",
                    allow_legacy_browser=True,
                )
            with self.assertRaises(InvalidRelease):
                transfer.stage_native(
                    self.context, Path("unused"), Path("new"), "other"
                )
            invoke.assert_not_called()


if __name__ == "__main__":
    unittest.main()
