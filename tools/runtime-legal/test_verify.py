#!/usr/bin/env python3
"""Boundary checks for offline runtime legal evidence verification."""

import importlib.util
import io
import json
from pathlib import Path
import shutil
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location(
    "runtime_legal_verify", ROOT / "verify.py"
)
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


class ReviewEvidenceTests(unittest.TestCase):
    def test_pinned_evidence_is_complete_as_evidence_only(self):
        review = VERIFY.verify_evidence()
        self.assertTrue(review["review_required"])
        self.assertEqual(len(review["origins"]), 8)
        for origin in review["origins"]:
            self.assertTrue(origin["review_required"])
            self.assertTrue(origin["remaining_review"])

    def test_corrupted_notice_or_recipe_is_rejected(self):
        review = VERIFY.verify_evidence()
        for name in (
            review["documents"][0]["path"],
            review["origins"][0]["recipe_path"],
        ):
            with self.subTest(path=name), tempfile.TemporaryDirectory(
                prefix="psst-legal-test-"
            ) as temp:
                root = Path(temp)
                shutil.copytree(ROOT, root, dirs_exist_ok=True)
                with (root / name).open("ab") as output:
                    output.write(b"changed\n")
                with self.assertRaisesRegex(ValueError, "checksum differs"):
                    VERIFY.verify_evidence(root)

    def test_standard_license_templates_cannot_waive_origin_review(self):
        with tempfile.TemporaryDirectory(prefix="psst-legal-test-") as temp:
            root = Path(temp)
            shutil.copytree(ROOT, root, dirs_exist_ok=True)
            review = json.loads((root / "review.json").read_text())
            origin = next(
                item for item in review["origins"] if item["origin"] == "alpine-keys"
            )
            origin["review_required"] = False
            (root / "review.json").write_text(json.dumps(review))
            with self.assertRaisesRegex(ValueError, "cannot be waived"):
                VERIFY.verify_evidence(root)

    def test_unreviewed_retained_source_version_is_rejected(self):
        with tempfile.TemporaryDirectory(prefix="psst-legal-test-") as temp:
            collection = Path(temp)
            (collection / "runtime-inventory.json").write_text(
                json.dumps(
                    {
                        "review_required": True,
                        "sources": [
                            {
                                "origin": "alpine-baselayout",
                                "version": "3.7.1-r0",
                                "aports_commit": "a" * 40,
                            }
                        ],
                        "packages": [],
                    }
                )
            )
            with self.assertRaisesRegex(
                ValueError, "Unreviewed runtime source revision"
            ):
                VERIFY.verify_collection(ROOT, collection, VERIFY.verify_evidence())

    def test_ambiguous_source_members_and_symlinks_are_rejected(self):
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode="w") as source:
            for name in ("a/input.tar.bz2", "b/input.tar.bz2"):
                member = tarfile.TarInfo(name)
                member.size = 1
                source.addfile(member, io.BytesIO(b"x"))
        with self.assertRaisesRegex(ValueError, "ambiguous"):
            VERIFY.archive_member(stream.getvalue(), "input.tar.bz2", basename=True)
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode="w") as source:
            member = tarfile.TarInfo("input.tar.bz2")
            member.type = tarfile.SYMTYPE
            member.linkname = "elsewhere"
            source.addfile(member)
        with self.assertRaisesRegex(ValueError, "Invalid source member"):
            VERIFY.archive_member(stream.getvalue(), "input.tar.bz2", basename=True)

    def test_escaped_document_path_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "Unsafe evidence path"):
            VERIFY.read_local(ROOT, "../review.json")


if __name__ == "__main__":
    unittest.main()
