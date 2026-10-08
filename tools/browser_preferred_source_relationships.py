"""Map validated browser inputs to offered originals and build recipes.

This library consumes already authenticated capture/npm facts and pinned,
archive-verified upstream file maps. It does not authenticate an archive, approve
publication, execute upstream scripts, or claim byte-identical reproduction.
Upstream maps have {id: {record: catalog_record, files: {relative_path: bytes}}}.
"""

from __future__ import annotations

from pathlib import Path
import re
import xml.etree.ElementTree as ET

import measure_browser_source_inventory as browser


def require(condition, message):
    if not condition:
        raise ValueError("Browser preferred sources: " + message)


def source_fact(upstreams, identifier, path, package=None):
    path = browser.safe_path(path)
    entry = upstreams.get(identifier)
    require(isinstance(entry, dict), "Missing pinned upstream " + identifier)
    record = entry["record"]
    require(record["id"] == identifier, "Upstream identifier differs")
    if package:
        require(
            record.get("packages", {}).get(package["name"]) == package["version"],
            "Preferred upstream version differs from installed package",
        )
    raw = entry["files"].get(path)
    require(
        isinstance(raw, bytes) and len(raw) <= browser.MAX_FILE,
        "Missing or excessive preferred input " + identifier + ":" + path,
    )
    return raw, {
        "upstream": identifier,
        "commit": record["commit"],
        "archive_sha256": record["archive"]["sha256"],
        "path": path,
        "sha256": browser.digest(raw),
        "size": len(raw),
    }


def recipe_facts(upstreams, identifier, paths, package):
    return [source_fact(upstreams, identifier, path, package)[1] for path in paths]


def json_input(raw):
    return browser.json_record(raw)


def lucide_icon(raw, upstreams, relative, package):
    require(
        re.fullmatch(r"dist/icons/[a-z0-9-]+\.svelte", relative),
        "Unclassified Lucide icon path",
    )
    name = relative.removeprefix("dist/icons/").removesuffix(".svelte")
    svg, svg_fact = source_fact(upstreams, "lucide", "icons/" + name + ".svg", package)
    metadata, metadata_fact = source_fact(
        upstreams, "lucide", "icons/" + name + ".json", package
    )
    require(
        b"<!DOCTYPE" not in svg.upper() and b"<!ENTITY" not in svg.upper(),
        "Unexpected SVG declarations",
    )
    root = ET.fromstring(svg)
    require(root.tag.split("}")[-1] == "svg", "Invalid original SVG")
    require(all(len(child) == 0 for child in root), "Nested original icon nodes")
    expected = {
        "name": name,
        "size": 24,
        "node": [[child.tag.split("}")[-1], child.attrib] for child in root],
    }
    aliases = json_input(metadata).get("aliases", [])
    require(isinstance(aliases, list), "Invalid original aliases")
    if aliases:
        expected["aliases"] = [
            alias if isinstance(alias, str) else alias["name"] for alias in aliases
        ]
    matches = re.findall(r"const iconData = (.*?);", raw.decode("utf-8"))
    require(
        len(matches) == 1 and json_input(matches[0].encode()) == expected,
        "Generated icon data differs from pinned SVG/metadata",
    )
    return [svg_fact, metadata_fact]


def qr_sources(upstreams, package, relative):
    """Compare source-map originals, never execute/minify the retained code."""
    map_path = relative + ".map"
    raw, map_fact = source_fact(upstreams, "qr-scanner", map_path, package)
    mapping = json_input(raw)
    names, contents = mapping.get("sources"), mapping.get("sourcesContent")
    require(
        isinstance(names, list)
        and isinstance(contents, list)
        and len(names) == len(contents)
        and 0 < len(names) <= 100
        and len(names) == len(set(names)),
        "Invalid embedded source map",
    )
    facts = [map_fact]
    for name, content in zip(names, contents, strict=True):
        require(isinstance(content, str), "Embedded original source is absent")
        if name.startswith("node_modules/jsqr-es6/src/"):
            identifier, path = "qr-scanner-decoder", name.removeprefix(
                "node_modules/jsqr-es6/"
            )
        else:
            require(
                name in {"src/qr-scanner.ts", "src/worker.ts"},
                "Unknown embedded QR source",
            )
            identifier, path = "qr-scanner", name
        original, fact = source_fact(upstreams, identifier, path, package)
        require(
            original == content.encode("utf-8"),
            "Embedded QR source differs from pinned original",
        )
        facts.append(fact)
    return facts


