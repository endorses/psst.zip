"""Tiny real OCI layers exercise final notice substitutions, not coverage padding."""

from __future__ import annotations

import copy
import io
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import final_image_notice_inventory as notices
from generate_release_gate_reports import NativeSourceContext
from release_artifacts import InvalidRelease, json_bytes, read_json
from test_release_oci import fixture


def tar_bytes(files, *, special=()):
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:") as archive:
        for name, body in files.items():
            member = tarfile.TarInfo(name)
            member.size = len(body)
            archive.addfile(member, io.BytesIO(body))
        for name, target in special:
            member = tarfile.TarInfo(name)
            member.type = tarfile.SYMTYPE
            member.linkname = target
            archive.addfile(member)
    return buffer.getvalue()


class FinalBackendNotices(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="psst-final-notices-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.archive = self.root / "backend.tar"
        self.context = NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", "a" * 40, "linux/amd64"
        )
        dep = "dependencies/example.org/tiny@v1.0.0/LICENSE"
        self.dep = dep
        notice = b"Tiny dependency copyright and license\n"
        self.committed = {
            "LICENSE": b"Application AGPL license\n",
            "backend/go.mod": b"module fixture\n",
            "backend/go.sum": b"fixture sum\n",
            "backend/licenses/AGPL-3.0-only.txt": b"Application AGPL license\n",
            "backend/licenses/THIRD_PARTY_NOTICES.txt": b"Combined backend notices\n",
            "backend/licenses/" + dep: notice,
        }
        self.committed["backend/licenses/dependency-inventory.json"] = json_bytes(
            {
                "inputs": {
                    name: notices.digest(self.committed["backend/" + name])[7:]
                    for name in ("go.mod", "go.sum")
                },
                "modules": [
                    {
                        "module": "example.org/tiny",
                        "version": "v1.0.0",
                        "notices": [
                            {
                                "path": dep,
                                "sha256": notices.digest(notice)[7:],
                                "upstream_sha256": notices.digest(notice)[7:],
                            }
                        ],
                    }
                ],
            }
        )
        go_commit = "c" * 40
        go_license = b"Go original copyright and license\n"
        runtime = {
            "version": self.context.version,
            "revision": self.context.commit,
            "architecture": "amd64",
            "component": "backend",
            "caddy": {
                "go_version": "go1.26.8",
                "go_source": {
                    "version": "go1.26.8",
                    "commit": go_commit,
                    "file": "go-" + go_commit + ".tar.gz",
                },
                "notices": {
                    "go-"
                    + go_commit
                    + ".tar.gz::go-"
                    + go_commit
                    + "/LICENSE": notices.digest(go_license)[7:]
                },
            },
        }
        overlay = {
            "runtime-inventory.json": json_bytes(runtime),
            "SOURCE.txt": b"Runtime source locator\n",
            "THIRD_PARTY_NOTICES.txt": b"Runtime supplied notices\n",
        }
        self.pack = {
            "version": self.context.version,
            "revision": self.context.commit,
            "architecture": "amd64",
            "bindings": {"backend": {"go_version": "go1.26.8"}},
            "overlays": {
                "backend": {
                    name: notices.digest(raw)[7:] for name, raw in overlay.items()
                }
            },
            "additional_files": {"backend": {}},
        }
        self.files = {
            name.removeprefix("backend/licenses/"): raw
            for name, raw in self.committed.items()
            if name.startswith("backend/licenses/")
        }
        self.files.update({"runtime/" + name: raw for name, raw in overlay.items()})
        self.files["go/LICENSE"] = go_license
        self.files["SOURCE.txt"] = (
            f"psst.zip {self.context.version}\nRevision: {self.context.commit}\nSource: https://github.com/{self.context.repository}/archive/{self.context.commit}.tar.gz\nLicense: AGPL-3.0-only\n"
        ).encode()

    def write_image(self, layers):
        existing, _ = fixture(gzip_layer=False)
        index = read_json(existing["index.json"])
        old_manifest = read_json(
            existing["blobs/sha256/" + index["manifests"][0]["digest"][7:]]
        )
        config = read_json(
            existing["blobs/sha256/" + old_manifest["config"]["digest"][7:]]
        )
        config["rootfs"]["diff_ids"] = [notices.digest(raw) for raw in layers]
        config_raw = json_bytes(config)

        def descriptor(raw, media):
            return {"digest": notices.digest(raw), "size": len(raw), "mediaType": media}

        manifest = json_bytes(
            {
                "schemaVersion": 2,
                "mediaType": old_manifest["mediaType"],
                "config": descriptor(config_raw, old_manifest["config"]["mediaType"]),
                "layers": [
                    descriptor(raw, "application/vnd.oci.image.layer.v1.tar")
                    for raw in layers
                ],
            }
        )
        index["manifests"][0].update(descriptor(manifest, old_manifest["mediaType"]))
        files = {"oci-layout": existing["oci-layout"], "index.json": json_bytes(index)}
        files.update(
            {
                "blobs/sha256/" + notices.digest(raw)[7:]: raw
                for raw in (config_raw, manifest, *layers)
            }
        )
        self.archive.write_bytes(tar_bytes(files))
        return notices.inspect_archive(
            self.archive,
            platform=self.context.platform,
            repository=self.context.repository,
            version=self.context.version,
            commit=self.context.commit,
            tested_config=notices.digest(config_raw),
            component="backend",
        )

    def verify(self, layers=None, *, committed=None):
        image = self.write_image(
            layers
            or [
                tar_bytes(
                    {"app/licenses/" + name: raw for name, raw in self.files.items()}
                )
            ]
        )
        with patch.object(
            notices, "git", return_value=tar_bytes(committed or self.committed)
        ) as selected_git:
            result = notices.verify_backend_notices(
                self.context, self.archive, image=image, pack=self.pack, root=self.root
            )
            selected_git.assert_called_once_with(
                self.root,
                "archive",
                "--format=tar",
                self.context.commit,
                "backend/licenses",
                "LICENSE",
                "backend/go.mod",
                "backend/go.sum",
            )
            return result

    def test_exact_files_locks_and_generated_notice_origins(self):
        result = self.verify()
        self.assertEqual(
            result["notice_inventory_digest"],
            notices.digest(json_bytes(result["files"])),
        )
        self.assertEqual(result["committed_dependency_notice_count"], 1)
        self.assertFalse(result["corresponding_source_completeness_verified"])
        self.assertFalse(result["publication_authorized"])
        for name in (
            self.dep,
            "go/LICENSE",
            "SOURCE.txt",
            "runtime/THIRD_PARTY_NOTICES.txt",
        ):
            for action in ("delete", "alter"):
                with self.subTest(name=name, action=action):
                    changed = dict(self.files)
                    if action == "delete":
                        changed.pop(name)
                    else:
                        changed[name] += b"substitution"
                    with self.assertRaises(InvalidRelease):
                        self.verify(
                            [
                                tar_bytes(
                                    {
                                        "app/licenses/" + name: raw
                                        for name, raw in changed.items()
                                    }
                                )
                            ]
                        )
        committed = copy.deepcopy(self.committed)
        record = read_json(committed["backend/licenses/dependency-inventory.json"])
        record["inputs"]["go.sum"] = "b" * 64
        committed["backend/licenses/dependency-inventory.json"] = json_bytes(record)
        changed = dict(self.files)
        changed["dependency-inventory.json"] = committed[
            "backend/licenses/dependency-inventory.json"
        ]
        with self.assertRaisesRegex(InvalidRelease, "lock hashes differ"):
            self.verify(
                [
                    tar_bytes(
                        {"app/licenses/" + name: raw for name, raw in changed.items()}
                    )
                ],
                committed=committed,
            )

    def test_layer_removals_opaque_replacement_and_link_substitution(self):
        base = tar_bytes(
            {"app/licenses/" + name: raw for name, raw in self.files.items()}
        )
        # Whiteouts hide lower files regardless of their position in this layer.
        for name in (
            "app/licenses/.wh.AGPL-3.0-only.txt",
            "app/.wh.licenses",
            "app/licenses/.wh..wh..opq",
        ):
            with self.subTest(whiteout=name), self.assertRaises(InvalidRelease):
                self.verify([base, tar_bytes({name: b""})])
        replacement = {"app/licenses/" + name: raw for name, raw in self.files.items()}
        replacement["app/licenses/.wh..wh..opq"] = b""
        result = self.verify([base, tar_bytes(replacement)])
        self.assertEqual(len(result["files"]), len(self.files))
        for name in (
            "app",
            "app/licenses",
            "app/licenses/runtime",
            "app/licenses/go/LICENSE",
        ):
            with self.subTest(link=name), self.assertRaises(InvalidRelease):
                self.verify(
                    [base, tar_bytes({}, special=[(name, "/tmp/substitution")])]
                )
        with self.assertRaises(InvalidRelease):
            self.verify([base, tar_bytes({"app/licenses/unattributed.txt": b"extra"})])


if __name__ == "__main__":
    unittest.main()
