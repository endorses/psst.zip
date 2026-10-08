"""Small privacy and identity checks for public publication diagnostics."""

import copy
import os
from pathlib import Path
import tempfile
import unittest

import publication_diagnostics as diagnostics
from release_artifacts import InvalidRelease, json_bytes


class PublicationDiagnostics(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.state = Path(directory.name)
        self.arguments = {
            "repository": "endorses/psst.zip",
            "version": "v1.2.3",
            "commit": "a" * 40,
            "run_id": "77",
            "attempt": "1",
            "outcome": "failure",
        }
        self.identity = {
            "repository": "endorses/psst.zip",
            "version": "v1.2.3",
            "commit": "a" * 40,
            "run_id": 77,
            "attempt": 1,
        }
        self.rows = [
            self.row(1, "begin", "transaction", self.identity),
            self.row(2, "intent", "upload-private-fixture.tar.gz", {"token": "secret"}),
            self.row(
                3, "uncertain", "upload-private-fixture.tar.gz", {"error": "secret"}
            ),
            self.row(4, "stopped", "transaction", {"details": "secret"}),
        ]
        self.journal = self.state / "v1.2.3.jsonl"

    def row(self, sequence, phase, operation, details):
        return {
            "sequence": sequence,
            "binding": "sha256:" + "b" * 64,
            "phase": phase,
            "operation": operation,
            "details": details,
        }

    def write(self, rows=None):
        self.journal.write_bytes(
            b"\n".join(
                json_bytes(row).replace(b"\n", b" ") for row in (rows or self.rows)
            )
            + b"\n"
        )

    def test_partial_failure_exports_only_identity_progress_and_hash(self):
        self.write()
        result = diagnostics.project(self.state, **self.arguments)
        raw = json_bytes(result)
        self.assertNotIn(b"secret", raw)
        self.assertNotIn(b"private-fixture", raw)
        self.assertEqual(
            result["records"][2],
            {"sequence": 3, "phase": "uncertain", "operation": "upload-asset"},
        )
        self.assertEqual(
            set(result),
            {
                "schema_version",
                "kind",
                "repository",
                "version",
                "commit",
                "run_id",
                "attempt",
                "step_outcome",
                "journal_present",
                "records",
                "binding_digest",
                "journal_sha256",
            },
        )
        self.assertLess(len(raw), diagnostics.MAX_OUTPUT)

    def test_not_started_is_explicit_and_has_no_fake_receipt_or_binding(self):
        result = diagnostics.project(
            self.state, **(self.arguments | {"outcome": "skipped"})
        )
        self.assertFalse(result["journal_present"])
        self.assertEqual(result["records"], [])
        self.assertNotIn("binding_digest", result)

    def test_wrong_attempt_unknown_operation_and_broken_sequence_are_rejected(self):
        for mutation in ("identity", "operation", "sequence", "binding"):
            with self.subTest(mutation=mutation):
                rows = copy.deepcopy(self.rows)
                if mutation == "identity":
                    rows[0]["details"]["attempt"] = 2
                elif mutation == "operation":
                    rows[1]["operation"] = "token-secret"
                elif mutation == "sequence":
                    rows[1]["sequence"] = True
                else:
                    rows[1]["binding"] = "sha256:" + "c" * 64
                self.write(rows)
                with self.assertRaises(InvalidRelease):
                    diagnostics.project(self.state, **self.arguments)

    def test_fifo_is_rejected_without_waiting_for_a_writer(self):
        os.mkfifo(self.journal)
        with self.assertRaisesRegex(InvalidRelease, "not regular"):
            diagnostics.project(self.state, **self.arguments)

    def test_oversized_symlink_and_incomplete_journals_are_not_uploaded(self):
        self.journal.write_bytes(b"x" * (diagnostics.MAX_JOURNAL + 1))
        with self.assertRaises(InvalidRelease):
            diagnostics.project(self.state, **self.arguments)
        self.journal.unlink()
        self.journal.symlink_to(self.state / "absent")
        with self.assertRaises(OSError):
            diagnostics.project(self.state, **self.arguments)
        self.journal.unlink()
        self.write()
        with self.journal.open("ab") as stream:
            stream.write(b'{"sequence":')
        with self.assertRaises(ValueError):
            diagnostics.project(self.state, **self.arguments)


if __name__ == "__main__":
    unittest.main()
