"""Tiny refusal/data checks; no dependency installation or upstream builds."""

from pathlib import Path
import tempfile
import unittest

import browser_preferred_source_relationships as preferred
import measure_browser_source_inventory as browser


class PreferredSources(unittest.TestCase):
    def fixture(self, name="qrcode", relative="lib/browser.js", raw=b"original\n"):
        temporary = tempfile.TemporaryDirectory(prefix="psst-preferred-test-")
        self.addCleanup(temporary.cleanup)
        tree = Path(temporary.name)
        location = "node_modules/" + name
        path = location + "/" + relative
        (tree / path).parent.mkdir(parents=True)
        (tree / path).write_bytes(raw)
        package = {"name": name, "version": "1.5.4", "lock_path": location}
        inventory = {
            "modules": [
                {
                    "package": package,
                    "module_path": path,
                    "source_sha256": browser.digest(raw),
                }
            ]
        }
        npm = {
            "archive": {
                "sha256": browser.digest(b"archive"),
                "members": {path: browser.digest(raw)},
            }
        }
        sources = {
            "qrcode": {
                "record": {
                    "id": "qrcode",
                    "commit": "a" * 40,
                    "archive": {"sha256": browser.digest(b"pinned")},
                    "packages": {name: "1.5.4"},
                },
                "files": {relative: raw},
            }
        }
        return tree, inventory, npm, sources

    def test_original_and_missing_or_changed_sources_refuse(self):
        tree, inventory, npm, sources = self.fixture()
        result = preferred.verify_preferred_relationships(inventory, tree, npm, sources)
        self.assertEqual(len(result["modules"]), 1)
        self.assertFalse(result["byte_reproduction_verified"])
        for files in ({}, {"lib/browser.js": b"changed"}):
            sources["qrcode"]["files"] = files
            with self.assertRaises(ValueError):
                preferred.verify_preferred_relationships(inventory, tree, npm, sources)
        (tree / inventory["modules"][0]["module_path"]).write_bytes(b"tampered")
        with self.assertRaisesRegex(ValueError, "authenticated npm"):
            preferred.verify_preferred_relationships(inventory, tree, npm, sources)

    def test_unclassified_rendered_input_is_not_covered(self):
        tree, inventory, npm, sources = self.fixture(name="unknown")
        with self.assertRaisesRegex(ValueError, "Unclassified rendered package"):
            preferred.verify_preferred_relationships(inventory, tree, npm, sources)

    def test_noble_configuration_must_match_all_locked_external_inputs(self):
        tree, inventory, npm, _ = self.fixture(
            name="@noble/curves", relative="index.js"
        )
        package = inventory["modules"][0]["package"]
        lock = {
            "version": "0.7.1",
            "resolved": "https://registry.npmjs.org/@paulmillr/jsbt/-/jsbt-0.7.1.tgz",
            "integrity": "sha512-" + "A" * 86 + "==",
        }
        config = browser.canonical({"compilerOptions": {"strict": True}})
        originals = {
            "noble-curves": {
                "record": {
                    "id": "noble-curves",
                    "commit": "a" * 40,
                    "archive": {"sha256": browser.digest(b"noble original")},
                    "packages": {package["name"]: package["version"]},
                },
                "files": {
                    "src/index.ts": b"original TypeScript source\n",
                    "package.json": browser.canonical({"scripts": {"build": "tsc"}}),
                    "tsconfig.json": browser.canonical(
                        {
                            "compilerOptions": {"rootDir": "src", "outDir": "."},
                            "extends": "@paulmillr/jsbt/tsconfig.json",
                        }
                    ),
                    "package-lock.json": browser.canonical(
                        {"packages": {"node_modules/@paulmillr/jsbt": lock}}
                    ),
                },
            },
            "jsbt": {
                "record": {
                    "id": "jsbt",
                    "commit": "b" * 40,
                    "archive": {"sha256": browser.digest(b"jsbt original")},
                    "relationship": {
                        "kind": "build-configuration",
                        "name": "@paulmillr/jsbt",
                        "version": "0.7.1",
                        "integrity": lock["integrity"],
                        "configuration_sha256": browser.digest(config),
                    },
                },
                "files": {
                    "tsconfig.json": config,
                    "package.json": browser.canonical(
                        {"name": "@paulmillr/jsbt", "version": "0.7.1"}
                    ),
                },
            },
        }
        result = preferred.verify_preferred_relationships(
            inventory, tree, npm, originals
        )
        external = next(iter(result["modules"].values()))["external_configuration"]
        self.assertTrue(external["retained"])
        self.assertEqual(external["source"]["sha256"], browser.digest(config))
        for name, replacement in (
            ("integrity", "sha512-wrong"),
            ("version", "0.7.2"),
            ("configuration_sha256", browser.digest(b"wrong")),
        ):
            relationship = originals["jsbt"]["record"]["relationship"]
            original = relationship[name]
            relationship[name] = replacement
            with self.subTest(name=name), self.assertRaisesRegex(
                ValueError, "external configuration differs"
            ):
                preferred.verify_preferred_relationships(
                    inventory, tree, npm, originals
                )
            relationship[name] = original
        del originals["jsbt"]
        with self.assertRaisesRegex(ValueError, "Missing pinned upstream"):
            preferred.verify_preferred_relationships(inventory, tree, npm, originals)

    def test_icon_original_data_and_aliases_are_required(self):
        record = {
            "id": "lucide",
            "commit": "b" * 40,
            "archive": {"sha256": browser.digest(b"lucide")},
            "packages": {"@lucide/svelte": "1.51.0"},
        }
        files = {
            "icons/x.svg": b'<svg><path d="M0 0"/></svg>',
            "icons/x.json": b'{"aliases":[{"name":"close"}]}',
        }
        sources = {"lucide": {"record": record, "files": files}}
        package = {"name": "@lucide/svelte", "version": "1.51.0"}
        raw = b'const iconData = {"name":"x","size":24,"node":[["path",{"d":"M0 0"}]],"aliases":["close"]};'
        self.assertEqual(
            len(preferred.lucide_icon(raw, sources, "dist/icons/x.svelte", package)), 2
        )
        with self.assertRaisesRegex(ValueError, "icon data differs"):
            preferred.lucide_icon(
                raw.replace(b"M0 0", b"M1 1"), sources, "dist/icons/x.svelte", package
            )
        del files["icons/x.json"]
        with self.assertRaisesRegex(ValueError, "Missing"):
            preferred.lucide_icon(raw, sources, "dist/icons/x.svelte", package)


if __name__ == "__main__":
    unittest.main()
