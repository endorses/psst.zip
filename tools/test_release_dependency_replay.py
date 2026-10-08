"""Replay real archive/Git fixtures; fixture measurements confer no authority."""

import copy
import gzip
import io
from pathlib import Path
import tarfile
import unittest
import zipfile

from generate_release_gate_reports import NativeSourceContext
import package_application_dependencies as package
from publish_container_release import sha256
from release_artifacts import InvalidRelease, git, json_bytes, read_json
import test_release_dependency_inputs as fixtures
import verify_application_dependency_inputs as replay


def source_measurement_fixture(context, repository_root, collection_dir, output):
    """Create explicit untrusted fixture facts for real archive replay tests only."""
    result = read_json((collection_dir / "dependency-collection.json").read_bytes())
    files = replay.archive_files(
        (collection_dir / result["asset"]["name"]).read_bytes()
    )
    record = read_json(files["dependency-inputs.json"])
    collector = record["go_collector"]
    output.mkdir()
    rows = []
    for target, name, body in (
        ("backend-source", "go-modules.json", files["collection/go-modules.json"]),
        (
            "web-source",
            "npm-lock-graph.json",
            json_bytes(
                {
                    "dependencies": {
                        row["name"]: {"version": row["version"]}
                        for row in record["npm_packages"]
                    }
                }
            ),
        ),
    ):
        raw_root = output / target
        raw_root.mkdir()
        (raw_root / name).write_bytes(body)
        rows.append(
            {
                "target": target,
                "status": "complete",
                "exit_code": 0,
                "builder": {
                    "reference": collector["image"],
                    "config_digest": collector["config"],
                    "architecture": context.platform.split("/")[1],
                },
                "raw_files": {name: sha256(body)},
            }
        )
    rows[0]["graphs"] = {"go-modules.json": sha256(files["collection/go-modules.json"])}
    rows[1].update(
        lock_sha256=sha256(files["inputs/web/package-lock.json"]),
        lock_graph_sha256=rows[1]["raw_files"]["npm-lock-graph.json"],
        dependency_packages=len(record["npm_packages"]),
    )
    measurement = {
        "schema_version": 1,
        "kind": "native-source-scanner-measurement",
        "source": context.checked(),
        "execution": "native",
        "publication_authorized": False,
        "source_archive_sha256": sha256(
            git(repository_root, "archive", context.commit)
        ),
        "source_inputs": {
            name: sha256(files["inputs/" + name]) for name in replay.LOCKS
        },
        "scans": rows,
    }
    path = output / "source-scan-measurement.json"
    path.write_bytes(json_bytes(measurement))
    return path


