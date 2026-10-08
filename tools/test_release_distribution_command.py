"""Small offline command fixtures; real review authorization remains API-derived."""

from __future__ import annotations

from contextlib import ExitStack
import copy
import io
import unittest
from unittest.mock import Mock, patch

import aggregate_release_recovery as recovery
import generate_distribution_review as command
import github_release_evidence
from github_release_transport import Response
from publish_container_release import VerifiedEvidence, WORKFLOW
from release_artifacts import InvalidRelease, json_bytes, read_json
import test_release_corresponding_source_command as source_fixtures


class DistributionCommand(unittest.TestCase):
    def setUp(self):
        fixture = source_fixtures.CorrespondingSourceCommand()
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        self.args = fixture.args
        self.binding = fixture.binding
        self.source = fixture.report
        self.args.source_report = self.args.root / "corresponding-source.json"
        self.args.source_report.write_bytes(json_bytes(self.source))
        self.args.mode = "present"
        self.args.output = self.args.root / "presentation"
        for name in (
            fixture.source,
            "oci-correspondence.json",
            "backend-index.json",
            "web-index.json",
        ):
            (self.args.prepared / name).unlink()
        self.policy = {
            "schema_version": 1,
            "kind": "container-distribution-review-policy",
            "environment": "container-release",
            "reviewers": ["fixture-owner"],
        }
        self.fact = self.source["details"]["policy"]
        self.trusted = {self.args.source_report.read_bytes()}
        self.verifier = Mock()

        def authenticate(raw, binding):
            if raw not in self.trusted or binding != self.binding:
                raise InvalidRelease("Unsigned caller evidence")

        def verify(gate, path, binding):
            raw = path.read_bytes()
            self.verifier.authenticate(raw, binding)
            report = read_json(raw)
            return VerifiedEvidence(
                gate, binding.digest, command.sha256(raw), True, report["details"]
            )

        self.verifier.authenticate.side_effect = authenticate
        self.verifier.verify.side_effect = verify
        self.user = {"type": "User", "login": "fixture-owner", "id": 92}
        self.run = {
            "id": self.args.run_id,
            "run_attempt": self.args.run_attempt,
            "head_sha": self.binding.commit,
            "head_branch": self.binding.version,
            "event": "push",
            "path": WORKFLOW,
            "status": "in_progress",
            "repository": {"full_name": self.binding.repository},
            "head_repository": {"full_name": self.binding.repository},
        }
        self.environment = {
            "id": 31,
            "name": self.policy["environment"],
            "protection_rules": [
                {
                    "type": "required_reviewers",
                    "reviewers": [{"type": "User", "reviewer": self.user}],
                }
            ],
        }
        self.reviews = [
            {
                "state": "approved",
                "comment": command.approval_comment(
                    self.binding,
                    command.sha256(self.args.source_report.read_bytes()),
                    self.args.run_id,
                    self.args.run_attempt,
                ),
                "user": self.user,
                "environments": [{"id": 31, "name": self.policy["environment"]}],
            }
        ]
        self.http = Mock()

        def request(method, url, *, headers):
            self.assertEqual(method, "GET")
            self.assertEqual(headers["Authorization"], "Bearer fixture-token")
            if url.endswith("/approvals"):
                value = self.reviews
            elif "/environments/" in url:
                value = self.environment
            else:
                value = self.run
            return Response(200, {}, json_bytes(value))

        self.http.request.side_effect = request
        stack = ExitStack()
        self.addCleanup(stack.close)
        stack.enter_context(
            patch.dict(command.os.environ, {"GH_TOKEN": "fixture-token"})
        )
        self.auth_factory = stack.enter_context(
            patch.object(
                github_release_evidence,
                "GhEvidenceVerifier",
                return_value=self.verifier,
            )
        )
        stack.enter_context(
            patch.object(
                command, "committed_policy", return_value=(self.policy, self.fact)
            )
        )
        stack.enter_context(patch.object(command, "HTTPS", return_value=self.http))
        manifest = read_json(
            (self.args.prepared / "release-manifest.json").read_bytes()
        )
        bundle = manifest["bundle"]["name"]
        manifest.update(
            payload_profile="deployment-ready",
            version=self.binding.version,
            source={"commit": self.binding.commit},
            bundle={
                "name": bundle,
                "sha256": command.sha256(
                    (self.args.prepared / bundle).read_bytes()
                ).removeprefix("sha256:"),
            },
        )
        stack.enter_context(
            patch.object(recovery, "validate_manifest", return_value=manifest)
        )
        stack.enter_context(
            patch.object(
                recovery,
                "image_subjects",
                return_value={
                    k: v
                    for k, v in self.binding.subjects
                    if not k.startswith("source:") and k not in {"manifest", "bundle"}
                },
            )
        )

    def cli(self):
        keys = (
            "mode",
            "root",
            "prepared",
            "source_report",
            "repository",
            "version",
            "commit",
            "run_id",
            "run_attempt",
            "output",
        )
        command.main(
            [
                "--" + key.replace("_", "-") + "=" + str(getattr(self.args, key))
                for key in keys
            ]
        )

    def produced(self):
        self.args.mode = "produce"
        self.args.output = self.args.root / "review"
        result = command.run_command(self.args)
        self.trusted.update(path.read_bytes() for path in self.args.output.iterdir())
        self.args.mode = "verify"
        return result

    def test_present_produce_verify_exact_attempt_without_caller_approval_or_requery(
        self,
    ):
        self.cli()
        self.auth_factory.assert_called_with(
            token="fixture-token", run_id=1234, run_attempt=2
        )
        self.http.request.assert_not_called()
        presentation = read_json(
            (self.args.output / "review-presentation.json").read_bytes()
        )
        self.assertFalse(presentation["distribution_authorized"])
        self.assertEqual(presentation["final_subjects"], dict(self.binding.subjects))
        summary = (self.args.output / "review.md").read_text()
        artifacts = presentation["workflow_artifacts"]
        self.assertIn(
            "https://github.com/endorses/psst.zip/actions/runs/1234/attempts/2#artifacts",
            summary,
        )
        for name in (
            "candidate-prepared-inputs-1234-2",
            "candidate-source-review-1234-2",
            "candidate-native-inputs-amd64-1234-2",
            "candidate-native-inputs-arm64-1234-2",
            "candidate-upgrade-recovery-gate-1234-2",
        ):
            self.assertIn(name, summary)
            self.assertIn(name, str(artifacts))
        self.assertIn("exact final OCI exports", summary)
        self.assertIn("named offered source artifacts", summary)
        self.assertIn(self.reviews[0]["comment"], summary)
        for image in self.source["details"]["images"].values():
            self.assertIn(image["notice_inventory_digest"], summary)
        for subject in self.binding.subjects:
            self.assertIn(subject[1], summary)
        self.args.mode = "produce"
        self.args.output = self.args.root / "review"
        approved = self.reviews
        self.reviews = []
        with patch("sys.stderr", new_callable=io.StringIO) as stderr, self.assertRaises(
            SystemExit
        ):
            self.cli()
        self.assertNotIn("fixture-token", stderr.getvalue())
        self.assertFalse(self.args.output.exists())
        self.reviews = approved
        expected = self.produced()
        calls = self.http.request.call_count
        self.http.request.side_effect = AssertionError(
            "Verification must not query review history"
        )
        self.assertEqual(command.run_command(self.args), expected)
        self.assertEqual(self.http.request.call_count, calls)
        self.args.mode = "produce"
        with self.assertRaisesRegex(InvalidRelease, "new directory"):
            command.run_command(self.args)

    def test_reject_unsigned_wrong_attempt_binding_evidence_and_output_mutation(self):
        report, evidence = self.produced()
        report_path = self.args.output / "distribution-review.json"
        evidence_path = self.args.output / "distribution-review-evidence.json"
        original_report, original_evidence = (
            report_path.read_bytes(),
            evidence_path.read_bytes(),
        )
        for change in (
            lambda e: e.update(run_attempt=1),
            lambda e: e.update(binding_digest="sha256:" + "0" * 64),
            lambda e: e["github_evidence"].update(reviews=[]),
        ):
            changed = copy.deepcopy(evidence)
            change(changed)
            raw = json_bytes(changed)
            evidence_path.write_bytes(raw)
            with self.assertRaises(InvalidRelease):
                command.run_command(self.args)
            self.trusted.add(raw)
            with self.assertRaises(InvalidRelease):
                command.run_command(self.args)
            evidence_path.write_bytes(original_evidence)
        for key in ("coverage_digest", "source_gate_report_digest"):
            changed = copy.deepcopy(report)
            target = (
                changed["details"]
                if key == "coverage_digest"
                else changed["details"]["review"]
            )
            target[key] = "sha256:" + "0" * 64
            raw = json_bytes(changed)
            report_path.write_bytes(raw)
            self.trusted.add(raw)
            with self.assertRaises(InvalidRelease):
                command.run_command(self.args)
            report_path.write_bytes(original_report)
        self.args.run_attempt = 1
        with self.assertRaises(InvalidRelease):
            command.run_command(self.args)
        self.args.run_attempt = 2
        record_path = self.args.prepared / "release-inputs.json"
        record_raw = record_path.read_bytes()
        record = read_json(record_raw)
        record["binding_sha256"] = "sha256:" + "0" * 64
        record_path.write_bytes(json_bytes(record))
        with self.assertRaises(InvalidRelease):
            command.run_command(self.args)
        record_path.write_bytes(record_raw)
        extra = self.args.output / "caller-approval.json"
        extra.write_bytes(b"{}")
        with self.assertRaisesRegex(InvalidRelease, "exactly two"):
            command.run_command(self.args)
        extra.unlink()
        original_authenticate = self.verifier.authenticate.side_effect

        def mutate(raw, binding):
            original_authenticate(raw, binding)
            if raw == original_evidence:
                report_path.write_bytes(original_report + b" ")

        self.verifier.authenticate.side_effect = mutate
        with self.assertRaisesRegex(InvalidRelease, "changed during authentication"):
            command.run_command(self.args)

    def test_surrounding_ascii_whitespace_preserves_original_review_evidence(self):
        original = "\r\n\t " + self.reviews[0]["comment"] + " \t\r\n\v\f"
        self.reviews[0]["comment"] = original
        report, evidence = self.produced()
        self.assertEqual(evidence["github_evidence"]["reviews"], self.reviews)
        self.assertEqual(evidence["github_evidence"]["reviews"][0]["comment"], original)
        self.assertEqual(
            report["details"]["review"]["record_digest"],
            command.sha256(command.json_bytes(evidence)),
        )
        self.assertEqual(command.run_command(self.args), (report, evidence))

    def test_whitespace_handling_rejects_internal_changes_and_malformed_comments(self):
        exact = self.reviews[0]["comment"]
        for invalid in (
            exact.replace("; run ", ";  run "),
            exact.replace("; attempt ", ";\n attempt "),
            exact.replace("attempt 2;", "attempt 1;"),
            exact.replace(self.binding.digest, "sha256:" + "0" * 64),
            exact.replace(
                command.sha256(self.args.source_report.read_bytes()),
                "sha256:" + "0" * 64,
            ),
            "\u00a0" + exact,
            None,
            42,
            [],
            {"comment": exact},
        ):
            self.reviews[0]["comment"] = invalid
            with self.subTest(comment=invalid), self.assertRaisesRegex(
                InvalidRelease, "Missing or ambiguous approval"
            ):
                command.distribution_review_report(
                    self.binding,
                    root=self.args.root,
                    source_report=self.args.source_report,
                    verifier=self.verifier,
                    run_id=self.args.run_id,
                    attempt=self.args.run_attempt,
                    token="fixture-token",
                    http=self.http,
                )

    def test_padded_duplicate_approvals_remain_ambiguous(self):
        duplicate = copy.deepcopy(self.reviews[0])
        duplicate["comment"] = "\r\n" + duplicate["comment"] + "\r\n"
        self.reviews.append(duplicate)
        with self.assertRaisesRegex(InvalidRelease, "Missing or ambiguous approval"):
            command.distribution_review_report(
                self.binding,
                root=self.args.root,
                source_report=self.args.source_report,
                verifier=self.verifier,
                run_id=self.args.run_id,
                attempt=self.args.run_attempt,
                token="fixture-token",
                http=self.http,
            )


if __name__ == "__main__":
    unittest.main()
