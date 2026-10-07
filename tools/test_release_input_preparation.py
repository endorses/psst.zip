"""Actual Git/OCI assembly with disposable measurements; no release approval."""

import copy
import tarfile
import unittest
from pathlib import Path
from unittest.mock import patch

import prepare_release_inputs as preparation
import package_application_dependencies as dependency
import package_upstream_application_sources as upstream
from test_release_upstream_sources import upstream_fixture
from generate_release_gate_reports import NativeSourceContext
from prepare_release_candidate import BASES
from release_artifacts import InvalidRelease, PLATFORMS, json_bytes, read_json
import test_release_gate_reports as report_fixtures
import test_release_publication as publication_fixtures
import test_release_dependency_inputs as dependency_fixtures
from test_release_dependency_replay import source_measurement_fixture


class InputPreparation(unittest.TestCase):
    def setUp(self):
        self.dependencies = dependency_fixtures.DependencyInputs()
        self.dependencies.setUp()
        self.addCleanup(self.dependencies.doCleanups)
        original_git = publication_fixtures.PublicationChecks.git

        def source_repository(repository, *arguments):
            if arguments[:1] == ("commit",):
                for name, content in {
                    "backend/go.mod": b"module example.org/application\n\ngo 1.23\n",
                    "backend/go.sum": self.dependencies.sum,
                    "web/package-lock.json": json_bytes(self.dependencies.lock),
                }.items():
                    path = repository.root / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_bytes(content)
                self.upstream_fetch = upstream_fixture(repository.root)
                original_git(
                    repository, "add", "tools/upstream-application-sources.json"
                )
                original_git(
                    repository,
                    "add",
                    "backend/go.mod",
                    "backend/go.sum",
                    "web/package-lock.json",
                )
            return original_git(repository, *arguments)

        self.fixture = report_fixtures.GateReports()
        with patch.object(
            publication_fixtures.PublicationChecks, "git", source_repository
        ):
            self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        binding = self.fixture.binding
        self.repository = self.fixture.fixture.fixture
        self.folder = self.fixture.root
        self.candidate = {
            "schema_version": 1,
            "kind": "release-candidate",
            "candidate_only": True,
            "version": binding.version,
            "source_commit": binding.commit,
            "platforms": PLATFORMS,
            "base_images": {
                k: v.rsplit(":", 1)[0] + "@sha256:" + "1" * 64 for k, v in BASES.items()
            },
            "base_platform_digests": {
                k: {
                    "linux/amd64": "sha256:" + "2" * 64,
                    "linux/arm64": "sha256:" + "3" * 64,
                }
                for k in BASES
            },
        }
        self.candidate_path = self.folder / "candidate.json"
        self.candidate_path.write_bytes(json_bytes(self.candidate))
        self.builds = {}
        for platform in PLATFORMS:
            arch = platform.split("/")[1]
            path = self.folder / (arch + "-build.json")
            path.write_bytes(
                json_bytes(
                    {
                        **self.candidate,
                        "checked_platform": platform,
                        "native_execution": True,
                        "toolchain_output": {
                            "go": "go version go1.26.8 " + platform,
                            "node": "v22.22.0",
                        },
                    }
                )
            )
            self.builds[platform] = path
            pack = self.fixture.packs[platform]
            record = read_json((pack / "runtime-pack.json").read_bytes())
            old = record["source_asset"]["file"]
            name = "runtime-sources-" + arch + ".tar.gz"
            (pack / old).rename(pack / name)
            record["source_asset"]["file"] = name
            record["source_asset"][
                "url"
            ] = f"https://github.com/{binding.repository}/releases/download/{binding.version}/{name}"
            (pack / "runtime-pack.json").write_bytes(json_bytes(record))
            self.fixture.smokes[platform]["runtime_pack_sha256"] = (
                preparation.source_digest(pack / "runtime-pack.json")
            )
        self.measurements = self.fixture.collect_both()
        self.dependency_collections, self.source_scans = {}, {}
        for platform in PLATFORMS:
            architecture = platform.split("/")[1]

            def execution(args, *, environment, timeout):
                if args[1:3] == ["image", "inspect"]:
                    return json_bytes(
                        [
                            {
                                "Id": "sha256:" + "c" * 64,
                                "Os": "linux",
                                "Architecture": architecture,
                            }
                        ]
                    )
                result = self.dependencies.execute(
                    args, environment=environment, timeout=timeout
                )
                reports = Path(
                    next(value for value in args if value.endswith("dst=/reports"))
                    .split("src=", 1)[1]
                    .split(",dst=", 1)[0]
                )
                (reports / "go-version.txt").write_text(
                    "go version go1.26.8 " + platform + "\n"
                )
                return result

            collection = self.folder / ("dependencies-" + architecture)
            dependency.collect(
                root=self.repository.root,
                repository=binding.repository,
                version=binding.version,
                commit=binding.commit,
                platform=platform,
                go_image=self.candidate["base_images"]["golang"],
                output=collection,
                execute=execution,
                fetch=lambda _: self.dependencies.npm,
            )
            self.dependency_collections[platform] = collection
            self.source_scans[platform] = source_measurement_fixture(
                NativeSourceContext(
                    binding.repository, binding.version, binding.commit, platform
                ),
                self.repository.root,
                collection,
                self.folder / ("source-scan-" + architecture),
            )
        self.upstream_collection = self.folder / "upstream-sources"
        upstream.collect(
            root=self.repository.root,
            repository=binding.repository,
            version=binding.version,
            commit=binding.commit,
            output=self.upstream_collection,
            fetch=lambda url: self.upstream_fetch[url],
        )
        self.args = dict(
            root=self.repository.root,
            repository=binding.repository,
            ref="refs/tags/" + binding.version,
            event_sha=binding.commit,
            reviewed_commit=binding.commit,
            candidate=self.candidate_path,
            builds=self.builds,
            measurements=self.measurements,
            packs=self.fixture.packs,
            archives=self.fixture.fixture.archives,
            dependency_collections=self.dependency_collections,
            source_scans=self.source_scans,
            upstream_collection=self.upstream_collection,
            output=self.folder / "prepared",
            migration_notes="Stopped checkpoint required.",
            rollback_notes="Restore checkpoint into fresh volumes before activation.",
        )

    def prepare(self, **overrides):
        return preparation.prepare(**(self.args | overrides))

    def test_four_real_oci_exports_git_bundle_and_source_assemble_without_approval(
        self,
    ):
        (self.repository.root / "private.env").write_text(
            "never archive untracked input"
        )
        result = self.prepare()
        self.assertFalse(result["publication_authorized"])
        self.assertTrue(result["measurement_authentication_required"])
        self.assertEqual(len(result["subjects"]), 14)
        self.assertEqual(len(result["assets"]), 8)
        output = self.args["output"]
        manifest = read_json((output / "release-manifest.json").read_bytes())
        self.assertEqual(
            manifest["build"]["toolchains"], {"go": "go1.26.8", "node": "v22.22.0"}
        )
        self.assertEqual(
            manifest["build"]["base_images"], self.candidate["base_images"]
        )
        source = output / "psst.zip-source-v1.2.3.tar.gz"
        for name, digest in result["assets"].items():
            self.assertEqual(preparation.source_digest(output / name), digest)
        self.assertEqual(set(result["dependency_replays"]), set(PLATFORMS))
        self.assertTrue(result["upstream_replay"]["package_inputs_replayed"])
        self.assertFalse(result["upstream_replay"]["publication_authorized"])
        self.assertIn(result["upstream_replay"]["asset"]["name"], result["assets"])
        with tarfile.open(source, "r:gz") as archive:
            names = archive.getnames()
        self.assertIn("psst.zip-v1.2.3/deploy/update.py", names)
        self.assertFalse(any("private.env" in name for name in names))
        with self.assertRaisesRegex(InvalidRelease, "must be new"):
            self.prepare()

    def planned_arguments(self):
        binding = self.fixture.binding
        self.repository.git("tag", "-d", binding.version)
        return {
            "ref": "refs/heads/main",
            "planned_version": binding.version,
            "event_name": "workflow_dispatch",
        }

    def test_unused_main_candidate_assembles_without_tag_or_publication(self):
        planned = self.planned_arguments()
        result = self.prepare(**planned)
        self.assertEqual(result["kind"], "planned-candidate-inputs")
        self.assertEqual(result["source_kind"], "planned-main-dispatch")
        self.assertFalse(result["publication_authorized"])
        self.assertFalse(result["tagged_source_ci_gate_verified"])
        self.assertFalse(result["signer_identity_verified"])
        self.assertEqual(len(result["assets"]), 8)
        self.assertEqual(len(result["subjects"]), 14)
        self.assertEqual(self.repository.git("tag", "--list").strip(), b"")

    def test_planned_source_event_branch_reviewed_commit_and_checkout_guards(self):
        planned = self.planned_arguments()
        for change in (
            {"event_name": "push"},
            {"event_name": "pull_request"},
            {"ref": "refs/heads/other"},
            {"reviewed_commit": "a" * 40},
            {"event_sha": "a" * 40},
        ):
            with self.subTest(change=change), self.assertRaises(InvalidRelease):
                self.prepare(**(planned | change))
            self.assertFalse(self.args["output"].exists())
        (self.repository.root / "deploy/update.py").write_text("unreviewed helper")
        with self.assertRaisesRegex(InvalidRelease, "Tracked checkout differs"):
            self.prepare(**planned)
        self.assertFalse(self.args["output"].exists())

    def test_planned_version_cannot_borrow_existing_tag(self):
        with self.assertRaisesRegex(InvalidRelease, "already has a tag"):
            self.prepare(
                ref="refs/heads/main",
                planned_version=self.fixture.binding.version,
                event_name="workflow_dispatch",
            )
        self.assertFalse(self.args["output"].exists())

    def test_missing_platform_or_export_rejected_before_outputs(self):
        for key in (
            "builds",
            "measurements",
            "packs",
            "archives",
            "dependency_collections",
            "source_scans",
        ):
            pair = dict(self.args[key])
            pair.pop(next(iter(pair)))
            with self.subTest(key=key), self.assertRaises(InvalidRelease):
                self.prepare(**{key: pair})
            self.assertFalse(self.args["output"].exists())

    def test_stale_emulated_or_substituted_native_records_are_rejected(self):
        path = self.measurements["linux/arm64"]
        original = read_json(path.read_bytes())
        for change in (
            lambda v: v["source"].update(commit="a" * 40),
            lambda v: v["smoke"].update(execution="emulated"),
            lambda v: v["smoke"]["tested_configs"].update(backend="sha256:" + "b" * 64),
            lambda v: v["runtime"].update(distribution_review_required=False),
            lambda v: v.update(publication_authorized=True),
        ):
            value = copy.deepcopy(original)
            change(value)
            path.write_bytes(json_bytes(value))
            with self.assertRaises(InvalidRelease):
                self.prepare()
            self.assertFalse(self.args["output"].exists())

    def test_different_resolved_inputs_or_toolchains_are_rejected(self):
        path = self.builds["linux/arm64"]
        original = read_json(path.read_bytes())
        for change in (
            lambda v: v.update(native_execution=False),
            lambda v: v["base_images"].update(
                node="docker.io/library/node@sha256:" + "4" * 64
            ),
            lambda v: v["toolchain_output"].update(
                go="go version go1.26.7 linux/arm64"
            ),
            lambda v: v["toolchain_output"].update(
                go="go version go1.26.8 linux/amd64"
            ),
            lambda v: v["toolchain_output"].update(node="arbitrary text"),
        ):
            value = copy.deepcopy(original)
            change(value)
            path.write_bytes(json_bytes(value))
            with self.assertRaises(InvalidRelease):
                self.prepare()
            self.assertFalse(self.args["output"].exists())

    def test_dependency_collection_archive_and_source_receipt_substitution_fail(self):
        platform = "linux/arm64"
        directory = self.dependency_collections[platform]
        record = directory / "dependency-collection.json"
        archive = directory / read_json(record.read_bytes())["asset"]["name"]
        for path in (record, archive, self.source_scans[platform]):
            original = path.read_bytes()
            path.write_bytes(original + b"substituted")
            with self.subTest(path=path.name), self.assertRaises(
                (InvalidRelease, ValueError)
            ):
                self.prepare()
            self.assertFalse(self.args["output"].exists())
            path.write_bytes(original)

    def test_missing_upstream_collection_is_rejected_before_outputs(self):
        with self.assertRaises((InvalidRelease, OSError)):
            self.prepare(upstream_collection=self.folder / "missing-upstream")
        self.assertFalse(self.args["output"].exists())

    def test_upstream_collection_and_archive_substitution_fail_before_outputs(self):
        record = self.upstream_collection / "upstream-source-collection.json"
        archive = (
            self.upstream_collection / read_json(record.read_bytes())["asset"]["name"]
        )
        for path in (record, archive):
            original = path.read_bytes()
            path.write_bytes(original + b"substituted")
            with self.subTest(path=path.name), self.assertRaises(
                (InvalidRelease, ValueError)
            ):
                self.prepare()
            self.assertFalse(self.args["output"].exists())
            path.write_bytes(original)

    def test_upstream_asset_changed_after_replay_cannot_be_bound(self):
        original = preparation.verify_upstream

        def replay_then_substitute(**arguments):
            result = original(**arguments)
            path = arguments["collection"] / result["asset"]["name"]
            path.write_bytes(path.read_bytes() + b"changed after replay")
            return result

        with patch.object(
            preparation, "verify_upstream", side_effect=replay_then_substitute
        ), self.assertRaisesRegex(InvalidRelease, "changed after verification"):
            self.prepare()

    def test_tampered_source_archive_or_dirty_checkout_rejected(self):
        pack = self.fixture.packs["linux/amd64"]
        asset = next(pack.glob("*.tar.gz"))
        original = asset.read_bytes()
        asset.write_bytes(original + b"tamper")
        with self.assertRaises(InvalidRelease):
            self.prepare()
        asset.write_bytes(original)
        (self.repository.root / "deploy/update.py").write_text("unreviewed helper")
        with self.assertRaisesRegex(InvalidRelease, "Tracked checkout differs"):
            self.prepare()
        self.assertFalse(self.args["output"].exists())

    def test_dependency_asset_changed_after_replay_cannot_be_bound(self):
        original = preparation.verify_dependencies

        def replay_then_substitute(context, root, collection, source_scan):
            result = original(context, root, collection, source_scan)
            if context.platform == "linux/amd64":
                metadata = read_json(
                    (collection / "dependency-collection.json").read_bytes()
                )
                path = collection / metadata["asset"]["name"]
                path.write_bytes(path.read_bytes() + b"changed after replay")
            return result

        with patch.object(
            preparation, "verify_dependencies", side_effect=replay_then_substitute
        ), self.assertRaisesRegex(InvalidRelease, "changed after verification"):
            self.prepare()

    def test_collection_changed_after_replay_cannot_select_another_asset(self):
        original = preparation.verify_dependencies

        def replay_then_replace_collection(context, root, collection, source_scan):
            result = original(context, root, collection, source_scan)
            if context.platform == "linux/amd64":
                path = collection / "dependency-collection.json"
                metadata = read_json(path.read_bytes())
                metadata["asset"]["name"] = "unreplayed-inputs.tar.gz"
                path.write_bytes(json_bytes(metadata))
            return result

        with patch.object(
            preparation,
            "verify_dependencies",
            side_effect=replay_then_replace_collection,
        ), self.assertRaisesRegex(InvalidRelease, "collection changed after replay"):
            self.prepare()
        self.assertFalse(self.args["output"].exists())

    def test_runtime_source_changed_after_native_verification_cannot_be_bound(self):
        original = preparation.runtime_inputs

        def runtime_then_substitute(context, pack):
            record, runtime = original(context, pack)
            if context.platform == "linux/amd64":
                path = pack / runtime["source_asset"]["name"]
                path.write_bytes(
                    path.read_bytes() + b"changed after native verification"
                )
            return record, runtime

        with patch.object(
            preparation, "runtime_inputs", side_effect=runtime_then_substitute
        ), self.assertRaisesRegex(InvalidRelease, "changed after verification"):
            self.prepare()


if __name__ == "__main__":
    unittest.main()
