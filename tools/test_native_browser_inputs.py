"""Tiny real Git, npm and OCI byte fixtures exercise the private browser boundary."""

from __future__ import annotations

import base64
import hashlib
import io
import subprocess
import tarfile
import unittest
from unittest.mock import Mock

import measure_native_browser_inputs as native
import measure_browser_source_inventory as browser
from generate_release_gate_reports import NativeSourceContext
from release_artifacts import InvalidRelease, json_bytes, read_json
import test_release_browser_inventory as browser_fixtures
from test_release_oci import fixture


def tar_bytes(files):
    stream = io.BytesIO()
    with tarfile.open(
        fileobj=stream, mode="w:", format=tarfile.USTAR_FORMAT
    ) as archive:
        for name, raw in sorted(files.items()):
            member = tarfile.TarInfo(name)
            member.size = len(raw)
            archive.addfile(member, io.BytesIO(raw))
    return stream.getvalue()


class NativeBrowser(unittest.TestCase):
    def setUp(self):
        self.local = browser_fixtures.BrowserInventory()
        self.local.setUp()
        self.addCleanup(self.local.doCleanups)
        self.web = self.local.root
        self.root = self.web.parent
        self.pack, self.archive = self.root / "browser.tar", self.root / "web.oci.tar"
        for name in native.FIXED - {"package-lock.json"}:
            self.local.write(self.web, name, b"fixed Git input\n")
        location = self.local.package_path
        npm = tar_bytes(
            {
                "package/package.json": (
                    self.web / location / "package.json"
                ).read_bytes(),
                "package/index.js": (self.web / location / "index.js").read_bytes(),
            }
        )
        import gzip

        npm = gzip.compress(npm, mtime=0)
        integrity = "sha512-" + base64.b64encode(hashlib.sha512(npm).digest()).decode()
        self.local.package["integrity"] = integrity
        self.local.lock["packages"][location]["integrity"] = integrity
        lock = self.local.write(self.web, "package-lock.json", self.local.lock)
        self.local.inventory["source"]["package_lock"]["sha256"] = browser.digest(lock)
        npm_name = (
            "npm/" + hashlib.sha256((location + "@1.2.3").encode()).hexdigest() + ".tgz"
        )
        self.local.write(self.web, npm_name, npm)
        self.git("init", "--quiet")
        self.git("add", *["web/" + name for name in native.FIXED], "web/src/main.ts")
        self.git(
            "-c",
            "user.name=fixture",
            "-c",
            "user.email=fixture@example.invalid",
            "commit",
            "--quiet",
            "-m",
            "fixture",
        )
        revision = self.git("rev-parse", "HEAD").decode().strip()
        self.context = NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", revision, "linux/amd64"
        )
        self.local.inventory["source"].update(
            version=self.context.version, revision=revision
        )
        self.local.save()
        self.names = (
            native.selected(self.local.inventory)
            | {"build/" + name for name in native.static_facts(self.web / "build")}
            | {npm_name}
        )
        self.save_pack()
        self.runtime = {"overlays": {"web": {}}, "additional_files": {"web": {}}}
        self.save_oci()

    def git(self, *args):
        return subprocess.check_output(
            ["git", "-C", str(self.root), *args], stderr=subprocess.DEVNULL
        )

    def save_pack(self, omit=None):
        self.pack.write_bytes(
            tar_bytes(
                {
                    name: (self.web / name).read_bytes()
                    for name in self.names
                    if name != omit
                }
            )
        )

    def save_oci(self, changes=None):
        static = {
            "srv/web/" + name: (self.web / "build" / name).read_bytes()
            for name in native.static_facts(self.web / "build")
        }
        static.update(changes or {})
        layer = tar_bytes(static)
        files, config_id = fixture("web", gzip_layer=False)
        config = read_json(files["blobs/sha256/" + config_id[7:]])
        config["rootfs"]["diff_ids"] = [browser.digest(layer)]
        config["config"]["Labels"][
            "org.opencontainers.image.revision"
        ] = self.context.commit
        config["config"]["Labels"]["zip.psst.source.archive"] = (
            "https://github.com/endorses/psst.zip/archive/"
            + self.context.commit
            + ".tar.gz"
        )
        config = json_bytes(config)
        self.config = browser.digest(config)

        def desc(raw, kind):
            return {"mediaType": kind, "digest": browser.digest(raw), "size": len(raw)}

        manifest = json_bytes(
            {
                "schemaVersion": 2,
                "mediaType": "application/vnd.oci.image.manifest.v1+json",
                "config": desc(config, "application/vnd.oci.image.config.v1+json"),
                "layers": [desc(layer, "application/vnd.oci.image.layer.v1.tar")],
            }
        )
        index = json_bytes(
            {
                "schemaVersion": 2,
                "mediaType": "application/vnd.oci.image.index.v1+json",
                "manifests": [
                    {
                        **desc(manifest, "application/vnd.oci.image.manifest.v1+json"),
                        "platform": {"os": "linux", "architecture": "amd64"},
                    }
                ],
            }
        )
        self.archive.write_bytes(
            tar_bytes(
                {
                    "oci-layout": files["oci-layout"],
                    "index.json": index,
                    **{
                        "blobs/sha256/" + browser.digest(raw)[7:]: raw
                        for raw in (config, manifest, layer)
                    },
                }
            )
        )

    def replay(self):
        return native.replay(
            self.context,
            self.pack,
            self.archive,
            self.config,
            self.runtime,
            source_root=self.root,
        )

    def test_exact_git_npm_and_final_bytes_then_missing_or_substituted_inputs(self):
        result = self.replay()
        self.assertTrue(result["oci_image_verified"])
        self.assertTrue(result["git_source_binding_verified"])
        self.assertFalse(result["browser_module_closure_verified"])
        self.assertFalse(result["distribution_authorized"])
        self.save_pack(omit=".svelte-kit/generated/client.js")
        with self.assertRaises((ValueError, InvalidRelease)):
            self.replay()
        self.local.write(self.web, "src/main.ts", b"changed application source\n")
        self.local.inventory["modules"][0]["source_sha256"] = browser.digest(
            b"changed application source\n"
        )
        self.local.save()
        self.save_pack()
        self.save_oci()
        with self.assertRaisesRegex(InvalidRelease, "exact Git blob"):
            self.replay()

    def test_changed_final_static_bytes_and_npm_member_bytes_fail_after_rehashing(self):
        self.save_oci({"srv/web/appearance.js": b"different copied script\n"})
        with self.assertRaisesRegex(InvalidRelease, "static bytes differ"):
            self.replay()
        location = self.local.package_path + "/index.js"
        self.local.write(self.web, location, b"changed installed module\n")
        self.local.inventory["modules"][1]["source_sha256"] = browser.digest(
            b"changed installed module\n"
        )
        self.local.save()
        self.save_pack()
        self.save_oci()
        with self.assertRaisesRegex(InvalidRelease, "integrity-bound archive member"):
            self.replay()

    def test_missing_builder_prevents_native_browser_measurement(self):
        operations = Mock()
        operations.image.return_value = {
            "Id": "sha256:" + "b" * 64,
            "Os": "linux",
            "Architecture": "amd64",
        }
        with self.assertRaisesRegex(InvalidRelease, "builder config/platform differs"):
            native.collect(
                operations,
                self.context,
                "sha256:" + "a" * 64,
                self.root,
                self.root,
                self.archive,
                self.config,
                self.runtime,
                self.root / "collected",
            )
        operations.run.assert_not_called()
        self.assertFalse((self.root / "collected").exists())


if __name__ == "__main__":
    unittest.main()
