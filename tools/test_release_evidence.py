"""Report authenticity boundaries; these fixtures do not claim live attestations."""

from __future__ import annotations

import copy
import os
from pathlib import Path
import shutil
import subprocess
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

    def test_measurement_authentication_is_bounded_and_has_no_gate_receipt(self):
        content = self.path.read_bytes()
        with patch.object(
            evidence, "bounded_verify", return_value=json_bytes(self.output())
        ) as runner:
            self.assertIsNone(self.verifier.authenticate(content, self.binding))
            self.assertIsNone(self.verifier.authenticate(content, self.binding))
            runner.assert_called_once()
        for value in (b"", "not-bytes", b"x" * (evidence.MAX_BUNDLE_BYTES + 1)):
            with self.subTest(value_type=type(value)):
                with patch.object(evidence, "bounded_verify") as runner:
                    with self.assertRaises(InvalidRelease):
                        self.verifier.authenticate(value, self.binding)
                    runner.assert_not_called()

    def test_exact_attempt_provenance_and_same_result_subject_scope_are_cached(self):
        for run_id, run_attempt in ((1, None), (None, 1), (0, 1), (1, True)):
            with self.subTest(scope=(run_id, run_attempt)), self.assertRaises(
                InvalidRelease
            ):
                evidence.GhEvidenceVerifier(
                    gh=Path(sys.executable), run_id=run_id, run_attempt=run_attempt
                )
        verifier = evidence.GhEvidenceVerifier(
            gh=Path(sys.executable), run_id=123, run_attempt=2
        )
        valid = self.output()
        statement = valid[0]["verificationResult"]["statement"]
        statement["_type"] = "https://in-toto.io/Statement/v1"
        repository = "https://github.com/" + self.binding.repository
        ref = "refs/tags/" + self.binding.version
        statement["predicate"] = {
            "buildDefinition": {
                "buildType": "https://actions.github.io/buildtypes/workflow/v1",
                "externalParameters": {
                    "workflow": {
                        "repository": repository,
                        "ref": ref,
                        "path": evidence.WORKFLOW,
                    }
                },
                "resolvedDependencies": [
                    {
                        "uri": "git+" + repository + "@" + ref,
                        "digest": {"gitCommit": self.binding.commit},
                    }
                ],
                "internalParameters": {
                    "github": {
                        "event_name": "push",
                        "runner_environment": "github-hosted",
                    }
                },
            },
            "runDetails": {
                "builder": {"id": repository + "/" + evidence.WORKFLOW + "@" + ref},
                "metadata": {
                    "invocationId": repository + "/actions/runs/123/attempts/2"
                },
            },
        }
        content = self.path.read_bytes()
        mutations = (
            (
                ("runDetails", "metadata", "invocationId"),
                repository + "/actions/runs/123/attempts/1",
            ),
            (
                ("runDetails", "metadata", "invocationId"),
                repository + "/actions/runs/999/attempts/2",
            ),
            (
                ("buildDefinition", "externalParameters", "workflow", "repository"),
                "https://github.com/other/project",
            ),
            (
                ("buildDefinition", "externalParameters", "workflow", "ref"),
                "refs/tags/v9.0.0",
            ),
            (
                ("buildDefinition", "externalParameters", "workflow", "path"),
                ".github/workflows/ci.yml",
            ),
            (
                ("buildDefinition", "resolvedDependencies"),
                [
                    {
                        "uri": "git+" + repository + "@" + ref,
                        "digest": {"gitCommit": "b" * 40},
                    }
                ],
            ),
            (
                ("buildDefinition", "internalParameters", "github", "event_name"),
                "workflow_dispatch",
            ),
            (
                (
                    "buildDefinition",
                    "internalParameters",
                    "github",
                    "runner_environment",
                ),
                "self-hosted",
            ),
            (
                ("runDetails", "builder", "id"),
                repository + "/.github/workflows/ci.yml@" + ref,
            ),
        )
        stale = None
        for keys, value in mutations:
            changed = copy.deepcopy(valid)
            destination = changed[0]["verificationResult"]["statement"]["predicate"]
            for key in keys[:-1]:
                destination = destination[key]
            destination[keys[-1]] = value
            if stale is None:
                stale = changed
            with self.subTest(path=keys, value=value), patch.object(
                evidence, "bounded_verify", return_value=json_bytes(changed)
            ) as runner:
                with self.assertRaises(InvalidRelease):
                    verifier.authenticate(content, self.binding)
                # A failed verification must not poison or populate the cache.
                with self.assertRaises(InvalidRelease):
                    verifier.authenticate(content, self.binding)
                self.assertEqual(runner.call_count, 2)
        wrong_subject = copy.deepcopy(valid)
        wrong_subject[0]["verificationResult"]["statement"]["subject"][0]["digest"][
            "sha256"
        ] = ("0" * 64)
        with patch.object(
            evidence, "bounded_verify", return_value=json_bytes(stale + wrong_subject)
        ):
            with self.assertRaises(InvalidRelease):
                verifier.authenticate(content, self.binding)
        with patch.object(
            evidence, "bounded_verify", return_value=json_bytes(stale + valid)
        ) as runner:
            verifier.authenticate(content, self.binding)
            verifier.authenticate(content, self.binding)
            runner.assert_called_once()
            changed_binding = Binding(
                self.binding.repository,
                self.binding.version,
                self.binding.commit,
                (("manifest", "file:different-subject@x"),),
            )
            verifier.authenticate(content, changed_binding)
            self.assertEqual(runner.call_count, 2)
            with self.assertRaises(InvalidRelease):
                verifier.authenticate(content + b"\n", self.binding)
            self.assertEqual(runner.call_count, 3)
            other_attempt = evidence.GhEvidenceVerifier(
                gh=Path(sys.executable), run_id=123, run_attempt=3
            )
            with self.assertRaises(InvalidRelease):
                other_attempt.authenticate(content, self.binding)
            self.assertEqual(runner.call_count, 4)
        with patch.object(evidence, "MAX_AUTHENTICATION_CACHE", 1), patch.object(
            evidence, "bounded_verify", return_value=json_bytes(valid)
        ) as runner:
            fresh = evidence.GhEvidenceVerifier(
                gh=Path(sys.executable), run_id=123, run_attempt=2
            )
            fresh.authenticate(content, self.binding)
            fresh.authenticate(content, changed_binding)
            fresh.authenticate(content, self.binding)
            self.assertEqual(runner.call_count, 3)
            self.assertEqual(len(fresh._authenticated), 1)

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
            self.assertNotIn("--signer-workflow", args)
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

    @unittest.skipUnless(
        shutil.which("gh"), "Install GitHub CLI for argument compatibility"
    )
    def test_real_cli_accepts_identity_policy_before_local_trust_failure(self):
        # Missing local trust material fails before network verification. Exercise
        # the real parser; this test-only override never enters production policy.
        args = evidence.verification_arguments(
            Path(shutil.which("gh")), self.path, self.binding
        )
        args += ["--bundle", str(self.root / "missing-attestation.json")]
        args += ["--custom-trusted-root", str(self.root / "missing-trusted-root.jsonl")]
        result = subprocess.run(
            args,
            capture_output=True,
            timeout=5,
            env={
                "PATH": os.defpath,
                "HOME": str(self.root),
                "GH_CONFIG_DIR": str(self.root / "gh-config"),
                "GH_PROMPT_DISABLED": "1",
                "GH_TOKEN": "fixture-token",
            },
        )
        self.assertNotEqual(result.returncode, 0)
        diagnostic = result.stderr.decode()
        self.assertIn("missing-trusted-root.jsonl", diagnostic)
        self.assertNotIn("cannot be used together", diagnostic)
        self.assertNotIn("mutually exclusive", diagnostic)


if __name__ == "__main__":
    unittest.main()
