"""Small offline recovery evidence fixtures; no container/image/archive builds."""

from __future__ import annotations

import copy
import json
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import aggregate_release_recovery as aggregation
import measure_release_recovery as recovery
from publish_container_release import (
    Binding,
    SOURCE_COVERAGE,
    image_subjects,
    recovery_review_details,
    sha256,
)
from release_artifacts import InvalidRelease, PLATFORMS, git, json_bytes, require
from test_release_artifacts import digest, manifest

START = "2020-01-01T00:00:00+00:00"
OBSERVED = "2020-01-01T00:01:00+00:00"
END = "2020-01-01T00:02:00+00:00"


class ExactAttemptAuthenticator:
    def __init__(self, binding, records, attempt=2):
        self.binding = binding
        self.records = {sha256(raw) for raw in records}
        self.attempt = attempt
        self.calls = []

    def authenticate(self, raw, binding):
        self.calls.append(sha256(raw))
        require(
            binding == self.binding
            and self.attempt == 2
            and sha256(raw) in self.records,
            "No exact-attempt authenticated evidence",
        )


def experiment(context, previous, configs, tools, *, failure, paused):
    first = "20200101T000001Z-" + "a" * 12
    last = first if failure else "20200101T000002Z-" + "b" * 12
    stages = (
        [("post-startup-failure", first)]
        if failure
        else [("candidate-activation", first), ("repeat-activation", last)]
    )
    stages.append(("isolated-restore-activation", last))
    observations = []
    for stage, transaction in stages:
        restoring = stage == "isolated-restore-activation"
        failed = stage == "post-startup-failure"
        version = "v0.0.0" if restoring and failure else context.version
        observations.append(
            {
                "stage": stage,
                "transaction": transaction,
                "version": context.version,
                "active_version": None if failed else version,
                "previous_version": (
                    "v0.0.0"
                    if failure or stage == "candidate-activation"
                    else context.version
                ),
                "phase": "failed-closed" if failed else "completed",
                "mutation_started": True,
                "prior_pause": paused,
                "restoring": restoring,
                "verification": (
                    None
                    if failed
                    else {
                        "checks": {
                            name: json.dumps({"fixture_status": 200})
                            for name in (
                                {"flow"} | ({"restore"} if restoring else set())
                            )
                        },
                        "observed_at": OBSERVED,
                        "version": version,
                        "source_commit": (
                            "checkpoint:" + transaction if restoring else context.commit
                        ),
                    }
                ),
                "public_ingress": (
                    {"running_services": 0}
                    if failed
                    else {
                        "https_port": 18443,
                        "config_status": 200,
                        "public_transfers_paused": paused,
                    }
                ),
            }
        )
    return {
        "schema_version": 1,
        "kind": "disposable-updater-experiment",
        "scenario": "post-startup-failure" if failure else "normal",
        "candidate": {
            "version": context.version,
            "commit": context.commit,
            "configs": configs,
        },
        "baseline": {
            "version": "v0.0.0",
            "commit": previous,
            "configs": {"backend": digest("8"), "web": digest("9")},
        },
        "prior_pause": paused,
        "repeat_mode": "same-exact-candidate",
        "observations": observations,
        "startup_observations": [
            {
                "transaction": item["transaction"],
                "restoring": item["restoring"],
                "observed_at": OBSERVED,
            }
            for item in observations
        ],
        "schema": {
            "before": 10,
            "original_after": 11,
            "restored": 10 if failure else 11,
            "old_cli_rejected_migrated_original": True,
        },
        "checkpoint": {
            "sha256": "c" * 64,
            "preserved_sha256": "c" * 64,
            "records": {"image": 2, "volume": 3, "configuration": 4},
            "corrupt_archive_refused": True,
            "encrypted_export_receipt": {
                "checkpoint_sha256": "c" * 64,
                "encrypted_off_host_receipt": "d" * 64,
                "restore_exercise": "e" * 64,
                "verified_at": OBSERVED,
            },
        },
        "restore": {
            "volume_mapping": {
                name: "new-" + name for name in ("data", "caddy-data", "caddy-config")
            },
            "original_volumes": {
                "backend:/app/data": "data",
                "caddy:/data": "caddy-data",
                "caddy:/config": "caddy-config",
            },
            "original_payload_sha256": "d" * 64,
            "preserved_original_payload_sha256": "d" * 64,
            "certificate_sha256": "e" * 64,
            "restored_certificate_sha256": "e" * 64,
        },
        "completed_at": END,
        "acquisition": "fixture-local-exact-loaded-configs",
        "backup_provider": "same-host-separated-store-encryption-simulation",
        "nested_tools": {**tools, "docker": "29.8.2", "compose": "5.6.0"},
        "public_provenance_verified": False,
        "off_host_provider_verified": False,
        "publication_authorized": False,
    }