def relationship(raw, relative, package, upstreams):
    """Return one explicit relationship for a known integrity-bound npm input."""
    name = package["name"]
    if name == "svelte" and relative == "compiler/index.js":
        return {
            "relationship": "compiler-bundle-from-preferred-javascript",
            "inputs": [
                source_fact(
                    upstreams,
                    "svelte",
                    "packages/svelte/src/compiler/index.js",
                    package,
                )[1]
            ],
            "recipes": recipe_facts(
                upstreams,
                "svelte",
                [
                    "package.json",
                    "pnpm-lock.yaml",
                    "packages/svelte/package.json",
                    "packages/svelte/rollup.config.js",
                    "packages/svelte/scripts/generate-version.js",
                    "packages/svelte/scripts/process-messages/index.js",
                ],
                package,
            ),
        }
    direct = {
        "svelte": ("svelte", "packages/svelte/"),
        "@sveltejs/kit": ("sveltekit", "packages/kit/"),
        "@sveltejs/adapter-static": ("sveltekit", "packages/adapter-static/"),
        "qrcode": ("qrcode", ""),
    }
    if name in direct:
        identifier, prefix = direct[name]
        original, fact = source_fact(upstreams, identifier, prefix + relative, package)
        require(original == raw, "Installed input differs from preferred original")
        return {
            "relationship": "byte-identical-original",
            "inputs": [fact],
            "recipes": [],
        }
    if name in {"dijkstrajs", "esm-env", "@sveltejs/vite-plugin-svelte"}:
        require(
            (name == "dijkstrajs" and relative == "dijkstra.js")
            or (
                name == "esm-env"
                and relative
                in {"true.js", "false.js", "dev-browser.js", "dev-server.js"}
            )
            or (
                name == "@sveltejs/vite-plugin-svelte"
                and relative.startswith("src/")
                and relative.endswith(".js")
            ),
            "Unclassified original npm input",
        )
        return {"relationship": "original-npm-javascript", "inputs": [], "recipes": []}
    if name == "qr-scanner":
        require(
            relative in {"qr-scanner.min.js", "qr-scanner-worker.min.js"},
            "Unclassified QR scanner input",
        )
        original, fact = source_fact(upstreams, "qr-scanner", relative, package)
        require(original == raw, "QR scanner input differs from pinned original")
        return {
            "relationship": "generated-with-embedded-originals",
            "inputs": [fact, *qr_sources(upstreams, package, relative)],
            "recipes": recipe_facts(
                upstreams,
                "qr-scanner",
                ["package.json", "rollup.config.js", "tsconfig.json", "yarn.lock"],
                package,
            ),
        }
    noble = {
        "@noble/curves": "noble-curves",
        "@noble/hashes": "noble-hashes",
        "@noble/ciphers": "noble-ciphers",
    }
    if name in noble:
        require(relative.endswith(".js"), "Unclassified Noble input")
        identifier = noble[name]
        preferred = "src/" + relative.removesuffix(".js") + ".ts"
        _, fact = source_fact(upstreams, identifier, preferred, package)
        package_raw, _ = source_fact(upstreams, identifier, "package.json", package)
        config_raw, _ = source_fact(upstreams, identifier, "tsconfig.json", package)
        config, manifest = json_input(config_raw), json_input(package_raw)
        require(
            manifest.get("scripts", {}).get("build") == "tsc"
            and config.get("compilerOptions", {}).get("rootDir") == "src"
            and config["compilerOptions"].get("outDir") == "."
            and config.get("extends") == "@paulmillr/jsbt/tsconfig.json",
            "Noble source emission recipe changed",
        )
        lock_raw, _ = source_fact(upstreams, identifier, "package-lock.json", package)
        jsbt = (
            json_input(lock_raw).get("packages", {}).get("node_modules/@paulmillr/jsbt")
        )
        require(
            isinstance(jsbt, dict)
            and jsbt.get("version") == "0.7.1"
            and jsbt.get("resolved")
            == "https://registry.npmjs.org/@paulmillr/jsbt/-/jsbt-0.7.1.tgz"
            and isinstance(jsbt.get("integrity"), str),
            "Noble external config lock absent",
        )
        return {
            "relationship": "typescript-emission",
            "inputs": [fact],
            "recipes": recipe_facts(
                upstreams,
                identifier,
                ["package.json", "tsconfig.json", "package-lock.json"],
                package,
            ),
            "external_configuration": {
                "name": "@paulmillr/jsbt",
                "path": "tsconfig.json",
                **{key: jsbt[key] for key in ("version", "resolved", "integrity")},
            },
        }
    if name in {"hpke", "@panva/hpke-noble"}:
        require(relative == "index.js", "Unclassified HPKE input")
        preferred = "index.ts" if name == "hpke" else "examples/noble-suite/index.ts"
        _, fact = source_fact(upstreams, "hpke", preferred, package)
        recipes = [
            "package.json",
            "build.cjs",
            "tools/clean-javascript.cjs",
            "tsconfig.json",
            "package-lock.json",
        ]
        if name != "hpke":
            recipes += [
                "examples/noble-suite/package.json",
                "examples/noble-suite/tsconfig.json",
            ]
        else:
            original, emitted = source_fact(upstreams, "hpke", "index.js", package)
            require(
                original == raw, "HPKE generated input differs from committed original"
            )
            return {
                "relationship": "byte-identical-upstream-emission",
                "inputs": [fact, emitted],
                "recipes": recipe_facts(upstreams, "hpke", recipes, package),
            }
        return {
            "relationship": "type-stripping-and-import-rewrite",
            "inputs": [fact],
            "recipes": recipe_facts(upstreams, "hpke", recipes, package),
        }
    if name == "fflate":
        require(relative == "esm/browser.js", "Unclassified fflate input")
        return {
            "relationship": "typescript-emission-and-browser-worker-rewrite",
            "inputs": [
                source_fact(upstreams, "fflate", path, package)[1]
                for path in ["src/index.ts", "src/worker.ts"]
            ],
            "recipes": recipe_facts(
                upstreams,
                "fflate",
                [
                    "package.json",
                    "package-lock.json",
                    "tsconfig.json",
                    "tsconfig.esm.json",
                    "scripts/rewriteBuilds.ts",
                ],
                package,
            ),
        }
    if name == "clsx":
        require(relative == "dist/clsx.mjs", "Unclassified clsx input")
        return {
            "relationship": "javascript-minification",
            "inputs": [source_fact(upstreams, "clsx", "src/index.js", package)[1]],
            "recipes": recipe_facts(
                upstreams, "clsx", ["package.json", "bin/index.js"], package
            ),
        }
    if name == "@lucide/svelte":
        recipes = [
            "packages/svelte/package.json",
            "packages/svelte/svelte.config.js",
            "packages/svelte/tsconfig.json",
            "packages/svelte/scripts/license.mts",
            "packages/svelte/scripts/appendBlockComments.mts",
            "pnpm-lock.yaml",
        ]
        if relative.startswith("dist/icons/"):
            facts = lucide_icon(raw, upstreams, relative, package)
            recipes += [
                "packages/svelte/scripts/exportTemplate.mts",
                "tools/build-icons/index.ts",
                "tools/build-icons/building/generateIconFiles.ts",
                "tools/build-icons/render/renderIconsObject.ts",
                "tools/build-icons/utils/getIconMetaData.ts",
                "tools/build-icons/package.json",
            ]
            kind = "svg-and-metadata-icon-generation"
        else:
            mapping = {
                "dist/Icon.svelte": "packages/svelte/src/Icon.svelte",
                "dist/context.js": "packages/svelte/src/context.ts",
                "dist/utils/buildLucideIconNode.js": "packages/shared/src/build/buildLucideIconNode.ts",
                "dist/utils/defaultAttributes.js": "packages/shared/src/build/defaultAttributes.ts",
                "dist/utils/hasA11yProp.js": "packages/shared/src/utils/hasA11yProp.ts",
                "dist/utils/mergeClasses.js": "packages/shared/src/utils/mergeClasses.ts",
            }
            require(relative in mapping, "Unclassified Lucide input")
            facts = [source_fact(upstreams, "lucide", mapping[relative], package)[1]]
            recipes += ["packages/svelte/scripts/patchCopiedUtils.mts"]
            kind = "svelte-packaging-and-type-stripping"
        return {
            "relationship": kind,
            "inputs": facts,
            "recipes": recipe_facts(upstreams, "lucide", recipes, package),
        }
    require(False, "Unclassified rendered package " + name)


