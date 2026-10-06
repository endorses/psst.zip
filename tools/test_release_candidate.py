"""Release-candidate trust boundaries without registry writes or Docker state."""

from __future__ import annotations

import copy
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import prepare_release_candidate as candidate
from release_artifacts import InvalidRelease, json_bytes


def digest(character: str) -> str:
    return "sha256:" + character * 64


def index() -> dict:
    return {
        "schemaVersion": 2,
        "mediaType": "application/vnd.oci.image.index.v1+json",
        "digest": digest("1"),
        "manifests": [
            {
                "mediaType": "application/vnd.oci.image.manifest.v1+json",
                "digest": digest(character),
                "size": 100,
                "platform": {"os": "linux", "architecture": architecture},
            }
            for architecture, character in (("amd64", "2"), ("arm64", "3"))
        ],
    }


class IndexChecks(unittest.TestCase):
    def test_indexes_require_real_platform_pair_and_distinct_digests(self) -> None:
        expected = {"linux/amd64": digest("2"), "linux/arm64": digest("3")}
        self.assertEqual(candidate.index_record(index()), (digest("1"), expected))
        variations = []
        for key, value in (
            ("schemaVersion", True),
            ("mediaType", "application/vnd.oci.image.manifest.v1+json"),
            ("mediaType", []),
            ("digest", "latest"),
            ("manifests", []),
        ):
            record = index()
            record[key] = value
            variations.append(record)
        record = index()
        record["manifests"].pop()
        variations.append(record)
        record = index()
        record["manifests"].append(copy.deepcopy(record["manifests"][0]))
        variations.append(record)
        for field, value in (
            ("digest", digest("1")),
            ("digest", "../../image"),
            ("size", False),
            ("mediaType", "application/vnd.oci.image.index.v1+json"),
        ):
            record = index()
            record["manifests"][0][field] = value
            variations.append(record)
        record = index()
        record["manifests"][1]["digest"] = digest("2")
        variations.append(record)
        record = index()
        record["manifests"][1]["platform"]["variant"] = "v9"
        variations.append(record)
        for number, record in enumerate(variations):
            with self.subTest(number=number), self.assertRaises(InvalidRelease):
                candidate.index_record(record)

    def test_expected_arm_variant_and_attestation_descriptors_are_supported(
        self,
    ) -> None:
        value = index()
        value["manifests"][1]["platform"]["variant"] = "v8"
        value["manifests"].append(
            {
                "mediaType": "application/vnd.oci.image.manifest.v1+json",
                "digest": digest("4"),
                "size": 100,
                "platform": {"os": "unknown", "architecture": "unknown"},
            }
        )
        self.assertEqual(
            set(candidate.index_record(value)[1]), set(candidate.PLATFORMS)
        )

    def resolve(self) -> dict:
        with patch.object(
            candidate, "run", return_value=json_bytes(index())
        ) as command:
            result = candidate.resolve_bases("v1.2.3", "a" * 40)
            self.assertEqual(command.call_count, len(candidate.BASES) * 2)
            references = [call.args[4] for call in command.call_args_list]
            for tagged in candidate.BASES.values():
                self.assertIn(tagged, references)
                self.assertIn(tagged.rsplit(":", 1)[0] + "@" + digest("1"), references)
        return result

    def test_base_resolution_rechecks_immutable_digest_and_records_candidate_only(
        self,
    ) -> None:
        result = self.resolve()
        self.assertTrue(result["candidate_only"])
        self.assertNotIn("payload_profile", result)
        candidate.validate_candidate(result)
        changed = index()
        changed["manifests"][0]["digest"] = digest("5")
        with patch.object(
            candidate, "run", side_effect=[json_bytes(index()), json_bytes(changed)]
        ):
            with self.assertRaisesRegex(InvalidRelease, "Pinned base index differs"):
                candidate.resolve_bases("v1.2.3", "a" * 40)

    def test_downloaded_candidate_cannot_substitute_repository_platform_or_profile(
        self,
    ) -> None:
        original = self.resolve()
        for field, value in (
            ("candidate_only", False),
            ("kind", "deployment-ready"),
            ("version", "v1.2.3;id"),
            ("source_commit", "a" * 39),
            ("platforms", ["linux/amd64"]),
        ):
            record = copy.deepcopy(original)
            record[field] = value
            with self.assertRaises(InvalidRelease):
                candidate.validate_candidate(record)
        record = copy.deepcopy(original)
        record["base_images"]["golang"] = "docker.io/attacker/golang@" + digest("1")
        with self.assertRaises(InvalidRelease):
            candidate.validate_candidate(record)
        record = copy.deepcopy(original)
        record["payload_profile"] = "deployment-ready"
        with self.assertRaises(InvalidRelease):
            candidate.validate_candidate(record)

    def test_record_requires_build_metadata_and_records_actual_toolchain_commands(
        self,
    ) -> None:
        record = self.resolve()
        metadata = {
            name: {
                "containerimage.config.digest": digest("6"),
                "containerimage.digest": digest("6"),
            }
            for name in ("backend", "web")
        }
        # Docker's --load exporter can report the config digest under both keys;
        # candidate metadata never claims this is a published registry digest.
        with tempfile.TemporaryDirectory(
            prefix="psst-candidate-record-test-"
        ) as folder:
            archive = Path(folder) / "candidate-pair.tar"
            archive.write_bytes(b"fixture archive")

            def tool_output(*args):
                return (
                    b"aarch64\n"
                    if args[-1] == "{{.Architecture}}"
                    else b"actual tool output\n"
                )

            with patch.object(candidate, "run", side_effect=tool_output) as command:
                result = candidate.record_build(
                    record, "linux/arm64", metadata, archive
                )
                self.assertEqual(command.call_count, 7)
                self.assertEqual(
                    command.call_args_list[1].args,
                    (
                        "docker",
                        "run",
                        "--rm",
                        "--network",
                        "none",
                        "--platform",
                        "linux/arm64",
                        record["base_images"]["golang"],
                        "go",
                        "version",
                    ),
                )
                self.assertEqual(result["toolchain_output"]["go"], "actual tool output")
                self.assertTrue(result["candidate_only"])
                self.assertNotIn("payload_profile", result)
                self.assertEqual(
                    result["image_archive"]["size"], len(b"fixture archive")
                )
            for invalid in (
                {"backend": metadata["backend"]},
                {"backend": [], "web": metadata["web"]},
                {"backend": {}, "web": metadata["web"]},
            ):
                with self.assertRaises(InvalidRelease):
                    candidate.record_build(record, "linux/arm64", invalid, archive)
            with patch.object(candidate, "run", return_value=b"x86_64\n"):
                with self.assertRaisesRegex(InvalidRelease, "native Docker host"):
                    candidate.record_build(record, "linux/arm64", metadata, archive)
            del metadata["backend"]["containerimage.config.digest"]
            with self.assertRaises(InvalidRelease):
                candidate.record_build(record, "linux/arm64", metadata, archive)


