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

    def go_source_fixture(
        self,
        entries=None,
        *,
        version="go1.26.8",
        commit=None,
        folder_name="go-collection",
    ):
        folder = self.root / folder_name
        folder.mkdir(exist_ok=True)
        commit = commit or "a" * 40
        prefix = "go-" + commit
        if entries is None:
            entries = [
                ("LICENSE", b"Original Go BSD terms\n"),
                ("PATENTS", b"Original Go patent grant\n"),
                ("VERSION", version.encode() + b"\ntime 2026-08-28T16:20:06Z\n"),
                ("src/go.mod", b"module std\n"),
                ("src/runtime/proc.go", b"package runtime\n"),
                ("src/cmd/compile/main.go", b"package main\n"),
                ("src/make.bash", b"#!/bin/bash\n"),
                ("src/vendor/example/LICENSE", b"Original vendored terms\n"),
                (
                    "src/archive/zip/testdata/invalid.zip",
                    b"deliberately malformed fixture",
                ),
            ]
        raw = gzip.compress(
            tar_bytes([(prefix + "/" + name, data) for name, data in entries]), mtime=0
        )
        name = prefix + ".tar.gz"
        (folder / name).write_bytes(raw)
        for notice in ("LICENSE", "PATENTS"):
            (folder / ("go-" + notice)).write_bytes(
                dict(entries).get(notice, b"absent")
            )
        pinned = {
            "version": version,
            "commit": commit,
            "file": name,
            "url": f"https://codeload.github.com/golang/go/tar.gz/{commit}",
            "sha256": digest(raw)[7:],
            "size": len(raw),
        }
        policy = self.root / "go-runtime-sources.json"
        policy.write_bytes(
            json_bytes(
                {
                    "schema_version": 1,
                    "kind": "pinned-go-runtime-sources",
                    "sources": [pinned],
                }
            )
        )
        version_file = dict(entries).get("VERSION")
        inventory = {
            "go_version": version,
            "go_source_revision": commit,
            "go_source": {key: value for key, value in pinned.items() if key != "url"}
            | {
                "version_file_sha256": (
                    digest(version_file)[7:] if version_file is not None else None
                )
            },
            "sources": [
                {"file": name, "sha256": pinned["sha256"], "url": pinned["url"]}
            ],
        }
        return folder, inventory, policy, entries

    def test_backend_and_caddy_keep_distinct_exact_go_sources(self):
        caddy_folder, caddy_inventory, policy, _ = self.go_source_fixture(
            folder_name="caddy"
        )
        older = json.loads(policy.read_bytes())["sources"][0]
        folder, inventory, policy, _ = self.go_source_fixture(
            version="go1.27.1", commit="b" * 40, folder_name="backend-go-runtime"
        )
        pinned = json.loads(policy.read_bytes())
        pinned["sources"].insert(0, older)
        policy.write_bytes(json_bytes(pinned))
        with patch.object(replay.caddy, "GO_SOURCE_POLICY", policy):
            notices = replay.caddy.verify_go_source(folder, inventory)
            archive_name = inventory["go_source"]["file"]
            supplied = {
                archive_name + "::" + name: data for name, data in notices.items()
            }
            for name in ("LICENSE", "PATENTS"):
                filename = "go-" + name
                supplied[filename] = (folder / filename).read_bytes()
                inventory["sources"].append(
                    {
                        "file": filename,
                        "sha256": digest(supplied[filename])[7:],
                        "url": f"https://raw.githubusercontent.com/golang/go/{inventory['go_source_revision']}/{name}",
                    }
                )
            inventory["notices"] = {
                name: digest(data)[7:] for name, data in supplied.items()
            }
            inventory_path = folder / "go-source-inventory.json"
            inventory_path.write_bytes(json_bytes(inventory))
            backend, backend_notices = replay.pack.backend_go_runtime(
                self.root, caddy_inventory, "go1.27.1"
            )
            self.assertEqual(backend["go_source"]["commit"], "b" * 40)
            self.assertEqual(backend_notices, notices)
            same, _ = replay.pack.backend_go_runtime(
                self.root, caddy_inventory, "go1.26.8"
            )
            self.assertEqual(same, caddy_inventory)
            for mutated in (
                caddy_inventory,
                {**inventory, "go_source": caddy_inventory["go_source"]},
            ):
                inventory_path.write_bytes(json_bytes(mutated))
                with self.subTest(
                    source=mutated["go_source"]["version"]
                ), self.assertRaises(InvalidRelease):
                    replay.pack.backend_go_runtime(
                        self.root, caddy_inventory, "go1.27.1"
                    )
            inventory_path.write_bytes(json_bytes(inventory))
            (folder / archive_name).unlink()
            with self.assertRaisesRegex(InvalidRelease, "archive differs"):
                replay.pack.backend_go_runtime(self.root, caddy_inventory, "go1.27.1")

    def test_full_go_source_preserves_original_notices_and_malformed_test_fixtures(
        self,
    ):
        folder, inventory, policy, entries = self.go_source_fixture()
        archive = folder / inventory["go_source"]["file"]
        original = archive.read_bytes()
        with patch.object(replay.caddy, "GO_SOURCE_POLICY", policy):
            notices = replay.caddy.verify_go_source(folder, inventory)
            self.assertEqual(
                notices["go-" + "a" * 40 + "/src/vendor/example/LICENSE"],
                b"Original vendored terms\n",
            )
            self.assertEqual(len(notices), 3)
            replay.caddy.verify_binary_go_source({"GoVersion": "go1.26.8"}, inventory)
            for version in ("go1.26.7", "devel go1.27", None):
                with self.subTest(version=version), self.assertRaises(InvalidRelease):
                    replay.caddy.verify_binary_go_source(
                        {"GoVersion": version}, inventory
                    )
        self.assertEqual(archive.read_bytes(), original)
        folder, inventory, policy, _ = self.go_source_fixture(
            [(name, data) for name, data in entries if name != "VERSION"]
        )
        with patch.object(replay.caddy, "GO_SOURCE_POLICY", policy):
            self.assertEqual(len(replay.caddy.verify_go_source(folder, inventory)), 3)

    def test_go_source_rejects_missing_runtime_wrong_version_or_substituted_policy_and_notices(
        self,
    ):
        folder, inventory, policy, entries = self.go_source_fixture()
        with patch.object(replay.caddy, "GO_SOURCE_POLICY", policy):
            changed = copy.deepcopy(inventory)
            changed["go_source"]["sha256"] = "f" * 64
            changed["sources"][0]["sha256"] = "f" * 64
            with self.assertRaisesRegex(InvalidRelease, "committed pinned policy"):
                replay.caddy.verify_go_source(folder, changed)
            changed = copy.deepcopy(inventory)
            changed["sources"][0]["url"] = "https://other.example/go.tar.gz"
            with self.assertRaisesRegex(InvalidRelease, "retained source binding"):
                replay.caddy.verify_go_source(folder, changed)
            (folder / "go-LICENSE").write_bytes(b"different owner or terms")
            with self.assertRaisesRegex(InvalidRelease, "legal files differ"):
                replay.caddy.verify_go_source(folder, inventory)
        for mutated in (
            [(name, data) for name, data in entries if name != "src/runtime/proc.go"],
            [
                (name, b"go1.26.7\n" if name == "VERSION" else data)
                for name, data in entries
            ],
            entries + [("src/runtime/proc.go", b"duplicate source")],
        ):
            folder, inventory, policy, _ = self.go_source_fixture(mutated)
            with patch.object(
                replay.caddy, "GO_SOURCE_POLICY", policy
            ), self.assertRaises(InvalidRelease):
                replay.caddy.verify_go_source(folder, inventory)
        link = tarfile.TarInfo("go-" + "a" * 40 + "/src/runtime/link.go")
        link.type, link.linkname = tarfile.SYMTYPE, "/etc/passwd"
        folder, inventory, policy, _ = self.go_source_fixture(
            entries + [("src/runtime/link.go", link)]
        )
        with patch.object(
            replay.caddy, "GO_SOURCE_POLICY", policy
        ), self.assertRaisesRegex(
            InvalidRelease, "Unsupported Go runtime source member"
        ):
            replay.caddy.verify_go_source(folder, inventory)

    def test_go_executable_metadata_is_read_without_executing_the_program(self):
        def read_metadata(*args):
            self.assertEqual(args[:4], ("go", "version", "-m", "-json"))
            self.assertEqual(Path(args[4]).read_bytes(), b"executable bytes")
            self.assertEqual(Path(args[4]).stat().st_mode & 0o111, 0)
            return json_bytes({"GoVersion": "go1.26.8"})

        with patch.object(replay.caddy, "command", side_effect=read_metadata) as reader:
            self.assertEqual(
                replay.caddy.binary_build_info(b"executable bytes"),
                {"GoVersion": "go1.26.8"},
            )
        reader.assert_called_once()
        self.assertFalse(Path(reader.call_args.args[4]).exists())

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
