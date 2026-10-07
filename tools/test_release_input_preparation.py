"""Actual Git/OCI assembly with disposable measurements; no release approval."""

import copy
import tarfile
import unittest

import prepare_release_inputs as preparation
from prepare_release_candidate import BASES
from release_artifacts import InvalidRelease, PLATFORMS, json_bytes, read_json
import test_release_gate_reports as report_fixtures


class InputPreparation(unittest.TestCase):
    def setUp(self):
        self.fixture = report_fixtures.GateReports()
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
        self.assertEqual(len(result["subjects"]), 11)
        self.assertEqual(len(result["assets"]), 5)
        output = self.args["output"]
        manifest = read_json((output / "release-manifest.json").read_bytes())
        self.assertEqual(
            manifest["build"]["toolchains"], {"go": "go1.26.8", "node": "v22.22.0"}
        )
        self.assertEqual(
            manifest["build"]["base_images"], self.candidate["base_images"]
        )
        source = output / "psst.zip-source-v1.2.3.tar.gz"
        with tarfile.open(source, "r:gz") as archive:
            names = archive.getnames()
        self.assertIn("psst.zip-v1.2.3/deploy/update.py", names)
        self.assertFalse(any("private.env" in name for name in names))
        with self.assertRaisesRegex(InvalidRelease, "must be new"):
            self.prepare()

    def test_missing_platform_or_export_rejected_before_outputs(self):
        for key in ("builds", "measurements", "packs", "archives"):
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


if __name__ == "__main__":
    unittest.main()