def verify_preferred_relationships(inventory, tree: Path, npm_members, upstreams):
    """Resolve every actually rendered package input using authenticated facts.

    Nonpackage, virtual and excluded rows remain the producer's other categories.
    npm_members is the unchanged mapping returned by native.npm_members().
    External build configurations are reported explicitly, not marked retained.
    """
    members = {}
    for archive in npm_members.values():
        for path, checksum in archive["members"].items():
            require(path not in members, "Duplicate authenticated npm member")
            members[path] = (checksum, archive["sha256"])
    facts = {}
    for row in inventory["modules"]:
        package = row.get("package")
        if package is None:
            continue
        path = browser.safe_path(row["module_path"])
        prefix = browser.safe_path(package["lock_path"]) + "/"
        require(
            path.startswith(prefix) and path not in facts,
            "Rendered package path differs or repeats",
        )
        raw = browser.read_file(tree, path)
        checksum = browser.digest(raw)
        require(
            path in members and checksum == members[path][0] == row["source_sha256"],
            "Rendered input differs from authenticated npm original",
        )
        facts[path] = {
            "sha256": checksum,
            "npm_archive_sha256": members[path][1],
            **relationship(raw, path[len(prefix) :], package, upstreams),
        }
    return {
        "kind": "browser-preferred-source-relationships",
        "modules": facts,
        "byte_reproduction_verified": False,
    }


