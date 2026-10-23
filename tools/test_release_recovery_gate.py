"""Small offline recovery evidence fixtures; no container/image/archive builds."""

from __future__ import annotations

import copy
import io
import json
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

import aggregate_release_recovery as aggregation
import assemble_release_oci as oci
import measure_release_recovery as recovery
from generate_release_gate_reports import CHECKS
from publish_container_release import (
    SOURCE_COVERAGE,
    Binding,
    VerifiedEvidence,
    image_subjects,
    recovery_review_details,
    sha256,
)
from release_artifacts import (
    PLATFORMS,
    REQUIRED_FILES,
    InvalidRelease,
    build_bundle,
    git,
    json_bytes,
    require,
)
from test_release_artifacts import digest, manifest
from test_release_oci import fixture as oci_fixture

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
        with (
            patch.object(
                recovery, "validate_inputs", side_effect=AssertionError("replay")
            ),
            patch.object(
                recovery.integration,
                "execute_experiment",
                side_effect=AssertionError("Docker"),
            ),
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
        with (
            patch.object(
                aggregation, "GhEvidenceVerifier", return_value=auth
            ) as verifier,
            patch.dict(
                sys.modules,
                {
                    "generate_corresponding_source_review": None,
                    "measure_browser_source_inventory": None,
                    "measure_native_browser_inputs": None,
                },
            ),
        ):
            report = aggregation.run_command(args)
            verifier.assert_called_once_with(token=None, run_id=123, run_attempt=2)
        self.assertEqual(len(auth.calls), 3)
        self.assertEqual(args.output.read_bytes(), json_bytes(report))
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


class AuthenticatedRecoveryInputs(unittest.TestCase):
    """Real tiny OCI/save/bundle bytes; only hosted signature trust is injected."""

    @staticmethod
    def archive(path, files):
        path.parent.mkdir(parents=True, exist_ok=True)
        with tarfile.open(path, "w:") as archive:
            for name, body in files.items():
                member = tarfile.TarInfo(name)
                member.size = len(body)
                archive.addfile(member, io.BytesIO(body))

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="psst-recovery-projection-")
        self.addCleanup(self.temporary.cleanup)
        self.folder = Path(self.temporary.name)
        self.root = self.folder / "source"
        self.root.mkdir()
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        for name in REQUIRED_FILES | {
            "deploy/update.py",
            "backend/licenses/fixture.txt",
        }:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("Small tracked source fixture\n")
        git(self.root, "add", ".")
        git(
            self.root,
            "-c",
            "user.name=Fixture",
            "-c",
            "user.email=fixture@example.invalid",
            "commit",
            "-qm",
            "fixture",
        )
        self.commit = git(self.root, "rev-parse", "HEAD").decode().strip()
        self.context = recovery.NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", self.commit, PLATFORMS[0]
        )
        self.prepared = self.folder / "prepared"
        self.prepared.mkdir()
        self.bundle = build_bundle(
            self.root, self.prepared, self.context.version, "deployment-ready"
        )
        self.natives, self.images, configs, archives, self.saved_files = (
            {},
            {},
            {},
            {},
            {},
        )
        self.oci_files = {}
        self.descriptor_values = {}
        for platform in PLATFORMS:
            arch = platform.split("/")[1]
            native = self.folder / arch / "native"
            native.mkdir(parents=True)
            pair, saved_manifest, artifacts = {}, [], {}
            for component in ("backend", "web"):
                files, old_config = oci_fixture(component, arch, gzip_layer=False)
                index = json.loads(files["index.json"])
                old_image = index["manifests"][0]["digest"]
                image = json.loads(files.pop("blobs/sha256/" + old_image[7:]))
                config = json.loads(files.pop("blobs/sha256/" + old_config[7:]))
                labels = config["config"]["Labels"]
                labels["org.opencontainers.image.revision"] = self.commit
                labels["zip.psst.source.archive"] = (
                    "https://github.com/endorses/psst.zip/archive/"
                    + self.commit
                    + ".tar.gz"
                )
                config_raw = json_bytes(config)
                config_digest = sha256(config_raw)
                image["config"].update(digest=config_digest, size=len(config_raw))
                image_raw = json_bytes(image)
                index["manifests"][0].update(
                    digest=sha256(image_raw), size=len(image_raw)
                )
                files.update(
                    {
                        "index.json": json_bytes(index),
                        "blobs/sha256/" + config_digest[7:]: config_raw,
                        "blobs/sha256/" + sha256(image_raw)[7:]: image_raw,
                    }
                )
                self.oci_files[arch, component] = files
                path = native / "export" / (component + "-" + arch + ".oci.tar")
                self.archive(path, files)
                target = component + "-" + arch
                pair[component] = configs[target] = config_digest
                archives[target] = path
                self.images[target] = oci.inspect_archive(
                    path,
                    platform=platform,
                    repository=self.context.repository,
                    version=self.context.version,
                    commit=self.commit,
                    tested_config=config_digest,
                    component=component,
                )
                artifacts[component + "_archive"] = recovery.file_record(path, native)
                config_name, layer_name = component + ".json", component + "-layer.tar"
                self.saved_files[arch, config_name] = config_raw
                self.saved_files[arch, layer_name] = files[
                    "blobs/sha256/" + image["layers"][0]["digest"][7:]
                ]
                saved_manifest.append(
                    {
                        "Config": config_name,
                        "Layers": [layer_name],
                        "RepoTags": [component + ":fixture"],
                    }
                )
            saved = {
                name: raw for (a, name), raw in self.saved_files.items() if a == arch
            }
            saved["manifest.json"] = json_bytes(saved_manifest)
            self.saved_files[arch, "manifest.json"] = saved["manifest.json"]
            final = native / "final-images.docker.tar"
            self.archive(final, saved)
            artifacts["final_archive"] = recovery.file_record(final, native)
            for name in recovery.ARTIFACTS | {"browser_inputs", "browser_verification"}:
                artifacts.setdefault(
                    name, {"file": name + ".fixture", "sha256": digest("f"), "size": 1}
                )
            descriptor = {
                "schema_version": 2,
                "kind": "native-release-artifacts",
                "source": recovery.NativeSourceContext(
                    self.context.repository, self.context.version, self.commit, platform
                ).checked(),
                "original_tested_configs": pair,
                "tested_configs": pair,
                "source_helper_config": digest("d"),
                "oci_exporter": "docker-save-byte-preserving-oci-v1",
                "artifacts": artifacts,
                "publication_authorized": False,
                "measurement_authentication_required": True,
            }
            self.descriptor_values[platform] = descriptor
            self.natives[platform] = native / "native-artifacts.json"
            self.natives[platform].write_bytes(json_bytes(descriptor))
        assembled = oci.assemble(
            archives,
            configs,
            repository=self.context.repository,
            version=self.context.version,
            commit=self.commit,
            output=self.prepared,
        )
        value = manifest(self.commit)
        value.update(payload_profile="deployment-ready", images=assembled["images"])
        value["bundle"]["sha256"] = sha256(self.bundle.read_bytes())[7:]
        self.manifest = self.prepared / "release-manifest.json"
        self.manifest.write_bytes(json_bytes(value))
        (self.prepared / "oci-correspondence.json").unlink()
        subjects = {
            **image_subjects(value),
            "manifest": "file:release-manifest.json@"
            + sha256(self.manifest.read_bytes()),
            "bundle": "file:"
            + self.bundle.name
            + "@"
            + sha256(self.bundle.read_bytes()),
            "source:fixture.tar.gz": "file:fixture.tar.gz@" + digest("f"),
        }
        self.binding = Binding(
            self.context.repository,
            self.context.version,
            self.commit,
            tuple(sorted(subjects.items())),
        )
        source_details = {
            "schema_version": 1,
            "source_subjects": {
                k: v for k, v in subjects.items() if k.startswith("source:")
            },
            "images": {
                name: {
                    "subject": subjects[name],
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
        }
        records = {}
        for platform in PLATFORMS:
            arch = platform.split("/")[1]
            records[platform] = {
                "measurement_digest": digest("d"),
                "smoke": {
                    "schema_version": 1,
                    "kind": "release-image-smoke",
                    "version": self.context.version,
                    "revision": self.commit,
                    "platform": platform,
                    "execution": "native",
                    "tested_configs": self.descriptor_values[platform][
                        "tested_configs"
                    ],
                    "runtime_pack_sha256": digest("c"),
                    "checks": sorted(CHECKS),
                    "completed_at": END,
                    "publication_authorized": False,
                },
                "images": {c: self.images[c + "-" + arch] for c in ("backend", "web")},
                "runtime": {"runtime_pack_sha256": digest("c")},
            }
        self.source_report, self.smoke_report = (
            self.folder / "source.json",
            self.folder / "smoke.json",
        )
        for path, gate, details in (
            (self.source_report, "corresponding-source", source_details),
            (
                self.smoke_report,
                "final-image-smoke",
                {
                    "execution": {p: "native" for p in PLATFORMS},
                    "tested_configs": configs,
                    "native_measurements": records,
                },
            ),
        ):
            path.write_bytes(
                json_bytes(
                    {
                        "schema_version": 1,
                        "gate": gate,
                        "binding_digest": self.binding.digest,
                        "passed": True,
                        "details": details,
                    }
                )
            )
        self.trusted = {
            sha256(p.read_bytes()) for p in (self.source_report, self.smoke_report)
        }
        self.calls = []
        self.select(PLATFORMS[0])

    def select(self, platform):
        self.context = recovery.NativeSourceContext(
            self.context.repository, self.context.version, self.commit, platform
        )
        for candidate in PLATFORMS:
            arch = candidate.split("/")[1]
            native = self.natives[candidate].parent
            final = native / "final-images.docker.tar"
            local_files = [
                native / "export" / (c + "-" + arch + ".oci.tar")
                for c in ("backend", "web")
            ]
            if candidate == platform:
                self.archive(
                    final,
                    {
                        name: raw
                        for (a, name), raw in self.saved_files.items()
                        if a == arch
                    },
                )
                for component, path in zip(
                    ("backend", "web"), local_files, strict=True
                ):
                    self.archive(path, self.oci_files[arch, component])
            else:
                for path in [final, *local_files]:
                    path.unlink(missing_ok=True)

    def verify(self, gate, path, binding):
        raw = path.read_bytes()
        require(
            binding == self.binding and sha256(raw) in self.trusted,
            "No exact-attempt authenticated evidence",
        )
        self.calls.append(gate)
        return VerifiedEvidence(
            gate, binding.digest, sha256(raw), True, json.loads(raw)["details"]
        )

    def validate(self):
        return recovery.validate_authenticated_inputs(
            self.context,
            natives=self.natives,
            manifest_path=self.manifest,
            bundle=self.bundle,
            source_report=self.source_report,
            smoke_report=self.smoke_report,
            verifier=self,
            root=self.root,
        )

    def current_source(self):
        subjects = {
            key: value
            for key, value in self.binding.subjects
            if not key.startswith("source:")
        }
        subjects.update(
            {
                "source:" + name: "file:" + name + "@" + digest("f")
                for name in recovery.source_asset_names(self.context.version)
            }
        )
        self.binding = Binding(
            self.context.repository,
            self.context.version,
            self.commit,
            tuple(sorted(subjects.items())),
        )
        record = json.loads(self.source_report.read_bytes())
        details = record["details"]
        details["schema_version"] = 2
        details["source_subjects"] = {
            key: value for key, value in subjects.items() if key.startswith("source:")
        }
        details["coverage"] = {}
        for target, image in self.images.items():
            arch = target.rsplit("-", 1)[1]
            descriptor = self.descriptor_values["linux/" + arch]
            details["coverage"][target] = {
                "application_source_asset": "psst.zip-source-"
                + self.context.version
                + ".tar.gz",
                "common_source_asset": "psst.zip-upstream-inputs-"
                + self.context.version
                + ".tar.gz",
                "runtime_source_asset": "psst.zip-"
                + self.context.version
                + "-runtime-sources-"
                + arch
                + ".tar.gz",
                "module_source_asset": "psst.zip-dependency-inputs-"
                + self.context.version
                + "-"
                + arch
                + ".tar.gz",
                "runtime_inventory_sha256": descriptor["artifacts"]["runtime_pack"][
                    "sha256"
                ],
                "backend_preferred_key": digest("c"),
                "notice_inventory_digest": details["images"][target][
                    "notice_inventory_digest"
                ],
                "tested_config": image["config_digest"],
                "manifest_sha256": image["manifest_digest"],
                "archive_sha256": image["archive_digest"],
                "preferred_form_verified": True,
            }
        for path in (self.source_report, self.smoke_report):
            value = (
                record if path == self.source_report else json.loads(path.read_bytes())
            )
            value["binding_digest"] = self.binding.digest
            path.write_bytes(json_bytes(value))
        self.trusted = {
            sha256(path.read_bytes())
            for path in (self.source_report, self.smoke_report)
        }
        return details

    def test_current_authenticated_source_binds_native_preferred_coverage(self):
        self.current_source()
        observed = self.validate()
        self.assertEqual(observed.images, self.images)
        self.assertEqual(self.calls, ["corresponding-source", "final-image-smoke"])
        original = self.source_report.read_bytes()
        for key, value in (
            ("preferred_form_verified", False),
            ("preferred_form_verified", "true"),
            ("runtime_inventory_sha256", digest("a")),
            ("tested_config", digest("a")),
            ("archive_sha256", digest("a")),
            ("notice_inventory_digest", digest("a")),
            ("runtime_source_asset", "psst.zip-v1.2.3-runtime-sources-arm64.tar.gz"),
        ):
            with self.subTest(key=key, value=value):
                record = json.loads(original)
                record["details"]["coverage"]["backend-amd64"][key] = value
                self.source_report.write_bytes(json_bytes(record))
                self.trusted = {
                    sha256(path.read_bytes())
                    for path in (self.source_report, self.smoke_report)
                }
                with self.assertRaises(InvalidRelease):
                    self.validate()
        self.source_report.write_bytes(original)
        self.trusted = {
            sha256(path.read_bytes())
            for path in (self.source_report, self.smoke_report)
        }
        self.trusted.remove(sha256(original))
        with self.assertRaisesRegex(InvalidRelease, "authenticated"):
            self.validate()

    def planned_foundation(self):
        """Real saved/OCI/Git/bundle bytes; transfer policy has its own Go tests."""
        version = self.context.version
        assets = {name: digest("f") for name in recovery.source_asset_names(version)}
        assets.update(
            {
                self.manifest.name: sha256(self.manifest.read_bytes()),
                self.bundle.name: sha256(self.bundle.read_bytes()),
            }
        )
        facts = {}
        for platform in PLATFORMS:
            arch = platform.split("/")[1]
            native = self.natives[platform].parent
            tree = native.parent
            descriptor = self.descriptor_values[platform]
            for component in ("backend", "web"):
                self.archive(
                    native / "export" / (component + "-" + arch + ".oci.tar"),
                    self.oci_files[arch, component],
                )
            self.archive(
                native / "final-images.docker.tar",
                {name: raw for (a, name), raw in self.saved_files.items() if a == arch},
            )
            names = {
                "build_record": "build-record.json",
                "native_measurement": "native-measurement.json",
                "smoke_report": "smoke-report.json",
                "source_verification": "source-completeness-verification.json",
                "runtime_pack": "pack/source-inventory.json",
                "runtime_source": "pack/psst.zip-"
                + version
                + "-runtime-sources-"
                + arch
                + ".tar.gz",
                "browser_inputs": "browser/browser-inputs.tar",
                "browser_verification": "browser/browser-verification.json",
                "original_archive": "original/pair.tar",
            }
            for key, name in names.items():
                path = native / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes((key + " opaque fixture").encode())
                descriptor["artifacts"][key] = recovery.file_record(path, native)
            self.natives[platform].write_bytes(json_bytes(descriptor))
            extras = {
                "source-scans/source-scan-measurement.json": b"{}",
                "application-dependencies/source-inventory.json": b"{}",
                "application-dependencies/psst.zip-dependency-inputs-"
                + version
                + "-"
                + arch
                + ".tar.gz": b"module offering",
                "source-scans/empty.stderr": b"",
            }
            for name, raw in extras.items():
                path = tree / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(raw)
            files = {
                path.relative_to(tree).as_posix(): {
                    "sha256": sha256(path.read_bytes()),
                    "size": path.stat().st_size,
                }
                for path in tree.rglob("*")
                if path.is_file()
            }
            metadata = {
                "schema_version": 1,
                "kind": "native-candidate-transfer",
                "source": descriptor["source"],
                "source_kind": "planned-main-dispatch",
                "files": files,
                "publication_authorized": False,
                "measurement_authentication_required": True,
            }
            (tree / "candidate-transfer.json").write_bytes(json_bytes(metadata))
            input_paths = {
                "build_record": "native/build-record.json",
                "measurement": "native/native-measurement.json",
                "source_scan": "source-scans/source-scan-measurement.json",
                "browser_verification": "native/browser/browser-verification.json",
                "browser_capture": "native/browser/browser-inputs.tar",
                "module_inventory": "application-dependencies/source-inventory.json",
                "runtime_inventory": "native/pack/source-inventory.json",
            }
            facts[platform] = {
                "inputs": {
                    key: files[name]["sha256"] for key, name in input_paths.items()
                },
                "tested_configs": descriptor["tested_configs"],
                "images": {c: self.images[c + "-" + arch] for c in ("backend", "web")},
                "source_scan": {},
                "browser": {},
                "preferred_sources": {},
            }
            for name in (
                "psst.zip-" + version + "-runtime-sources-" + arch + ".tar.gz",
                "psst.zip-dependency-inputs-" + version + "-" + arch + ".tar.gz",
            ):
                prefix = (
                    "native/pack/"
                    if "runtime-sources" in name
                    else "application-dependencies/"
                )
                assets[name] = files[prefix + name]["sha256"]
        subjects = image_subjects(json.loads(self.manifest.read_bytes()))
        subjects.update(
            {
                (
                    "manifest"
                    if name == self.manifest.name
                    else "bundle"
                    if name == self.bundle.name
                    else "source:" + name
                ): "file:" + name + "@" + value
                for name, value in assets.items()
            }
        )
        binding = Binding(
            self.context.repository,
            version,
            self.commit,
            tuple(sorted(subjects.items())),
        )
        foundation = {
            "schema_version": 2,
            "kind": "planned-candidate-inputs",
            "repository": self.context.repository,
            "version": version,
            "source_commit": self.commit,
            "source_kind": "planned-main-dispatch",
            "binding_sha256": binding.digest,
            "assets": assets,
            "subjects": subjects,
            "inputs": {"candidate_bases": digest("a"), "common_inventory": digest("b")},
            "native": facts,
            "tagged_source_ci_gate_verified": False,
            "signer_identity_verified": False,
            "publication_authorized": False,
            "measurement_authentication_required": True,
        }
        path = self.prepared / "release-inputs.json"
        path.write_bytes(json_bytes(foundation))

        def verified_transfer(context, tree, source_kind):
            self.assertEqual(source_kind, "planned-main-dispatch")
            value = json.loads((tree / "candidate-transfer.json").read_bytes())
            self.assertEqual(value["source"], context.checked())
            return value

        return path, verified_transfer

    def test_planned_v2_requires_parent_assembly_and_binds_all_transferred_bytes(self):
        path, transfer = self.planned_foundation()
        args = {
            "natives": self.natives,
            "manifest_path": self.manifest,
            "bundle": self.bundle,
            "root": self.root,
        }
        with self.assertRaisesRegex(InvalidRelease, "parent assembly"):
            recovery.validate_inputs(self.context, **args)
        with patch(
            "prepare_candidate_transfer.verify_native", side_effect=transfer
        ) as verify:
            # The Go transfer seam is injected here; actual saved layer checks
            # execute, and no historical browser/runtime replay may execute.
            with patch.object(
                recovery, "runtime_inputs", side_effect=AssertionError("runtime replay")
            ):
                inputs = recovery.validate_inputs(
                    self.context, parent_foundation=path, **args
                )
            self.assertEqual(verify.call_count, 2)
            self.assertEqual(inputs.images, self.images)
            empty = (
                self.natives[PLATFORMS[1]].parent.parent / "source-scans/empty.stderr"
            )
            empty.write_bytes(b"changed")
            with self.assertRaisesRegex(InvalidRelease, "changed"):
                inputs.unchanged()
            empty.write_bytes(b"")
            original = path.read_bytes()
            value = json.loads(original)
            value["native"][PLATFORMS[1]]["inputs"]["browser_capture"] = digest("a")
            path.write_bytes(json_bytes(value))
            with self.assertRaisesRegex(InvalidRelease, "hashes"):
                recovery.validate_inputs(self.context, parent_foundation=path, **args)
            value = json.loads(original)
            value.update(kind="prepared-release-inputs", source_kind="version-tag")
            path.write_bytes(json_bytes(value))
            with self.assertRaisesRegex(InvalidRelease, "tags require authenticated"):
                recovery.validate_inputs(self.context, parent_foundation=path, **args)

    def test_small_recovery_binding_accepts_current_authenticated_source_only(self):
        path, _ = self.planned_foundation()
        foundation = json.loads(path.read_bytes())
        foundation.update(kind="prepared-release-inputs", source_kind="version-tag")
        path.write_bytes(json_bytes(foundation))
        details = self.current_source()
        self.binding = Binding(
            self.context.repository,
            self.context.version,
            self.commit,
            tuple(sorted(foundation["subjects"].items())),
        )
        details["source_subjects"] = {
            key: value
            for key, value in foundation["subjects"].items()
            if key.startswith("source:")
        }
        source = json.loads(self.source_report.read_bytes())
        source.update(binding_digest=self.binding.digest, details=details)
        self.source_report.write_bytes(json_bytes(source))
        for component in ("backend", "web"):
            (self.prepared / (component + "-index.json")).unlink()
        args = SimpleNamespace(
            root=self.root,
            prepared=self.prepared,
            repository=self.context.repository,
            version=self.context.version,
            commit=self.commit,
            source_report=self.source_report,
        )
        auth = ExactAttemptAuthenticator(
            self.binding, [self.source_report.read_bytes()]
        )
        with patch.dict(
            sys.modules,
            {
                "generate_corresponding_source_review": None,
                "measure_browser_source_inventory": None,
                "measure_native_browser_inputs": None,
            },
        ):
            observed, _, names, _ = aggregation.authenticated_command_inputs(args, auth)
        self.assertEqual(observed, self.binding)
        self.assertEqual(names, {path.name, self.manifest.name, self.bundle.name})
        auth.records.clear()
        with self.assertRaisesRegex(InvalidRelease, "authenticated"):
            aggregation.authenticated_command_inputs(args, auth)

    def test_one_canonical_source_result_drives_native_recovery_projection(self):
        import publish_container_release as publication

        source = json.loads(self.source_report.read_bytes())["details"]
        smoke = json.loads(self.smoke_report.read_bytes())["details"]
        checks = {gate: {} for gate in publication.GATES - {"upgrade-recovery"}}
        checks.update(
            {
                "corresponding-source": source,
                "final-image-smoke": smoke,
                "source-ci": {"run_id": 123, "run_attempt": 2},
            }
        )
        result = {
            "schema_version": 1,
            "kind": "container-release-result",
            "repository": self.binding.repository,
            "version": self.binding.version,
            "commit": self.binding.commit,
            "binding_digest": self.binding.digest,
            "subjects": dict(self.binding.subjects),
            "run_id": 123,
            "attempt": 2,
            "checks": checks,
            "inputs": {"corresponding-source-replay": {"sha256": digest("e")}},
            "platforms": {
                p: {"execution": "native", "checks": ["final-image-smoke"]}
                for p in PLATFORMS
            },
        }
        self.source_report.write_bytes(json_bytes(result))
        self.smoke_report = self.source_report
        self.trusted = {sha256(self.source_report.read_bytes())}

        def verify(gate, path, binding):
            require(
                binding == self.binding and sha256(path.read_bytes()) in self.trusted,
                "No exact-attempt authenticated evidence",
            )
            return VerifiedEvidence(
                gate,
                binding.digest,
                sha256(path.read_bytes()),
                True,
                result["checks"][gate],
            )

        with patch.object(self, "verify", side_effect=verify):
            observed = self.validate()
            self.assertEqual(observed.images, self.images)
            observed.unchanged()

    def test_projection_uses_signed_other_platform_facts_and_selected_real_bytes(self):
        inputs = self.validate()
        self.assertEqual(self.calls, ["corresponding-source", "final-image-smoke"])
        self.assertEqual(inputs.images, self.images)
        self.assertEqual(inputs.paths[PLATFORMS[1]], {})
        self.assertEqual(
            set(p.name for p in self.prepared.iterdir()),
            {
                "release-manifest.json",
                self.bundle.name,
                "backend-index.json",
                "web-index.json",
            },
        )
        inputs.unchanged()
        original = self.smoke_report.read_bytes()
        self.smoke_report.write_bytes(self.smoke_report.read_bytes() + b"\n")
        with self.assertRaisesRegex(InvalidRelease, "changed"):
            inputs.unchanged()
        self.smoke_report.write_bytes(original)
        verify = self.verify

        def change_after_authentication(gate, path, binding):
            receipt = verify(gate, path, binding)
            if gate == "final-image-smoke":
                self.source_report.write_bytes(self.source_report.read_bytes() + b"\n")
            return receipt

        with patch.object(self, "verify", side_effect=change_after_authentication):
            with self.assertRaisesRegex(InvalidRelease, "changed"):
                self.validate()

    def test_projection_refuses_unsigned_reports_and_changed_binding(self):
        self.trusted.remove(sha256(self.source_report.read_bytes()))
        with self.assertRaisesRegex(InvalidRelease, "authenticated"):
            self.validate()
        self.trusted.add(sha256(self.source_report.read_bytes()))
        value = json.loads(self.source_report.read_bytes())
        value["details"]["source_subjects"]["source:fixture.tar.gz"] = (
            "file:fixture.tar.gz@" + digest("e")
        )
        self.source_report.write_bytes(json_bytes(value))
        with self.assertRaisesRegex(InvalidRelease, "stale"):
            self.validate()

    def test_projection_selects_native_arm_images_and_rejects_partial_authentication(
        self,
    ):
        self.select(PLATFORMS[1])
        inputs = self.validate()
        self.assertEqual(inputs.paths[PLATFORMS[0]], {})
        self.assertEqual(
            set(inputs.paths[PLATFORMS[1]]),
            {"final_archive", "backend_archive", "web_archive"},
        )
        for arguments in (
            {"source_report": self.source_report},
            {"source_report": self.source_report, "smoke_report": self.smoke_report},
            {"verifier": self},
        ):
            with (
                self.subTest(arguments=tuple(arguments)),
                patch.object(recovery, "native_platform", return_value=PLATFORMS[1]),
                patch.object(
                    recovery,
                    "validate_inputs",
                    side_effect=AssertionError("unsigned replay"),
                ),
                patch.object(
                    recovery,
                    "validate_authenticated_inputs",
                    side_effect=AssertionError("partial signed replay"),
                ),
            ):
                execute = unittest.mock.Mock(
                    side_effect=AssertionError("Docker execution")
                )
                with self.assertRaisesRegex(InvalidRelease, "both reports"):
                    recovery.measure_recovery(
                        self.context,
                        natives=self.natives,
                        manifest=self.manifest,
                        bundle=self.bundle,
                        previous_source="a" * 40,
                        output=self.folder / "terminal.json",
                        root=self.root,
                        execute=execute,
                        **arguments,
                    )
                execute.assert_not_called()

    def test_projection_refuses_changed_descriptors_oci_and_missing_inputs(self):
        descriptor_path = self.natives[PLATFORMS[1]]
        original = descriptor_path.read_bytes()
        descriptor = json.loads(original)
        descriptor["tested_configs"]["web"] = digest("a")
        descriptor_path.write_bytes(json_bytes(descriptor))
        with self.assertRaisesRegex(InvalidRelease, "configurations"):
            self.validate()
        descriptor_path.write_bytes(original)
        extra = descriptor_path.parent / "unexpected.tar"
        extra.write_bytes(b"unrequested payload")
        with self.assertRaisesRegex(InvalidRelease, "projection input"):
            self.validate()
        extra.unlink()
        index = self.prepared / "web-index.json"
        index_raw = index.read_bytes()
        index.unlink()
        with self.assertRaises((OSError, InvalidRelease)):
            self.validate()
        index.write_bytes(index_raw)
        local = self.natives[PLATFORMS[0]].parent / "export/backend-amd64.oci.tar"
        original = local.read_bytes()
        local.write_bytes(original + b"bad")
        with self.assertRaisesRegex(InvalidRelease, "changed"):
            self.validate()
        local.write_bytes(original)
        local.unlink()
        with self.assertRaises((OSError, InvalidRelease)):
            self.validate()

    def test_projection_checks_saved_layer_bytes_even_with_rehashed_descriptor(self):
        arch = "amd64"
        files = {name: raw for (a, name), raw in self.saved_files.items() if a == arch}
        files["backend-layer.tar"] = b"tampered loaded layer"
        native = self.natives[PLATFORMS[0]].parent
        final = native / "final-images.docker.tar"
        self.archive(final, files)
        descriptor = json.loads(self.natives[PLATFORMS[0]].read_bytes())
        descriptor["artifacts"]["final_archive"] = recovery.file_record(final, native)
        self.natives[PLATFORMS[0]].write_bytes(json_bytes(descriptor))
        with self.assertRaisesRegex(InvalidRelease, "Saved layer differs"):
            self.validate()


if __name__ == "__main__":
    unittest.main()
