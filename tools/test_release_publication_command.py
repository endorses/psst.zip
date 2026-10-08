"""Actual orchestration/transport fixtures; these never attest or publish live."""

import copy
import tarfile
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from generate_release_gate_reports import CHECKS
import github_release_transport as transport
import publish_container_release as publication
import publish_verified_release as driver
import publication_diagnostics as diagnostics
from release_artifacts import InvalidRelease, read_json
import test_release_transport as transport_fixtures
import github_release_attestor as signer_module


class FixtureVerifier:
    def __init__(self, upstream):
        self.upstream, self.signed = upstream, set()

    def verify(self, gate, report, binding):
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


class FixtureAttestor:
    """Explicit test injection, never a production bridge or trust flag."""

    def __init__(self, case):
        self.case, self.fail, self.mutate = case, None, None
        self.calls = []

    def attest_subjects(self, plan, files, output):
        self.calls.append("provenance")
        if self.fail == "provenance":
            raise InvalidRelease("fixture signer interrupted")
        self.case.assertTrue(self.case.adapter.held)
        self.case.assertEqual(len(self.case.api.registry), 6)
        self.case.assertEqual(set(files), set(dict(plan.assets)))
        path = output / "provenance.json"
        driver.exclusive_report(
            path,
            {
                "schema_version": 1,
                "gate": "provenance",
                "binding_digest": plan.binding.digest,
                "passed": True,
                "details": {"subjects": dict(plan.binding.subjects)},
            },
        )
        self.case.verifier.signed.add(path.read_bytes())
        return path

    def sign_report(self, binding, report):
        value = read_json(report.read_bytes())
        self.calls.append(value["gate"])
        self.case.assertTrue(self.case.adapter.held)
        if self.fail == value["gate"]:
            raise InvalidRelease("fixture readback signer failed")
        if self.mutate == value["gate"]:
            report.chmod(0o600)
            report.write_bytes(report.read_bytes() + b"\n")
        else:
            self.case.verifier.signed.add(report.read_bytes())


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
        self.attestor = FixtureAttestor(self)
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
        args = dict(
            root=self.fixture.root,
            repository="endorses/psst.zip",
            ref="refs/tags/v1.2.3",
            event_sha=self.fixture.commit,
            reviewed_commit=self.fixture.commit,
            manifest=self.fixture.manifest_path,
            bundle=self.fixture.bundle,
            indexes=self.fixture.indexes,
            archives=self.real.archives,
            source_assets={self.fixture.source.name: self.fixture.source},
            reports={gate: self.fixture.reports[gate] for gate in publication.GATES},
            state=self.state,
            environment=self.environment,
            verifier=self.verifier,
            attestor=self.attestor,
            transport_factory=self.factory,
        )
        return driver.publish(**(args | changes))

    def events(self):
        return self.real.events()

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
        self.assertEqual(
            self.attestor.calls,
            ["provenance", "registry-readback", "anonymous-pull", "asset-readback"],
        )
        self.assertEqual(
            result["journal_sha256"],
            publication.source_digest(self.state / "v1.2.3.jsonl"),
        )
        intents = [
            row["operation"] for row in self.events() if row["phase"] == "intent"
        ]
        self.assertLess(
            intents.index("push-web-index"), intents.index("attest-reviewed-subjects")
        )
        self.assertLess(
            intents.index("attest-reviewed-subjects"),
            intents.index("upload-" + self.fixture.source.name),
        )
        self.assertLess(
            intents.index("attest-asset-readback"), intents.index("tag-backend")
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
            if k in publication.GATES and k != "distribution-review"
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

    def test_default_bucket_free_driver_uses_a_disposable_official_signer_cache(self):
        action_caches = []

        def official_fixture(binding, **kwargs):
            cache = kwargs["private_action_cache"]
            self.assertFalse(cache.is_relative_to(kwargs["private_output"].parent))
            (cache / "dist").mkdir(mode=0o700)
            (cache / "dist" / "index.js").write_bytes(b"tiny official-action fixture")
            action_caches.append(cache)
            return self.attestor

        with patch.object(
            signer_module, "WorkflowAttestor", side_effect=official_fixture
        ):
            result = self.publish(attestor=None)
        self.assertTrue(result["immutable"])
        self.assertEqual(len(action_caches), 1)
        self.assertFalse(action_caches[0].exists())
        self.assertFalse(
            any("S3" in key or "AWS" in key for key in driver.WORKFLOW_ENV)
        )

    def test_authenticated_smoke_must_bind_all_four_native_configs(self):
        self.fixture.verifier.details["final-image-smoke"]["tested_configs"][
            "backend-arm64"
        ] = ("sha256:" + "0" * 64)
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
        self.attestor.fail = "provenance"
        with self.assertRaises(InvalidRelease):
            self.publish()
        self.assertEqual(len(self.api.registry), 6)
        self.assertEqual(self.api.assets, {})
        self.assertTrue(self.api.releases[0]["draft"])
        self.assertTrue(
            any(
                row["operation"] == "attest-reviewed-subjects"
                and row["phase"] == "uncertain"
                for row in self.events()
            )
        )
        retained = list(self.state.glob("publication-inputs-*"))
        self.assertEqual(len(retained), 1)
        self.assertTrue((retained[0] / "snapshot-binding.json").is_file())

    def test_failed_or_substituted_signed_readback_never_creates_tags_or_publishes(
        self,
    ):
        self.attestor.mutate = "asset-readback"
        with self.assertRaisesRegex(InvalidRelease, "changed during signing"):
            self.publish()
        self.assertTrue(self.api.assets)
        self.assertFalse(self.adapter.tags_created)
        self.assertTrue(self.api.releases[0]["draft"])
        self.assertTrue(
            any(
                row["operation"] == "attest-asset-readback"
                and row["phase"] == "uncertain"
                for row in self.events()
            )
        )

    def test_immutable_policy_failure_never_creates_draft(self):
        self.api.immutable_status = 403
        with self.assertRaises(InvalidRelease):
            self.publish()
        self.assertEqual(self.api.releases, [])

    def test_private_packages_block_before_reservation_or_registry_mutations(self):
        self.api.visibility = "private"
        with self.assertRaisesRegex(InvalidRelease, "not public"):
            self.publish()
        self.assertEqual(self.api.registry, {})
        self.assertEqual(self.api.releases, [])
        self.assertEqual(self.api.assets, {})
        self.assertFalse(self.adapter.tags_created)
        self.assertFalse((self.state / "v1.2.3.jsonl").exists())

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

    def test_default_official_signer_checks_capability_before_reservation(self):
        constructor_calls = []

        def unavailable(binding, **kwargs):
            constructor_calls.append((binding, kwargs))
            raise InvalidRelease("official signer capability is missing")

        with patch.dict(
            sys.modules,
            {"github_release_attestor": SimpleNamespace(WorkflowAttestor=unavailable)},
        ):
            with self.assertRaisesRegex(InvalidRelease, "capability"):
                self.publish(attestor=None)
        self.assertEqual(len(constructor_calls), 1)
        self.assertEqual(constructor_calls[0][0].commit, self.fixture.commit)
        self.assertIs(constructor_calls[0][1]["verifier"], self.verifier)
        self.assertEqual(self.api.calls, [])
        self.assertFalse((self.state / "v1.2.3.jsonl").exists())


if __name__ == "__main__":
    unittest.main()