def verify_captured_compiler_inputs(tree: Path, npm_members, upstreams, recipes):
    """Map validated compiler/plugin capture catalog as a separate category.

    Pass the recipes dictionary returned by native.recipe_plan(). Both captures
    are required. Vite virtual modules and Kit generated application outputs
    remain separate producer associations.
    """
    rows = []
    authenticated = {
        path: checksum
        for archive in npm_members.values()
        for path, checksum in archive["members"].items()
    }
    for key, expected_name in (
        ("svelte_compiler", "svelte"),
        ("svelte_plugin", "@sveltejs/vite-plugin-svelte"),
    ):
        recipe = recipes.get(key)
        require(
            isinstance(recipe, dict) and recipe.get("name") == expected_name,
            "Compiler/preprocessor recipe capture is absent",
        )
        location = browser.safe_path(recipe["lock_path"])
        require(
            location == "node_modules/" + expected_name,
            "Compiler/preprocessor location differs",
        )
        require(
            isinstance(recipe["members"], list)
            and 0 < len(recipe["members"]) <= 512
            and len(set(recipe["members"])) == len(recipe["members"]),
            "Invalid captured compiler members",
        )
        for relative in recipe["members"]:
            path = location + "/" + browser.safe_path(relative)
            require(
                path in authenticated, "Compiler input lacks authenticated npm original"
            )
            rows.append(
                {
                    "package": {
                        "name": expected_name,
                        "version": recipe["version"],
                        "lock_path": location,
                    },
                    "module_path": path,
                    "source_sha256": authenticated[path],
                }
            )
    result = verify_preferred_relationships(
        {"modules": rows}, tree, npm_members, upstreams
    )
    return {
        "kind": "browser-compiler-preferred-source-relationships",
        "inputs": result["modules"],
        "byte_reproduction_verified": False,
    }
