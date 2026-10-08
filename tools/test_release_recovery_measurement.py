"""Recovery input/fact boundaries; Docker execution is mocked in these unit tests.

Git, bundle, Docker-save layers, four OCI graphs and index bytes are real
disposable fixtures. Structured experiment records are deliberately simulated;
they do not establish a live Docker, migration, provenance or backup-provider gate.
"""

from __future__ import annotations

import copy
from datetime import datetime, timezone
import gzip
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import assemble_release_oci as oci
import generate_release_gate_reports as reports
import measure_release_recovery as recovery
from prepare_native_release import file_record
from prepare_release_candidate import BASES
from release_artifacts import (
    InvalidRelease,
    PLATFORMS,
    REQUIRED_FILES,
    build_bundle,
    json_bytes,
    read_json,
)
from test_release_artifacts import manifest as fixture_manifest
from test_release_oci import fixture as oci_fixture
from test_native_release_preparation import write_tar


def digest(character):
    return "sha256:" + character * 64


def stamp():
    return datetime.now(timezone.utc).isoformat()


class RecoveryMeasurements(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(
            prefix="psst-recovery-boundary-test-"
        )
        self.addCleanup(self.temporary.cleanup)
        self.folder = Path(self.temporary.name)
        self.root = self.folder / "repo"
        self.root.mkdir()
        self.git("init", "-q")
        self.git("config", "user.name", "Recovery fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        self.git("config", "core.hooksPath", os.devnull)
        for name in REQUIRED_FILES | {"backend/licenses/example/LICENSE"}:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("Fixture " + name + "\n")
        for name in recovery.FIXTURE_FILES + ("deploy/update.py",):
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(recovery.ROOT / name, target)
        self.git("add", ".")
        self.git("commit", "-qm", "Historical fixture, not a live migration result")
        self.previous = self.git("rev-parse", "HEAD").decode().strip()
        (self.root / "candidate").write_text("Candidate fixture\n")
        self.git("add", ".")
        self.git("commit", "-qm", "Candidate fixture")
        self.commit = self.git("rev-parse", "HEAD").decode().strip()
        self.context = reports.NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", self.commit, "linux/amd64"
        )
        self.bases = {
            key: value.rsplit(":", 1)[0] + "@" + digest("1")
            for key, value in BASES.items()
        }
        self.natives, self.archives, self.configs = {}, {}, {}
        for platform in PLATFORMS:
            self.native(platform)
        assembled = self.folder / "assembled"
        built = oci.assemble(
            self.archives,
            self.configs,
            repository=self.context.repository,
            version=self.context.version,
            commit=self.commit,
            output=assembled,
        )
        self.bundle = build_bundle(
            self.root, assembled, self.context.version, "deployment-ready"
        )
        self.manifest = fixture_manifest(self.commit)
        self.manifest["payload_profile"] = "deployment-ready"
        self.manifest["images"] = built["images"]
        self.manifest["build"]["base_images"] = self.bases
        self.manifest["bundle"]["sha256"] = file_record(self.bundle)["sha256"][7:]
        self.manifest_path = assembled / "release-manifest.json"
        self.manifest_path.write_bytes(json_bytes(self.manifest))
        self.output = self.folder / "recovery-measurement.json"
        self.args = dict(
            natives=self.natives,
            manifest=self.manifest_path,
            bundle=self.bundle,
            previous_source=self.previous,
            output=self.output,
            root=self.root,
            allow_legacy_browser=True,
        )
        self.policy = recovery.flow_policy(self.root / "deploy/update.py")

    def git(self, *args):
        return subprocess.run(
            ["git", "-C", str(self.root), *args],
            check=True,
            capture_output=True,
            env={
                **os.environ,
                "GIT_CONFIG_GLOBAL": os.devnull,
                "GIT_CONFIG_NOSYSTEM": "1",
            },
        ).stdout

    def native(self, platform):
        arch = platform.split("/")[1]
        root = self.folder / ("native-" + arch)
        root.mkdir()
        saved, saved_manifest, images, configs = {}, [], {}, {}
        for component in ("backend", "web"):
            files, old_config = oci_fixture(component, arch)
            index = read_json(files["index.json"])
            old_manifest = index["manifests"][0]["digest"]
            image = read_json(files.pop("blobs/sha256/" + old_manifest[7:]))
            config = read_json(files.pop("blobs/sha256/" + old_config[7:]))
            config["config"]["Labels"][
                "org.opencontainers.image.revision"
            ] = self.commit
            config["config"]["Labels"]["zip.psst.source.archive"] = (
                "https://github.com/endorses/psst.zip/archive/"
                + self.commit
                + ".tar.gz"
            )
            raw = json_bytes(config)
            identity = "sha256:" + hashlib.sha256(raw).hexdigest()
            configs[component] = identity
            image["config"].update(digest=identity, size=len(raw))
            image_raw = json_bytes(image)
            child = "sha256:" + hashlib.sha256(image_raw).hexdigest()
            index["manifests"][0].update(digest=child, size=len(image_raw))
            files["index.json"] = json_bytes(index)
            files["blobs/sha256/" + identity[7:]] = raw
            files["blobs/sha256/" + child[7:]] = image_raw
            archive = root / (component + ".oci.tar")
            write_tar(archive, files)
            key = component + "-" + arch
            self.archives[key], self.configs[key] = archive, identity
            images[component] = oci.inspect_archive(
                archive,
                platform=platform,
                repository=self.context.repository,
                version=self.context.version,
                commit=self.commit,
                tested_config=identity,
                component=component,
            )
            config_name = identity[7:] + ".json"
            layer_name = component + "/layer.tar"
            saved[config_name] = raw
            saved[layer_name] = gzip.decompress(
                files["blobs/sha256/" + image["layers"][0]["digest"][7:]]
            )
            saved_manifest.append(
                {
                    "Config": config_name,
                    "RepoTags": [component + ":fixture"],
                    "Layers": [layer_name],
                }
            )
        saved["manifest.json"] = json_bytes(saved_manifest)
        final_archive = root / "final.docker.tar"
        write_tar(final_archive, saved)
        pack = root / "pack"
        pack.mkdir()
        source = pack / ("runtime-" + arch + ".tar.gz")
        source.write_bytes(
            gzip.compress(b"Fixture source, not distribution approval", mtime=0)
        )
        record = {
            "schema_version": 1,
            "version": self.context.version,
            "revision": self.commit,
            "architecture": arch,
            "source_asset": {
                "file": source.name,
                "sha256": file_record(source)["sha256"][7:],
                "size": source.stat().st_size,
                "url": "https://github.com/endorses/psst.zip/releases/download/v1.2.3/"
                + source.name,
            },
            "overlays": {
                component: {
                    name: "a" * 64
                    for name in (
                        "THIRD_PARTY_NOTICES.txt",
                        "runtime-inventory.json",
                        "SOURCE.txt",
                    )
                }
                for component in ("backend", "web")
            },
        }
        pack_path = pack / "runtime-pack.json"
        pack_path.write_bytes(json_bytes(record))
        context = reports.NativeSourceContext(
            self.context.repository, self.context.version, self.commit, platform
        )
        _, runtime = reports.runtime_inputs(context, pack)
        smoke = {
            "schema_version": 1,
            "kind": "release-image-smoke",
            "version": self.context.version,
            "revision": self.commit,
            "platform": platform,
            "execution": "native",
            "tested_configs": configs,
            "runtime_pack_sha256": runtime["runtime_pack_sha256"],
            "checks": sorted(reports.CHECKS),
            "completed_at": stamp(),
            "publication_authorized": False,
        }
        smoke_path = root / "smoke.json"
        smoke_path.write_bytes(json_bytes(smoke))
        measurement_path = root / "native-measurement.json"
        measurement_path.write_bytes(
            json_bytes(
                {
                    "schema_version": 2,
                    "kind": "native-release-measurement",
                    "source": context.checked(),
                    "smoke": smoke,
                    "images": images,
                    "runtime": runtime,
                    "publication_authorized": False,
                }
            )
        )
        build = root / "build-record.json"
        build.write_bytes(
            json_bytes(
                {
                    "schema_version": 1,
                    "kind": "release-candidate",
                    "candidate_only": True,
                    "version": self.context.version,
                    "source_commit": self.commit,
                    "platforms": PLATFORMS,
                    "base_images": self.bases,
                    "base_platform_digests": {
                        key: {"linux/amd64": digest("2"), "linux/arm64": digest("3")}
                        for key in BASES
                    },
                    "checked_platform": platform,
                    "native_execution": True,
                    "toolchain_output": {
                        "go": "go version go"
                        + BASES["golang"].rsplit(":", 1)[1].split("-", 1)[0]
                        + " "
                        + platform,
                        "node": "v" + BASES["node"].rsplit(":", 1)[1].split("-", 1)[0],
                        "docker": "29.8.2",
                        "compose": "5.6.0",
                        "buildx": "v0.37.1",
                        "builder": "Fixture",
                    },
                    "build_metadata": {
                        c: {
                            "containerimage.config.digest": d,
                            "containerimage.digest": d,
                        }
                        for c, d in configs.items()
                    },
                    "image_archive": {
                        "name": final_archive.name,
                        "sha256": file_record(final_archive)["sha256"][7:],
                        "size": final_archive.stat().st_size,
                    },
                    "limitations": [
                        "Mocked source/smoke execution, not release approval"
                    ],
                }
            )
        )
        source_verification = root / "source-verification.json"
        source_verification.write_bytes(
            json_bytes(
                {
                    "schema_version": 1,
                    "kind": "runtime-source-completeness",
                    "repository": self.context.repository,
                    "version": self.context.version,
                    "revision": self.commit,
                    "platform": platform,
                    "runtime_source_inputs_verified": True,
                    "distribution_authorized": False,
                    "runtime_pack_sha256": runtime["runtime_pack_sha256"],
                    "runtime_source_asset_sha256": runtime["source_asset"]["digest"],
                    "native_smoke_report_sha256": file_record(smoke_path)["sha256"],
                    "images": images,
                }
            )
        )
        paths = {
            "build_record": build,
            "original_archive": final_archive,
            "final_archive": final_archive,
            "native_measurement": measurement_path,
            "smoke_report": smoke_path,
            "source_verification": source_verification,
            "runtime_pack": pack_path,
            "runtime_source": source,
            "backend_archive": self.archives["backend-" + arch],
            "web_archive": self.archives["web-" + arch],
        }
        descriptor = {
            "schema_version": 1,
            "kind": "native-release-artifacts",
            "source": context.checked(),
            "original_tested_configs": configs,
            "tested_configs": configs,
            "source_helper_config": digest("7"),
            "oci_exporter": "docker-save-byte-preserving-oci-v1",
            "artifacts": {key: file_record(path, root) for key, path in paths.items()},
            "publication_authorized": False,
            "measurement_authentication_required": True,
        }
        descriptor_path = root / "native-artifacts.json"
        descriptor_path.write_bytes(json_bytes(descriptor))
        self.natives[platform] = descriptor_path

    def validate(self, **overrides):
        return recovery.validate_inputs(
            self.context,
            natives=self.natives,
            manifest_path=self.manifest_path,
            bundle=self.bundle,
            root=self.root,
            allow_legacy_browser=overrides.pop("allow_legacy_browser", True),
            **overrides,
        )

    def experiment(self, *, failure=False, paused=False, **unused):
        # Explicitly simulated facts for validation boundaries, never live proof.
        first = "20261007T010101Z-" + "a" * 12
        last = first if failure else "20261007T010102Z-" + "b" * 12
        configs = {
            component: self.configs[component + "-amd64"]
            for component in ("backend", "web")
        }

        def observation(stage, identity, restoring=False):
            failed = stage == "post-startup-failure"
            version = "v0.0.0" if restoring and failure else self.context.version
            checks = self.policy[0] | (self.policy[1] if restoring else set())
            return {
                "stage": stage,
                "transaction": identity,
                "version": self.context.version,
                "active_version": None if failed else version,
                "previous_version": (
                    "v0.0.0"
                    if failure or stage == "candidate-activation"
                    else self.context.version
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
                            name: json.dumps({"fixture_status": 200}) for name in checks
                        },
                        "observed_at": stamp(),
                        "version": version,
                        "source_commit": (
                            "checkpoint:" + identity if restoring else self.commit
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

        observations = (
            [observation("post-startup-failure", first)]
            if failure
            else [
                observation("candidate-activation", first),
                observation("repeat-activation", last),
            ]
        )
        observations.append(observation("isolated-restore-activation", last, True))
        return {
            "schema_version": 1,
            "kind": "disposable-updater-experiment",
            "scenario": "post-startup-failure" if failure else "normal",
            "candidate": {
                "version": self.context.version,
                "commit": self.commit,
                "configs": configs,
            },
            "baseline": {
                "version": "v0.0.0",
                "commit": self.previous,
                "configs": {"backend": digest("8"), "web": digest("9")},
            },
            "prior_pause": paused,
            "repeat_mode": "same-exact-candidate",
            "observations": observations,
            "startup_observations": [
                {
                    "transaction": item["transaction"],
                    "restoring": item["restoring"],
                    "observed_at": stamp(),
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
                    "encrypted_off_host_receipt": "fixture separated-store " + "d" * 64,
                    "restore_exercise": "fixture decrypt readback " + "e" * 64,
                    "verified_at": stamp(),
                },
            },
            "restore": {
                "volume_mapping": {
                    name: "new-" + name
                    for name in ("data", "caddy-data", "caddy-config")
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
            "completed_at": stamp(),
            "acquisition": "fixture-local-exact-loaded-configs",
            "backup_provider": "same-host-separated-store-encryption-simulation",
            "public_provenance_verified": False,
            "off_host_provider_verified": False,
            "publication_authorized": False,
            "nested_tools": {
                "docker": "29.8.2",
                "compose": "5.6.0",
                "architecture": "x86_64",
            },
        }

    def command(self, args, **kwargs):
        return b"x86_64\n" if "info" in args else b"29.8.2\n"

    def test_replay_requires_real_four_oci_graphs_layers_index_and_bundle(self):
        actual = self.validate()
        self.assertEqual(len(actual.images), 4)
        self.assertEqual(actual.context, self.context)
        destination = self.folder / "unpacked"
        recovery.extract_bundle(actual, destination)
        self.assertEqual(
            (destination / "deploy/update.py").read_bytes(),
            (self.root / "deploy/update.py").read_bytes(),
        )

    def test_missing_peer_emulation_wrong_source_or_pair_never_reaches_docker(self):
        original = read_json(self.natives["linux/arm64"].read_bytes())
        for mutate in [
            lambda v: v["source"].update(commit="a" * 40),
            lambda v: v.update(publication_authorized=True),
            lambda v: v["tested_configs"].update(backend=digest("b")),
            lambda v: v["artifacts"]["final_archive"].update(file="../escape"),
            lambda v: v["artifacts"]["runtime_pack"].update(size=True),
        ]:
            value = copy.deepcopy(original)
            mutate(value)
            self.natives["linux/arm64"].write_bytes(json_bytes(value))
            with self.assertRaises(InvalidRelease), patch.object(
                recovery.integration, "execute_experiment"
            ) as execute:
                recovery.measure_recovery(
                    self.context, **self.args, execute=self.command
                )
            execute.assert_not_called()
            self.assertFalse(self.output.exists())
        self.natives["linux/arm64"].write_bytes(json_bytes(original))
        self.args["natives"] = {"linux/amd64": self.natives["linux/amd64"]}
        with self.assertRaisesRegex(InvalidRelease, "both retained"), patch.object(
            recovery.integration, "execute_experiment"
        ) as execute:
            recovery.measure_recovery(self.context, **self.args, execute=self.command)
        execute.assert_not_called()

    def test_rehashed_saved_payload_substitution_still_fails_actual_diff_ids(self):
        path = self.natives["linux/amd64"]
        descriptor = read_json(path.read_bytes())
        saved = path.parent / descriptor["artifacts"]["final_archive"]["file"]
        with tarfile.open(saved) as archive:
            files = {
                member.name: archive.extractfile(member).read()
                for member in archive
                if member.isfile()
            }
        files["backend/layer.tar"] = b"substituted payload"
        write_tar(saved, files)
        for name in ("final_archive", "original_archive"):
            descriptor["artifacts"][name] = file_record(saved, path.parent)
        build_path = path.parent / descriptor["artifacts"]["build_record"]["file"]
        build = read_json(build_path.read_bytes())
        build["image_archive"].update(
            sha256=file_record(saved)["sha256"][7:], size=saved.stat().st_size
        )
        build_path.write_bytes(json_bytes(build))
        descriptor["artifacts"]["build_record"] = file_record(build_path, path.parent)
        path.write_bytes(json_bytes(descriptor))
        with self.assertRaisesRegex(InvalidRelease, "Saved layer differs"):
            self.validate()

    def test_emulated_smoke_and_stale_bundle_or_index_fail_closed(self):
        path = self.natives["linux/amd64"]
        descriptor = read_json(path.read_bytes())
        measurement_path = (
            path.parent / descriptor["artifacts"]["native_measurement"]["file"]
        )
        measurement = read_json(measurement_path.read_bytes())
        measurement["smoke"]["execution"] = "emulated"
        measurement_path.write_bytes(json_bytes(measurement))
        descriptor["artifacts"]["native_measurement"] = file_record(
            measurement_path, path.parent
        )
        path.write_bytes(json_bytes(descriptor))
        with self.assertRaises(InvalidRelease):
            self.validate()
        self.bundle.write_bytes(self.bundle.read_bytes() + b"changed")
        with self.assertRaisesRegex(InvalidRelease, "bundle checksum"):
            self.validate()

    def test_rehashed_index_metadata_and_unrelated_runtime_source_are_refused(self):
        index_path = self.manifest_path.parent / "backend-index.json"
        original = index_path.read_bytes()
        index = read_json(original)
        index["manifests"][0]["size"] += 1
        index_path.write_bytes(json_bytes(index))
        self.manifest["images"]["backend"]["index"] = (
            "ghcr.io/endorses/psst-zip-backend@" + file_record(index_path)["sha256"]
        )
        self.manifest_path.write_bytes(json_bytes(self.manifest))
        with self.assertRaisesRegex(InvalidRelease, "index child metadata"):
            self.validate()
        index_path.write_bytes(original)
        self.manifest["images"]["backend"]["index"] = (
            "ghcr.io/endorses/psst-zip-backend@" + file_record(index_path)["sha256"]
        )
        self.manifest_path.write_bytes(json_bytes(self.manifest))
        path = self.natives["linux/amd64"]
        descriptor = read_json(path.read_bytes())
        unrelated = path.parent / "unrelated-source.tar.gz"
        unrelated.write_bytes(b"Another hashed file is not the pack's source asset")
        descriptor["artifacts"]["runtime_source"] = file_record(unrelated, path.parent)
        path.write_bytes(json_bytes(descriptor))
        with self.assertRaisesRegex(InvalidRelease, "source descriptor differs"):
            self.validate()

    def test_dirty_execution_helper_is_refused_before_any_docker_command(self):
        helper = self.root / "tools/fixtures/release-updater/controller.py"
        helper.write_bytes(helper.read_bytes() + b"\n# different current helper\n")
        with patch.object(
            recovery, "native_platform", return_value="linux/amd64"
        ), patch.object(
            recovery.integration, "execute_experiment"
        ) as experiment, self.assertRaisesRegex(
            InvalidRelease, "helper differs"
        ):
            recovery.measure_recovery(self.context, **self.args, execute=self.command)
        experiment.assert_not_called()
        self.assertFalse(self.output.exists())

    def test_rehashed_bundle_code_config_and_optional_license_substitution_are_refused(
        self,
    ):
        original = self.bundle.read_bytes()
        with tarfile.open(self.bundle, "r:gz") as archive:
            original_files = {
                member.name: archive.extractfile(member).read() for member in archive
            }
        for name in (
            "deploy/update.py",
            "tools/release_artifacts.py",
            "deploy/compose.release.yml",
            "deploy/external-proxy/Caddyfile",
            "backend/licenses/example/LICENSE",
        ):
            for omitted in (
                (False, True) if name.endswith("example/LICENSE") else (False,)
            ):
                with self.subTest(name=name, omitted=omitted):
                    files = dict(original_files)
                    if omitted:
                        files.pop(name)
                    else:
                        files[name] += b"\n# Rehashed source substitution\n"
                    rebuilt = self.folder / "modified-bundle.tar"
                    write_tar(rebuilt, files)
                    self.bundle.write_bytes(
                        gzip.compress(rebuilt.read_bytes(), mtime=0)
                    )
                    self.manifest["bundle"]["sha256"] = file_record(self.bundle)[
                        "sha256"
                    ][7:]
                    self.manifest_path.write_bytes(json_bytes(self.manifest))
                    with patch.object(
                        recovery, "native_platform", return_value="linux/amd64"
                    ), patch.object(
                        recovery.integration, "execute_experiment"
                    ) as experiment, patch.object(
                        recovery, "command"
                    ) as docker, self.assertRaisesRegex(
                        InvalidRelease, "bundle (source differs|omits)"
                    ):
                        recovery.measure_recovery(
                            self.context, **self.args, execute=docker
                        )
                    experiment.assert_not_called()
                    docker.assert_not_called()
                    self.assertFalse(self.output.exists())
        self.bundle.write_bytes(original)

    def test_owned_cleanup_requires_bounded_success_and_verified_resource_absence(self):
        identity = "psst-update-gate-cleanup-fixture"
        image = identity + "-daemon"
        for fault in (
            None,
            "container-remove",
            "image-remove",
            "container-query",
            "timeout",
            "remains",
        ):
            with self.subTest(fault=fault):
                temp = self.folder / ("cleanup-" + str(fault))
                temp.mkdir()
                resources = {"container", "image"}
                calls = []

                def execute(args, **kwargs):
                    calls.append((args, kwargs))
                    self.assertEqual(kwargs["timeout"], 55)
                    self.assertTrue(kwargs["capture_output"])
                    kind = "container" if args[1] in {"container", "rm"} else "image"
                    removal = args[1] == "rm" or args[1:3] == ("image", "rm")
                    if fault == "timeout" and removal and kind == "container":
                        raise subprocess.TimeoutExpired(args, 55)
                    if (
                        fault == kind + "-remove"
                        and removal
                        or fault == "container-query"
                        and not removal
                        and kind == "container"
                    ):
                        return subprocess.CompletedProcess(
                            args, 1, b"", b"dummy secret diagnostic"
                        )
                    if removal:
                        if fault != "remains" or kind != "container":
                            resources.discard(kind)
                        return subprocess.CompletedProcess(args, 0, b"removed\n", b"")
                    return subprocess.CompletedProcess(
                        args, 0, b"fixture-id\n" if kind in resources else b"", b""
                    )

                with patch.object(
                    recovery.integration.subprocess, "run", side_effect=execute
                ):
                    if fault:
                        with self.assertRaisesRegex(
                            RuntimeError, "no success measurement"
                        ) as failure:
                            recovery.integration.cleanup_owned(identity, [image], temp)
                        self.assertNotIn("dummy secret", str(failure.exception))
                    else:
                        recovery.integration.cleanup_owned(identity, [image], temp)
                        self.assertEqual(resources, set())
                self.assertFalse(temp.exists())
                self.assertTrue(any(args[1:3] == ("image", "rm") for args, _ in calls))

    def checked(self, record, failure=False, paused=False):
        return recovery.checked_experiment(
            record,
            context=self.context,
            previous=self.previous,
            configs={c: self.configs[c + "-amd64"] for c in ("backend", "web")},
            failure=failure,
            paused=paused,
            policy=self.policy,
            started="2026-10-07T00:00:00+00:00",
        )

    def test_actual_fact_validator_binds_restore_to_same_checkpoint_transaction(self):
        for failure, paused in [(False, False), (False, True), (True, False)]:
            self.checked(
                self.experiment(failure=failure, paused=paused), failure, paused
            )
        record = self.experiment()
        changes = [
            lambda v: v["schema"].update(original_after=10),
            lambda v: v["schema"].update(old_cli_rejected_migrated_original=False),
            lambda v: v["checkpoint"]["records"].update(image=1),
            lambda v: v["checkpoint"].update(corrupt_archive_refused=False),
            lambda v: v["restore"]["volume_mapping"].update(data="data"),
            lambda v: v["restore"].update(restored_certificate_sha256="f" * 64),
            lambda v: v.update(public_provenance_verified=True),
            lambda v: v["observations"][-1].update(
                transaction="20261007T020202Z-" + "c" * 12
            ),
            lambda v: v["observations"][-1]["verification"].update(
                source_commit=self.commit
            ),
            lambda v: v["observations"][0]["verification"]["checks"].pop(
                next(iter(self.policy[0]))
            ),
            lambda v: v["candidate"]["configs"].update(web=digest("f")),
            lambda v: v["nested_tools"].update(architecture="aarch64"),
            lambda v: v.update(completed_at="2026-01-01T00:00:00+00:00"),
        ]
        for mutate in changes:
            value = copy.deepcopy(record)
            mutate(value)
            with self.subTest(mutate=mutate), self.assertRaises(InvalidRelease):
                self.checked(value)

    def test_mocked_execution_runs_every_fixed_scenario_then_writes_unsigned_measurement(
        self,
    ):
        def simulated(**kwargs):
            candidate = kwargs["exact_candidate"]
            self.assertEqual(candidate.saved_pair.name, "final.docker.tar")
            self.assertEqual(
                (candidate.bundle_root / "deploy/update.py").read_bytes(),
                (self.root / "deploy/update.py").read_bytes(),
            )
            self.assertTrue(kwargs["require_schema_change"])
            return self.experiment(
                failure=kwargs["failure_after_start"], paused=kwargs["initially_paused"]
            )

        with patch.object(
            recovery, "native_platform", return_value="linux/amd64"
        ), patch.object(
            recovery.integration, "execute_experiment", side_effect=simulated
        ) as experiment:
            value = recovery.measure_recovery(
                self.context, **self.args, execute=self.command
            )
        self.assertEqual(experiment.call_count, 3)
        self.assertEqual(
            set(value["experiments"]), {s[0] for s in recovery.EXPERIMENTS}
        )
        self.assertFalse(value["public_provenance_verified"])
        self.assertFalse(value["off_host_provider_verified"])
        self.assertTrue(value["upgrade_recovery_gate_pending"])
        self.assertEqual(read_json(self.output.read_bytes()), value)
        for call in experiment.call_args_list:
            self.assertFalse(call.kwargs["exact_candidate"].bundle_root.exists())

    def test_failure_changed_inputs_and_wrong_daemon_never_emit_terminal_record(self):
        with patch.object(
            recovery, "native_platform", return_value="linux/amd64"
        ), patch.object(
            recovery.integration,
            "execute_experiment",
            side_effect=RuntimeError("fixture migration/flow failed"),
        ):
            with self.assertRaises(RuntimeError):
                recovery.measure_recovery(
                    self.context, **self.args, execute=self.command
                )
        self.assertFalse(self.output.exists())
        with patch.object(
            recovery, "native_platform", return_value="linux/amd64"
        ), patch.object(recovery.integration, "execute_experiment") as experiment:
            with self.assertRaisesRegex(InvalidRelease, "Docker daemon differs"):
                recovery.measure_recovery(
                    self.context, **self.args, execute=lambda *a, **kw: b"aarch64\n"
                )
            experiment.assert_not_called()

        def changed(**kwargs):
            actual = self.experiment(
                failure=kwargs["failure_after_start"], paused=kwargs["initially_paused"]
            )
            self.manifest_path.write_bytes(self.manifest_path.read_bytes() + b"\n")
            return actual

        with patch.object(
            recovery, "native_platform", return_value="linux/amd64"
        ), patch.object(
            recovery.integration, "execute_experiment", side_effect=changed
        ) as experiment, self.assertRaisesRegex(
            InvalidRelease, "inputs changed"
        ):
            recovery.measure_recovery(self.context, **self.args, execute=self.command)
        self.assertEqual(experiment.call_count, 1)
        self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
