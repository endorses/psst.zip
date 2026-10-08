"""Small offline regressions for release source identity and generator gaps."""

import copy
import unittest
from unittest.mock import patch

import backend_preferred_source_relationships as backend
from release_artifacts import InvalidRelease


def fixture():
    upstreams, modules = {}, {}
    for name, version in (("sqlite", "v1.37.0"), ("libc", "v1.65.0")):
        module = "modernc.org/" + name
        record = {
            "id": "backend-" + name + "-project",
            "repository": "cznic/" + name,
            "commit": "a" * 40,
            "go_modules": {module: version},
            "archive": {"sha256": "sha256:" + "b" * 64},
        }
        files = {
            "LICENSE": b"license",
            "Makefile": b"vendor:\n",
            "go.mod": b"module " + module.encode(),
            "go.sum": b"sums",
        }
        upstreams[record["id"]] = {"record": record, "files": files}
        modules[module] = {
            "record": {
                "module": module,
                "version": version,
                "sum": "h1:test",
                "zip_sha256": "sha256:" + "c" * 64,
            },
            "files": {},
            "origin": {
                "VCS": "git",
                "URL": "https://gitlab.com/cznic/" + name,
                "Hash": "a" * 40,
                "Ref": "refs/tags/" + version,
            },
        }
    sqlite = upstreams["backend-sqlite-project"]["files"]
    for path in (
        "vendor_libsqlite3/main.go",
        "vendor_libsqlite3/go.mod",
        "vendor_libsqlite3/go.sum",
    ):
        sqlite[path] = b"original recipe"
    generated = (
        'const SQLITE_VERSION = "3.49.1"\nconst SQLITE_VERSION_NUMBER = 3049001\nconst SQLITE_SOURCE_ID = "2025-02-18 13:38:58 '
        + backend.SQLITE_ID
        + '"\n'
    ).encode()
    for arch in ("amd64", "arm64"):
        sqlite["lib/sqlite_linux_" + arch + ".go"] = (
            "//go:build linux && " + arch + "\n"
        ).encode() + generated
    sibling_files = {
        "generator.go": b'versionTag = "3490100"; archivePath = "sqlite-amalgamation-" + versionTag + ".zip"; archive2Path = "sqlite-src-" + versionTag + ".zip"; "sqlite_issue173.patch"; "issue1.patch"',
        "go.mod": b"module modernc.org/libsqlite3\nrequire (\n modernc.org/cc/v4 v4.25.2\n modernc.org/ccgo/v4 v4.25.2\n modernc.org/fileutil v1.3.0\n)\n",
        "go.sum": b"".join(
            (m + " " + v + " h1:" + "A" * 43 + "=\n").encode()
            for m, v in (
                ("modernc.org/cc/v4", "v4.25.2"),
                ("modernc.org/ccgo/v4", "v4.25.2"),
                ("modernc.org/fileutil", "v1.3.0"),
            )
        ),
        "internal/sqlite_issue173.patch": b"original patch",
        "internal/issue1.patch": b"original patch",
        "ccgo_linux_amd64.go": b"//go:build linux && amd64\noriginal AMD64 syntax",
        "ccgo_linux_arm64.go": b"//go:build linux && arm64\noriginal ARM64 syntax",
    }
    upstreams["backend-libsqlite3-project"] = {
        "record": {
            "id": "backend-libsqlite3-project",
            "repository": "cznic/libsqlite3",
            "commit": "8" * 40,
            "archive": {"sha256": "sha256:" + "7" * 64},
            "go_modules": {"modernc.org/sqlite": "v1.37.0"},
            "relationship": {
                "kind": "generator-project",
                "name": "modernc.org/libsqlite3",
                "version": "v1.9.0",
                "module": "modernc.org/sqlite",
            },
            "inspect_paths": sorted(sibling_files),
        },
        "files": sibling_files,
    }
    musl_pin = "d" * 40
    archive = "musl-" + musl_pin + ".tar.gz"
    libc = upstreams["backend-libc-project"]["files"]
    libc.update(
        {
            "COPYRIGHT-MUSL": b"original copyright",
            "Makefile": ("TAR = " + archive + "\n").encode(),
            "builder.json": (
                '{"download":[{"files":["https://git.musl-libc.org/cgit/musl/snapshot/'
                + archive
                + '"]}]}'
            ).encode(),
            "internal/archive/archive.go": (
                'const (\nVersion = "musl-'
                + musl_pin
                + '"\nFile = Version + ".tar.gz"\n)'
            ).encode(),
            "generator.go": b'"modernc.org/cc/v4" "modernc.org/ccgo/v4/lib" "modernc.org/fileutil/ccgo" os.Open(archive.File) archive.Version "internal", "overlay", "musl" "internal", "overlay", goos, goarch, "musl" "lib/libc.so"',
            "internal/overlay/musl/src/string/explicit_bzero.c": b"original overlay",
        }
    )
    libc["go.mod"] = (
        b"module modernc.org/libc\nrequire (\n"
        + b"".join((m + " " + v + "\n").encode() for m, v in backend.TOOLS.items())
        + b")\n"
    )
    libc["go.sum"] = b"".join(
        (m + " " + v + " h1:tool\n").encode() for m, v in backend.TOOLS.items()
    )
    for arch in ("amd64", "arm64"):
        libc["ccgo_linux_" + arch + ".go"] = (
            "// Code generated for linux/" + arch + " by gcc\n"
        ).encode()
        libc["capi_linux_" + arch + ".go"] = b"generated capi"
        libc["include/linux/" + arch + "/stdlib.h"] = b"original header"
    for module, version in backend.TOOLS.items():
        modules[module] = {
            "record": {
                "module": module,
                "version": version,
                "sum": "h1:tool",
                "zip_sha256": "sha256:" + "e" * 64,
            },
            "files": {
                "go.mod": b"module " + module.encode(),
                **{path: b"package tool" for path in backend.TOOL_ENTRYPOINTS[module]},
            },
        }
    for name in ("sqlite", "libc"):
        modules["modernc.org/" + name]["files"] = dict(
            upstreams["backend-" + name + "-project"]["files"]
        )
    for identifier, module, version, pin, files in (
        (
            "backend-sqlite-c",
            "modernc.org/sqlite",
            "v1.37.0",
            "f" * 40,
            {
                "VERSION": b"3.49.1\n",
                "manifest.uuid": backend.SQLITE_ID.encode() + b"\n",
                "src/main.c": b"original C",
            },
        ),
        (
            "backend-libc-musl",
            "modernc.org/libc",
            "v1.65.0",
            musl_pin,
            {
                "COPYRIGHT": b"copyright",
                "configure": b"original configure",
                "src/string/memcpy.c": b"original C",
            },
        ),
    ):
        upstreams[identifier] = {
            "record": {
                "id": identifier,
                "commit": pin,
                "go_modules": {module: version},
                "archive": {"sha256": "sha256:" + "9" * 64},
                "inspect_paths": sorted(files),
            },
            "files": files,
        }
    return upstreams, modules


