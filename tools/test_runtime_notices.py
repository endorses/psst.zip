"""Exact runtime source identity, download and archive boundary regressions."""

from __future__ import annotations

import hashlib
import io
from pathlib import Path
import subprocess
import tarfile
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import zipfile
import urllib.error

import collect_runtime_notices as runtime
import collect_caddy_sources as caddy
from release_artifacts import InvalidRelease


def tar(path: Path, members: list[tuple[str, bytes]]) -> None:
    with tarfile.open(path, "w:gz") as archive:
        for name, data in members:
            entry = tarfile.TarInfo(name)
            entry.size = len(data)
            archive.addfile(entry, io.BytesIO(data))


def apk(**overrides: str) -> bytes:
    fields = {
        "P": "musl-utils",
        "V": "1.2.5-r11",
        "A": "x86_64",
        "L": "MIT AND BSD-2-Clause AND GPL-2.0-or-later",
        "o": "musl",
        "c": "a" * 40,
        "C": "Q1packagechecksum",
    }
    fields.update(overrides)
    return (
        "\n".join(f"{key}:{value}" for key, value in fields.items()) + "\n"
    ).encode()


class RuntimeBoundaries(unittest.TestCase):
    def test_runtime_command_failure_names_operation_without_private_details(self):
        result = SimpleNamespace(
            returncode=125, stdout=b"private stdout", stderr=b"private stderr"
        )
        with patch.object(runtime.subprocess, "run", return_value=result) as execute:
            with self.assertRaises(InvalidRelease) as rejected:
                runtime.command(
                    "docker",
                    "run",
                    "private command argument",
                    timeout=19,
                    operation="helper-apk-inventory",
                )
            self.assertEqual(
                str(rejected.exception),
                "Runtime collection command failed: helper-apk-inventory (exit 125)",
            )
            execute.assert_called_once_with(
                ("docker", "run", "private command argument"),
                capture_output=True,
                timeout=19,
            )
        result.returncode = 0
        with patch.object(runtime.subprocess, "run", return_value=result):
            self.assertEqual(runtime.command("docker", "version"), result.stdout)
        identity = ("alpine-baselayout", "3.7.2-r1", "a" * 40)
        result.returncode = 1
        for category, stderr in (
            ("checksum", b"checksum failed for private source"),
            ("permission", b"Permission denied: private path"),
            ("dns", b"Could not resolve host: private hostname"),
            ("network", b"Failed to connect to private address"),
            ("tls", b"SSL certificate problem: private details"),
            ("http", b"server returned error: HTTP/1.1 403 Forbidden"),
            ("unclassified", b"private stderr https://private.example/input"),
        ):
            result.stderr = stderr
            with self.subTest(category=category), patch.object(
                runtime.subprocess, "run", return_value=result
            ):
                with self.assertRaises(InvalidRelease) as rejected:
                    runtime.command(
                        "docker",
                        "private command argument",
                        operation="apk-source-package-fetch",
                        source_identity=identity,
                    )
                self.assertEqual(
                    str(rejected.exception),
                    "Runtime collection command failed: apk-source-package-fetch (exit 1)"
                    f" [origin={identity[0]} version={identity[1]} aports_commit={identity[2]}]"
                    f" [category={category}]",
                )
        for field in range(3):
            unsafe = list(identity)
            unsafe[field] = "private\n::warning::unsafe identity"
            with self.subTest(field=field), patch.object(
                runtime.subprocess, "run"
            ) as execute:
                with self.assertRaises(InvalidRelease) as rejected:
                    runtime.command(
                        "docker",
                        operation="apk-source-package-fetch",
                        source_identity=tuple(unsafe),
                    )
                self.assertNotIn("private", str(rejected.exception))
                execute.assert_not_called()
        for error, expected in (
            (
                subprocess.TimeoutExpired(
                    ["docker", "private command argument"],
                    19,
                    output=b"private stdout",
                    stderr=b"private stderr",
                ),
                "Runtime collection command timed out: helper-apk-inventory",
            ),
            (
                OSError("private launch details"),
                "Runtime collection command could not start: helper-apk-inventory",
            ),
        ):
            with self.subTest(error=type(error).__name__), patch.object(
                runtime.subprocess, "run", side_effect=error
            ):
                with self.assertRaises(InvalidRelease) as rejected:
                    runtime.command(
                        "docker",
                        "private command argument",
                        timeout=19,
                        operation="helper-apk-inventory",
                    )
                self.assertEqual(str(rejected.exception), expected)
                self.assertTrue(rejected.exception.__suppress_context__)

    def test_caddy_wrapper_is_selected_at_archive_root(self):
        with tempfile.TemporaryDirectory(prefix="psst-runtime-test-") as folder:
            archive = Path(folder) / "buildable.tar.gz"
            tar(
                archive,
                [("main.go", b"actual wrapper"), ("vendor/module/main.go", b"library")],
            )
            self.assertEqual(
                caddy.archive_member(archive, "main.go", exact=True), b"actual wrapper"
            )
            with self.assertRaises(InvalidRelease):
                caddy.archive_member(archive, "main.go")

    def test_caddy_source_modules_match_actual_binary_versions_and_sums(self):
        dependency = {
            "Path": "example.org/module",
            "Version": "v1.2.3",
            "Sum": "h1:actual",
        }
        info = {"Deps": [dependency]}
        sums = b"example.org/module v1.2.3 h1:actual\n"
        modules = b"# example.org/module v1.2.3\n## explicit\n"
        caddy.verify_modules(info, sums, modules)
        for value in (
            {"Deps": [{**dependency, "Version": "v1.2.4"}]},
            {"Deps": [{**dependency, "Sum": "h1:different"}]},
            {"Deps": [{**dependency, "Replace": {"Path": "local"}}]},
        ):
            with self.subTest(value=value), self.assertRaises(InvalidRelease):
                caddy.verify_modules(value, sums, modules)

    def test_caddy_source_recipe_must_be_bound_by_immutable_native_descriptor(self):
        base = "caddy:2-alpine@sha256:" + "a" * 64
        commit = "b" * 40
        descriptor = {
            "platform": {"os": "linux", "architecture": "amd64"},
            "annotations": {
                "org.opencontainers.image.source": "https://github.com/caddyserver/caddy-docker.git#"
                + commit
                + ":2.11/alpine",
                "org.opencontainers.image.revision": commit,
            },
        }
        index = {"digest": "sha256:" + "a" * 64, "manifests": [descriptor]}
        self.assertEqual(
            caddy.recipe_identity(index, base, "amd64"), (commit, "2.11/alpine")
        )
        for value in (
            {**index, "digest": "sha256:" + "c" * 64},
            {**index, "manifests": [descriptor, descriptor]},
        ):
            with self.assertRaises(InvalidRelease):
                caddy.recipe_identity(value, base, "amd64")
        with self.assertRaises(InvalidRelease):
            caddy.recipe_identity(index, "caddy:2-alpine", "amd64")

    def test_public_source_redirect_cannot_reach_non_github_host(self):
        redirect = urllib.error.HTTPError(
            "https://github.com/source",
            302,
            "Redirect",
            {"Location": "https://other.example/source"},
            None,
        )
        with patch.object(caddy.urllib.request, "build_opener") as opener:
            opener.return_value.open.side_effect = redirect
            with self.assertRaises(InvalidRelease):
                caddy.public_download("https://github.com/source")
            self.assertEqual(opener.return_value.open.call_count, 1)
            request = opener.return_value.open.call_args.args[0]
            self.assertNotIn("Authorization", request.headers)

    def test_lower_image_layers_keep_upgraded_runtime_sources_in_scope(self):
        with tempfile.TemporaryDirectory(prefix="psst-runtime-test-") as folder:
            root = Path(folder)
            old, new = root / "old.tar.gz", root / "new.tar.gz"
            tar(old, [("lib/apk/db/installed", apk(V="1.2.5-r10", c="b" * 40))])
            tar(new, [("lib/apk/db/installed", apk())])
            config = b'{"rootfs":{"diff_ids":["old","new"]}}'
            image = "sha256:" + hashlib.sha256(config).hexdigest()
            saved = root / "image.tar.gz"
            tar(
                saved,
                [
                    (
                        "manifest.json",
                        b'[{"Config":"config.json","Layers":["old.tar.gz","new.tar.gz"]}]',
                    ),
                    ("config.json", config),
                    ("old.tar.gz", old.read_bytes()),
                    ("new.tar.gz", new.read_bytes()),
                ],
            )
            packages, records = runtime.layer_packages(saved, image)
            self.assertEqual(
                {item["version"] for item in packages}, {"1.2.5-r10", "1.2.5-r11"}
            )
            self.assertEqual(len(records), 2)
            with self.assertRaises(InvalidRelease):
                runtime.layer_packages(saved, "sha256:" + "0" * 64)

    def test_local_recipe_helper_copyright_is_not_lost(self):
        with tempfile.TemporaryDirectory(prefix="psst-runtime-test-") as folder:
            root = Path(folder)
            data = b"/* Copyright exact utility authors. Full BSD header. */\nint main() {}\n"
            (root / "getent.c").write_bytes(data)
            (root / "other.c").write_bytes(b"int main() {}\n")
            self.assertEqual(runtime.recipe_notices(root), {"recipe/getent.c": data})

    def test_apk_inventory_preserves_origin_commit_and_license_expression(self):
        value = runtime.packages(apk() + b"F:usr/bin\nR:getconf\nc:filechecksum\n")
        self.assertEqual(value[0]["origin"], "musl")
        self.assertEqual(value[0]["aports_commit"], "a" * 40)
        self.assertIn("GPL-2.0-or-later", value[0]["license"])

    def test_apk_identity_cannot_fall_back_to_current_source_recipe(self):
        for change in ({"c": ""}, {"c": "main"}, {"o": "../musl"}, {"A": "s390x"}):
            with self.subTest(change=change), self.assertRaises(InvalidRelease):
                runtime.packages(apk(**change))

    def test_duplicate_apk_identity_and_metadata_are_rejected(self):
        for data in (apk() + b"P:another\n", apk() + b"\n" + apk()):
            with self.subTest(data=data), self.assertRaises(InvalidRelease):
                runtime.packages(data)

    def test_download_redirect_is_rejected_before_credentials_are_forwarded(self):
        handler = runtime.NoRedirect()
        with self.assertRaises(InvalidRelease):
            handler.redirect_request(None, None, 302, "redirect", {}, "https://other/")

    def test_token_is_not_attached_to_unapproved_api_url(self):
        with patch.dict(runtime.os.environ, {"GH_TOKEN": "fixture-token"}):
            with patch.object(runtime.urllib.request, "build_opener") as opener:
                with self.assertRaises(InvalidRelease):
                    runtime.read_url("https://other/", 100, github_api=True)
                opener.assert_not_called()

    def test_nested_source_archives_retain_full_notice_bytes(self):
        with tempfile.TemporaryDirectory(prefix="psst-runtime-test-") as folder:
            nested = Path(folder) / "upstream.tar.gz"
            content = b"Copyright contributors\nComplete notice text.\n"
            tar(nested, [("source/LICENSE", content)])
            outer = Path(folder) / "srcpkg.tar.gz"
            tar(outer, [("recipe/upstream.tar.gz", nested.read_bytes())])
            self.assertEqual(
                runtime.source_notices(outer),
                {"recipe/upstream.tar.gz::source/LICENSE": content},
            )

    def test_zip_notices_and_unsafe_paths(self):
        with tempfile.TemporaryDirectory(prefix="psst-runtime-test-") as folder:
            archive = Path(folder) / "module.zip"
            with zipfile.ZipFile(archive, "w") as source:
                source.writestr("module/LICENSE.txt", "Full module notice")
            self.assertEqual(len(runtime.source_notices(archive)), 1)
            for name in ("/LICENSE", "../LICENSE", "source\\LICENSE"):
                with zipfile.ZipFile(archive, "w") as source:
                    source.writestr(name, "Notice")
                with self.subTest(name=name), self.assertRaises(InvalidRelease):
                    runtime.source_notices(archive)

    def test_archive_duplicate_paths_and_shared_expansion_budget(self):
        with tempfile.TemporaryDirectory(prefix="psst-runtime-test-") as folder:
            archive = Path(folder) / "source.tar.gz"
            tar(archive, [("LICENSE", b"first"), ("LICENSE", b"second")])
            with self.assertRaises(InvalidRelease):
                runtime.source_notices(archive)
            tar(archive, [("LICENSE", b"123456")])
            with self.assertRaises(InvalidRelease):
                runtime.source_notices(archive, budget=[5, 10, 10])
            with self.assertRaises(InvalidRelease):
                runtime.source_notices(archive, budget=[10, 10, 5])

    def test_nested_depth_is_bounded(self):
        with self.assertRaises(InvalidRelease):
            runtime.source_notices(Path("unused"), depth=5)

    def test_original_source_checksums_are_verified_without_regeneration(self):
        with tempfile.TemporaryDirectory(prefix="psst-runtime-test-") as folder:
            archive = Path(folder) / "source.tar.gz"
            data = b"Exact original upstream archive bytes"
            tar(archive, [("recipe/upstream.tar.xz", data)])
            sums = hashlib.sha512(data).hexdigest() + "  upstream.tar.xz\n"
            runtime.verify_source_package(archive, sums)
            tar(archive, [("recipe/upstream.tar.xz", data + b"tampered")])
            with self.assertRaises(InvalidRelease):
                runtime.verify_source_package(archive, sums)

    def test_missing_and_ambiguous_source_checksum_inputs_fail_closed(self):
        with tempfile.TemporaryDirectory(prefix="psst-runtime-test-") as folder:
            archive = Path(folder) / "source.tar.gz"
            sums = hashlib.sha512(b"bytes").hexdigest() + "  upstream.tar.gz\n"
            for members in (
                [("recipe/other.tar.gz", b"bytes")],
                [("a/upstream.tar.gz", b"bytes"), ("b/upstream.tar.gz", b"bytes")],
                [("../upstream.tar.gz", b"bytes")],
            ):
                tar(archive, members)
                with self.subTest(members=members), self.assertRaises(InvalidRelease):
                    runtime.verify_source_package(archive, sums)


if __name__ == "__main__":
    unittest.main()
