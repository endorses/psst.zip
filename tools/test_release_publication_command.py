"""Actual orchestration/transport fixtures; these never attest or publish live."""

import copy
import io
import tarfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest.mock import patch

import github_release_transport as transport
import publication_diagnostics as diagnostics
import publish_container_release as publication
import publish_verified_release as driver
import test_release_transport as transport_fixtures
from generate_release_gate_reports import CHECKS
from release_artifacts import InvalidRelease, read_json


class FixtureVerifier:
    def __init__(self, upstream):
        self.upstream, self.signed = upstream, set()

    def verify(self, gate, report, binding):
        value = read_json(report.read_bytes())
        if isinstance(value, dict) and value.get("kind") == "container-release-result":
            from release_result import checked_result

            result = checked_result(value, binding, (77, 1))
            return publication.VerifiedEvidence(
                gate,
                binding.digest,
                publication.sha256(report.read_bytes()),
                True,
                result["checks"][gate],
            )
        if gate in publication.GATES:
            return self.upstream.verify(gate, report, binding)
        raw = report.read_bytes()
        if raw not in self.signed:
            raise InvalidRelease("unsigned fixture readback")
        record = read_json(raw)
        if (
            record["gate"] != gate
            or record["binding_digest"] != binding.digest
            or record["passed"] is not True
        ):
            raise InvalidRelease("fixture readback binding differs")
        return publication.VerifiedEvidence(
            gate, binding.digest, publication.sha256(raw), True, record["details"]
        )


