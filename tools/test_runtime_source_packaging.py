#!/usr/bin/env python3
"""Fail-closed boundaries for runtime corresponding-source and legal overlays."""

from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import package_runtime_sources as pack
from release_artifacts import InvalidRelease, json_bytes


class RuntimeSourcePackTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="psst-source-pack-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def test_every_license_alternative_needs_full_terms(self):
        documents = {"BSD-3-Clause": {}, "GPL-2.0-only": {}}
        self.assertEqual(
            pack.license_ids("BSD-3-Clause OR GPL-2.0-only", documents),
            ["BSD-3-Clause", "GPL-2.0-only"],
        )
        with self.assertRaisesRegex(InvalidRelease, "terms require review"):
            pack.license_ids("BSD-3-Clause OR GPL-2.0-only", {"BSD-3-Clause": {}})
        with self.assertRaises(InvalidRelease):
            pack.license_ids("MIT WITH Unknown-exception", {"MIT": {}})
        for malformed in ("MIT OR", "(MIT", "MIT MIT", ")MIT", ""):
            with self.subTest(expression=malformed), self.assertRaises(InvalidRelease):
                pack.license_ids(malformed, {"MIT": {}})

    def test_data_license_does_not_invent_an_owner(self):
        self.assertEqual(pack.license_ids("Public Domain", {}), [])
        review = pack.REVIEW.verify_evidence()
        for origin in review["origins"]:
            if origin["origin"] in {"alpine-base", "alpine-keys"}:
                self.assertIn("no copyright owner", origin["supported_treatment"])
                self.assertFalse(
                    any("owner" in item for item in origin["remaining_review"])
                )

    def test_only_exact_malformed_upstream_test_fixtures_are_supported(self):
        entry = {"path": next(iter(pack.FIXTURES))}
        entry["sha256"] = pack.FIXTURES[entry["path"]]
        pack.reviewed_fixtures("busybox", [entry])
        for changed in (
            {**entry, "sha256": "f" * 64},
            {**entry, "path": "unknown.zip"},
        ):
            with self.assertRaisesRegex(InvalidRelease, "Unreviewed malformed"):
                pack.reviewed_fixtures("busybox", [changed])
        with self.assertRaises(InvalidRelease):
            pack.reviewed_fixtures("another-origin", [entry])

    def test_source_urls_cannot_hide_credentials_or_retarget_assets(self):
        self.assertEqual(
            pack.source_url("https://example.org/releases/v1", "source.tar.gz"),
            "https://example.org/releases/v1/source.tar.gz",
        )
        for base in (
            "http://example.org",
            "https://user:pass@example.org",
            "https://example.org?a=b",
            "https://example.org/#fragment",
            "https://example.org:8443",
            "https://example.org/\n",
        ):
            with self.subTest(base=base), self.assertRaises(InvalidRelease):
                pack.source_url(base, "source.tar.gz")

    def test_original_bytes_are_retained_with_deterministic_archive_metadata(self):
        tree = self.root / "source"
        tree.mkdir()
        (tree / "LICENSE").write_bytes(b"Copyright upstream\nFull terms\n")
        first, second = self.root / "first.tar.gz", self.root / "second.tar.gz"
        pack.deterministic_archive(tree, first)
        pack.deterministic_archive(tree, second)
        self.assertEqual(first.read_bytes(), second.read_bytes())
        import tarfile

        with tarfile.open(first) as archive:
            self.assertEqual(
                archive.extractfile("LICENSE").read(), (tree / "LICENSE").read_bytes()
            )
        (tree / "escape").symlink_to("LICENSE")
        with self.assertRaisesRegex(InvalidRelease, "symlink"):
            pack.deterministic_archive(tree, self.root / "third.tar.gz")

    def test_missing_or_unreviewed_retained_origin_cannot_be_packaged(self):
        inventory = {
            "schema_version": 1,
            "review_required": True,
            "image_id": "sha256:" + "a" * 64,
            "architecture": "amd64",
            "layer_databases": [{"layer": 0}],
            "packages": [
                {
                    "name": "unknown",
                    "version": "1-r0",
                    "architecture": "x86_64",
                    "license": "MIT",
                    "origin": "unknown",
                    "aports_commit": "a" * 40,
                    "apk_checksum": "Q1test",
                }
            ],
            "sources": [],
        }
        (self.root / "runtime-inventory.json").write_bytes(json_bytes(inventory))
        with self.assertRaisesRegex(InvalidRelease, "missing source revisions"):
            pack.verify_apk_collection(self.root, {"MIT": {}}, {})

    def test_overlay_rejects_changed_inherited_layers_before_accepting_notices(self):
        source = self.root / "sources.tar.gz"
        source.write_bytes(b"private source asset")
        original = {
            "rootfs_layers": ["sha256:" + "b" * 64],
            "runtime_config_sha256": pack.digest(json_bytes({})),
        }
        manifest = {
            "architecture": "amd64",
            "source_asset": {"file": source.name, "sha256": pack.file_hash(source)},
            "bindings": {"backend": original, "web": original},
        }
        (self.root / "runtime-pack.json").write_bytes(json_bytes(manifest))
        changed = {
            "Architecture": "amd64",
            "RootFS": {"Layers": ["sha256:" + "c" * 64]},
            "Config": {},
        }
        with patch.object(pack, "image_info", return_value=changed):
            with self.assertRaisesRegex(InvalidRelease, "inherited runtime layers"):
                pack.verify_overlays(self.root, "backend", "web")

    def test_signature_proof_never_authenticates_apk_or_uncovered_sources(self):
        source = {
            "origin": "alpine-keys",
            "version": "2.6-r0",
            "aports_commit": "a" * 40,
            "source_file": "origin/source.tar.gz",
            "source_sha256": "b" * 64,
        }
        inventory = {"sources": [source]}
        caddy = {
            "sources": [
                {"file": "buildable.tar.gz", "sha256": "c" * 64},
                {"file": "binary.tar.gz", "sha256": "d" * 64},
                {"file": "other.tar.gz", "sha256": "e" * 64},
            ]
        }
        signatures = {
            "verifications": [{"artifact": "buildable.tar.gz"}],
            "signed_sha512_bindings": [
                {"file": "buildable.tar.gz"},
                {"file": "binary.tar.gz"},
            ],
        }
        proof = pack.artifact_provenance(inventory, inventory, caddy, signatures)
        self.assertFalse(
            proof["apk_origins"]["backend"][0]["apk_binary_signature_verified"]
        )
        self.assertFalse(
            proof["apk_origins"]["backend"][0]["upstream_source_signature_verified"]
        )
        assets = {item["file"]: item for item in proof["caddy_assets"]}
        self.assertTrue(assets["buildable.tar.gz"]["upstream_signature_verified"])
        self.assertTrue(assets["binary.tar.gz"]["signature_proof_covered"])
        self.assertFalse(assets["binary.tar.gz"]["upstream_signature_verified"])
        self.assertFalse(assets["other.tar.gz"]["signature_proof_covered"])

    def test_runtime_discovery_preserves_exact_original_release_identity(self):
        original = {
            "name": "psst.zip",
            "version": "v1.2.3",
            "revision": "a" * 40,
            "license": "AGPL-3.0-only",
            "source": "https://github.com/example/psst.zip",
            "source_archive": "https://github.com/example/psst.zip/archive/"
            + "a" * 40
            + ".tar.gz",
            "notice_files": ["/licenses/backend/THIRD_PARTY_NOTICES.txt"],
        }
        info = {
            "Config": {
                "Labels": {"org.opencontainers.image.source": original["source"]}
            }
        }
        with patch.object(pack, "image_info", return_value=info), patch.object(
            pack, "command", return_value=json_bytes(original)
        ):
            result = json.loads(pack.web_release_metadata("image", "v1.2.3", "a" * 40))
        self.assertEqual(
            {key: value for key, value in result.items() if key != "notice_files"},
            {key: value for key, value in original.items() if key != "notice_files"},
        )
        self.assertIn(
            "/licenses/runtime/THIRD_PARTY_NOTICES.txt", result["notice_files"]
        )
        original["revision"] = "b" * 40
        with patch.object(pack, "image_info", return_value=info), patch.object(
            pack, "command", return_value=json_bytes(original)
        ):
            with self.assertRaisesRegex(InvalidRelease, "metadata differs"):
                pack.web_release_metadata("image", "v1.2.3", "a" * 40)

    def test_collection_reads_actual_collected_src_recipe_layout(self):
        review = pack.REVIEW.verify_evidence()
        origin = next(
            item for item in review["origins"] if item["origin"] == "alpine-keys"
        )
        folder = self.root / "alpine-keys-original"
        (folder / "recipe").mkdir(parents=True)
        (folder / "collected/src").mkdir(parents=True)
        source = folder / "collected/src/original.src.tar.gz"
        source.write_bytes(b"fixture source")
        (folder / "recipe/APKBUILD").write_bytes(
            (pack.LEGAL / origin["recipe_path"]).read_bytes()
        )
        for expected in origin["recipe_files"]:
            if expected["name"] == "APKBUILD":
                continue
            # A matching path must be used before any byte mismatch is reported.
            (folder / "recipe" / expected["name"]).write_bytes(b"modified helper")
        inventory = {
            "review_required": True,
            "sources": [
                {
                    **origin,
                    "source_file": str(source.relative_to(self.root)),
                    "source_sha256": pack.file_hash(source),
                }
            ],
            "packages": [],
        }
        (self.root / "runtime-inventory.json").write_bytes(json_bytes(inventory))
        with self.assertRaisesRegex(ValueError, "helper differs"):
            pack.REVIEW.verify_collection(pack.LEGAL, self.root, review)


if __name__ == "__main__":
    unittest.main()
