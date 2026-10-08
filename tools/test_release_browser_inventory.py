"""Local byte-replay fixtures confer no browser source or publication approval."""

from __future__ import annotations

import base64
import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import measure_browser_source_inventory as measurement


class BrowserInventory(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(
            prefix="psst-browser-inventory-test-"
        )
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "web"
        self.output = self.root / "build"
        self.output.mkdir(parents=True)
        self.version, self.revision = "v0.1.0", "a" * 40
        self.chunk = "_app/immutable/chunks/main.js"
        self.package_path = "node_modules/@fixture/icons"
        package_raw = self.write(
            self.root,
            self.package_path + "/package.json",
            {"name": "@fixture/icons", "version": "1.2.3"},
        )
        integrity = "sha512-" + base64.b64encode(b"x" * 64).decode()
        self.package = {
            "name": "@fixture/icons",
            "version": "1.2.3",
            "integrity": integrity,
            "lock_path": self.package_path,
            "package_json_sha256": measurement.digest(package_raw),
        }
        self.lock = {
            "lockfileVersion": 3,
            "packages": {
                "": {"name": "web"},
                self.package_path: {
                    "version": "1.2.3",
                    "integrity": integrity,
                    "resolved": "https://registry.npmjs.org/@fixture/icons/-/icons-1.2.3.tgz",
                },
            },
        }
        lock_raw = self.write(self.root, "package-lock.json", self.lock)
        app = self.module("src/main.ts", "application-source", rendered=True)
        package = self.module(
            self.package_path + "/index.js",
            "package-source",
            rendered=True,
            package=self.package,
        )
        generated = self.module(
            ".svelte-kit/generated/client.js", "generated-application", rendered=False
        )
        virtual = {
            "id": "virtual:" + "b" * 64,
            "kind": "virtual",
            "module_path": None,
            "source_sha256": None,
            "package": None,
            "transform_input_sha256": measurement.digest(
                b"opaque transformed virtual input"
            ),
            "rollup_input_sha256": None,
            "rendered_in": [{"file": self.chunk, "rendered_length": 1}],
        }
        self.inventory = {
            "schema_version": 1,
            "kind": "browser-module-inventory",
            "source": {
                "version": self.version,
                "revision": self.revision,
                "package_lock": {
                    "file": "package-lock.json",
                    "sha256": measurement.digest(lock_raw),
                },
            },
            "execution": "vite-rollup-client-build",
            "outputs": [
                self.output_row(self.chunk, "chunk", b"console.log('built client');\n"),
                self.output_row(
                    "worker.js",
                    "asset",
                    b"console.log('unattributed emitted asset');\n",
                ),
                self.output_row("style.css", "asset", b"body{}"),
            ],
            "build_metadata_outputs": [],
            "modules": [app, package, virtual],
            "excluded_modules": [generated],
            **{key: False for key in measurement.FLAGS},
        }
        metadata_root = self.root / ".svelte-kit/output/client"
        raw = self.write(
            metadata_root, ".vite/manifest.json", {"src/main.ts": {"file": self.chunk}}
        )
        self.inventory["build_metadata_outputs"].append(
            {
                "file": ".vite/manifest.json",
                "type": "asset",
                "sha256": measurement.digest(raw),
                "size": len(raw),
            }
        )
        self.write(
            self.output, "appearance.js", b"console.log('copied static script');\n"
        )
        self.save()

    def write(self, root, name, value):
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        raw = measurement.canonical(value) if isinstance(value, dict) else value
        path.write_bytes(raw)
        return raw

    def module(self, name, kind, *, rendered, package=None):
        raw = self.write(self.root, name, ("Source fixture " + name + "\n").encode())
        result = {
            "id": "file:" + name,
            "kind": kind,
            "module_path": name,
            "source_sha256": measurement.digest(raw),
            "package": package,
            "transform_input_sha256": measurement.digest(
                b"opaque transform observation"
            ),
            "rollup_input_sha256": measurement.digest(b"opaque parser observation"),
            "rendered_in": [{"file": self.chunk, "rendered_length": 5}]
            if rendered
            else [],
        }
        if not rendered:
            result["reason"] = "not-in-client-chunks"
        return result

    def output_row(self, name, kind, raw):
        self.write(self.output, name, raw)
        return {
            "file": name,
            "type": kind,
            "sha256": measurement.digest(raw),
            "size": len(raw),
        }

    def save(self):
        self.write(self.output, measurement.INVENTORY, self.inventory)

    def verify(self, **changes):
        return measurement.verify(
            self.root,
            self.output,
            changes.get("version", self.version),
            changes.get("revision", self.revision),
        )

    def test_exact_source_outputs_packages_and_static_scripts_are_measured_without_approval(
        self,
    ):
        result = self.verify()
        self.assertEqual(result["observed_module_count"], 4)
        self.assertEqual(result["rendered_modules"], 3)
        self.assertEqual(result["excluded_modules"], 1)
        self.assertEqual(result["physical_modules"], 3)
        self.assertEqual(result["virtual_modules"], 1)
        self.assertEqual(result["rendered_package_count"], 1)
        self.assertEqual(result["rendered_packages"], [self.package])
        self.assertEqual(result["verified_output_count"], 3)
        self.assertEqual(result["build_metadata_output_count"], 1)
        self.assertFalse((self.output / ".vite/manifest.json").exists())
        self.assertEqual(
            [
                (row["file"], row["origin"])
                for row in result["unattributed_javascript_outputs"]
            ],
            [("appearance.js", "not-in-inventory"), ("worker.js", "emitted-asset")],
        )
        for flag in measurement.FLAGS | {
            "oci_image_verified",
            "git_source_binding_verified",
        }:
            self.assertIs(result[flag], False)
        serialized = measurement.canonical(result)
        self.assertNotIn(str(self.root).encode(), serialized)
        self.assertNotIn(b"console.log", serialized)

    def test_missing_or_tampered_output_and_metadata_are_refused(self):
        for path in (
            self.output / self.chunk,
            self.root / ".svelte-kit/output/client/.vite/manifest.json",
        ):
            original = path.read_bytes()
            path.write_bytes(original + b"changed")
            with (
                self.subTest(path=path.name),
                self.assertRaisesRegex(ValueError, "Output bytes differ"),
            ):
                self.verify()
            path.unlink()
            with self.assertRaises(ValueError):
                self.verify()
            path.write_bytes(original)

    def test_changed_lock_package_manifest_source_and_excluded_source_are_refused(self):
        for name in (
            "package-lock.json",
            self.package_path + "/package.json",
            self.package_path + "/index.js",
            ".svelte-kit/generated/client.js",
        ):
            path = self.root / name
            original = path.read_bytes()
            path.write_bytes(original + b"changed")
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.verify()
            path.write_bytes(original)

    def test_stale_source_and_true_or_missing_approval_fields_are_refused(self):
        with self.assertRaises(ValueError):
            self.verify(version="v0.2.0")
        with self.assertRaises(ValueError):
            self.verify(revision="b" * 40)
        for flag in measurement.FLAGS:
            original = copy.deepcopy(self.inventory)
            for value in (True, 0, None):
                self.inventory[flag] = value
                self.save()
                with (
                    self.subTest(flag=flag, value=value),
                    self.assertRaises(ValueError),
                ):
                    self.verify()
            del self.inventory[flag]
            self.save()
            with self.assertRaises(ValueError):
                self.verify()
            self.inventory = original
        self.save()

    def test_path_traversal_absolute_paths_symlinks_and_unobserved_links_are_refused(
        self,
    ):
        for name in (
            "../outside.js",
            "/tmp/source.js",
            "src/../main.ts",
            "src/main.ts?raw",
            "src\\main.ts",
        ):
            original = copy.deepcopy(self.inventory)
            self.inventory["modules"][0]["module_path"] = name
            self.save()
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.verify()
            self.inventory = original
        self.save()
        path = self.root / self.package_path / "index.js"
        original = path.read_bytes()
        target = self.root / "other.js"
        target.write_bytes(original)
        path.unlink()
        path.symlink_to(target)
        with self.assertRaises(ValueError):
            self.verify()
        path.unlink()
        path.write_bytes(original)
        extra = self.output / "linked.js"
        extra.symlink_to(target)
        with self.assertRaisesRegex(ValueError, "Linked static"):
            self.verify()

    def test_duplicate_modules_outputs_and_false_rendered_correspondence_refuse(self):
        cases = (
            lambda value: value["modules"].append(copy.deepcopy(value["modules"][0])),
            lambda value: value["outputs"].append(copy.deepcopy(value["outputs"][0])),
            lambda value: value["modules"][0]["rendered_in"][0].update(
                file="worker.js"
            ),
            lambda value: value["modules"][0]["rendered_in"][0].update(
                rendered_length=True
            ),
            lambda value: value["excluded_modules"][0].update(reason="approved"),
            lambda value: value["outputs"][0].update(size=True),
            lambda value: value["outputs"][0].update(type="source"),
        )
        original = copy.deepcopy(self.inventory)
        for mutate in cases:
            self.inventory = copy.deepcopy(original)
            mutate(self.inventory)
            self.save()
            with self.assertRaises(ValueError):
                self.verify()

    def test_lock_package_location_registry_and_manifest_bindings_refuse(self):
        original_lock = copy.deepcopy(self.lock)
        for change in (
            lambda item: item.update(resolved="https://evil.invalid/pkg.tgz"),
            lambda item: item.update(link=True),
            lambda item: item.update(version="9.0.0"),
            lambda item: item.update(integrity="sha512-invalid"),
        ):
            self.lock = copy.deepcopy(original_lock)
            change(self.lock["packages"][self.package_path])
            raw = self.write(self.root, "package-lock.json", self.lock)
            self.inventory["source"]["package_lock"]["sha256"] = measurement.digest(raw)
            self.save()
            with self.assertRaises(ValueError):
                self.verify()

    def test_unknown_or_final_advertised_build_metadata_is_refused(self):
        original = copy.deepcopy(self.inventory)
        self.inventory["build_metadata_outputs"][0]["file"] = ".vite/private.json"
        self.save()
        with self.assertRaises(ValueError):
            self.verify()
        self.inventory = original
        self.inventory["outputs"].append(
            copy.deepcopy(self.inventory["build_metadata_outputs"][0])
        )
        self.save()
        with self.assertRaises(ValueError):
            self.verify()

    def test_changed_source_during_verification_cannot_create_success_fact(self):
        real_fact, calls = measurement.file_fact, []

        def mutate(root, name):
            actual = real_fact(root, name)
            if name == "src/main.ts" and not calls:
                (root / name).write_bytes((root / name).read_bytes() + b"changed")
                calls.append(name)
            return actual

        with patch.object(measurement, "file_fact", side_effect=mutate):
            with self.assertRaisesRegex(ValueError, "changed during verification"):
                self.verify()

    def test_bounds_duplicate_json_keys_and_cli_no_overwrite(self):
        proof = self.root.parent / "measurement.json"
        args = [
            "--root",
            str(self.root),
            "--output-directory",
            str(self.output),
            "--version",
            self.version,
            "--revision",
            self.revision,
            "--output",
            str(proof),
        ]
        measurement.main(args)
        self.assertIs(json.loads(proof.read_bytes())["publication_authorized"], False)
        with self.assertRaisesRegex(ValueError, "already exists"):
            measurement.main(args)
        with patch.object(measurement, "MAX_MODULES", 2):
            with self.assertRaises(ValueError):
                self.verify()
        with self.assertRaisesRegex(ValueError, "Duplicate JSON"):
            measurement.json_record(b'{"value":1,"value":2}')
        with self.assertRaises(ValueError):
            measurement.json_record(b'{"value":NaN}')

    def test_cached_package_manifest_mutation_is_refused_at_terminal_recheck(self):
        real_read, calls = measurement.read_file, []

        def mutate(root, name):
            raw = real_read(root, name)
            if name == self.package_path + "/package.json" and not calls:
                (root / name).write_bytes(raw + b" ")
                calls.append(name)
            return raw

        with patch.object(measurement, "read_file", side_effect=mutate):
            with self.assertRaisesRegex(
                ValueError, "package manifest changed during verification"
            ):
                self.verify()

    def test_local_dev_main_identity_supported_without_reproduction_claim(self):
        self.inventory["source"].update(version="dev", revision="main")
        self.save()
        self.assertIs(
            self.verify(version="dev", revision="main")["source_reproduction_verified"],
            False,
        )


if __name__ == "__main__":
    unittest.main()
