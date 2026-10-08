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

    def generator_fixture(self):
        tree, _, _, _ = self.fixture()
        project = {
            name: {"sha256": browser.digest(name.encode()), "git_blob": "a" * 40}
            for name in (
                "package.json",
                "package-lock.json",
                "svelte.config.js",
                "vite.config.ts",
                "Dockerfile",
            )
        }
        return tree, {"modules": [], "excluded_modules": []}, {}, {}, project

    def test_kit_generator_requires_preferred_bytes_and_all_rendered_inputs(self):
        tree, inventory, npm, sources, project = self.generator_fixture()
        generator = "node_modules/@sveltejs/kit/src/core/sync/write_root.js"
        path = ".svelte-kit/generated/root.js"
        raw = b"export function write_root() {}\n"
        derived = b"export const generated = true;\n"
        for name, content in ((generator, raw), (path, derived)):
            (tree / name).parent.mkdir(parents=True, exist_ok=True)
            (tree / name).write_bytes(content)
        npm["kit"] = {"members": {generator: browser.digest(raw)}}
        sources["sveltekit"] = {
            "record": {
                "id": "sveltekit",
                "commit": "a" * 40,
                "archive": {"sha256": browser.digest(b"original kit")},
                "packages": {"@sveltejs/kit": "2.70.3"},
            },
            "files": {"packages/kit/src/core/sync/write_root.js": raw},
        }
        inventory["modules"] = [
            {
                "id": "file:" + path,
                "kind": "generated-application",
                "module_path": path,
                "source_sha256": browser.digest(derived),
                "rendered_in": [{}],
            }
        ]
        row = {
            "file": path,
            "association": "reviewed-generator",
            "generator": generator,
            "function": "write_root",
            "invocation": "create -> write_root",
            "rendered_in_client_chunks": True,
        }
        plan = {
            "recipes": {
                "kit": {
                    "name": "@sveltejs/kit",
                    "version": "2.70.3",
                    "lock_path": "node_modules/@sveltejs/kit",
                    "members": ["src/core/sync/write_root.js"],
                }
            },
            "virtual_inputs": [],
            "generated_inputs": [row],
        }

        def verify():
            return preferred.verify_generator_relationships(
                inventory, tree, npm, sources, plan, {"modules": {}}, project
            )

        result = verify()
        self.assertEqual(set(result["generated_inputs"]), {path})
        self.assertFalse(result["byte_reproduction_verified"])
        sources["sveltekit"]["files"][
            "packages/kit/src/core/sync/write_root.js"
        ] = b"changed original"
        with self.assertRaisesRegex(ValueError, "Kit generator differs"):
            verify()
        sources["sveltekit"]["files"]["packages/kit/src/core/sync/write_root.js"] = raw
        plan["generated_inputs"] = []
        with self.assertRaisesRegex(ValueError, "Rendered application input missing"):
            verify()
        plan["generated_inputs"] = [row]
        (tree / path).write_bytes(b"changed generated input")
        with self.assertRaisesRegex(ValueError, "differs from measured source"):
            verify()

    def test_vite_templates_and_importer_are_bound_to_original_generators(self):
        tree, inventory, npm, sources, project = self.generator_fixture()
        body = b"\nexport function fixtureHelper() {}\n"
        helpers = (
            b"const HELPERS = `"
            + body
            + b"`;\n\nexport function getHelpersModule() {\n  return HELPERS;\n}"
        )
        preload = b"const preloadCode = `const scriptRel = ${scriptRel}; const assetsURL = ${assetsURL}; export const ${preloadMethod} = ${preload.toString()}`;\n"
        markers = b"\\0vite/preload-helper.js __vitePreload __VITE_PRELOAD__"
        renamed = (
            preload.split(b"`", 2)[1]
            .replace(b"${scriptRel}", b"${scriptRel2}")
            .replace(b"${assetsURL}", b"${assetsURL2}")
        )
        chunk = body + renamed + markers
        path = "node_modules/vite/dist/generator.js"
        manifest = browser.canonical(
            {
                "name": "vite",
                "version": "6.4.3",
                "devDependencies": {"@rollup/plugin-commonjs": "^28.0.3"},
            }
        )
        for name, raw in ((path, chunk), ("node_modules/vite/package.json", manifest)):
            (tree / name).parent.mkdir(parents=True, exist_ok=True)
            (tree / name).write_bytes(raw)
        npm["vite"] = {
            "members": {
                path: browser.digest(chunk),
                "node_modules/vite/package.json": browser.digest(manifest),
            }
        }
        lock = b"importers:\n  other-workspace:\n    devDependencies:\n      '@rollup/plugin-commonjs':\n        specifier: ^28.0.2\n        version: 28.0.2(rollup@4.34.9)\n  packages/vite:\n    devDependencies:\n      '@rollup/plugin-commonjs':\n        specifier: ^28.0.3\n        version: 28.0.3(rollup@4.34.9)\npackages:\n"
        vite_files = {
            "packages/vite/package.json": manifest,
            "packages/vite/rollup.config.ts": b"build recipe",
            "pnpm-lock.yaml": lock,
            "packages/vite/src/node/build.ts": b"build source",
            "packages/vite/src/node/plugins/importAnalysisBuild.ts": preload + markers,
        }
        commonjs_files = {
            name: b"original build input"
            for name in (
                "package.json",
                "pnpm-lock.yaml",
                "pnpm-workspace.yaml",
                "packages/commonjs/rollup.config.mjs",
                "shared/rollup.config.mjs",
                "packages/commonjs/src/index.js",
                "packages/commonjs/src/proxies.js",
                "packages/commonjs/src/utils.js",
            )
        }
        commonjs_files.update(
            {
                "packages/commonjs/package.json": browser.canonical(
                    {"name": "@rollup/plugin-commonjs", "version": "28.0.3"}
                ),
                "packages/commonjs/src/helpers.js": helpers,
            }
        )
        for identifier, files in (
            ("vite", vite_files),
            ("rollup-commonjs", commonjs_files),
        ):
            sources[identifier] = {
                "record": {
                    "id": identifier,
                    "commit": "a" * 40,
                    "archive": {"sha256": browser.digest(identifier.encode())},
                    "packages": {"vite": "6.4.3"},
                },
                "files": files,
            }
        row = {
            "id": "virtual:" + "a" * 64,
            "association": "reviewed-generator",
            "family": "vite-commonjs-helper",
            "module_path": None,
            "generator": path,
            "rendered_in_client_chunks": True,
        }
        inventory["modules"] = [
            {"id": row["id"], "kind": "virtual", "rendered_in": [{}]}
        ]
        plan = {
            "recipes": {
                "vite": {
                    "name": "vite",
                    "version": "6.4.3",
                    "lock_path": "node_modules/vite",
                    "generator": "dist/generator.js",
                }
            },
            "virtual_inputs": [row],
            "generated_inputs": [],
        }

        def verify():
            return preferred.verify_generator_relationships(
                inventory, tree, npm, sources, plan, {"modules": {}}, project
            )

        result = verify()
        self.assertEqual(
            result["generators"][path]["commonjs_helpers_sha256"], browser.digest(body)
        )
        self.assertFalse(result["byte_reproduction_verified"])
        vite_files["pnpm-lock.yaml"] = lock.replace(
            b"version: 28.0.3", b"version: 28.0.2"
        )
        with self.assertRaisesRegex(ValueError, "generator version differs"):
            verify()
        vite_files["pnpm-lock.yaml"] = lock
        plan["virtual_inputs"] = []
        with self.assertRaisesRegex(ValueError, "Rendered virtual input missing"):
            verify()
        plan["virtual_inputs"] = [row]
        row["family"] = "unknown"
        with self.assertRaisesRegex(ValueError, "Unclassified rendered virtual"):
            verify()
        row["family"] = "vite-commonjs-helper"
        changed = chunk.replace(b"fixtureHelper", b"changedHelper")
        (tree / path).write_bytes(changed)
        npm["vite"]["members"][path] = browser.digest(changed)
        with self.assertRaisesRegex(ValueError, "CommonJS helper differs"):
            verify()

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