class PublicationCommand(unittest.TestCase):
    def setUp(self):
        self.real = transport_fixtures.TransportChecks()
        self.real.setUp()
        self.addCleanup(self.real.doCleanups)
        self.fixture, self.api, self.commands, self.state = (
            self.real.fixture,
            self.real.api,
            self.real.commands,
            self.real.state,
        )
        self.verifier = FixtureVerifier(self.fixture.verifier)
        self.provenance_failure = False
        self.provenance_calls = 0
        self.adapter = None
        native = {}
        for platform in ("linux/amd64", "linux/arm64"):
            arch = platform.split("/")[1]
            configs = {
                component: self.real.tested_configs[component + "-" + arch]
                for component in ("backend", "web")
            }
            native[platform] = {
                "smoke": {
                    "schema_version": 1,
                    "kind": "release-image-smoke",
                    "publication_authorized": False,
                    "version": "v1.2.3",
                    "revision": self.fixture.commit,
                    "platform": platform,
                    "execution": "native",
                    "runtime_pack_sha256": "sha256:" + "b" * 64,
                    "checks": sorted(CHECKS),
                    "tested_configs": configs,
                    "completed_at": "2026-10-07T00:00:00Z",
                },
                "runtime": {"runtime_pack_sha256": "sha256:" + "b" * 64},
                "images": {
                    component: {
                        "platform": platform,
                        "config_digest": configs[component],
                        "manifest_digest": self.fixture.manifest["images"][component][
                            "platform_digests"
                        ][platform],
                    }
                    for component in ("backend", "web")
                },
            }
        self.fixture.verifier.details["final-image-smoke"] = {
            "execution": {p: "native" for p in native},
            "tested_configs": self.real.tested_configs,
            "native_measurements": native,
        }
        self.environment = {
            "GITHUB_ACTIONS": "true",
            "RUNNER_ENVIRONMENT": "github-hosted",
            "GITHUB_REPOSITORY": "endorses/psst.zip",
            "GITHUB_JOB": "publish",
            "GITHUB_REF": "refs/tags/v1.2.3",
            "GITHUB_SHA": self.fixture.commit,
            "GITHUB_EVENT_NAME": "push",
            "GITHUB_WORKFLOW_REF": "endorses/psst.zip/.github/workflows/release.yml@refs/tags/v1.2.3",
            "GITHUB_WORKFLOW_SHA": self.fixture.commit,
            "GITHUB_RUN_ID": "77",
            "GITHUB_RUN_ATTEMPT": "1",
            "GITHUB_ACTOR": "endorses",
            "GH_TOKEN": "fixture-workflow-token",
            "PSST_IMMUTABLE_INSPECTION_TOKEN": "fixture-admin-read-token",
        }

    def factory(self, plan, state, context, **credentials):
        self.adapter = transport.GitHubReleaseTransport(
            plan, state, context, http=self.api, execute=self.commands, **credentials
        )
        return self.adapter

    def publish(self, **changes):
        args = {
            "root": self.fixture.root,
            "repository": "endorses/psst.zip",
            "ref": "refs/tags/v1.2.3",
            "event_sha": self.fixture.commit,
            "reviewed_commit": self.fixture.commit,
            "manifest": self.fixture.manifest_path,
            "bundle": self.fixture.bundle,
            "indexes": self.fixture.indexes,
            "archives": self.real.archives,
            "source_assets": {self.fixture.source.name: self.fixture.source},
            "reports": {gate: self.fixture.reports[gate] for gate in publication.GATES},
            "state": self.state,
            "environment": self.environment,
            "verifier": self.verifier,
            "provenance_verifier": self.provenance,
            "transport_factory": self.factory,
        }
        return driver.publish(**(args | changes))

    def provenance(self, plan, files, verifier):
        self.provenance_calls += 1
        self.assertTrue(self.adapter.held)
        self.assertEqual(set(files), set(dict(plan.assets)))
        if self.provenance_failure:
            raise InvalidRelease("fixture final provenance verification failed")
        return {"subjects": dict(plan.binding.subjects)}

    def test_one_canonical_result_runs_the_same_real_transport_lifecycle(self):
        plan = self.fixture.prepare()
        checks = {
            gate: self.verifier.upstream.verify(
                gate, self.fixture.reports[gate], plan.binding
            ).details
            for gate in publication.GATES
        }
        checks["source-ci"].update(run_id=77, run_attempt=1)
        result = {
            "schema_version": 1,
            "kind": "container-release-result",
            "repository": plan.binding.repository,
            "version": plan.binding.version,
            "commit": plan.binding.commit,
            "binding_digest": plan.binding.digest,
            "subjects": dict(plan.binding.subjects),
            "run_id": 77,
            "attempt": 1,
            "checks": checks,
            "inputs": {
                "corresponding-source-replay": {"sha256": "sha256:" + "e" * 64},
                "source-result": {"sha256": "sha256:" + "f" * 64},
            },
            "platforms": {
                p: {
                    "execution": "native",
                    "checks": ["final-image-smoke", "upgrade-recovery"],
                }
                for p in ("linux/amd64", "linux/arm64")
            },
        }
        path = self.fixture.folder / "release-result.json"
        path.write_bytes(driver.json_bytes(result))
        retained = []
        original_snapshot = driver.snapshot

        def capture(source, destination, maximum=2 * 1024**3):
            if source == path:
                retained.append(destination)
            return original_snapshot(source, destination, maximum)

        with patch.object(driver, "snapshot", side_effect=capture):
            receipt = self.publish(reports={gate: path for gate in publication.GATES})
        self.assertTrue(receipt["immutable"])
        self.assertTrue(self.adapter.public_verified)
        self.assertEqual(len(retained), 1, "canonical result must be snapshotted once")

    def test_standard_action_checksum_input_covers_all_authenticated_final_subjects(
        self,
    ):
        output = self.fixture.folder / "final-subject-checksums.txt"
        inputs = {
            "manifest": str(self.fixture.manifest_path),
            "bundle": str(self.fixture.bundle),
            "indexes": {name: str(path) for name, path in self.fixture.indexes.items()},
            "source_assets": {self.fixture.source.name: str(self.fixture.source)},
            "reports": {
                name: str(self.fixture.reports[name]) for name in publication.GATES
            },
        }
        with patch.object(driver, "GhEvidenceVerifier", return_value=self.verifier):
            driver.prepare_attestation(
                root=self.fixture.root,
                inputs=inputs,
                environment=self.environment,
                reviewed_commit=self.fixture.commit,
                output=output,
            )
        plan = self.fixture.prepare()
        expected = {
            digest.removeprefix("sha256:")
            + "  "
            + name.removeprefix("file:").removeprefix("oci://")
            for _, subject in plan.binding.subjects
            for name, digest in (subject.split("@"),)
        }
        self.assertEqual(set(output.read_text().splitlines()), expected)
        self.assertFalse(self.api.calls)
        self.assertFalse(self.state.exists())
        with (
            patch.object(driver, "GhEvidenceVerifier", return_value=self.verifier),
            self.assertRaises(FileExistsError),
        ):
            driver.prepare_attestation(
                root=self.fixture.root,
                inputs=inputs,
                environment=self.environment,
                reviewed_commit=self.fixture.commit,
                output=output,
            )

    def events(self):
        return self.real.events()

    def reject_expired_scanner_recheck(self, boundary):
        original = driver.verify_gates
        checks = 0

        def verify(reports, gates, binding, verifier):
            nonlocal checks
            if set(gates) == {"final-image-scanners"}:
                checks += 1
                if checks == boundary:
                    raise InvalidRelease("Temporary Caddy acceptance has expired")
            return original(reports, gates, binding, verifier)

        with (
            patch.object(driver, "verify_gates", side_effect=verify),
            self.assertRaisesRegex(InvalidRelease, "acceptance has expired"),
        ):
            self.publish()
        self.assertEqual(checks, boundary)

    def test_expiry_after_preflight_prevents_first_remote_mutation(self):
        self.reject_expired_scanner_recheck(1)
        self.assertFalse((self.state / "v1.2.3.jsonl").exists())
        self.assertTrue(all(method == "GET" for method, _, _, _ in self.api.calls))

    def test_expiry_during_readbacks_prevents_version_tags_and_preserves_transaction(
        self,
    ):
        self.reject_expired_scanner_recheck(2)
        intents = [
            row["operation"] for row in self.events() if row["phase"] == "intent"
        ]
        self.assertIn("upload-release-manifest.json", intents)
        self.assertNotIn("tag-backend", intents)
        self.assertNotIn("publish-release", intents)
        self.assertTrue(list(self.state.glob("publication-inputs-*")))

    def test_expiry_after_version_tags_prevents_immutable_publication(self):
        self.reject_expired_scanner_recheck(3)
        intents = [
            row["operation"] for row in self.events() if row["phase"] == "intent"
        ]
        self.assertIn("tag-backend", intents)
        self.assertNotIn("publish-release", intents)
        self.assertTrue(list(self.state.glob("publication-inputs-*")))

    def diagnostics(self, outcome):
        return diagnostics.project(
            self.state,
            repository=self.environment["GITHUB_REPOSITORY"],
            version="v1.2.3",
            commit=self.fixture.commit,
            run_id=self.environment["GITHUB_RUN_ID"],
            attempt=self.environment["GITHUB_RUN_ATTEMPT"],
            outcome=outcome,
        )

    def test_complete_real_transport_lifecycle_has_actual_receipt_and_ordered_signing(
        self,
    ):
        result = self.publish()
        self.assertEqual(result["kind"], "container-publication-receipt")
        self.assertEqual(result["release_id"], 17)
        self.assertTrue(result["immutable"])
        self.assertTrue(self.adapter.public_verified)
        self.assertEqual(self.provenance_calls, 1)
        self.assertEqual(
            result["journal_sha256"],
            publication.source_digest(self.state / "v1.2.3.jsonl"),
        )
        intents = [
            row["operation"] for row in self.events() if row["phase"] == "intent"
        ]
        self.assertLess(
            intents.index("push-web-index"),
            intents.index("upload-" + self.fixture.source.name),
        )
        self.assertLess(
            intents.index("upload-release-manifest.json"), intents.index("tag-backend")
        )
        self.assertEqual(intents[-1], "publish-release")
        progress = self.diagnostics("success")
        self.assertEqual(progress["records"][-1]["operation"], "public-readback")
        self.assertEqual(progress["journal_sha256"], result["journal_sha256"])
        self.assertFalse(self.adapter.held)
        self.assertFalse(list(self.state.glob("publication-inputs-*")))
        self.assertNotIn("fixture-workflow-token", str(result) + str(self.events()))

    def test_missing_gate_wrong_workflow_and_mutated_oci_fail_before_remote_calls(self):
        reports = {
            k: v
            for k, v in self.fixture.reports.items()
            if k in publication.GATES and k != "source-ci"
        }
        with self.assertRaises(InvalidRelease):
            self.publish(reports=reports)
        env = copy.deepcopy(self.environment)
        env["GITHUB_JOB"] = "candidate"
        with self.assertRaises(InvalidRelease):
            self.publish(environment=env)
        self.real.archives["backend-arm64"].write_bytes(b"changed archive")
        with self.assertRaises((InvalidRelease, tarfile.TarError)):
            self.publish()
        self.assertEqual(self.api.calls, [])
        self.assertEqual(self.api.registry, {})

    def test_default_verifier_requires_the_current_run_attempt_before_snapshotting(
        self,
    ):
        # An otherwise valid report from an earlier attempt must not authorize
        # publication. The cryptographic matcher is exercised in evidence tests;
        # here verify the real command supplies that matcher its exact context.
        for field in ("GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT"):
            for value in (None, "0", "01", "not-a-number"):
                with self.subTest(field=field, value=value):
                    env = self.environment | {field: value}
                    with patch.object(driver, "GhEvidenceVerifier") as constructor:
                        with self.assertRaisesRegex(InvalidRelease, "Invalid workflow"):
                            self.publish(environment=env, verifier=None)
                    constructor.assert_not_called()
                    self.assertFalse(list(self.state.glob("publication-inputs-*")))
                    self.assertEqual(self.api.calls, [])
        for value in (True, None, "TRUE", "yes"):
            with self.subTest(initialization=value):
                with self.assertRaisesRegex(InvalidRelease, "initialization setting"):
                    self.publish(
                        environment=self.environment
                        | {"PSST_INITIALIZE_GHCR_PACKAGES": value}
                    )
                self.assertFalse(list(self.state.glob("publication-inputs-*")))
                self.assertEqual(self.api.calls, [])
        with patch.object(
            driver, "GhEvidenceVerifier", return_value=self.verifier
        ) as constructor:
            result = self.publish(verifier=None)
        constructor.assert_called_once_with(
            token="fixture-workflow-token", run_id=77, run_attempt=1
        )
        self.assertTrue(result["immutable"])

    def test_retained_snapshot_directories_are_durable_before_first_mutation(self):
        synced, mutations = set(), []
        original_sync, original_request = driver.sync_directory, self.api.request

        def sync(path):
            original_sync(path)
            synced.add(path)

        def request(method, url, **kwargs):
            if method in {"POST", "PUT", "PATCH", "DELETE"} and not mutations:
                retained = list(self.state.glob("publication-inputs-*"))
                self.assertEqual(len(retained), 1)
                private = retained[0]
                expected = {private, self.state} | {
                    private / name
                    for name in ("assets", "indexes", "archives", "gates", "readbacks")
                }
                self.assertTrue(expected <= synced, expected - synced)
                self.assertTrue((private / "snapshot-binding.json").is_file())
                self.assertEqual(self.events()[-1]["phase"], "intent")
                self.assertEqual(self.events()[-1]["operation"], "reserve-draft")
                mutations.append((method, url))
            return original_request(method, url, **kwargs)

        with (
            patch.object(driver, "sync_directory", side_effect=sync),
            patch.object(self.api, "request", side_effect=request),
        ):
            self.publish()
        self.assertEqual(len(mutations), 1)

    def test_authenticated_smoke_must_bind_all_four_native_configs(self):
        self.fixture.verifier.details["final-image-smoke"]["tested_configs"][
            "backend-arm64"
        ] = "sha256:" + "0" * 64
        with self.assertRaisesRegex(InvalidRelease, "config/child"):
            self.publish()
        self.assertEqual(self.api.calls, [])

    def test_partial_push_or_attestation_failure_leaves_durable_uncertain_journal_no_retry(
        self,
    ):
        self.commands.fail_copy = True
        with self.assertRaises(InvalidRelease):
            self.publish()
        self.assertEqual(len(self.api.releases), 1)
        self.assertTrue(self.api.releases[0]["draft"])
        self.assertTrue(any(row["phase"] == "uncertain" for row in self.events()))
        progress = self.diagnostics("failure")
        self.assertEqual(progress["records"][-1]["phase"], "stopped")
        self.assertTrue(list(self.state.glob("publication-inputs-*")))
        self.commands.fail_copy = False
        before = len(self.api.calls)
        with self.assertRaisesRegex(InvalidRelease, "journal exists"):
            self.publish()
        self.assertFalse(
            any(
                method in {"POST", "PUT", "PATCH"}
                for method, _, _, _ in self.api.calls[before:]
            )
        )

    def test_subject_signing_failure_does_not_upload_or_create_version_tags(self):
        self.provenance_failure = True
        with self.assertRaises(InvalidRelease):
            self.publish()
        self.assertEqual(len(self.api.registry), 6)
        self.assertEqual(self.api.assets, {})
        self.assertTrue(self.api.releases[0]["draft"])
        self.assertTrue(any(row["phase"] == "stopped" for row in self.events()))
        retained = list(self.state.glob("publication-inputs-*"))
        self.assertEqual(len(retained), 1)
        self.assertTrue((retained[0] / "snapshot-binding.json").is_file())

    def test_immutable_policy_failure_never_creates_draft(self):
        self.api.immutable_status = 403
        with self.assertRaises(InvalidRelease):
            self.publish()
        self.assertEqual(self.api.releases, [])

    def test_private_packages_block_before_reservation_or_registry_mutations(self):
        self.api.visibility = "private"
        output = io.StringIO()
        with (
            redirect_stdout(output),
            self.assertRaisesRegex(InvalidRelease, "not public"),
        ):
            self.publish()
        self.assertEqual(
            output.getvalue().splitlines()[-1], "Publication stage: package-preflight"
        )
        self.assertNotIn("Publication stage: publication-lease", output.getvalue())
        self.assertEqual(self.api.registry, {})
        self.assertEqual(self.api.releases, [])
        self.assertEqual(self.api.assets, {})
        self.assertFalse(self.adapter.tags_created)
        self.assertFalse((self.state / "v1.2.3.jsonl").exists())

    def test_real_image_inspection_failure_creates_no_draft_or_journal(self):
        execute = self.commands.__call__
        staged_roots = []

        def fail_inspection(args, **kwargs):
            if args[:3] == ["skopeo", "inspect", "--raw"]:
                self.assertFalse(self.adapter.held)
                self.assertEqual(self.api.releases, [])
                staged_roots.append(Path(args[-1].removeprefix("oci:")).parent)
                raise InvalidRelease("fixture unprivileged image inspection failed")
            return execute(args, **kwargs)

        with (
            patch.object(
                transport_fixtures.Commands, "__call__", side_effect=fail_inspection
            ),
            self.assertRaisesRegex(InvalidRelease, "unprivileged image inspection"),
        ):
            self.publish()
        self.assertEqual(self.api.releases, [])
        self.assertEqual(self.api.registry, {})
        self.assertFalse((self.state / "v1.2.3.jsonl").exists())
        self.assertEqual(len(staged_roots), 1)
        self.assertFalse(staged_roots[0].exists())

    def test_explicit_first_package_setup_reuses_the_reviewed_pair_in_one_run(self):
        request = self.api.request

        def first_packages(method, url, **kwargs):
            if "/packages/container/psst-zip-" in url and not self.api.registry:
                return self.api.response({"message": "Not Found"}, 404)
            return request(method, url, **kwargs)

        with patch.object(self.api, "request", side_effect=first_packages):
            result = self.publish(
                environment=self.environment | {"PSST_INITIALIZE_GHCR_PACKAGES": "true"}
            )
        self.assertTrue(result["immutable"])
        self.assertEqual(len(self.api.releases), 1)
        self.assertEqual(len(self.api.registry), 8)  # Six digests plus version tags.
        self.assertTrue(self.adapter.public_verified)
        intents = [
            row["operation"] for row in self.events() if row["phase"] == "intent"
        ]
        self.assertEqual(intents.count("reserve-draft"), 1)
        self.assertEqual(sum(name.startswith("push-") for name in intents), 6)


if __name__ == "__main__":
    unittest.main()
