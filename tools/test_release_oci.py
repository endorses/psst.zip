"""OCI graph, tested-layer correspondence, and deterministic index boundaries."""

from __future__ import annotations

import copy
import gzip
import io
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import assemble_release_oci as oci
from publish_container_release import sha256
from release_artifacts import InvalidRelease, json_bytes


def fixture(component="backend", arch="amd64", *, gzip_layer=True):
    layer_tar = io.BytesIO()
    with tarfile.open(fileobj=layer_tar, mode="w:") as archive:
        member = tarfile.TarInfo("app/fixture.txt")
        body = (component + arch).encode()
        member.size = len(body)
        archive.addfile(member, io.BytesIO(body))
    uncompressed = layer_tar.getvalue()
    layer = gzip.compress(uncompressed, mtime=0) if gzip_layer else uncompressed
    config = json_bytes(
        {
            "os": "linux",
            "architecture": arch,
            "config": {
                "User": "10001",
                "Labels": {
                    "org.opencontainers.image.title": "psst.zip " + component,
                    "org.opencontainers.image.source": "https://github.com/endorses/psst.zip",
                    "org.opencontainers.image.version": "v1.2.3",
                    "org.opencontainers.image.revision": "a" * 40,
                    "org.opencontainers.image.licenses": "AGPL-3.0-only",
                    "zip.psst.source.archive": "https://github.com/endorses/psst.zip/archive/"
                    + "a" * 40
                    + ".tar.gz",
                },
            },
            "rootfs": {"type": "layers", "diff_ids": [sha256(uncompressed)]},
        }
    )
    config_digest = sha256(config)

    def descriptor(raw, media_type):
        return {"mediaType": media_type, "digest": sha256(raw), "size": len(raw)}

    image = json_bytes(
        {
            "schemaVersion": 2,
            "mediaType": "application/vnd.oci.image.manifest.v1+json",
            "config": descriptor(config, "application/vnd.oci.image.config.v1+json"),
            "layers": [
                descriptor(
                    layer,
                    (
                        "application/vnd.oci.image.layer.v1.tar+gzip"
                        if gzip_layer
                        else "application/vnd.oci.image.layer.v1.tar"
                    ),
                )
            ],
        }
    )
    index = {
        "schemaVersion": 2,
        "mediaType": "application/vnd.oci.image.index.v1+json",
        "manifests": [
            {
                **descriptor(image, "application/vnd.oci.image.manifest.v1+json"),
                "platform": {"os": "linux", "architecture": arch},
            }
        ],
    }
    files = {
        "oci-layout": json_bytes({"imageLayoutVersion": "1.0.0"}),
        "index.json": json_bytes(index),
    }
    for content in (config, image, layer):
        files["blobs/sha256/" + sha256(content)[7:]] = content
    return files, config_digest