class DependencyReplay(unittest.TestCase):
    def setUp(self):
        self.fixture = fixtures.DependencyInputs()
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.root, commit = self.fixture.repository()
        self.context = NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", commit, "linux/amd64"
        )
        self.collection = self.fixture.folder / "collection"
        self.result = package.collect(
            root=self.root,
            repository=self.context.repository,
            version=self.context.version,
            commit=commit,
            platform=self.context.platform,
            go_image="docker.io/library/golang@sha256:" + "a" * 64,
            output=self.collection,
            execute=self.fixture.execute,
            fetch=lambda _: self.fixture.npm,
        )
        # The collector transition may be in progress in another worker. Fixtures
        # adopt the new contract explicitly; production verifier never accepts legacy names.
        old = self.collection / self.result["asset"]["name"]
        self.archive = (
            self.collection / "psst.zip-dependency-inputs-v1.2.3-amd64.tar.gz"
        )
        if old != self.archive:
            old.rename(self.archive)
        self.result["platform"] = self.context.platform
        self.result["asset"]["name"] = self.archive.name
        self.collection_record = self.collection / "dependency-collection.json"
        self.collection_record.write_bytes(json_bytes(self.result))
        self.files = replay.archive_files(self.archive.read_bytes())
        self.scan_root = self.fixture.folder / "source-scans"
        self.measurement_path = source_measurement_fixture(
            self.context, self.root, self.collection, self.scan_root
        )
        self.measurement = read_json(self.measurement_path.read_bytes())

    def save_measurement(self):
        self.measurement_path.write_bytes(json_bytes(self.measurement))

    def repack(self, *, extra=None):
        output = io.BytesIO()
        with tarfile.open(
            fileobj=output, mode="w:gz", format=tarfile.USTAR_FORMAT
        ) as archive:
            for name, body in sorted(self.files.items()):
                info = tarfile.TarInfo(name)
                info.size = len(body)
                info.mtime = int(
                    git(self.root, "show", "-s", "--format=%ct", self.context.commit)
                )
                info.mode = 0o644
                archive.addfile(info, io.BytesIO(body))
            if extra is not None:
                archive.addfile(
                    extra[0], io.BytesIO(extra[1]) if extra[1] is not None else None
                )
        body = output.getvalue()
        self.archive.write_bytes(body)
        self.result["asset"].update(sha256=sha256(body), size=len(body))
        self.collection_record.write_bytes(json_bytes(self.result))

    def verify(self):
        return replay.verify(
            self.context, self.root, self.collection, self.measurement_path
        )

    def test_complete_archive_replays_exact_go_and_all_optional_npm_without_approval(
        self,
    ):
        before = set(Path("/tmp").glob("psst-dependency-replay-*"))
        result, sources = replay.verify_module_source_files(
            self.context,
            self.root,
            self.collection,
            self.measurement_path,
            modules=frozenset({self.fixture.module}),
        )
        self.assertEqual(set(sources), {self.fixture.module})
        self.assertEqual(
            sources[self.fixture.module]["files"],
            {
                "go.mod": self.fixture.mod,
                "source.go": b"package dependency\n",
            },
        )
        self.assertEqual(
            sources[self.fixture.module]["record"], result["go_module_inputs"][0]
        )
        # A metadata-free proxy cannot invent a project-origin claim.
        self.assertIsNone(sources[self.fixture.module]["origin"])
        with self.assertRaisesRegex(
            InvalidRelease, "missing from verified dependency graph"
        ):
            replay.verify_module_source_files(
                self.context,
                self.root,
                self.collection,
                self.measurement_path,
                modules=frozenset({"not.selected/module"}),
            )
        self.assertEqual((result["go_modules"], result["npm_packages"]), (1, 1))
        self.assertTrue(result["preferred_source_review_required"])
        self.assertFalse(result["publication_authorized"])
        self.assertFalse(result["corresponding_source_completeness_verified"])
        self.assertEqual(result["archive_sha256"], sha256(self.archive.read_bytes()))
        self.assertEqual(
            result["go_module_inputs"],
            [
                {
                    "module": self.fixture.module,
                    "version": self.fixture.version,
                    "sum": self.fixture.zip_sum,
                    "zip_sha256": sha256(self.fixture.zip),
                }
            ],
        )
        self.assertEqual(set(Path("/tmp").glob("psst-dependency-replay-*")), before)

    def test_archive_hash_collection_identity_platform_name_and_origin_drift_fail(self):
        original = copy.deepcopy(self.result)
        for change in (
            lambda v: v.update(platform="linux/arm64"),
            lambda v: v.update(source_commit="f" * 40),
            lambda v: v["asset"].update(name="../elsewhere.tar.gz"),
            lambda v: v["asset"].update(sha256="sha256:" + "0" * 64),
        ):
            self.result = copy.deepcopy(original)
            change(self.result)
            self.collection_record.write_bytes(json_bytes(self.result))
            with self.assertRaises(InvalidRelease):
                self.verify()
        self.result = original
        self.collection_record.write_bytes(json_bytes(original))
        git(
            self.root,
            "remote",
            "set-url",
            "origin",
            "https://github.com/other/project.git",
        )
        with self.assertRaisesRegex(InvalidRelease, "origin"):
            self.verify()

    def test_exact_git_locks_selected_graph_and_raw_measurement_hashes_required(self):
        original = copy.deepcopy(self.measurement)
        for change in (
            lambda v: v["source"].update(commit="0" * 40),
            lambda v: v["source_inputs"].update(
                {"backend/go.sum": "sha256:" + "0" * 64}
            ),
            lambda v: v["scans"][0]["builder"].update(
                config_digest="sha256:" + "0" * 64
            ),
            lambda v: v["scans"][0]["raw_files"].update(
                {"go-modules.json": "sha256:" + "0" * 64}
            ),
            lambda v: v["scans"][1].update(dependency_packages=0),
        ):
            self.measurement = copy.deepcopy(original)
            change(self.measurement)
            self.save_measurement()
            with self.assertRaises(InvalidRelease):
                self.verify()
        self.measurement = original
        self.save_measurement()
        modules = self.scan_root / "backend-source/go-modules.json"
        body = json_bytes(
            {"Path": "example.org/application", "Main": True}
        ) + json_bytes({"Path": "other.org/module", "Version": "v1.0.0"})
        modules.write_bytes(body)
        self.measurement["scans"][0]["raw_files"]["go-modules.json"] = sha256(body)
        self.measurement["scans"][0]["graphs"]["go-modules.json"] = sha256(body)
        self.save_measurement()
        with self.assertRaisesRegex(InvalidRelease, "Selected Go"):
            self.verify()

    def test_missing_extra_duplicate_symlink_and_traversal_payload_fail(self):
        original = copy.deepcopy(self.files)
        for change in ("missing", "extra", "duplicate", "link", "traversal", "hidden"):
            self.files = copy.deepcopy(original)
            extra = None
            if change == "missing":
                del self.files[
                    next(name for name in self.files if name.startswith("npm/"))
                ]
            if change == "extra":
                self.files["unexpected.bin"] = b"extra"
            if change in {"duplicate", "traversal"}:
                entry = tarfile.TarInfo(
                    "SOURCE.md" if change == "duplicate" else "../escape"
                )
                entry.size = 1
                extra = entry, b"x"
            if change == "link":
                entry = tarfile.TarInfo("linked")
                entry.type = tarfile.SYMTYPE
                entry.linkname = "../escape"
                extra = entry, None
            self.repack(extra=extra)
            if change == "hidden":
                body = self.archive.read_bytes() + gzip.compress(b"hidden payload")
                self.archive.write_bytes(body)
                self.result["asset"].update(sha256=sha256(body), size=len(body))
                self.collection_record.write_bytes(json_bytes(self.result))
            with self.subTest(change=change), self.assertRaises(InvalidRelease):
                self.verify()

    def test_module_npm_inventory_sum_or_payload_tampering_cannot_replay(self):
        original = copy.deepcopy(self.files)
        for change in (
            "zip",
            "mod",
            "info",
            "npm",
            "module-inventory",
            "npm-inventory",
            "committed-sum",
            "committed-lock",
        ):
            self.files = copy.deepcopy(original)
            record = read_json(self.files["dependency-inputs.json"])
            if change in {"zip", "mod", "info"}:
                filename = record["go_modules"][0]["inputs"][change]["file"]
                if change == "zip":
                    changed = io.BytesIO()
                    with zipfile.ZipFile(changed, "w") as archive:
                        archive.writestr(
                            self.fixture.module
                            + "@"
                            + self.fixture.version
                            + "/source.go",
                            b"changed source",
                        )
                    self.files[filename] = changed.getvalue()
                else:
                    self.files[filename] += b"tampered"
                record["go_modules"][0]["inputs"][change].update(
                    sha256=sha256(self.files[filename]), size=len(self.files[filename])
                )
            if change == "npm":
                self.files[record["npm_packages"][0]["file"]] += b"tampered"
            if change == "module-inventory":
                record["go_modules"] = []
            if change == "npm-inventory":
                record["npm_packages"][0]["optional"] = False
            if change == "committed-sum":
                self.files["inputs/authenticated-go.sum"] = (
                    b"altered.org/module v1.0.0 h1:fake\n"
                )
            if change == "committed-lock":
                self.files["inputs/backend/go.mod"] += b"// changed original\n"
                record["inputs"]["backend/go.mod"] = sha256(
                    self.files["inputs/backend/go.mod"]
                )
            self.files["dependency-inputs.json"] = json_bytes(record)
            self.repack()
            with (
                self.subTest(change=change),
                self.assertRaises((InvalidRelease, ValueError)),
            ):
                self.verify()


if __name__ == "__main__":
    unittest.main()
