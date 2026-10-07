"""Dependency retention boundaries with real Git and archive bytes, no approvals."""

import base64
import copy
import hashlib
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
import zipfile

import package_application_dependencies as dependency
from release_artifacts import InvalidRelease, json_bytes, read_json


def npm_tar(name="fixture-package", version="1.0.0", extra=(), prefix="package"):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as archive:
        body = json_bytes({"name": name, "version": version})
        entry = tarfile.TarInfo(prefix + "/package.json")
        entry.size = len(body)
        archive.addfile(entry, io.BytesIO(body))
        for entry, body in extra:
            archive.addfile(entry, io.BytesIO(body) if body is not None else None)
    return output.getvalue()


class DependencyInputs(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(
            prefix="psst-dependency-input-test-"
        )
        self.addCleanup(self.temporary.cleanup)
        self.folder = Path(self.temporary.name)
        self.module, self.version = "example.org/dependency", "v1.0.0"
        self.mod = b"module example.org/dependency\n\ngo 1.23\n"
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, "w") as archive:
            archive.writestr(self.module + "@" + self.version + "/go.mod", self.mod)
            archive.writestr(
                self.module + "@" + self.version + "/source.go", b"package dependency\n"
            )
        self.zip = buffer.getvalue()
        self.zip_sum = dependency.module_zip_sum(self.zip, self.module, self.version)
        self.mod_sum = dependency.h1([("go.mod", hashlib.sha256(self.mod).hexdigest())])
        self.sum = (
            f"{self.module} {self.version} {self.zip_sum}\n"
            f"{self.module} {self.version}/go.mod {self.mod_sum}\n"
        ).encode()
        self.records = [
            {
                "Path": self.module,
                "Version": self.version,
                "Sum": self.zip_sum,
                "GoModSum": self.mod_sum,
                **{
                    key: "/reports/cache/" + suffix
                    for key, suffix in (
                        ("Zip", "input.zip"),
                        ("GoMod", "input.mod"),
                        ("Info", "input.info"),
                    )
                },
            }
        ]
        self.selected = [
            {"Path": "example.org/application", "Main": True},
            {"Path": self.module, "Version": self.version},
        ]
        self.npm = npm_tar()
        self.lock = {
            "lockfileVersion": 3,
            "packages": {
                "": {"name": "fixture"},
                "node_modules/fixture-package": {
                    "version": "1.0.0",
                    "dev": True,
                    "optional": True,
                    "resolved": "https://registry.npmjs.org/fixture-package/-/fixture-package-1.0.0.tgz",
                    "integrity": "sha512-"
                    + base64.b64encode(hashlib.sha512(self.npm).digest()).decode(),
                },
            },
        }
        self.cache = self.folder / "cache"
        self.fill_cache(self.cache)

    def fill_cache(self, cache):
        cache.mkdir()
        for name, content in {
            "input.zip": self.zip,
            "input.mod": self.mod,
            "input.info": json_bytes({"Version": self.version}),
        }.items():
            (cache / name).write_bytes(content)

    def modules(self, **overrides):
        args = dict(
            records=self.records,
            selected=self.selected,
            cache=self.cache,
            sums=self.sum,
        )
        return dependency.module_inputs(**(args | overrides))

    def test_module_inputs_preserve_original_bytes_and_missing_coverage_fails(self):
        files, records = self.modules()
        self.assertEqual(len(files), 3)
        self.assertIn(self.zip, files.values())
        self.assertEqual(records[0]["sum"], self.zip_sum)
        for changes in (
            {"records": []},
            {"records": self.records * 2},
            {
                "selected": self.selected
                + [{"Path": "other.org/module", "Version": "v1.0.0"}]
            },
        ):
            with self.subTest(changes=changes), self.assertRaises(InvalidRelease):
                self.modules(**changes)

    def test_tampered_module_zip_mod_info_and_cache_links_fail(self):
        for name, body in (
            ("input.zip", b"not a zip"),
            ("input.mod", b"module altered\n"),
            ("input.info", json_bytes({"Version": "v2.0.0"})),
        ):
            path = self.cache / name
            original = path.read_bytes()
            path.write_bytes(body)
            with self.subTest(name=name), self.assertRaises(
                (InvalidRelease, zipfile.BadZipFile)
            ):
                self.modules()
            path.write_bytes(original)
        path = self.cache / "input.zip"
        path.unlink()
        path.symlink_to(self.cache / "input.mod")
        with self.assertRaises(InvalidRelease):
            self.modules()

    def test_collection_can_add_authenticated_graph_sums_but_not_change_original(self):
        extra = b"other.org/module v1.0.0 h1:authenticatedfixture\n"
        self.assertEqual(len(dependency.additional_sums(self.sum, self.sum + extra)), 1)
        for changed in (
            extra,
            self.sum.replace(self.zip_sum.encode(), b"h1:changed"),
            self.sum * 2,
        ):
            with self.subTest(changed=changed), self.assertRaises(InvalidRelease):
                dependency.additional_sums(self.sum, changed)

    def test_archive_traversal_duplicate_and_foreign_module_prefix_fail(self):
        for names in (
            ("../escape",),
            (self.module + "@" + self.version + "/x",) * 2,
            ("other.org/module@v1.0.0/x",),
        ):
            raw = io.BytesIO()
            with zipfile.ZipFile(raw, "w") as archive:
                for name in names:
                    archive.writestr(name, b"x")
            with self.subTest(names=names), self.assertRaises(InvalidRelease):
                dependency.module_zip_sum(raw.getvalue(), self.module, self.version)

    def test_all_optional_development_npm_inputs_are_retained(self):
        files, records = dependency.npm_inputs(
            json_bytes(self.lock), lambda url: self.npm
        )
        self.assertEqual(list(files.values()), [self.npm])
        self.assertTrue(records[0]["development"])
        self.assertTrue(records[0]["optional"])

    def test_alternate_npm_archive_root_is_supported_but_mixed_roots_fail(self):
        for extra, valid in (
            ((), True),
            (((tarfile.TarInfo("other/root"), b""),), False),
        ):
            raw = npm_tar(prefix="fixture-package", extra=extra)
            lock = copy.deepcopy(self.lock)
            lock["packages"]["node_modules/fixture-package"]["integrity"] = (
                "sha512-" + base64.b64encode(hashlib.sha512(raw).digest()).decode()
            )
            if valid:
                self.assertEqual(
                    list(
                        dependency.npm_inputs(json_bytes(lock), lambda _: raw)[
                            0
                        ].values()
                    ),
                    [raw],
                )
            else:
                with self.assertRaises(InvalidRelease):
                    dependency.npm_inputs(json_bytes(lock), lambda _: raw)

    def test_npm_integrity_identity_registry_and_links_fail(self):
        for mutate in (
            lambda v: v.update(resolved="https://private.example/pkg.tgz"),
            lambda v: v.update(integrity="sha1-invalid"),
            lambda v: v.update(version="9.0.0"),
            lambda v: v.update(link=True),
        ):
            value = copy.deepcopy(self.lock)
            mutate(value["packages"]["node_modules/fixture-package"])
            with self.assertRaises(InvalidRelease):
                dependency.npm_inputs(json_bytes(value), lambda url: self.npm)
        with self.assertRaises(InvalidRelease):
            dependency.npm_inputs(
                json_bytes(self.lock), lambda url: self.npm + b"tamper"
            )
        entry = tarfile.TarInfo("package/escape")
        entry.type = tarfile.SYMTYPE
        entry.linkname = "../../escape"
        raw = npm_tar(extra=[(entry, None)])
        value = copy.deepcopy(self.lock)
        value["packages"]["node_modules/fixture-package"]["integrity"] = (
            "sha512-" + base64.b64encode(hashlib.sha512(raw).digest()).decode()
        )
        with self.assertRaises(InvalidRelease):
            dependency.npm_inputs(json_bytes(value), lambda url: raw)

    def repository(self):
        root = self.folder / "repository"
        root.mkdir()

        def git(*args):
            return subprocess.check_output(
                ["git", "-C", str(root), *args],
                env={
                    **os.environ,
                    "GIT_CONFIG_GLOBAL": os.devnull,
                    "GIT_CONFIG_NOSYSTEM": "1",
                },
            )

        git("init", "-q")
        git("config", "user.name", "Source fixture")
        git("config", "user.email", "source@example.invalid")
        git("config", "core.hooksPath", os.devnull)
        git("remote", "add", "origin", "https://github.com/endorses/psst.zip.git")
        (root / "backend").mkdir()
        (root / "web").mkdir()
        (root / "backend/go.mod").write_bytes(
            b"module example.org/application\n\ngo 1.23\n"
        )
        (root / "backend/go.sum").write_bytes(self.sum)
        (root / "web/package-lock.json").write_bytes(json_bytes(self.lock))
        git("add", ".")
        git("commit", "-qm", "Exact source fixture")
        return root, git("rev-parse", "HEAD").decode().strip()

    def execute(self, args, *, environment, timeout):
        self.assertNotIn("GH_TOKEN", environment)
        if args[1:3] == ["image", "inspect"]:
            return json_bytes(
                [{"Id": "sha256:" + "c" * 64, "Os": "linux", "Architecture": "amd64"}]
            )
        self.assertIn("--read-only", args)
        self.assertIn("ALL", args)
        self.assertIn("-ec", args)
        self.assertNotIn("/var/run/docker.sock", " ".join(args))
        reports = Path(
            next(value for value in args if value.endswith("dst=/reports"))
            .split("src=", 1)[1]
            .split(",dst=", 1)[0]
        )
        self.fill_cache(reports / "cache")
        (reports / "go-version.txt").write_text("go version go1.26.8 linux/amd64\n")
        (reports / "authenticated-go.sum").write_bytes(self.sum)
        (reports / "go-environment.json").write_bytes(
            json_bytes(
                {
                    "GOTOOLCHAIN": "local",
                    "GOPROXY": "https://proxy.golang.org",
                    "GOSUMDB": "sum.golang.org",
                    "GOPRIVATE": "",
                    "GONOPROXY": "",
                    "GONOSUMDB": "",
                    "GOFLAGS": "-mod=readonly",
                }
            )
        )
        (reports / "go-downloads.json").write_bytes(
            b"".join(map(json_bytes, self.records))
        )
        (reports / "go-modules.json").write_bytes(
            b"".join(map(json_bytes, self.selected))
        )
        return b""

    def test_actual_git_tree_and_original_archives_package_without_approval(self):
        root, commit = self.repository()
        (root / "private.env").write_text("excluded untracked input")
        args = dict(
            root=root,
            repository="endorses/psst.zip",
            version="v1.2.3",
            commit=commit,
            platform="linux/amd64",
            go_image="docker.io/library/golang@sha256:" + "a" * 64,
            output=self.folder / "output",
            execute=self.execute,
            fetch=lambda url: self.npm,
        )
        result = dependency.collect(**args)
        self.assertEqual(result["go_modules"], 1)
        self.assertEqual(result["npm_packages"], 1)
        self.assertFalse(result["publication_authorized"])
        self.assertTrue(result["preferred_source_review_required"])
        self.assertEqual(result["platform"], "linux/amd64")
        self.assertEqual(
            result["asset"]["name"], "psst.zip-dependency-inputs-v1.2.3-amd64.tar.gz"
        )
        with tarfile.open(args["output"] / result["asset"]["name"], "r:gz") as archive:
            names = archive.getnames()
            record = read_json(archive.extractfile("dependency-inputs.json").read())
        self.assertNotIn("private.env", " ".join(names))
        self.assertEqual(record["source_commit"], commit)
        self.assertTrue(
            any(name.startswith("go/") and name.endswith(".zip") for name in names)
        )
        self.assertTrue(any(name.startswith("npm/") for name in names))
        with self.assertRaisesRegex(InvalidRelease, "already exists"):
            dependency.collect(**args)

    def test_failed_collector_does_not_create_approval_or_output(self):
        root, commit = self.repository()
        output = self.folder / "failed"

        def fail(*args, **kwargs):
            raise InvalidRelease("failed authentic module acquisition")

        with self.assertRaises(InvalidRelease):
            dependency.collect(
                root=root,
                repository="endorses/psst.zip",
                version="v1.2.3",
                commit=commit,
                platform="linux/amd64",
                go_image="docker.io/library/golang@sha256:" + "a" * 64,
                output=output,
                execute=fail,
            )
        self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()
