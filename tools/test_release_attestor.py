"""Offline signing boundary fixtures; no authentic attestations or remote writes."""

from __future__ import annotations

import base64
import copy
import io
import json
import os
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch

import github_release_attestor as signing
from publish_container_release import Binding, GATES, PublicationPlan, sha256
from release_artifacts import InvalidRelease, json_bytes


def provenance(name, digest, binding, context):
    return {
        "_type": "https://in-toto.io/Statement/v1",
        "subject": [
            {"name": name, "digest": {"sha256": digest.removeprefix("sha256:")}}
        ],
        "predicateType": signing.PREDICATE,
        "predicate": {
            "buildDefinition": {
                "buildType": "https://actions.github.io/buildtypes/workflow/v1",
                "externalParameters": {
                    "workflow": {
                        "repository": "https://github.com/" + binding.repository,
                        "ref": "refs/tags/" + binding.version,
                        "path": ".github/workflows/release.yml",
                    }
                },
                "resolvedDependencies": [
                    {
                        "uri": "git+https://github.com/"
                        + binding.repository
                        + "@refs/tags/"
                        + binding.version,
                        "digest": {"gitCommit": binding.commit},
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
                "builder": {
                    "id": "https://github.com/" + context["GITHUB_WORKFLOW_REF"]
                },
                "metadata": {
                    "invocationId": "https://github.com/"
                    + binding.repository
                    + "/actions/runs/123/attempts/1"
                },
            },
        },
    }


def bundle(statement):
    return json_bytes(
        {
            "dsseEnvelope": {
                "payloadType": "application/vnd.in-toto+json",
                "payload": base64.b64encode(json_bytes(statement)).decode(),
            }
        }
    )


class AttestorFixtures(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="psst-attestor-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.files = {}
        assets, subjects = {}, {}
        for name in (
            "release-manifest.json",
            "deploy.tar.gz",
            "runtime-amd64.tar.gz",
            "runtime-arm64.tar.gz",
            "project.tar.gz",
            "dependencies-amd64.tar.gz",
            "dependencies-arm64.tar.gz",
            "psst.zip-upstream-inputs-v0.1.0.tar.gz",
        ):
            path = self.root / name
            path.write_bytes(("fixture-only:" + name).encode())
            self.files[name] = path
            assets[name] = sha256(path.read_bytes())
            subjects["file:" + name] = "file:" + name + "@" + assets[name]
        for component in ("backend", "web"):
            for target in ("index", "amd64", "arm64"):
                subjects[component + "-" + target] = (
                    "oci://ghcr.io/example/psst-zip-"
                    + component
                    + "@"
                    + sha256((component + target).encode())
                )
        self.binding = Binding(
            "example/psst.zip", "v0.1.0", "a" * 40, tuple(sorted(subjects.items()))
        )
        manifest = {
            "images": {
                component: {
                    "index": subjects[component + "-index"].removeprefix("oci://"),
                    "platform_digests": {
                        "linux/"
                        + architecture: subjects[component + "-" + architecture].split(
                            "@"
                        )[1]
                        for architecture in ("amd64", "arm64")
                    },
                }
                for component in ("backend", "web")
            }
        }
        self.plan = PublicationPlan(
            self.binding,
            manifest,
            sha256(json_bytes(manifest)),
            (),
            tuple(sorted(assets.items())),
            tuple((gate, sha256(gate.encode())) for gate in sorted(GATES)),
        )
        self.event = self.root / "event.json"
        self.event.write_bytes(
            json_bytes(
                {
                    "repository": {
                        "full_name": self.binding.repository,
                        "visibility": "public",
                    },
                    "after": self.binding.commit,
                    "ref": "refs/tags/v0.1.0",
                }
            )
        )
        self.environment = {
            "GITHUB_ACTIONS": "true",
            "RUNNER_ENVIRONMENT": "github-hosted",
            "GITHUB_REPOSITORY": self.binding.repository,
            "GITHUB_JOB": "publish",
            "GITHUB_REF": "refs/tags/v0.1.0",
            "GITHUB_SHA": self.binding.commit,
            "GITHUB_EVENT_NAME": "push",
            "GITHUB_WORKFLOW_REF": self.binding.repository
            + "/.github/workflows/release.yml@refs/tags/v0.1.0",
            "GITHUB_WORKFLOW_SHA": self.binding.commit,
            "GITHUB_RUN_ID": "123",
            "GITHUB_RUN_ATTEMPT": "1",
            "GITHUB_SERVER_URL": "https://github.com",
            "GITHUB_API_URL": "https://api.github.com",
            "GITHUB_EVENT_PATH": str(self.event),
            "ACTIONS_ID_TOKEN_REQUEST_URL": "https://run-actions-fixture.actions.githubusercontent.com/oidc",
            "ACTIONS_ID_TOKEN_REQUEST_TOKEN": "fixture-oidc-token",
        }
        self.output = self.root / "readbacks"
        self.verifier = SimpleNamespace(
            gh=Path("/usr/bin/gh"),
            token="fixture-verifier-token",
            authenticate=Mock(),
            verify=Mock(),
        )
        self.action_bytes = {
            "action.yml": b"runs: {using: node24, main: dist/index.js}\n",
            "package.json": b'{"type":"module"}\n',
            "dist/index.js": b"// harmless offline fixture, never executed\n",
        }
        fixture_pins = {
            name: (len(content), sha256(content).removeprefix("sha256:"))
            for name, content in self.action_bytes.items()
        }
        self.node = self.root / "fixture-node24"
        self.node.write_bytes(b"offline-runtime-fixture-never-executed")
        for target, value in (
            ("ACTION_FILES", fixture_pins),
            ("official_bytes", lambda name: self.action_bytes[name]),
            ("hosted_node", lambda: (self.node, "v24.9.0")),
            ("platform.system", lambda: "Linux"),
            ("platform.machine", lambda: "x86_64"),
        ):
            mocked = (
                patch.object(signing, target, value)
                if "." not in target
                else patch("github_release_attestor." + target, value)
            )
            mocked.start()
            self.addCleanup(mocked.stop)
        self.action_calls, self.verify_calls = [], []

    def create(self):
        return signing.WorkflowAttestor(
            self.binding,
            token="fixture-sign-token",
            environment=self.environment,
            verifier=self.verifier,
            private_output=self.output,
        )

    def action(self, node, action, environment):
        self.action_calls.append(dict(environment))
        path = environment["INPUT_SUBJECT-PATH"]
        digest = (
            sha256(Path(path).read_bytes())
            if path
            else environment["INPUT_SUBJECT-DIGEST"]
        )
        target = Path(environment["RUNNER_TEMP"]) / "bundle.json"
        target.write_bytes(
            bundle(
                provenance(
                    environment["INPUT_SUBJECT-NAME"],
                    digest,
                    self.binding,
                    self.environment,
                )
            )
        )
        Path(environment["GITHUB_OUTPUT"]).write_text(
            "bundle-path<<fixture\n" + str(target) + "\nfixture\n"
        )

    def verification(self, arguments, environment):
        self.verify_calls.append((arguments, environment))
        subject = arguments[3]
        digest = (
            subject.split("@")[1]
            if subject.startswith("oci://")
            else sha256(Path(subject).read_bytes())
        )
        # Explicit verifier-boundary fixture, never an authentic certificate.
        return json_bytes(
            [
                {
                    "verificationResult": {
                        "signature": {"certificate": {"offline_fixture": True}},
                        "verifiedTimestamps": [{"offline_fixture": True}],
                        "statement": {
                            "predicateType": signing.PREDICATE,
                            "subject": [
                                {"digest": {"sha256": digest.removeprefix("sha256:")}}
                            ],
                        },
                    }
                }
            ]
        )

    def test_all_fourteen_subjects_and_gate_are_signed_and_verified(self):
        signer = self.create()
        with patch.object(signing, "run_action", self.action), patch.object(
            signing, "bounded_verify", self.verification
        ):
            report = signer.attest_subjects(self.plan, self.files, self.output)
        record = json.loads(report.read_bytes())
        self.assertEqual(record["details"]["subjects"], dict(self.binding.subjects))
        self.assertEqual(len(self.action_calls), 15)
        self.assertEqual(len(self.verify_calls), 30)
        self.assertEqual(sum("--bundle" in args for args, _ in self.verify_calls), 15)
        self.assertEqual(
            sum(args[3].startswith("oci://") for args, _ in self.verify_calls), 12
        )
        self.assertTrue(
            all(
                "psst-zip-" in args[3] and "psst.zip-" not in args[3]
                for args, _ in self.verify_calls
                if args[3].startswith("oci://")
            )
        )
        for args, env in self.verify_calls:
            self.assertNotIn("--bundle-from-oci", args)
            self.assertIn("--deny-self-hosted-runners", args)
            self.assertEqual(
                args[args.index("--source-digest") + 1], self.binding.commit
            )
            self.assertNotIn("DOCKER_CONFIG", env)
            if args[3].startswith("oci://"):
                self.assertIn("verify-image-", env["HOME"])
        self.assertEqual(report.stat().st_mode & 0o777, 0o400)
        self.verifier.authenticate.assert_called_once_with(
            report.read_bytes(), self.binding
        )
        self.verifier.verify.assert_called_once_with("provenance", report, self.binding)
        self.assertFalse(list(self.output.glob("sign-*")))
        self.assertFalse(list(self.output.glob("subject-*")))

    def test_ambient_inputs_credentials_and_runtime_overrides_are_removed(self):
        self.environment.update(
            {
                "INPUT_PREDICATE": "evil",
                "INPUT_SUBJECT-DIGEST": "evil",
                "NODE_OPTIONS": "--require evil",
                "HTTP_PROXY": "http://evil",
                "SIGSTORE_ID_TOKEN": "evil",
                "ACTIONS_RUNTIME_TOKEN": "unneeded",
                "GH_CONFIG_DIR": "/evil",
                "GITHUB_OUTPUT": "/evil",
            }
        )
        signer = self.create()
        with patch.object(signing, "run_action", self.action), patch.object(
            signing, "bounded_verify", self.verification
        ):
            signer._attest("file.tar.gz", sha256(b"fixture"))
        env = self.action_calls[0]
        for key in (
            "INPUT_PREDICATE",
            "NODE_OPTIONS",
            "HTTP_PROXY",
            "SIGSTORE_ID_TOKEN",
            "ACTIONS_RUNTIME_TOKEN",
            "GH_CONFIG_DIR",
        ):
            self.assertNotIn(key, env)
        self.assertEqual(env["INPUT_GITHUB-TOKEN"], "fixture-sign-token")
        self.assertEqual(env["INPUT_SHOW-SUMMARY"], "false")
        self.assertTrue(Path(env["GITHUB_OUTPUT"]).is_relative_to(self.output))

    def test_non_hosted_wrong_job_and_source_context_fail_before_fetch(self):
        for key, value in (
            ("GITHUB_ACTIONS", "false"),
            ("RUNNER_ENVIRONMENT", "self-hosted"),
            ("GITHUB_JOB", "test"),
            ("GITHUB_SHA", "b" * 40),
            ("GITHUB_WORKFLOW_SHA", "b" * 40),
            ("GITHUB_REF", "refs/heads/main"),
            ("GITHUB_RUN_ATTEMPT", "0"),
        ):
            with self.subTest(key=key), patch.dict(
                self.environment, {key: value}
            ), patch.object(signing, "checked_action") as acquisition:
                with self.assertRaises(InvalidRelease):
                    self.create()
                acquisition.assert_not_called()

    def test_oidc_endpoint_token_and_event_are_checked(self):
        for key, value in (
            ("ACTIONS_ID_TOKEN_REQUEST_URL", "https://evil.invalid/oidc"),
            (
                "ACTIONS_ID_TOKEN_REQUEST_URL",
                "https://u:p@run-actions-fixture.actions.githubusercontent.com/oidc",
            ),
            ("ACTIONS_ID_TOKEN_REQUEST_TOKEN", ""),
            ("GITHUB_SERVER_URL", "https://evil.invalid"),
        ):
            with self.subTest(key=key), patch.dict(self.environment, {key: value}):
                with self.assertRaises(InvalidRelease):
                    self.create()
        for mutation in (
            {
                "repository": {
                    "full_name": self.binding.repository,
                    "visibility": "private",
                }
            },
            {"after": "b" * 40},
            {"ref": "refs/heads/main"},
        ):
            record = json.loads(self.event.read_bytes())
            original = self.event.read_bytes()
            record.update(mutation)
            self.event.write_bytes(json_bytes(record))
            with self.assertRaises(InvalidRelease):
                self.create()
            self.event.write_bytes(original)

    def test_action_source_hash_substitution_is_rejected(self):
        signer = self.create()
        signer.action.chmod(0o600)
        signer.action.write_bytes(b"// substituted different fixture bytes\n")
        with patch.object(signing, "run_action") as execution:
            with self.assertRaises(InvalidRelease):
                signer._attest("fixture", sha256(b"fixture"))
            execution.assert_not_called()

    def test_hosted_runtime_substitution_is_rejected_before_action_execution(self):
        signer = self.create()
        self.node.write_bytes(b"changed fixture runtime")
        with patch.object(signing, "run_action") as execution:
            with self.assertRaisesRegex(InvalidRelease, "runtime changed"):
                signer._attest("fixture", sha256(b"fixture"))
            execution.assert_not_called()

    def test_file_substitution_before_and_during_signing_is_rejected(self):
        signer = self.create()
        path = self.files["project.tar.gz"]
        expected = sha256(path.read_bytes())
        with self.assertRaises(InvalidRelease):
            signer._file(path.name, path, sha256(b"wrong"))

        def mutate(*args):
            path.write_bytes(b"changed")
            return sha256(b"fixture-bundle")

        with patch.object(signer, "_attest", mutate), patch.object(
            signing, "bounded_verify", self.verification
        ):
            with self.assertRaises(InvalidRelease):
                signer._file(path.name, path, expected)

    def test_verification_failure_cannot_produce_a_provenance_gate(self):
        signer = self.create()
        with patch.object(signing, "run_action", self.action), patch.object(
            signing,
            "bounded_verify",
            side_effect=InvalidRelease("offline verification failure"),
        ):
            with self.assertRaises(InvalidRelease):
                signer.attest_subjects(self.plan, self.files, self.output)
        self.assertFalse((self.output / "provenance.json").exists())

    def test_report_binding_status_and_schema_are_checked_before_signing(self):
        signer = self.create()
        report = self.root / "gate.json"
        valid = {
            "schema_version": 1,
            "gate": "registry-readback",
            "binding_digest": self.binding.digest,
            "passed": True,
            "details": {},
        }
        for change in (
            {"passed": False},
            {"binding_digest": sha256(b"other")},
            {"schema_version": True},
            {"gate": "invented"},
            {"extra": True},
        ):
            report.write_bytes(json_bytes(valid | change))
            with patch.object(signer, "_file") as execution:
                with self.assertRaises(InvalidRelease):
                    signer.sign_report(self.binding, report)
                execution.assert_not_called()

    def test_missing_or_mismatched_release_asset_is_rejected_before_any_signing(self):
        signer = self.create()
        with patch.object(signer, "_attest") as execution:
            with self.assertRaises(InvalidRelease):
                signer.attest_subjects(self.plan, {}, self.output)
            self.files["project.tar.gz"].write_bytes(b"other")
            with self.assertRaises(InvalidRelease):
                signer.attest_subjects(self.plan, self.files, self.output)
            execution.assert_not_called()

    def test_bundle_output_cannot_escape_or_link_or_claim_incomplete_success(self):
        output = self.root / "output"
        outside = self.root / "bundle.json"
        outside.write_bytes(b"fixture")
        temp = self.root / "private"
        temp.mkdir()
        linked = temp / "linked.json"
        linked.symlink_to(outside)
        for text in (
            "",
            "bundle-path<<d\n" + str(outside) + "\nd\n",
            "bundle-path<<d\n" + str(linked) + "\nd\n",
            "bundle-path<<d\n" + str(temp / "missing") + "\nd\n",
            "bundle-path<<d\nx\nwrong\n",
        ):
            output.write_text(text)
            with self.assertRaises(InvalidRelease):
                signing.bundle_output(output, temp)

    def test_bundle_structural_subject_source_workflow_and_invocation_checks(self):
        original = provenance(
            "fixture", sha256(b"fixture"), self.binding, self.environment
        )
        signing.check_bundle(
            bundle(original),
            "fixture",
            sha256(b"fixture"),
            self.binding,
            self.environment,
        )
        mutations = [
            lambda s: s["subject"][0]["digest"].update(sha256="b" * 64),
            lambda s: s["predicate"]["buildDefinition"]["externalParameters"][
                "workflow"
            ].update(ref="refs/heads/main"),
            lambda s: s["predicate"]["buildDefinition"]["resolvedDependencies"][0][
                "digest"
            ].update(gitCommit="b" * 40),
            lambda s: s["predicate"]["runDetails"]["metadata"].update(
                invocationId="other-run"
            ),
            lambda s: s["predicate"]["buildDefinition"]["internalParameters"][
                "github"
            ].update(runner_environment="self-hosted"),
        ]
        for mutation in mutations:
            statement = copy.deepcopy(original)
            mutation(statement)
            with self.assertRaises(InvalidRelease):
                signing.check_bundle(
                    bundle(statement),
                    "fixture",
                    sha256(b"fixture"),
                    self.binding,
                    self.environment,
                )


class ActionSubprocessFixtures(unittest.TestCase):
    def test_official_source_download_requires_pinned_hash_and_success_status(self):
        name = "action.yml"
        raw = b"bounded public fixture source"
        pins = {name: (len(raw), sha256(raw).removeprefix("sha256:"))}
        connection = Mock()
        response = connection.getresponse.return_value
        response.status, response.read.return_value = 200, raw
        with patch.object(signing, "ACTION_FILES", pins), patch.object(
            signing.http.client, "HTTPSConnection", return_value=connection
        ) as constructor:
            self.assertEqual(signing.official_bytes(name), raw)
            constructor.assert_called_with("raw.githubusercontent.com", timeout=30)
            connection.request.assert_called_with(
                "GET", "/actions/attest/" + signing.ACTION_COMMIT + "/action.yml"
            )
            for status, body in ((302, raw), (200, b"wrong"), (200, raw + b"overflow")):
                response.status, response.read.return_value = status, body
                with self.assertRaises(InvalidRelease):
                    signing.official_bytes(name)

    def test_native_node24_version_architecture_and_missing_runtime(self):
        with tempfile.TemporaryDirectory(prefix="psst-node-runtime-test-") as temporary:
            node = Path(temporary) / "fixture-node"
            node.write_bytes(b"runtime fixture not executed")
            node.chmod(0o700)
            result = SimpleNamespace(
                returncode=0, stdout=b'["v24.9.0","linux","x64"]\n'
            )
            with patch.object(Path, "glob", return_value=[node]), patch.object(
                signing.platform, "machine", return_value="x86_64"
            ), patch.object(
                signing.subprocess, "run", return_value=result
            ) as execution:
                self.assertEqual(signing.hosted_node(), (node, "v24.9.0"))
                self.assertEqual(
                    execution.call_args.kwargs["env"], {"PATH": os.defpath, "LANG": "C"}
                )
                for facts in (
                    b'["v22.9.0","linux","x64"]',
                    b'["v24.9.0","linux","arm64"]',
                    b'["v24.9.0","darwin","x64"]',
                    b"{}",
                ):
                    result.stdout = facts
                    with self.assertRaises(InvalidRelease):
                        signing.hosted_node()
            with patch.object(Path, "glob", return_value=[]):
                with self.assertRaisesRegex(InvalidRelease, "unavailable"):
                    signing.hosted_node()

    def test_action_commands_and_secret_diagnostics_are_withheld_and_failures_fail(
        self,
    ):
        with tempfile.TemporaryDirectory(
            prefix="psst-action-process-test-"
        ) as temporary:
            node = Path(temporary) / "fixture-node"
            node.write_text(
                "#!/bin/sh\nprintf '::add-mask::fixture-secret\\n'\nprintf 'fixture-private-diagnostic\\n' >&2\nexit 1\n"
            )
            node.chmod(0o700)
            logged = io.StringIO()
            with redirect_stdout(logged), self.assertRaises(InvalidRelease) as caught:
                signing.run_action(
                    node, Path(temporary) / "dist/index.js", {"PATH": os.defpath}
                )
            self.assertEqual(logged.getvalue(), "")
            self.assertNotIn("fixture-secret", str(caught.exception))
            self.assertNotIn("fixture-private-diagnostic", str(caught.exception))

    def test_timeout_kills_and_reaps_process_group(self):
        with tempfile.TemporaryDirectory(
            prefix="psst-action-timeout-test-"
        ) as temporary:
            node = Path(temporary) / "fixture-node"
            node.write_text("#!/bin/sh\nsleep 20\n")
            node.chmod(0o700)
            with patch.object(signing, "ACTION_TIMEOUT", 0.05), self.assertRaisesRegex(
                InvalidRelease, "timed out"
            ):
                signing.run_action(
                    node, Path(temporary) / "dist/index.js", {"PATH": os.defpath}
                )


if __name__ == "__main__":
    unittest.main()
