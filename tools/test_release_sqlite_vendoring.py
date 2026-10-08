"""Small source-identity regression; Go tests own the syntax transformation cases."""

from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

from publish_container_release import sha256
from release_artifacts import InvalidRelease, json_bytes
import sqlite_vendoring


class VendoringProtocol(unittest.TestCase):
    def test_checker_reused_and_receipts_bound_to_input_bytes_without_reproduction(
        self,
    ):
        original, vendored = b"original fixture", b"vendored fixture"
        receipt = {
            "kind": "sqlite-vendoring-go-ast-correspondence",
            "original_sha256": sha256(original),
            "vendored_sha256": sha256(vendored),
            "expected_structural_sha256": sha256(b"syntax"),
            "target_structural_sha256": sha256(b"syntax"),
            "added_alias_count": 7,
            "generated_output_reproduction_verified": False,
        }
        calls = []

        def execute(arguments, **options):
            calls.append(arguments)
            self.assertEqual(options["env"]["GOPROXY"], "off")
            if arguments[:2] == ["go", "build"]:
                return subprocess.CompletedProcess(arguments, 0, b"", b"")
            self.assertEqual(Path(arguments[1]).read_bytes(), original)
            self.assertEqual(Path(arguments[2]).read_bytes(), vendored)
            return subprocess.CompletedProcess(arguments, 0, json_bytes(receipt), b"")

        with patch.object(sqlite_vendoring.subprocess, "run", side_effect=execute):
            with sqlite_vendoring.build_verifier(
                b"committed checker fixture"
            ) as checker:
                directory = checker.directory
                result = checker(original, vendored)
                checker(original, vendored)
                self.assertEqual(sum(args[:2] == ["go", "build"] for args in calls), 1)
                self.assertEqual(
                    result["verifier_source_sha256"],
                    sha256(b"committed checker fixture"),
                )
                self.assertFalse(result["generated_output_reproduction_verified"])
                self.assertFalse((directory / "original.go.input").exists())
                for field, value in (
                    ("original_sha256", sha256(b"different original")),
                    ("target_structural_sha256", sha256(b"different syntax")),
                    ("generated_output_reproduction_verified", True),
                ):
                    previous = receipt[field]
                    receipt[field] = value
                    with self.subTest(field=field), self.assertRaises(InvalidRelease):
                        checker(original, vendored)
                    receipt[field] = previous
                with patch.object(
                    sqlite_vendoring.subprocess,
                    "run",
                    side_effect=subprocess.TimeoutExpired("comparator", 45),
                ), self.assertRaises(InvalidRelease):
                    checker(original, vendored)
            self.assertFalse(directory.exists())


if __name__ == "__main__":
    unittest.main()