class RecoveryGateChecks(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="psst-recovery-gate-test-")
        self.addCleanup(self.temporary.cleanup)
        self.folder = Path(self.temporary.name)
        self.root = self.folder / "source"
        self.root.mkdir()
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        for name in recovery.FIXTURE_FILES:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("# Small committed recovery fixture\n")
        path = self.root / "deploy/update.py"
        path.parent.mkdir()
        path.write_text(
            "FLOW_CHECKS = frozenset({'flow'})\nRESTORE_CHECKS = frozenset({'restore'})\n"
        )
        git(self.root, "add", ".")
        git(
            self.root,
            "-c",
            "user.name=Fixture",
            "-c",
            "user.email=fixture@example.invalid",
            "commit",
            "-qm",
            "historical",
        )
        self.previous = git(self.root, "rev-parse", "HEAD").decode().strip()
        (self.root / "candidate").write_text("candidate\n")
        git(self.root, "add", ".")
        git(
            self.root,
            "-c",
            "user.name=Fixture",
            "-c",
            "user.email=fixture@example.invalid",
            "commit",
            "-qm",
            "candidate",
        )
        self.commit = git(self.root, "rev-parse", "HEAD").decode().strip()
        self.assembled = manifest(self.commit)
        self.assembled["payload_profile"] = "deployment-ready"
        self.bundle = self.folder / self.assembled["bundle"]["name"]
        self.bundle.write_bytes(b"Opaque bound fixture; aggregation never unpacks this")
        self.assembled["bundle"]["sha256"] = sha256(self.bundle.read_bytes())[7:]
        self.manifest = self.folder / "release-manifest.json"
        self.manifest.write_bytes(json_bytes(self.assembled))
        subjects = {
            **image_subjects(self.assembled),
            "manifest": "file:release-manifest.json@"
            + sha256(self.manifest.read_bytes()),
            "bundle": "file:"
            + self.bundle.name
            + "@"
            + sha256(self.bundle.read_bytes()),
            "source:fixture.tar.gz": "file:fixture.tar.gz@" + digest("f"),
        }
        self.binding = Binding(
            "endorses/psst.zip", "v1.2.3", self.commit, tuple(sorted(subjects.items()))
        )
        self.descriptors, self.measurements, self.records, self.images = {}, {}, {}, {}
        for i, platform in enumerate(PLATFORMS):
            context = recovery.NativeSourceContext(
                self.binding.repository, self.binding.version, self.commit, platform
            )
            configs = {c: sha256((c + platform).encode()) for c in ("backend", "web")}
            artifacts = {
                name: {
                    "file": name + ".fixture",
                    "sha256": sha256((name + platform).encode()),
                    "size": 17,
                }
                for name in recovery.ARTIFACTS
                | {"browser_inputs", "browser_verification"}
            }
            descriptor = {
                "schema_version": 2,
                "kind": "native-release-artifacts",
                "source": context.checked(),
                "original_tested_configs": configs,
                "tested_configs": configs,
                "source_helper_config": digest("a"),
                "oci_exporter": "docker-save-byte-preserving-oci-v1",
                "artifacts": artifacts,
                "publication_authorized": False,
                "measurement_authentication_required": True,
            }
            self.descriptors[platform] = self.folder / (
                "descriptor-" + str(i) + ".json"
            )
            self.descriptors[platform].write_bytes(json_bytes(descriptor))
            for component in ("backend", "web"):
                self.images[component + "-" + platform.split("/")[1]] = {
                    "platform": platform,
                    "manifest_digest": self.assembled["images"][component][
                        "platform_digests"
                    ][platform],
                    "manifest_size": 123,
                    "manifest_media_type": "application/vnd.oci.image.manifest.v1+json",
                    "config_digest": configs[component],
                    "archive_digest": artifacts[component + "_archive"]["sha256"],
                    "blob_count": 3,
                }
            tools = {
                "docker": "28.0.4",
                "compose": "2.36.2",
                "architecture": "x86_64" if i == 0 else "aarch64",
            }
            self.records[platform] = {
                "schema_version": 1,
                "kind": "native-upgrade-recovery-measurement",
                "source": context.checked(),
                "execution": "native",
                "manifest_sha256": sha256(self.manifest.read_bytes()),
                "bundle_sha256": sha256(self.bundle.read_bytes()),
                "tested_configs": configs,
                "saved_pair": artifacts["final_archive"],
                "historical_source_commit": self.previous,
                "execution_inputs": {
                    name: sha256((self.root / name).read_bytes())
                    for name in recovery.FIXTURE_FILES
                },
                "tools": tools,
                "experiments": {
                    name: experiment(
                        context,
                        self.previous,
                        configs,
                        tools,
                        failure=failure,
                        paused=paused,
                    )
                    for name, failure, paused in recovery.EXPERIMENTS
                },
                "started_at": START,
                "completed_at": END,
                "upgrade_recovery_gate_pending": True,
                "measurement_authentication_required": True,
                **{flag: False for flag in aggregation.SCOPE_FLAGS},
            }
            self.measurements[platform] = self.folder / (
                "measurement-" + str(i) + ".json"
            )
        for record in self.records.values():
            record["native_descriptors"] = {
                p: sha256(path.read_bytes()) for p, path in self.descriptors.items()
            }
            record["images"] = copy.deepcopy(self.images)
        self.write_records()

    def write_records(self):
        for platform, path in self.measurements.items():
            path.write_bytes(json_bytes(self.records[platform]))
        self.authenticator = ExactAttemptAuthenticator(
            self.binding, [p.read_bytes() for p in self.measurements.values()]
        )

    def aggregate(self):
        return aggregation.aggregate_recovery(
            self.binding,
            measurements=self.measurements,
            native_descriptors=self.descriptors,
            manifest=self.manifest,
            bundle=self.bundle,
            root=self.root,
            authenticator=self.authenticator,
        )

    def test_complete_retained_native_records_derive_exact_contract_without_replay(
        self,
    ):
        with patch.object(
            recovery, "validate_inputs", side_effect=AssertionError("replay")
        ), patch.object(
            recovery.integration,
            "execute_experiment",
            side_effect=AssertionError("Docker"),
        ):
            result = self.aggregate()
        self.assertEqual(len(self.authenticator.calls), 2)
        self.assertEqual(result["details"]["checks"], aggregation.CHECKS)
        self.assertEqual(
            result["details"]["execution"], {p: "native" for p in PLATFORMS}
        )
        recovery_review_details(result["details"], self.binding)
        self.assertTrue(
            all(result["details"][flag] is False for flag in aggregation.SCOPE_FLAGS)
        )
        for p in PLATFORMS:
            self.assertEqual(
                result["details"]["native_measurements"][p]["record_digest"],
                sha256(self.measurements[p].read_bytes()),
            )

    def test_unsigned_substituted_and_wrong_attempt_records_are_rejected(self):
        self.authenticator.attempt = 1
        with self.assertRaisesRegex(InvalidRelease, "exact-attempt"):
            self.aggregate()
        self.authenticator.attempt = 2
        self.measurements[PLATFORMS[0]].write_bytes(
            json_bytes({**self.records[PLATFORMS[0]], "execution": "emulated"})
        )
        with self.assertRaisesRegex(InvalidRelease, "authenticated"):
            self.aggregate()

    def test_authenticated_omitted_scenario_and_changed_facts_are_rejected(self):
        base = copy.deepcopy(self.records)
        mutations = [
            lambda r: r["experiments"].pop(recovery.EXPERIMENTS[1][0]),
            lambda r: r.update(execution="emulated"),
            lambda r: r["source"].update(commit="a" * 40),
            lambda r: r["native_descriptors"].update({PLATFORMS[1]: digest("a")}),
            lambda r: r["tested_configs"].update(web=digest("f")),
            lambda r: r["images"]["web-arm64"].update(config_digest=digest("f")),
            lambda r: r["execution_inputs"].update(
                {recovery.FIXTURE_FILES[0]: digest("f")}
            ),
            lambda r: r.update(historical_source_commit=self.commit),
            lambda r: r.update(public_provenance_verified=True),
            lambda r: r["tools"].update(architecture="aarch64"),
            lambda r: r["experiments"][recovery.EXPERIMENTS[0][0]]["restore"].update(
                restored_certificate_sha256="f" * 64
            ),
            lambda r: r["experiments"][recovery.EXPERIMENTS[0][0]][
                "nested_tools"
            ].update(docker="fixture-fake"),
        ]
        for mutate in mutations:
            self.records = copy.deepcopy(base)
            mutate(self.records[PLATFORMS[0]])
            self.write_records()
            with self.subTest(mutate=mutate), self.assertRaises(InvalidRelease):
                self.aggregate()

    def test_timestamp_bounds_and_default_execution_freshness(self):
        base = copy.deepcopy(self.records)
        for mutate in (
            lambda r: r.update(completed_at=START),
            lambda r: r.update(started_at=END),
            lambda r: r["experiments"][recovery.EXPERIMENTS[0][0]].update(
                completed_at="2020-01-01T00:03:00+00:00"
            ),
            lambda r: r["experiments"][recovery.EXPERIMENTS[0][0]]["checkpoint"][
                "encrypted_export_receipt"
            ].update(verified_at="2019-01-01T00:00:00+00:00"),
            lambda r: r.update(completed_at="2999-01-01T00:00:00+00:00"),
        ):
            self.records = copy.deepcopy(base)
            mutate(self.records[PLATFORMS[0]])
            self.write_records()
            with self.subTest(mutate=mutate), self.assertRaises(InvalidRelease):
                self.aggregate()
        record = base[PLATFORMS[0]]
        with self.assertRaisesRegex(InvalidRelease, "completion"):
            recovery.checked_experiment(
                record["experiments"][recovery.EXPERIMENTS[0][0]],
                context=recovery.NativeSourceContext(**record["source"]),
                previous=self.previous,
                configs=record["tested_configs"],
                failure=False,
                paused=False,
                policy=({"flow"}, {"restore"}),
                started=START,
            )

    def test_descriptor_bundle_and_helper_substitution_are_rejected(self):
        for path in (
            self.descriptors[PLATFORMS[1]],
            self.bundle,
            self.root / recovery.FIXTURE_FILES[0],
        ):
            original = path.read_bytes()
            path.write_bytes(original + b"\n ")
            with self.subTest(path=path), self.assertRaises(InvalidRelease):
                self.aggregate()
            path.write_bytes(original)

    def test_authentication_cannot_change_retained_metadata(self):
        original = self.authenticator.authenticate

        def authenticate(raw, binding):
            original(raw, binding)
            self.measurements[PLATFORMS[0]].write_bytes(
                self.measurements[PLATFORMS[0]].read_bytes() + b" "
            )

        self.authenticator.authenticate = authenticate
        with self.assertRaisesRegex(InvalidRelease, "changed during"):
            self.aggregate()

    def test_verify_only_authenticates_canonical_gate_without_replay(self):
        report = self.aggregate()
        output = self.folder / "upgrade-recovery.json"
        output.write_bytes(json_bytes(report))
        authenticator = ExactAttemptAuthenticator(self.binding, [output.read_bytes()])
        with patch.object(
            aggregation, "aggregate_recovery", side_effect=AssertionError("replay")
        ):
            self.assertEqual(
                aggregation.verify_output(output, self.binding, authenticator), report
            )
        for mutate in (
            lambda r: r.update(binding_digest=digest("f")),
            lambda r: r["details"]["native_measurements"].pop(PLATFORMS[1]),
            lambda r: r["details"].update(checks=aggregation.CHECKS[:-1]),
        ):
            value = copy.deepcopy(report)
            mutate(value)
            output.write_bytes(json_bytes(value))
            authenticator = ExactAttemptAuthenticator(
                self.binding, [output.read_bytes()]
            )
            with self.assertRaises(InvalidRelease):
                aggregation.verify_output(output, self.binding, authenticator)
        output.write_bytes(json.dumps(report).encode())
        authenticator = ExactAttemptAuthenticator(self.binding, [output.read_bytes()])
        with self.assertRaisesRegex(InvalidRelease, "noncanonical"):
            aggregation.verify_output(output, self.binding, authenticator)

    def small_command_fixture(self):
        prepared = self.folder / "prepared"
        prepared.mkdir()
        (prepared / self.manifest.name).write_bytes(self.manifest.read_bytes())
        (prepared / self.bundle.name).write_bytes(self.bundle.read_bytes())
        record = {
            "schema_version": 1,
            "kind": "prepared-release-inputs",
            "source_kind": "version-tag",
            "tagged_source_ci_gate_verified": False,
            "signer_identity_verified": False,
            "repository": self.binding.repository,
            "version": self.binding.version,
            "source_commit": self.commit,
            "binding_sha256": self.binding.digest,
            "assets": {
                self.manifest.name: sha256(self.manifest.read_bytes()),
                self.bundle.name: sha256(self.bundle.read_bytes()),
                "fixture.tar.gz": digest("f"),
            },
            "subjects": dict(self.binding.subjects),
            "dependency_replays": {p: {} for p in PLATFORMS},
            "upstream_replay": {},
            "publication_authorized": False,
            "measurement_authentication_required": True,
        }
        (prepared / "release-inputs.json").write_bytes(json_bytes(record))
        source_gate = {
            "schema_version": 1,
            "gate": "corresponding-source",
            "binding_digest": self.binding.digest,
            "passed": True,
            "details": {
                "schema_version": 1,
                "source_subjects": {
                    k: v for k, v in self.binding.subjects if k.startswith("source:")
                },
                "images": {
                    name: {
                        "subject": dict(self.binding.subjects)[name],
                        "notice_inventory_digest": digest("b"),
                    }
                    for name in SOURCE_COVERAGE
                },
                "policy": {
                    "path": "tools/container-distribution-policy.json",
                    "source_commit": self.commit,
                    "record_digest": digest("c"),
                    "git_blob": "d" * 40,
                },
                "coverage": {
                    name: {
                        category: {"status": "complete", "evidence_digest": digest("e")}
                        for category in categories
                    }
                    for name, categories in SOURCE_COVERAGE.items()
                },
            },
        }
        source_report = self.folder / "corresponding-source.json"
        source_report.write_bytes(json_bytes(source_gate))
        trees = {}
        for platform in PLATFORMS:
            tree = self.folder / platform.split("/")[1]
            (tree / "native").mkdir(parents=True)
            (tree / "native/native-artifacts.json").write_bytes(
                self.descriptors[platform].read_bytes()
            )
            trees[platform] = tree
        args = SimpleNamespace(
            root=self.root,
            prepared=prepared,
            repository=self.binding.repository,
            version=self.binding.version,
            commit=self.commit,
            run_id=123,
            run_attempt=2,
            output=self.folder / "upgrade-recovery.json",
            source_report=source_report,
            verify_only=False,
            amd64_inputs=trees[PLATFORMS[0]],
            arm64_inputs=trees[PLATFORMS[1]],
            amd64_measurement=self.measurements[PLATFORMS[0]],
            arm64_measurement=self.measurements[PLATFORMS[1]],
        )
        auth = ExactAttemptAuthenticator(
            self.binding,
            [
                source_report.read_bytes(),
                *[p.read_bytes() for p in self.measurements.values()],
            ],
        )
        return args, auth

    def test_small_command_authenticates_source_and_native_facts_then_verifies_once(
        self,
    ):
        args, auth = self.small_command_fixture()
        with patch.object(
            aggregation, "GhEvidenceVerifier", return_value=auth
        ) as verifier, patch(
            "generate_corresponding_source_review.command_inputs",
            side_effect=AssertionError("full replay"),
        ):
            report = aggregation.run_command(args)
            verifier.assert_called_once_with(token=None, run_id=123, run_attempt=2)
        self.assertEqual(len(auth.calls), 3)
        self.assertEqual(args.output.read_bytes(), json_bytes(report))
        auth.records.add(sha256(args.output.read_bytes()))
        args.verify_only = True
        with patch.object(
            aggregation, "GhEvidenceVerifier", return_value=auth
        ), patch.object(
            aggregation,
            "aggregate_recovery",
            side_effect=AssertionError("repeat experiments"),
        ):
            self.assertEqual(aggregation.run_command(args), report)
        args.verify_only = False
        with self.assertRaisesRegex(InvalidRelease, "already exists"):
            aggregation.run_command(args)

    def test_small_binding_rejects_unsigned_source_scope_and_extra_files(self):
        args, auth = self.small_command_fixture()
        raw = args.source_report.read_bytes()
        auth.records.remove(sha256(raw))
        with self.assertRaisesRegex(InvalidRelease, "authenticated"):
            aggregation.authenticated_command_inputs(args, auth)
        auth.records.add(sha256(raw))
        source = json.loads(raw)
        source["details"]["coverage"]["backend-arm64"].pop("runtime")
        args.source_report.write_bytes(json_bytes(source))
        auth.records.add(sha256(args.source_report.read_bytes()))
        with self.assertRaisesRegex(InvalidRelease, "categories"):
            aggregation.authenticated_command_inputs(args, auth)
        args.source_report.write_bytes(raw)
        record_path = args.prepared / "release-inputs.json"
        record_raw = record_path.read_bytes()
        record = json.loads(record_raw)
        source_name = next(
            name.removeprefix("source:")
            for name in record["subjects"]
            if name.startswith("source:")
        )
        record["assets"].pop(source_name)
        record_path.write_bytes(json_bytes(record))
        with self.assertRaisesRegex(InvalidRelease, "asset inventory"):
            aggregation.authenticated_command_inputs(args, auth)
        record_path.write_bytes(record_raw)
        (args.prepared / "extra.json").write_text("{}")
        with self.assertRaisesRegex(InvalidRelease, "file set"):
            aggregation.authenticated_command_inputs(args, auth)


if __name__ == "__main__":
    unittest.main()
