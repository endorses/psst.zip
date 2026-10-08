"""Associate authenticated Go module inputs with pinned preferred originals.

Callers verify original archives and Go ZIP H1 independently. This helper checks
origin identity, every regular proxy member, and explicit generator relationships;
it never executes generators or approves source completeness/publication.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import PurePosixPath
import re

from release_artifacts import require

SQLITE_ID = "873d4e274b4988d260ba8354a9718324a1c26187a4ab4c1cc0227c03d0f10e70"
TOOLS = {
    "modernc.org/cc/v4": "v4.26.0",
    "modernc.org/ccgo/v4": "v4.26.0",
    "modernc.org/fileutil": "v1.3.1",
}
TOOL_ENTRYPOINTS = {
    "modernc.org/cc/v4": ("cc.go",),
    "modernc.org/ccgo/v4": ("lib/ccgo.go", "lib/compile.go"),
    "modernc.org/fileutil": ("ccgo/util.go",),
}


def digest(raw):
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def checked_files(files):
    require(isinstance(files, dict) and files, "Missing regular source members")
    for path, raw in files.items():
        require(
            isinstance(path, str)
            and path not in ("", ".")
            and path == str(PurePosixPath(path))
            and not path.startswith("/")
            and "\\" not in path
            and "\x00" not in path
            and not any(
                ord(character) < 32 or ord(character) == 127 for character in path
            )
            and ".." not in PurePosixPath(path).parts
            and isinstance(raw, bytes),
            "Invalid regular source member",
        )
    return files


def entry(upstreams, identifier, module, version):
    value = upstreams.get(identifier)
    require(isinstance(value, dict), "Missing upstream " + identifier)
    record = value["record"]
    require(
        record.get("id") == identifier
        and record.get("go_modules", {}).get(module) == version
        and re.fullmatch(r"[0-9a-f]{40}", record.get("commit", "")),
        "Upstream module identity differs",
    )
    return record, checked_files(value["files"])


def facts(files, paths):
    result = []
    for path in sorted(set(paths)):
        require(path in files, "Missing preferred input " + path)
        result.append(
            {"path": path, "sha256": digest(files[path]), "size": len(files[path])}
        )
    return result


def project(upstreams, module_inputs, identifier, module, version):
    record, preferred = entry(upstreams, identifier, module, version)
    value = module_inputs.get(module)
    require(isinstance(value, dict), "Missing authenticated module " + module)
    proxy_record = value["record"]
    require(
        proxy_record.get("module") == module and proxy_record.get("version") == version,
        "Proxy module version differs",
    )
    origin = value.get("origin")
    expected = {
        "VCS": "git",
        "URL": "https://gitlab.com/" + record["repository"],
        "Hash": record["commit"],
        "Ref": "refs/tags/" + version,
    }
    require(
        isinstance(origin, dict)
        and all(origin.get(key) == val for key, val in expected.items()),
        "Proxy origin differs from pinned project",
    )
    proxy = checked_files(value["files"])
    for path, raw in proxy.items():
        require(
            preferred.get(path) == raw, "Proxy member differs: " + module + ":" + path
        )
    return preferred, {
        "relationship": "go-proxy-members-equal-pinned-project-originals",
        "module": module,
        "version": version,
        "origin": expected,
        "upstream": identifier,
        "commit": record["commit"],
        "archive_sha256": record["archive"]["sha256"],
        "zip_sha256": proxy_record["zip_sha256"],
        "sum": proxy_record["sum"],
        "verified_proxy_files": facts(proxy, proxy),
    }


def sqlite_relationship(upstreams, preferred):
    record, original = entry(
        upstreams, "backend-sqlite-c", "modernc.org/sqlite", "v1.37.0"
    )
    require(
        original.get("VERSION", b"").strip() == b"3.49.1", "SQLite C version differs"
    )
    require(
        original.get("manifest.uuid", b"").strip() == SQLITE_ID.encode(),
        "SQLite Fossil source identity differs",
    )
    outputs = ["lib/sqlite_linux_" + arch + ".go" for arch in ("amd64", "arm64")]
    for path in outputs:
        require(path in preferred, "Missing SQLite platform output " + path)
        text = preferred[path].decode("utf-8")
        for name, expected in (
            ("SQLITE_VERSION", '"3.49.1"'),
            ("SQLITE_VERSION_NUMBER", "3049001"),
            ("SQLITE_SOURCE_ID", '"2025-02-18 13:38:58 ' + SQLITE_ID + '"'),
        ):
            values = re.findall(r"(?m)^const " + name + r" = (.+)$", text)
            require(values == [expected], "SQLite generated identity differs: " + name)
    paths = (
        "LICENSE",
        "Makefile",
        "go.mod",
        "go.sum",
        "vendor_libsqlite3/main.go",
        "vendor_libsqlite3/go.mod",
        "vendor_libsqlite3/go.sum",
    )
    return {
        "relationship": "sqlite-generated-identities-match-preferred-c-version",
        "upstream": "backend-sqlite-c",
        "commit": record["commit"],
        "archive_sha256": record["archive"]["sha256"],
        "project_upstream": "backend-sqlite-project",
        "original_inputs": facts(original, record["inspect_paths"]),
        "project_recipe_inputs": facts(preferred, paths),
        "generated_outputs": facts(preferred, outputs),
        "sibling_libsqlite3_translation_mapping_verified": False,
        "pending": [
            "Bind sibling libsqlite3 translator and its generation inputs to both outputs"
        ],
    }


def libc_relationship(upstreams, module_inputs, preferred):
    record, musl = entry(upstreams, "backend-libc-musl", "modernc.org/libc", "v1.65.0")
    pin = record["commit"]
    archive = "musl-" + pin + ".tar.gz"
    required = [
        "LICENSE",
        "COPYRIGHT-MUSL",
        "Makefile",
        "builder.json",
        "go.mod",
        "go.sum",
        "generator.go",
        "internal/archive/archive.go",
    ]
    facts(preferred, required)
    archive_text = preferred["internal/archive/archive.go"].decode()
    require(
        re.findall(r'Version\s*=\s*"([^"]+)"', archive_text) == ["musl-" + pin]
        and re.search(r'File\s*=\s*Version\s*\+\s*"\.tar\.gz"', archive_text),
        "Libc generator archive pin differs",
    )
    require(
        re.findall(r"(?m)^TAR\s*=\s*(\S+)", preferred["Makefile"].decode())
        == [archive],
        "Libc Makefile archive pin differs",
    )
    builder = json.loads(preferred["builder.json"])
    urls = [
        url for item in builder.get("download", []) for url in item.get("files", [])
    ]
    require(
        urls == ["https://git.musl-libc.org/cgit/musl/snapshot/" + archive],
        "Libc builder archive pin differs",
    )
    generator = preferred["generator.go"].decode()
    for snippet in (
        '"modernc.org/cc/v4"',
        '"modernc.org/ccgo/v4/lib"',
        '"modernc.org/fileutil/ccgo"',
        "os.Open(archive.File)",
        "archive.Version",
        '"internal", "overlay", "musl"',
        '"internal", "overlay", goos, goarch, "musl"',
        '"lib/libc.so"',
    ):
        require(snippet in generator, "Active libc recipe no longer binds " + snippet)
    tool_facts = []
    locks = preferred["go.mod"].decode()
    sums = preferred["go.sum"].decode().splitlines()
    for module, version in TOOLS.items():
        require(
            re.findall(r"(?m)^\s*" + re.escape(module) + r"\s+(v\S+)", locks)
            == [version],
            "Libc generator tool version differs: " + module,
        )
        value = module_inputs.get(module)
        require(isinstance(value, dict), "Missing generator tool original " + module)
        tool = value["record"]
        require(
            tool.get("module") == module
            and tool.get("version") == version
            and sums.count(module + " " + version + " " + tool.get("sum", "")) == 1,
            "Libc generator tool authenticated sum differs: " + module,
        )
        files = checked_files(value["files"])
        facts(files, TOOL_ENTRYPOINTS[module])
        paths = [
            path
            for path in files
            if (path.endswith(".go") and not path.endswith("_test.go"))
            or path in ("go.mod", "go.sum", "LICENSE", "Makefile")
        ]
        require(
            "go.mod" in paths and any(path.endswith(".go") for path in paths),
            "Generator tool source absent",
        )
        tool_facts.append(
            {
                "module": module,
                "version": version,
                "sum": tool["sum"],
                "zip_sha256": tool["zip_sha256"],
                "source_files": facts(files, paths),
            }
        )
    outputs = []
    for arch in ("amd64", "arm64"):
        outputs.extend(["ccgo_linux_" + arch + ".go", "capi_linux_" + arch + ".go"])
        facts(preferred, ["include/linux/" + arch + "/stdlib.h"])
        raw = preferred.get("ccgo_linux_" + arch + ".go", b"")
        require(
            raw.startswith(("// Code generated for linux/" + arch).encode()),
            "Libc output platform coverage differs",
        )
    paths = set(required)
    paths.update(
        path
        for path in preferred
        if (
            (
                "/" not in path
                and path.endswith(".go")
                and not path.endswith("_test.go")
                and path != "generate.go"
            )
            or path.startswith(
                (
                    "internal/overlay/musl/",
                    "internal/overlay/linux/amd64/",
                    "internal/overlay/linux/arm64/",
                    "include/linux/amd64/",
                    "include/linux/arm64/",
                )
            )
        )
    )
    require(
        any(path.startswith("internal/overlay/musl/") for path in paths),
        "Missing libc common musl overlays",
    )
    return {
        "relationship": "libc-active-generator-to-pinned-musl-and-authenticated-tools",
        "upstream": "backend-libc-musl",
        "commit": pin,
        "archive_sha256": record["archive"]["sha256"],
        "project_upstream": "backend-libc-project",
        "original_inputs": facts(musl, record["inspect_paths"]),
        "project_recipe_inputs": facts(preferred, paths),
        "generated_outputs": facts(preferred, outputs),
        "generator_tools": tool_facts,
    }


def verify_relationships(upstreams, module_inputs):
    sqlite, sqlite_project = project(
        upstreams,
        module_inputs,
        "backend-sqlite-project",
        "modernc.org/sqlite",
        "v1.37.0",
    )
    libc, libc_project = project(
        upstreams, module_inputs, "backend-libc-project", "modernc.org/libc", "v1.65.0"
    )
    return {
        "kind": "backend-preferred-source-relationships",
        "schema_version": 1,
        "associations": [
            sqlite_project,
            sqlite_relationship(upstreams, sqlite),
            libc_project,
            libc_relationship(upstreams, module_inputs, libc),
        ],
        "generated_output_reproduction_verified": False,
        "corresponding_source_completeness_verified": False,
        "publication_authorized": False,
    }
