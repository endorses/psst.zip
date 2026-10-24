"""Transitional Python adapters preserve the canonical Go candidate boundary."""

from __future__ import annotations

import copy
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import prepare_release_candidate as candidate
from release_artifacts import InvalidRelease, json_bytes, read_json


class CandidateAdapters(unittest.TestCase):
    def test_exported_defaults_match_canonical_go_policy(self):
        self.assertEqual(
            read_json(candidate._invoke(candidate.ROOT, "defaults")), candidate.BASES
        )

    def test_candidate_validation_crosses_real_go_boundary_and_cleans_input(self):
        value = {
            "schema_version": 1,
            "kind": "release-candidate",
            "candidate_only": True,
            "version": "v1.2.3",
            "source_commit": "a" * 40,
            "platforms": candidate.PLATFORMS,
            "base_images": {
                name: ref.rsplit(":", 1)[0] + "@sha256:" + "1" * 64
                for name, ref in candidate.BASES.items()
            },
            "base_platform_digests": {
                name: {
                    "linux/amd64": "sha256:" + "2" * 64,
                    "linux/arm64": "sha256:" + "3" * 64,
                }
                for name in candidate.BASES
            },
        }
        original = candidate._invoke
        inputs = []

        def observe(root, mode, *args):
            inputs.append(Path(args[1]))
            return original(root, mode, *args)

        with patch.object(candidate, "_invoke", side_effect=observe):
            self.assertEqual(candidate.validate_candidate(value), value)
            invalid = copy.deepcopy(value)
            invalid["base_images"]["golang"] = (
                "docker.io/other/golang@sha256:" + "1" * 64
            )
            with self.assertRaises(InvalidRelease):
                candidate.validate_candidate(invalid)
        self.assertTrue(inputs)
        self.assertTrue(
            all(not path.exists() and not path.parent.exists() for path in inputs)
        )

    def test_build_adapter_retains_original_metadata_and_cleans_private_inputs(self):
        metadata = {
            name: {
                "containerimage.config.digest": "sha256:" + "1" * 64,
                "containerimage.digest": "sha256:" + "1" * 64,
                "buildx.build.ref": "original",
            }
            for name in ("backend", "web")
        }
        files = []

        def record(root, mode, *args):
            self.assertEqual(root, candidate.ROOT)
            self.assertEqual(mode, "record-build")
            options = dict(zip(args[::2], args[1::2]))
            for name in ("backend", "web"):
                path = Path(options["--" + name + "-metadata"])
                files.append(path)
                self.assertEqual(read_json(path.read_bytes()), metadata[name])
            self.assertEqual(options["--platform"], "linux/arm64")
            return json_bytes({"recorded": True})

        with tempfile.TemporaryDirectory() as temporary:
            archive = Path(temporary) / "original.tar"
            archive.write_bytes(b"original")
            with patch.object(candidate, "_invoke", side_effect=record):
                self.assertEqual(
                    candidate.record_build({}, "linux/arm64", metadata, archive),
                    {"recorded": True},
                )
        self.assertTrue(
            all(not path.exists() and not path.parent.exists() for path in files)
        )

    def test_bundle_adapter_forwards_exact_reviewed_source(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / "output"
            destination = output / "psst.zip-deployment-v1.2.3.tar.gz"
            with patch.object(
                candidate,
                "_invoke",
                return_value=json_bytes({"path": str(destination)}),
            ) as invoke:
                self.assertEqual(
                    candidate.build_bundle(
                        root,
                        output,
                        "v1.2.3",
                        "deployment-ready",
                        expected_commit="a" * 40,
                    ),
                    destination,
                )
            self.assertEqual(
                invoke.call_args.args[-2:], ("--expected-commit", "a" * 40)
            )


if __name__ == "__main__":
    unittest.main()
