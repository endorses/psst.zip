#!/usr/bin/env python3
"""Source-byte, retained-layer and source-proof boundaries for replay verification."""

from __future__ import annotations

import copy
import gzip
import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import verify_runtime_source_pack as replay
from release_artifacts import InvalidRelease, json_bytes


def tar_bytes(entries):
    data = io.BytesIO()
    with tarfile.open(fileobj=data, mode="w:") as archive:
        for name, value in entries:
            member = tarfile.TarInfo(name)
            if isinstance(value, tarfile.TarInfo):
                member = value
                value = b""
            member.size = len(value)
            archive.addfile(member, io.BytesIO(value) if member.isfile() else None)
    return data.getvalue()


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


class ReplayTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="psst-source-replay-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)

    def source_asset(self, entries):
        path = self.root / "source.tar.gz"
        path.write_bytes(gzip.compress(tar_bytes(entries), mtime=0))
        return path

    def test_exact_existing_bytes_are_replayed_without_repackaging(self):
        path = self.source_asset(
            [("recipe/LICENSE", b"Copyright actual owner\nFull terms \n")]
        )
        expected = digest(path.read_bytes())
        target = self.root / "expanded"
        replay.extract_source_asset(path, target, expected)
        self.assertEqual(
            (target / "recipe/LICENSE").read_bytes(),
            b"Copyright actual owner\nFull terms \n",
        )
        self.assertEqual(digest(path.read_bytes()), expected)
        with self.assertRaisesRegex(
            InvalidRelease, "Bound runtime source asset differs"
        ):
            replay.extract_source_asset(path, self.root / "bad", "sha256:" + "f" * 64)

    def test_archive_paths_links_duplicates_and_expansion_fail_closed(self):
        link = tarfile.TarInfo("recipe/link")
        link.type = tarfile.SYMTYPE
        link.linkname = "/etc/passwd"
        for entries in [
            [("../escape", b"x")],
            [("same", b"a"), ("same", b"b")],
            [("recipe/link", link)],
        ]:
            with self.subTest(entries=entries):
                path = self.source_asset(entries)
                with self.assertRaises(InvalidRelease):
                    replay.extract_source_asset(
                        path, self.root / "out", digest(path.read_bytes())
                    )
        path = self.source_asset([("large", b"12345")])
        with patch.object(replay, "MEMBER_LIMIT", 4), self.assertRaisesRegex(
            InvalidRelease, "member exceeds"
        ):
            replay.extract_source_asset(
                path, self.root / "bounded", digest(path.read_bytes())
            )

    def recipe_fixture(self, missing=False, changed=False):
        folder = self.root / "apk"
        recipe = folder / "origin/recipe"
        recipe.mkdir(parents=True)
        content = b"patch bytes"
        checksum = hashlib.sha512(content).hexdigest()
        sums = checksum + "  fix.patch"
        build = ('sha512sums="' + sums + '"\n').encode()
        (recipe / "APKBUILD").write_bytes(build)
        (recipe / "fix.patch").write_bytes(content)
        (recipe / "unused.patch").write_bytes(b"unused but retained")
        inner = [("recipe/APKBUILD", build)]
        if not missing:
            inner.append(("recipe/fix.patch", b"changed" if changed else content))
        original = folder / "origin/collected/src/source.tar.gz"
        original.parent.mkdir(parents=True)
        original.write_bytes(gzip.compress(tar_bytes(inner)))
        return folder, {
            "sources": [
                {
                    "source_file": "origin/collected/src/source.tar.gz",
                    "source_sha512sums": sums,
                }
            ]
        }

    def test_checksum_table_and_every_declared_helper_are_preserved(self):
        folder, inventory = self.recipe_fixture()
        replay.check_recipe_inputs(folder, inventory)
        inventory["sources"][0]["source_sha512sums"] = "f" * 128 + "  fix.patch"
        with self.assertRaisesRegex(InvalidRelease, "checksum table differs"):
            replay.check_recipe_inputs(folder, inventory)

    def test_missing_declared_helper_is_not_excused_by_unused_recipe_files(self):
        folder, inventory = self.recipe_fixture(missing=True)
        with self.assertRaisesRegex(
            InvalidRelease, "Checksummed recipe helper missing"
        ):
            replay.check_recipe_inputs(folder, inventory)

    def test_changed_inner_recipe_helper_is_rejected(self):
        folder, inventory = self.recipe_fixture(changed=True)
        with self.assertRaisesRegex(InvalidRelease, "exact retained recipe/helper"):
            replay.check_recipe_inputs(folder, inventory)

    def oci_fixture(self, *, compressed=False, whiteout=False):
        database = (
            b"P:fixture\nV:1-r0\nA:x86_64\nL:MIT\no:fixture\nc:"
            + b"a" * 40
            + b"\nC:Q1fixture\n"
        )
        binary = b"actual executable"
        notice = b"actual full notices"
        entries = [
            ("lib/apk/db/installed", database),
            ("app/server", binary),
            ("app/licenses/runtime/THIRD_PARTY_NOTICES.txt", notice),
        ]
        if whiteout:
            entries.append(("app/licenses/runtime/.wh.THIRD_PARTY_NOTICES.txt", b""))
        raw = tar_bytes(entries)
        layer = gzip.compress(raw, mtime=0) if compressed else raw
        settings = {
            "Labels": {
                "org.opencontainers.image.title": "psst.zip backend",
                "org.opencontainers.image.source": "https://github.com/endorses/psst.zip",
                "org.opencontainers.image.version": "v1.2.3",
                "org.opencontainers.image.revision": "a" * 40,
                "org.opencontainers.image.licenses": "AGPL-3.0-only",
                "zip.psst.source.archive": "https://github.com/endorses/psst.zip/archive/"
                + "a" * 40
                + ".tar.gz",
            }
        }
        config = json_bytes(
            {
                "os": "linux",
                "architecture": "amd64",
                "config": settings,
                "rootfs": {"type": "layers", "diff_ids": [digest(raw)]},
            }
        )

        def descriptor(body, media):
            return {"digest": digest(body), "size": len(body), "mediaType": media}

        image = json_bytes(
            {
                "schemaVersion": 2,
                "mediaType": "application/vnd.oci.image.manifest.v1+json",
                "config": descriptor(
                    config, "application/vnd.oci.image.config.v1+json"
                ),
                "layers": [
                    descriptor(
                        layer,
                        "application/vnd.oci.image.layer.v1.tar"
                        + ("+gzip" if compressed else ""),
                    )
                ],
            }
        )
        index = json_bytes(
            {
                "schemaVersion": 2,
                "mediaType": "application/vnd.oci.image.index.v1+json",
                "manifests": [
                    descriptor(image, "application/vnd.oci.image.manifest.v1+json")
                ],
            }
        )
        files = [
            ("oci-layout", json_bytes({"imageLayoutVersion": "1.0.0"})),
            ("index.json", index),
        ] + [
            ("blobs/sha256/" + digest(body)[7:], body)
            for body in [config, image, layer]
        ]
        path = self.root / "image.oci.tar"
        path.write_bytes(tar_bytes(files))
        folder = self.root / "backend-runtime"
        folder.mkdir()
        (folder / "installed-apk-db").write_bytes(database)
        rows = replay.pack.graph(replay.packages(database))
        binding = {
            "binary_path": "/app/server",
            "binary_sha256": replay.pack.digest(binary),
            "rootfs_layers": [digest(raw)],
            "runtime_config_sha256": replay.pack.digest(json_bytes(settings)),
            "retained_packages": rows,
            "retained_graph_sha256": replay.pack.digest(json_bytes(rows)),
            "final_packages": rows,
        }
        inventory = {
            "packages": rows,
            "layer_databases": [
                {
                    "layer": 0,
                    "layer_sha256": digest(raw)[7:],
                    "database_sha256": replay.pack.digest(database),
                }
            ],
        }
        identity = {
            "repository": "endorses/psst.zip",
            "version": "v1.2.3",
            "revision": "a" * 40,
            "architecture": "amd64",
            "source_root": self.root,
        }
        return (
            path,
            binding,
            inventory,
            {"THIRD_PARTY_NOTICES.txt": replay.pack.digest(notice)},
            identity,
            digest(config),
        )

    def image_replay(self, fixture):
        path, binding, inventory, overlay, identity, config = fixture
        return replay.replay_image(
            path, "backend", binding, inventory, overlay, {}, identity, config
        )

    def test_real_raw_and_gzip_oci_bytes_bind_sources_and_notices(self):
        fixture = self.oci_fixture()
        report, binary = self.image_replay(fixture)
        self.assertEqual(report["archive_digest"], digest(fixture[0].read_bytes()))
        self.assertEqual(binary, b"actual executable")
        (self.root / "backend-runtime/installed-apk-db").unlink()
        (self.root / "backend-runtime").rmdir()
        report, _ = self.image_replay(self.oci_fixture(compressed=True))
        self.assertEqual(report["platform"], "linux/amd64")

    def test_retained_origin_cannot_be_omitted_by_approval_flags(self):
        fixture = list(self.oci_fixture())
        fixture[2]["packages"] = []
        fixture[2]["source_pack_complete"] = True
        fixture[2]["review_required"] = False
        with self.assertRaisesRegex(InvalidRelease, "retained package graph"):
            self.image_replay(fixture)

    def test_final_notice_deletion_and_runtime_config_substitution_fail(self):
        fixture = self.oci_fixture(whiteout=True)
        with self.assertRaisesRegex(InvalidRelease, "notice/source/discovery bytes"):
            self.image_replay(fixture)
        fixture[1]["runtime_config_sha256"] = "f" * 64
        with self.assertRaisesRegex(InvalidRelease, "runtime configuration differs"):
            self.image_replay(fixture)

    def test_wrong_package_database_or_binary_is_rejected(self):
        fixture = self.oci_fixture()
        fixture[1]["binary_sha256"] = "f" * 64
        with self.assertRaisesRegex(InvalidRelease, "executable differs"):
            self.image_replay(fixture)
        fixture[1]["binary_sha256"] = replay.pack.digest(b"actual executable")
        fixture[2]["layer_databases"][0]["database_sha256"] = "f" * 64
        with self.assertRaisesRegex(InvalidRelease, "layer databases differ"):
            self.image_replay(fixture)

    def test_cached_signature_success_cannot_replace_fresh_verification(self):
        folder = self.root / "caddy"
        folder.mkdir()
        (folder / "caddy-source-inventory.json").write_bytes(
            json_bytes(
                {
                    "image_id": "sha256:" + "a" * 64,
                    "upstream_signatures_verified": True,
                    "review_required": False,
                }
            )
        )
        with patch.object(
            replay.pack,
            "verify_caddy_signatures",
            side_effect=InvalidRelease("actual signature failed"),
        ) as verifier:
            with self.assertRaisesRegex(InvalidRelease, "actual signature failed"):
                replay.verify_caddy_collection(
                    folder,
                    Path("/unused/cosign"),
                    b"binary",
                    {"original_image_id": "sha256:" + "a" * 64},
                )
        verifier.assert_called_once()

    def test_whiteouts_cover_ancestors_and_root_opaque_directories(self):
        tracked = {"app/server", "app/legal/NOTICE", "other"}
        self.assertEqual(
            replay.whiteout_targets(".wh.app", tracked),
            {"app/server", "app/legal/NOTICE"},
        )
        self.assertEqual(
            replay.whiteout_targets("app/.wh..wh..opq", tracked),
            {"app/server", "app/legal/NOTICE"},
        )
        self.assertEqual(replay.whiteout_targets(".wh..wh..opq", tracked), tracked)


if __name__ == "__main__":
    unittest.main()
