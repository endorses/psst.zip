"""Report authenticity boundaries; these fixtures do not claim live attestations."""

from __future__ import annotations

import copy
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

import github_release_evidence as evidence
from publish_container_release import Binding, sha256
from release_artifacts import InvalidRelease, json_bytes


class EvidenceChecks(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="psst-evidence-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.path = self.root / "gate.json"
        self.binding = Binding(
            "endorses/psst.zip", "v1.2.3", "a" * 40, (("manifest", "file:manifest@x"),)
        )
        self.report = {
            "schema_version": 1,
            "gate": "source-ci",
            "binding_digest": self.binding.digest,
            "passed": True,
            "details": {"jobs": {"backend": "success"}},
        }
        self.path.write_bytes(json_bytes(self.report))
        self.verifier = evidence.GhEvidenceVerifier(
            gh=Path(sys.executable), token="test-token"
        )

    def output(self):
        return [
            {
                "verificationResult": {
                    "signature": {"certificate": {"issuer": evidence.ISSUER}},
                    "verifiedTimestamps": [{"type": "tlog"}],
                    "statement": {
                        "predicateType": evidence.PREDICATE,
                        "subject": [
                            {
                                "name": "gate.json",
                                "digest": {
                                    "sha256": sha256(self.path.read_bytes())[7:]
                                },
                            }
                        ],
                    },
                }
            }
        ]

    def test_exact_policy_flags_and_snapshot_environment(self):
        snapshots = []

        def verify(args, environment):
            self.assertEqual(args[:3], [sys.executable, "attestation", "verify"])
            snapshot = Path(args[3])
            snapshots.append(snapshot.parent)
            self.assertNotEqual(snapshot, self.path)
            self.assertEqual(snapshot.read_bytes(), self.path.read_bytes())
            self.assertEqual(snapshot.stat().st_mode & 0o777, 0o400)
            self.assertEqual(snapshot.parent.stat().st_mode & 0o777, 0o700)
            for flag, value in (
                ("--repo", "endorses/psst.zip"),
                ("--signer-digest", "a" * 40),
                ("--source-digest", "a" * 40),
                ("--source-ref", "refs/tags/v1.2.3"),
                ("--hostname", "github.com"),
                ("--cert-oidc-issuer", evidence.ISSUER),
                ("--predicate-type", evidence.PREDICATE),
                (
                    "--cert-identity",
                    "https://github.com/endorses/psst.zip/.github/workflows/release.yml@refs/tags/v1.2.3",
                ),
            ):
                self.assertEqual(args[args.index(flag) + 1], value)
            self.assertIn("--deny-self-hosted-runners", args)
            self.assertEqual(environment["GH_TOKEN"], "test-token")
            self.assertEqual(
                set(environment),
                {
                    "PATH",
                    "HOME",
                    "GH_CONFIG_DIR",
                    "GH_PROMPT_DISABLED",
                    "LANG",
                    "GH_TOKEN",
                },
            )
            return json_bytes(self.output())

        with patch.object(evidence, "bounded_verify", side_effect=verify):
            receipt = self.verifier.verify("source-ci", self.path, self.binding)
        self.assertTrue(receipt.passed)
        self.assertEqual(receipt.report_digest, sha256(self.path.read_bytes()))
        self.assertEqual(receipt.binding_digest, self.binding.digest)
        self.assertTrue(all(not path.exists() for path in snapshots))

    def test_unsigned_report_cannot_produce_a_receipt(self):
        snapshots = []

        def unsigned(args, environment):
            snapshots.append(Path(args[3]).parent)
            raise InvalidRelease("unsigned")

        with patch.object(evidence, "bounded_verify", side_effect=unsigned):
            with self.assertRaises(InvalidRelease):
                self.verifier.verify("source-ci", self.path, self.binding)
        self.assertTrue(all(not path.exists() for path in snapshots))

    def test_failed_stale_wrong_gate_and_unknown_schema_rejected_before_network(self):
        cases = [
            ("passed", False),
            ("passed", 1),
            ("binding_digest", "sha256:" + "b" * 64),
            ("gate", "runtime-notices"),
            ("schema_version", True),
            ("schema_version", 2),
            ("details", None),
        ]
        for field, value in cases:
            with self.subTest(field=field, value=value):
                report = {**self.report, field: value}
                self.path.write_bytes(json_bytes(report))
                with patch.object(evidence, "bounded_verify") as runner:
                    with self.assertRaises(InvalidRelease):
                        self.verifier.verify("source-ci", self.path, self.binding)
                    runner.assert_not_called()

    def test_cli_success_without_expected_verified_subject_fails(self):
        valid = self.output()
        cases = [[], {}, [None], [{"verificationResult": {}}]]
        for field, value in (("signature", {}), ("verifiedTimestamps", [])):
            mutated = copy.deepcopy(valid)
            mutated[0]["verificationResult"][field] = value
            cases.append(mutated)
        for field, value in (("predicateType", "custom"), ("subject", [])):
            mutated = copy.deepcopy(valid)
            mutated[0]["verificationResult"]["statement"][field] = value
            cases.append(mutated)
        mutated = copy.deepcopy(valid)
        mutated[0]["verificationResult"]["statement"]["subject"][0]["digest"][
            "sha256"
        ] = ("0" * 64)
        cases.append(mutated)
        for output in cases:
            with self.subTest(output=output):
                with patch.object(
                    evidence, "bounded_verify", return_value=json_bytes(output)
                ):
                    with self.assertRaises(InvalidRelease):
                        self.verifier.verify("source-ci", self.path, self.binding)

    def test_substitution_during_verification_does_not_change_verified_report(self):
        expected = self.path.read_bytes()
        output = json_bytes(self.output())

        def verify(args, environment):
            self.path.write_bytes(b"substituted caller bytes")
            self.assertEqual(Path(args[3]).read_bytes(), expected)
            return output

        with patch.object(evidence, "bounded_verify", side_effect=verify):
            receipt = self.verifier.verify("source-ci", self.path, self.binding)
        self.assertEqual(receipt.report_digest, sha256(expected))

    def test_mutated_snapshot_rejected_even_if_cli_claims_success(self):
        def verify(args, environment):
            Path(args[3]).chmod(0o600)
            Path(args[3]).write_bytes(b"substituted snapshot")
            return json_bytes(self.output())

        with patch.object(evidence, "bounded_verify", side_effect=verify):
            with self.assertRaises(InvalidRelease):
                self.verifier.verify("source-ci", self.path, self.binding)

    def test_real_process_failure_overflow_and_deadline_are_bounded(self):
        environment = {"PATH": os.defpath}
        with self.assertRaisesRegex(InvalidRelease, "failed"):
            evidence.bounded_verify(
                [sys.executable, "-c", "raise SystemExit(1)"], environment
            )
        with patch.object(evidence, "MAX_OUTPUT", 4096):
            with self.assertRaisesRegex(InvalidRelease, "bounds"):
                evidence.bounded_verify(
                    [sys.executable, "-c", "print('x'*8192)"], environment
                )
        with patch.object(evidence, "TIMEOUT", 0.05):
            with self.assertRaisesRegex(InvalidRelease, "timed out"):
                evidence.bounded_verify(
                    [sys.executable, "-c", "import time;time.sleep(10)"], environment
                )


if __name__ == "__main__":
    unittest.main()
