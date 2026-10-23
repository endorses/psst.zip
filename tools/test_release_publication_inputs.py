"""Downloaded artifact paths stay exact without reading large release payloads."""

from __future__ import annotations

import copy
import unittest
from pathlib import Path
from unittest.mock import patch

import publish_verified_release as command
import test_release_corresponding_source_command as source_fixtures
from publish_container_release import Binding, GATES, image_subjects
from release_artifacts import InvalidRelease, json_bytes, read_json
from test_release_artifacts import manifest


class PublicationInputMap(unittest.TestCase):
    def setUp(self):
        fixture = source_fixtures.CorrespondingSourceCommand()
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        self.fixture = fixture
        self.root = fixture.args.root
        self.inputs = self.root / "downloaded"
        self.inputs.mkdir()
        prepared = self.inputs / "prepared"
        fixture.args.prepared.rename(prepared)
        self.prepared = prepared
        value = manifest(fixture.args.commit)
        value["version"] = fixture.args.version
        value["payload_profile"] = "deployment-ready"
        value["bundle"]["name"] = "psst.zip-deployment-v0.1.0.tar.gz"
        record = copy.deepcopy(fixture.record)
        record["assets"][value["bundle"]["name"]] = (
            "sha256:" + value["bundle"]["sha256"]
        )
        record["subjects"]["bundle"] = (
            "file:" + value["bundle"]["name"] + "@sha256:" + value["bundle"]["sha256"]
        )
        record["subjects"].update(image_subjects(value))
        (prepared / "release-manifest.json").write_bytes(json_bytes(value))
        (prepared / "release-inputs.json").write_bytes(json_bytes(record))
        self.record = record
        self.payloads = {prepared / fixture.source, prepared / value["bundle"]["name"]}
        for component in ("backend", "web"):
            for arch in ("amd64", "arm64"):
                path = (
                    self.inputs / arch / "native/export" / f"{component}-{arch}.oci.tar"
                )
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b"tiny staged OCI fixture")
                self.payloads.add(path)
        binding = Binding(
            fixture.args.repository,
            fixture.args.version,
            fixture.args.commit,
            tuple(sorted(record["subjects"].items())),
        )
        record["binding_sha256"] = binding.digest
        (prepared / "release-inputs.json").write_bytes(json_bytes(record))
        path = self.inputs / "recovery/release-result.json"
        path.parent.mkdir(parents=True)
        checks = {gate: {} for gate in GATES}
        checks["source-ci"] = {"run_id": 123, "run_attempt": 2}
        checks["final-image-smoke"] = {
            "execution": {p: "native" for p in ("linux/amd64", "linux/arm64")}
        }
        checks["upgrade-recovery"] = checks["final-image-smoke"]
        path.write_bytes(
            json_bytes(
                {
                    "schema_version": 1,
                    "kind": "container-release-result",
                    "repository": binding.repository,
                    "version": binding.version,
                    "commit": binding.commit,
                    "run_id": 123,
                    "attempt": 2,
                    "binding_digest": binding.digest,
                    "subjects": dict(binding.subjects),
                    "checks": checks,
                    "inputs": {
                        "corresponding-source-replay": {"sha256": "sha256:" + "e" * 64},
                        "source-result": {"sha256": "sha256:" + "d" * 64},
                    },
                    "platforms": {
                        p: {
                            "execution": "native",
                            "checks": ["final-image-smoke", "upgrade-recovery"],
                        }
                        for p in ("linux/amd64", "linux/arm64")
                    },
                }
            )
        )
        self.reports = {gate: path for gate in GATES}

    def map(self, **changes):
        return command.publication_inputs(
            **(
                {
                    "inputs_root": self.inputs,
                    "repository": self.fixture.args.repository,
                    "version": self.fixture.args.version,
                    "commit": self.fixture.args.commit,
                }
                | changes
            )
        )

    def test_actual_download_layout_and_no_payload_rereads(self):
        original_open = Path.open

        def bounded_open(path, *args, **kwargs):
            self.assertNotIn(
                path, self.payloads, "Input map must not reread source/OCI payloads"
            )
            return original_open(path, *args, **kwargs)

        with patch.object(Path, "open", bounded_open):
            value = self.map()
        self.assertEqual(
            set(value),
            {"manifest", "bundle", "indexes", "archives", "source_assets", "reports"},
        )
        self.assertEqual(
            value["reports"], {gate: str(path) for gate, path in self.reports.items()}
        )
        self.assertEqual(
            set(value["archives"]),
            {c + "-" + a for c in ("backend", "web") for a in ("amd64", "arm64")},
        )
        self.assertEqual(
            value["source_assets"],
            {self.fixture.source: str(self.prepared / self.fixture.source)},
        )
        self.assertTrue(
            all(
                Path(path).is_absolute()
                for key in ("indexes", "archives", "source_assets", "reports")
                for path in value[key].values()
            )
        )

    def test_missing_linked_or_substituted_inputs_fail_before_map_output(self):
        target = self.reports["source-ci"]
        raw = target.read_bytes()
        target.unlink()
        with self.assertRaises((InvalidRelease, ValueError)):
            self.map()
        target.symlink_to(self.reports["runtime-notices"])
        with self.assertRaises((InvalidRelease, ValueError)):
            self.map()
        target.unlink()
        target.write_bytes(raw)
        for key, replacement in (
            ("binding_digest", "sha256:" + "0" * 64),
            ("kind", "unknown-result"),
        ):
            value = read_json(raw)
            value[key] = replacement
            target.write_bytes(json_bytes(value))
            with self.assertRaises((InvalidRelease, ValueError)):
                self.map()
        target.write_bytes(raw)
        with self.assertRaises((InvalidRelease, ValueError)):
            self.map(commit="b" * 40)
        manifest_path = self.prepared / "release-manifest.json"
        value = read_json(manifest_path.read_bytes())
        value["source"]["commit"] = "b" * 40
        value["source"]["archive_url"] = (
            "https://github.com/endorses/psst.zip/archive/" + "b" * 40 + ".tar.gz"
        )
        manifest_path.write_bytes(json_bytes(value))
        with self.assertRaises((InvalidRelease, ValueError)):
            self.map()
        value["source"]["commit"] = self.fixture.args.commit
        value["source"]["archive_url"] = (
            "https://github.com/endorses/psst.zip/archive/"
            + self.fixture.args.commit
            + ".tar.gz"
        )
        manifest_path.write_bytes(json_bytes(value))
        source = self.prepared / self.fixture.source
        source.unlink()
        source.symlink_to(self.reports["runtime-notices"])
        with self.assertRaises((InvalidRelease, ValueError)):
            self.map()


if __name__ == "__main__":
    unittest.main()