class TagChecks(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory(prefix="psst-candidate-tag-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.env = {
            **os.environ,
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_CONFIG_NOSYSTEM": "1",
        }
        self.git("init", "-q")
        self.git("config", "user.name", "Candidate fixture")
        self.git("config", "user.email", "candidate@example.invalid")
        self.git("config", "core.hooksPath", os.devnull)
        (self.root / "fixture").write_text("first\n")
        self.git("add", ".")
        self.git("commit", "-qm", "Reviewed fixture")
        self.git("branch", "-M", "main")
        self.commit = self.git("rev-parse", "HEAD").decode().strip()
        self.git("update-ref", "refs/remotes/origin/main", self.commit)
        self.git("tag", "v1.2.3")

    def git(self, *args):
        return subprocess.run(
            ["git", "-C", str(self.root), *args],
            env=self.env,
            capture_output=True,
            check=True,
        ).stdout

    def test_lightweight_and_annotated_tags_resolve_exact_full_commit(self) -> None:
        self.assertEqual(
            candidate.validated_tag(self.root, "refs/tags/v1.2.3", self.commit),
            ("v1.2.3", self.commit),
        )
        self.git("tag", "-a", "v1.2.4", "-m", "Annotated fixture")
        tag_object = self.git("rev-parse", "refs/tags/v1.2.4").decode().strip()
        self.assertEqual(
            candidate.validated_tag(self.root, "refs/tags/v1.2.4", tag_object),
            ("v1.2.4", self.commit),
        )

    def test_nonrelease_refs_and_argument_injection_are_rejected(self) -> None:
        for ref in (
            "refs/heads/main",
            "refs/tags/v1.02.3",
            "refs/tags/v1.2.3-rc1",
            "refs/tags/v1.2.3; id",
            "refs/tags/v1.2.3/../../main",
        ):
            with self.subTest(ref=ref), self.assertRaises(InvalidRelease):
                candidate.validated_tag(self.root, ref, self.commit)
        with self.assertRaises(InvalidRelease):
            candidate.validated_tag(self.root, "refs/tags/v1.2.3", "--help")

    def test_off_main_commit_and_event_tag_mismatch_are_rejected(self) -> None:
        self.git("checkout", "-qb", "unreviewed")
        (self.root / "fixture").write_text("unreviewed\n")
        self.git("add", ".")
        self.git("commit", "-qm", "Unreviewed fixture")
        other = self.git("rev-parse", "HEAD").decode().strip()
        self.git("tag", "v2.0.0")
        with self.assertRaisesRegex(InvalidRelease, "not reachable"):
            candidate.validated_tag(self.root, "refs/tags/v2.0.0", other)
        with self.assertRaisesRegex(InvalidRelease, "differ"):
            candidate.validated_tag(self.root, "refs/tags/v1.2.3", other)
        self.git("update-ref", "-d", "refs/remotes/origin/main")
        with self.assertRaisesRegex(InvalidRelease, "not reachable"):
            candidate.validated_tag(self.root, "refs/tags/v1.2.3", self.commit)


if __name__ == "__main__":
    unittest.main()