class OciChecks(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="psst-oci-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.path = self.root / "image.tar"
        self.files, self.config = fixture()

    def write(self, files=None, extras=()):
        with tarfile.open(self.path, "w:") as archive:
            for name, body in (files or self.files).items():
                member = tarfile.TarInfo(name)
                member.size = len(body)
                archive.addfile(member, io.BytesIO(body))
            for member, body in extras:
                archive.addfile(member, io.BytesIO(body) if body is not None else None)

    def inspect(self, **overrides):
        return oci.inspect_archive(
            self.path,
            **{
                "platform": "linux/amd64",
                "repository": "endorses/psst.zip",
                "version": "v1.2.3",
                "commit": "a" * 40,
                "tested_config": self.config,
                "component": "backend",
                **overrides,
            },
        )

    def test_real_payload_hashes_and_tested_config_are_bound(self):
        self.write()
        result = self.inspect()
        self.assertEqual(result["config_digest"], self.config)
        self.assertEqual(result["archive_digest"], sha256(self.path.read_bytes()))
        self.assertEqual(result["blob_count"], 3)
        self.assertNotEqual(result["manifest_digest"], self.config)
        for field, value in (
            ("tested_config", "sha256:" + "b" * 64),
            ("platform", "linux/arm64"),
            ("commit", "b" * 40),
            ("version", "v1.2.4"),
            ("component", "web"),
        ):
            with self.subTest(field=field):
                with self.assertRaises(InvalidRelease):
                    self.inspect(**{field: value})

    def test_replaced_layer_with_same_tested_config_is_rejected(self):
        files = copy.deepcopy(self.files)
        from release_artifacts import read_json

        index = read_json(files["index.json"])
        old_image_path = "blobs/sha256/" + index["manifests"][0]["digest"][7:]
        image = read_json(files.pop(old_image_path))
        files.pop("blobs/sha256/" + image["layers"][0]["digest"][7:])
        layer = gzip.compress(b"substituted uncompressed bytes", mtime=0)
        files["blobs/sha256/" + sha256(layer)[7:]] = layer
        image["layers"][0].update(digest=sha256(layer), size=len(layer))
        raw = json_bytes(image)
        files["blobs/sha256/" + sha256(raw)[7:]] = raw
        index["manifests"][0].update(digest=sha256(raw), size=len(raw))
        files["index.json"] = json_bytes(index)
        self.write(files)
        with self.assertRaisesRegex(InvalidRelease, "layer differs"):
            self.inspect()

    def test_corrupt_missing_unreferenced_and_unsafe_entries_are_rejected(self):
        blob = next(name for name in self.files if name.startswith("blobs/"))
        cases = [
            {**self.files, blob: b"corruption"},
            {key: value for key, value in self.files.items() if key != blob},
            {**self.files, "blobs/sha256/" + sha256(b"extra")[7:]: b"extra"},
            {**self.files, "../escape": b"escape"},
            {**self.files, "unexpected": b"unexpected"},
        ]
        for files in cases:
            with self.subTest(names=list(files)):
                self.write(files)
                with self.assertRaises(InvalidRelease):
                    self.inspect()
        duplicate = tarfile.TarInfo("index.json")
        duplicate.size = len(self.files["index.json"])
        self.write(extras=[(duplicate, self.files["index.json"])])
        with self.assertRaises(InvalidRelease):
            self.inspect()
        link = tarfile.TarInfo("escape")
        link.type, link.linkname = tarfile.SYMTYPE, "/etc/passwd"
        self.write(extras=[(link, None)])
        with self.assertRaises(InvalidRelease):
            self.inspect()

    def test_pax_and_hidden_trailing_archive_are_rejected(self):
        with tarfile.open(self.path, "w:", format=tarfile.PAX_FORMAT) as archive:
            member = tarfile.TarInfo("index.json")
            member.pax_headers = {"comment": "unexpected hidden metadata"}
            member.size = 2
            archive.addfile(member, io.BytesIO(b"{}"))
        with self.assertRaises(InvalidRelease):
            self.inspect()
        self.write()
        with self.path.open("ab") as stream:
            stream.write(b"hidden second tar")
        with self.assertRaises(InvalidRelease):
            self.inspect()

    def test_archive_and_uncompressed_payload_bounds_are_enforced(self):
        self.write()
        with patch.object(oci, "MAX_ARCHIVE", 100):
            with self.assertRaises(InvalidRelease):
                self.inspect()
        with patch.object(oci, "MAX_DOCUMENT", 10):
            with self.assertRaises(InvalidRelease):
                self.inspect()

    def test_four_archive_indexes_are_deterministic_and_preserve_children(self):
        archives, configs = {}, {}
        for component in ("backend", "web"):
            for arch in ("amd64", "arm64"):
                key = component + "-" + arch
                self.files, config = fixture(
                    component, arch, gzip_layer=(arch == "amd64")
                )
                self.path = self.root / (key + ".tar")
                self.write()
                archives[key], configs[key] = self.path, config

        def assemble(output):
            return oci.assemble(
                archives,
                configs,
                repository="endorses/psst.zip",
                version="v1.2.3",
                commit="a" * 40,
                output=output,
            )

        first, second = assemble(self.root / "first"), assemble(self.root / "second")
        self.assertEqual(first, second)
        self.assertFalse(first["publication_authorized"])
        for component in ("backend", "web"):
            self.assertEqual(
                (self.root / "first" / (component + "-index.json")).read_bytes(),
                (self.root / "second" / (component + "-index.json")).read_bytes(),
            )
            self.assertEqual(
                set(first["images"][component]["platform_digests"]), set(oci.PLATFORMS)
            )
        with self.assertRaises(InvalidRelease):
            assemble(self.root / "first")
        configs["web-arm64"] = configs["web-amd64"]
        with self.assertRaises(InvalidRelease):
            assemble(self.root / "bad")
        self.assertFalse((self.root / "bad").exists())


if __name__ == "__main__":
    unittest.main()