def comparison_fixture(original, vendored):
    # This tests source/review joins; real syntax semantics have two Go regressions.
    return {
        "kind": "sqlite-vendoring-go-ast-correspondence",
        "original_sha256": backend.digest(original),
        "vendored_sha256": backend.digest(vendored),
        "expected_structural_sha256": "sha256:" + "6" * 64,
        "target_structural_sha256": "sha256:" + "6" * 64,
        "added_alias_count": 14,
        "generated_output_reproduction_verified": False,
        "verifier_source_sha256": "sha256:" + "5" * 64,
    }


class BackendPreferredSources(unittest.TestCase):
    def setUp(self):
        # Tiny recipe bytes have their own reviewed hash; avoid copying upstream code.
        fixture_pin = patch.object(
            backend, "SQLITE_VENDOR_RECIPE_SHA256", backend.digest(b"original recipe")
        )
        fixture_pin.start()
        self.addCleanup(fixture_pin.stop)

    def verify(self, upstreams, modules, comparator=comparison_fixture):
        return backend.verify_relationships(
            upstreams, modules, vendoring_verifier=comparator
        )

    def reject(self, mutate):
        upstreams, modules = fixture()
        mutate(upstreams, modules)
        with self.assertRaises(InvalidRelease):
            self.verify(upstreams, modules)

    def test_exact_project_origin_version_and_every_proxy_member(self):
        for path in (".", "a\nb.go", "a\tb.go", "a\x7fb.go", "a\x00b.go"):
            with self.subTest(invalid_path=path), self.assertRaises(InvalidRelease):
                backend.checked_files({path: b"source"})
        upstreams, modules = fixture()
        result = self.verify(upstreams, modules)
        self.assertEqual(
            result,
            self.verify(copy.deepcopy(upstreams), copy.deepcopy(modules)),
        )
        self.assertFalse(result["corresponding_source_completeness_verified"])
        self.assertFalse(result["publication_authorized"])
        self.assertTrue(
            result["associations"][1]["sibling_libsqlite3_translation_mapping_verified"]
        )
        self.assertFalse(
            result["associations"][1]["generated_output_reproduction_verified"]
        )
        for key in ("VCS", "URL", "Hash", "Ref"):
            with self.subTest(origin=key):
                self.reject(
                    lambda u, m: m["modernc.org/sqlite"]["origin"].update(
                        {key: "substituted"}
                    )
                )
        self.reject(
            lambda u, m: m["modernc.org/sqlite"]["record"].update(version="v1.38.0")
        )
        self.reject(
            lambda u, m: m["modernc.org/libc"]["files"].update(
                {"LICENSE": b"different"}
            )
        )
        self.reject(
            lambda u, m: m["modernc.org/sqlite"]["files"].update(
                {"extra.go": b"unoffered"}
            )
        )

    def test_sqlite_original_identity_recipes_and_both_outputs(self):
        self.reject(lambda u, m: u["backend-sqlite-c"]["files"].pop("src/main.c"))
        self.reject(
            lambda u, m: u["backend-sqlite-c"]["files"].update(VERSION=b"3.49.2")
        )
        self.reject(
            lambda u, m: u["backend-sqlite-c"]["files"].update(
                {"manifest.uuid": b"wrong"}
            )
        )
        for path in ("vendor_libsqlite3/main.go", "lib/sqlite_linux_arm64.go"):
            with self.subTest(missing=path):

                def remove(u, m):
                    u["backend-sqlite-project"]["files"].pop(path)
                    m["modernc.org/sqlite"]["files"].pop(path)

                self.reject(remove)

        def changed_output(u, m):
            path = "lib/sqlite_linux_amd64.go"
            raw = u["backend-sqlite-project"]["files"][path].replace(
                b"3049001", b"3049002"
            )
            u["backend-sqlite-project"]["files"][path] = raw
            m["modernc.org/sqlite"]["files"][path] = raw

        self.reject(changed_output)
        self.reject(
            lambda u, m: u["backend-libsqlite3-project"]["files"].pop(
                "ccgo_linux_arm64.go"
            )
        )
        self.reject(
            lambda u, m: u["backend-libsqlite3-project"]["files"].update(
                {
                    "go.mod": b"module modernc.org/libsqlite3\nrequire modernc.org/ccgo/v4 v4.26.0\n"
                }
            )
        )

        def changed_recipe(u, m):
            u["backend-sqlite-project"]["files"][
                "vendor_libsqlite3/main.go"
            ] = b"changed recipe"
            m["modernc.org/sqlite"]["files"][
                "vendor_libsqlite3/main.go"
            ] = b"changed recipe"

        self.reject(changed_recipe)
        self.reject(
            lambda u, m: u["backend-libsqlite3-project"]["files"].update(
                {
                    "ccgo_linux_arm64.go": u["backend-libsqlite3-project"]["files"][
                        "ccgo_linux_amd64.go"
                    ]
                }
            )
        )
        upstreams, modules = fixture()
        for field in (
            "original_sha256",
            "vendored_sha256",
            "target_structural_sha256",
            "verifier_source_sha256",
        ):

            def substituted(original, target):
                return {**comparison_fixture(original, target), field: "substituted"}

            with self.subTest(comparison_field=field), self.assertRaises(
                InvalidRelease
            ):
                self.verify(upstreams, modules, substituted)
        with self.assertRaisesRegex(InvalidRelease, "requires its syntax verifier"):
            backend.verify_relationships(upstreams, modules)

    def test_active_libc_pin_tool_locks_originals_and_output_coverage(self):
        for path in (
            "generator.go",
            "Makefile",
            "builder.json",
            "internal/archive/archive.go",
            "go.mod",
            "go.sum",
            "ccgo_linux_arm64.go",
            "capi_linux_amd64.go",
            "include/linux/arm64/stdlib.h",
            "internal/overlay/musl/src/string/explicit_bzero.c",
        ):
            with self.subTest(missing=path):

                def remove(u, m):
                    u["backend-libc-project"]["files"].pop(path)
                    m["modernc.org/libc"]["files"].pop(path)

                self.reject(remove)
        for path in ("Makefile", "builder.json", "internal/archive/archive.go"):
            with self.subTest(pin=path):

                def substitute(u, m):
                    raw = u["backend-libc-project"]["files"][path].replace(
                        b"d" * 40, b"0" * 40
                    )
                    u["backend-libc-project"]["files"][path] = raw
                    m["modernc.org/libc"]["files"][path] = raw

                self.reject(substitute)
        self.reject(
            lambda u, m: m["modernc.org/ccgo/v4"]["record"].update(sum="h1:wrong")
        )
        self.reject(
            lambda u, m: m["modernc.org/cc/v4"]["record"].update(version="v4.27.0")
        )
        for module, paths in backend.TOOL_ENTRYPOINTS.items():
            for path in paths:
                with self.subTest(tool=module, missing=path):
                    self.reject(lambda u, m: m[module]["files"].pop(path))


if __name__ == "__main__":
    unittest.main()
