"""Tiny CLI fixtures check typed binding/path/output boundaries without rereplays."""

from __future__ import annotations

import argparse
from contextlib import ExitStack
import copy
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import generate_corresponding_source_review as command
from publish_container_release import (
    Binding,
    PublicationInputs,
    SOURCE_COVERAGE,
    SOURCE_REVIEW_POLICY,
    VerifiedEvidence,
)
from release_artifacts import InvalidRelease, json_bytes, read_json


class CorrespondingSourceCommand(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="psst-source-command-test-")
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        prepared = root / "prepared"
        prepared.mkdir()
        for name in ("amd64", "arm64", "upstream"):
            (root / name).mkdir()
        self.args = argparse.Namespace(
            root=root,
            prepared=prepared,
            amd64_inputs=root / "amd64",
            arm64_inputs=root / "arm64",
            upstream=root / "upstream",
            repository="endorses/psst.zip",
            version="v0.1.0",
            commit="a" * 40,
            run_id=1234,
            run_attempt=2,
            output=root / "source-reports",
        )
        bundle = "psst.zip-deployment-v0.1.0.tar.gz"
        self.source = "psst.zip-source-v0.1.0.tar.gz"
        assets = {
            "release-manifest.json": json_bytes({"bundle": {"name": bundle}}),
            bundle: b"small mocked bundle",
            self.source: b"small mocked source",
        }
        for name, raw in assets.items():
            (prepared / name).write_bytes(raw)
        for name in ("oci-correspondence.json", "backend-index.json", "web-index.json"):
            (prepared / name).write_bytes(json_bytes({"tiny": name}))
        subjects = {
            "manifest": "file:release-manifest.json@"
            + command.sha256(assets["release-manifest.json"]),
            "bundle": "file:" + bundle + "@" + command.sha256(assets[bundle]),
            "source:"
            + self.source: "file:"
            + self.source
            + "@"
            + command.sha256(assets[self.source]),
        }
        self.binding = Binding(
            self.args.repository,
            self.args.version,
            self.args.commit,
            tuple(sorted(subjects.items())),
        )
        self.inputs = PublicationInputs(
            self.binding,
            {"bundle": {"name": bundle}},
            command.sha256(assets["release-manifest.json"]),
            (),
            tuple(sorted((name, command.sha256(raw)) for name, raw in assets.items())),
        )
        self.record = {
            "schema_version": 1,
            "kind": "prepared-release-inputs",
            "source_kind": "version-tag",
            "tagged_source_ci_gate_verified": False,
            "signer_identity_verified": False,
            "repository": self.args.repository,
            "version": self.args.version,
            "source_commit": self.args.commit,
            "binding_sha256": self.binding.digest,
            "assets": dict(self.inputs.assets),
            "subjects": dict(self.binding.subjects),
            "dependency_replays": {
                platform: {"fixture": True} for platform in command.PLATFORMS
            },
            "upstream_replay": {"fixture": True},
            "publication_authorized": False,
            "measurement_authentication_required": True,
        }
        self.write_record()
        subjects.update(
            {
                name: "oci://fixture/" + name + "@sha256:" + "d" * 64
                for name in ("backend-index", "web-index", *SOURCE_COVERAGE)
            }
        )
        self.binding = Binding(
            self.args.repository,
            self.args.version,
            self.args.commit,
            tuple(sorted(subjects.items())),
        )
        self.inputs = PublicationInputs(
            self.binding,
            self.inputs.manifest,
            self.inputs.manifest_record_digest,
            (),
            self.inputs.assets,
        )
        self.record.update(
            binding_sha256=self.binding.digest, subjects=dict(self.binding.subjects)
        )
        self.write_record()
        retained = {}
        coverage, images = {}, {}
        for target, categories in SOURCE_COVERAGE.items():
            coverage[target] = {}
            for category in categories:
                key = target + "-" + category
                retained[key] = {"fixture": key}
                coverage[target][category] = {
                    "status": "complete",
                    "evidence_digest": command.sha256(json_bytes(retained[key])),
                }
            files = {"LICENSE": {"sha256": "sha256:" + "e" * 64, "size": 10}}
            retained[target + "-notices"] = {
                "files": files,
                "notice_inventory_digest": command.sha256(json_bytes(files)),
            }
            images[target] = {
                "subject": subjects[target],
                "notice_inventory_digest": command.sha256(json_bytes(files)),
            }
        self.report = {
            "schema_version": 1,
            "gate": "corresponding-source",
            "binding_digest": self.binding.digest,
            "passed": True,
            "details": {
                "schema_version": 1,
                "source_subjects": {
                    "source:" + self.source: subjects["source:" + self.source]
                },
                "images": images,
                "policy": {
                    "path": SOURCE_REVIEW_POLICY,
                    "source_commit": self.args.commit,
                    "record_digest": "sha256:" + "f" * 64,
                    "git_blob": "c" * 40,
                },
                "coverage": coverage,
            },
        }
        self.evidence = {
            "schema_version": 1,
            "kind": "complete-corresponding-source-replay-evidence",
            "binding_digest": self.binding.digest,
            "source_assets": {
                "schema_version": 1,
                "kind": "release-source-asset-measurements",
                "binding_digest": self.binding.digest,
                "assets": {
                    self.source: {
                        "digest": command.sha256(assets[self.source]),
                        "size": len(assets[self.source]),
                    }
                },
                "corresponding_source_completeness_verified": False,
                "distribution_review_required": True,
                "publication_authorized": False,
            },
            "coverage_evidence": retained,
            "distribution_review_required": True,
            "byte_reproduction_verified": False,
            "publication_authorized": False,
        }

    def write_record(self, record=None):
        (self.args.prepared / "release-inputs.json").write_bytes(
            json_bytes(self.record if record is None else record)
        )

    def mocks(self, *, side_effect=None):
        stack = ExitStack()
        self.addCleanup(stack.close)
        prepared = stack.enter_context(
            patch.object(command, "prepare_inputs", return_value=self.inputs)
        )
        auth = stack.enter_context(patch.object(command, "GhEvidenceVerifier"))
        producer = stack.enter_context(
            patch.object(
                command,
                "corresponding_source_report",
                return_value=(self.report, self.evidence),
                side_effect=side_effect,
            )
        )
        stack.enter_context(
            patch.dict(command.os.environ, {"GH_TOKEN": "fixture-token"})
        )
        return prepared, auth, producer

    def test_typed_tag_binding_native_paths_current_attempt_and_exclusive_outputs(self):
        prepared, auth, producer = self.mocks()
        command.main(
            [
                "--" + key.replace("_", "-") + "=" + str(value)
                for key, value in vars(self.args).items()
            ]
        )
        auth.assert_called_once_with(token="fixture-token", run_id=1234, run_attempt=2)
        measured = prepared.call_args.kwargs
        self.assertEqual(measured["ref"], "refs/tags/v0.1.0")
        self.assertEqual(measured["event_sha"], measured["reviewed_commit"])
        self.assertEqual(
            measured["source_assets"], {self.source: self.args.prepared / self.source}
        )
        replay = producer.call_args.kwargs
        self.assertIs(producer.call_args.args[0], self.binding)
        self.assertIs(replay["authenticator"], auth.return_value)
        self.assertEqual(
            set(replay["compiler_graphs"]),
            # verify_backend_source_inputs accepts exactly these two graphs;
            # browser source evidence has its separate observation interface.
            {"backend-amd64", "backend-arm64"},
        )
        for platform, tree in (
            ("linux/amd64", self.args.amd64_inputs),
            ("linux/arm64", self.args.arm64_inputs),
        ):
            arch = platform.split("/")[1]
            for key, relative in {
                "native_measurements": "native/native-measurement.json",
                "runtime_packs": "native/pack",
                "runtime_source_records": "native/source-completeness-verification.json",
                "dependency_collections": "application-dependencies",
                "source_scans": "source-scans/source-scan-measurement.json",
                "browser_measurements": "native/browser/browser-verification.json",
                "captures": "native/browser/browser-inputs.tar",
            }.items():
                self.assertEqual(replay[key][platform], tree / relative)
            for component in ("backend", "web"):
                self.assertEqual(
                    replay["archives"][platform][component],
                    tree / f"native/export/{component}-{arch}.oci.tar",
                )
            self.assertEqual(
                replay["compiler_graphs"]["backend-" + arch],
                tree / "compiler-backend/compiler-graph-measurement.json",
            )
        self.assertEqual(
            {p.name for p in self.args.output.iterdir()},
            {"corresponding-source.json", "corresponding-source-evidence.json"},
        )
        self.assertEqual(
            read_json((self.args.output / "corresponding-source.json").read_bytes()),
            self.report,
        )
        self.assertEqual(
            read_json(
                (self.args.output / "corresponding-source-evidence.json").read_bytes()
            ),
            self.evidence,
        )
        with self.assertRaises(InvalidRelease):
            command.run_command(self.args)
        self.assertEqual(producer.call_count, 1)
        self.args.verify_only = True
        auth.return_value.verify.return_value = VerifiedEvidence(
            "corresponding-source",
            self.binding.digest,
            command.sha256(
                (self.args.output / "corresponding-source.json").read_bytes()
            ),
            True,
            self.report["details"],
        )
        before = {path.name: path.read_bytes() for path in self.args.output.iterdir()}
        command.run_command(self.args)
        self.assertEqual(producer.call_count, 1)
        auth.return_value.verify.assert_called_once_with(
            "corresponding-source",
            self.args.output / "corresponding-source.json",
            self.binding,
        )
        auth.return_value.authenticate.assert_called_once_with(
            before["corresponding-source-evidence.json"], self.binding
        )
        self.assertEqual(
            before,
            {path.name: path.read_bytes() for path in self.args.output.iterdir()},
        )
        for mutation in ("coverage", "notices", "publication"):
            bad = copy.deepcopy(self.evidence)
            if mutation == "coverage":
                for key, value in bad["coverage_evidence"].items():
                    if not key.endswith("-notices"):
                        value["substitution"] = True
            elif mutation == "notices":
                bad["coverage_evidence"]["web-arm64-notices"]["files"]["LICENSE"][
                    "size"
                ] += 1
            else:
                bad["publication_authorized"] = True
            (self.args.output / "corresponding-source-evidence.json").write_bytes(
                json_bytes(bad)
            )
            with self.subTest(mutation=mutation), self.assertRaises(InvalidRelease):
                command.run_command(self.args)
            self.assertEqual(producer.call_count, 1)
        (self.args.output / "corresponding-source-evidence.json").write_bytes(
            before["corresponding-source-evidence.json"]
        )

        def change_after_authentication(raw, binding):
            (self.args.output / "corresponding-source-evidence.json").write_bytes(
                raw + b" "
            )

        auth.return_value.authenticate.side_effect = change_after_authentication
        with self.assertRaisesRegex(InvalidRelease, "changed during authentication"):
            command.run_command(self.args)
        self.assertEqual(producer.call_count, 1)
        auth.return_value.authenticate.side_effect = None

    def test_unbound_planned_changed_and_partial_inputs_fail_without_output(self):
        prepared, auth, producer = self.mocks()
        for key, value in (
            ("source_kind", "planned-main-dispatch"),
            ("kind", "planned-candidate-inputs"),
            ("binding_sha256", "sha256:" + "b" * 64),
            ("subjects", {}),
            ("assets", {**self.record["assets"], self.source: "sha256:" + "b" * 64}),
            ("source_commit", "b" * 40),
            ("measurement_authentication_required", False),
        ):
            with self.subTest(key=key):
                changed = copy.deepcopy(self.record)
                changed[key] = value
                self.write_record(changed)
                with self.assertRaises(InvalidRelease):
                    command.run_command(self.args)
                self.assertFalse(self.args.output.exists())
        self.write_record()
        (self.args.prepared / "unexpected.json").write_bytes(b"{}")
        with self.assertRaises(InvalidRelease):
            command.run_command(self.args)
        (self.args.prepared / "unexpected.json").unlink()
        producer.assert_not_called()

        def changed_metadata(*args, **kwargs):
            self.write_record({**self.record, "binding_sha256": "sha256:" + "b" * 64})
            return self.report, self.evidence

        producer.side_effect = changed_metadata
        with self.assertRaisesRegex(InvalidRelease, "changed during source replay"):
            command.run_command(self.args)
        self.assertFalse(self.args.output.exists())
        self.write_record()
        producer.side_effect = None
        producer.return_value = (self.report, {"oversized": "x" * 1024})
        with patch.object(command, "MAX_COMMAND_OUTPUT", 256), self.assertRaises(
            InvalidRelease
        ):
            command.run_command(self.args)
        self.assertFalse(self.args.output.exists())
        producer.side_effect = OSError("a sensitive network diagnostic fixture-token")
        with patch.object(
            command, "run_command", side_effect=producer.side_effect
        ), patch("sys.stderr", new_callable=io.StringIO) as error:
            with self.assertRaises(SystemExit) as rejected:
                command.main(
                    [
                        "--" + key.replace("_", "-") + "=" + str(value)
                        for key, value in vars(self.args).items()
                    ]
                )
            self.assertEqual(rejected.exception.code, 1)
            self.assertNotIn("fixture-token", error.getvalue())

    def paired_mocks(self):
        prepared, auth, producer = self.mocks()
        self.args.checks_output = self.args.root / "check-reports"
        self.args.resolved_bases = self.args.root / "candidate-bases.json"
        self.args.resolved_bases.write_bytes(b'{"fixture":true}\n')
        for tree in (self.args.amd64_inputs, self.args.arm64_inputs):
            (tree / "native").mkdir()
            (tree / "native/native-measurement.json").write_bytes(b'{"fixture":true}\n')
        common = {
            "schema_version": 1,
            "binding_digest": self.binding.digest,
            "passed": True,
        }
        self.checks = {
            "source-ci": {
                **common,
                "gate": "source-ci",
                "details": {
                    "run_id": self.args.run_id,
                    "run_attempt": self.args.run_attempt,
                    "jobs": {
                        name: "success"
                        for name in ("security", "backend", "web", "android", "ios")
                    },
                },
            },
            "final-image-smoke": {
                **common,
                "gate": "final-image-smoke",
                "details": {
                    "execution": {platform: "native" for platform in command.PLATFORMS}
                },
            },
            "runtime-notices": {
                **common,
                "gate": "runtime-notices",
                "details": {"distribution_review_required": True},
            },
        }
        for gate, targets in (
            ("source-scanners", ("backend-source", "web-source")),
            ("final-image-scanners", tuple(SOURCE_COVERAGE)),
        ):
            self.checks[gate] = {
                **common,
                "gate": gate,
                "details": {
                    "scans": [
                        {
                            "target": target,
                            "subject": (
                                "git:"
                                + self.binding.repository
                                + "@"
                                + self.binding.commit
                                if gate == "source-scanners"
                                else dict(self.binding.subjects)[target]
                            ),
                            "status": "complete",
                            "exit_code": 0,
                            "scanner": "fixture scanner",
                            "version": "fixture version",
                            "database": "fixture database",
                            "scanned_at": "2026-01-01T00:00:00Z",
                            "findings": [],
                        }
                        for target in targets
                    ]
                },
            }
        stack = ExitStack()
        self.addCleanup(stack.close)
        ci = stack.enter_context(
            patch.object(
                command, "source_ci_report", return_value=self.checks["source-ci"]
            )
        )
        native = stack.enter_context(
            patch.object(
                command,
                "aggregate_native_reports",
                return_value={
                    gate: self.checks[gate]
                    for gate in ("final-image-smoke", "runtime-notices")
                },
            )
        )

        def source_scans(binding, **kwargs):
            kwargs["authenticator"].authenticate(
                kwargs["resolved_bases"].read_bytes(), binding
            )
            return self.checks["source-scanners"]

        source = stack.enter_context(
            patch.object(command, "aggregate_source_scans", side_effect=source_scans)
        )
        image = stack.enter_context(
            patch.object(
                command,
                "aggregate_image_scans",
                return_value=self.checks["final-image-scanners"],
            )
        )
        signed = {**self.checks, "corresponding-source": self.report}

        def receipt(gate, path, binding):
            record = signed[gate]
            return VerifiedEvidence(
                gate,
                binding.digest,
                command.sha256(json_bytes(record)),
                True,
                record["details"],
            )

        auth.return_value.verify.side_effect = receipt
        return prepared, auth, producer, ci, native, source, image

    def test_paired_outputs_generate_and_verify_without_replays(self):
        prepared, auth, producer, ci, native, source, image = self.paired_mocks()
        command.main(
            [
                "--" + key.replace("_", "-") + "=" + str(value)
                for key, value in vars(self.args).items()
            ]
        )
        ci.assert_called_once_with(
            self.binding, run_id=1234, attempt=2, token="fixture-token"
        )
        native.assert_called_once_with(
            self.binding,
            producer.call_args.kwargs["native_measurements"],
            auth.return_value,
        )
        source.assert_called_once_with(
            self.binding,
            native_measurements=producer.call_args.kwargs["source_scans"],
            repository_root=self.args.root,
            resolved_bases=self.args.resolved_bases,
            authenticator=auth.return_value,
        )
        self.assertEqual(auth.return_value.authenticate.call_count, 2)
        auth.return_value.authenticate.assert_called_with(
            self.args.resolved_bases.read_bytes(), self.binding
        )
        image.assert_called_once_with(
            self.binding,
            native_measurements=producer.call_args.kwargs["native_measurements"],
            runtime_packs=producer.call_args.kwargs["runtime_packs"],
            **{
                key: {
                    component + "-" + arch: tree / relative.format(component=component)
                    for arch, tree in (
                        ("amd64", self.args.amd64_inputs),
                        ("arm64", self.args.arm64_inputs),
                    )
                    for component in ("backend", "web")
                }
                for key, relative in (
                    ("scans", "image-scan-{component}/measurement.json"),
                    ("raw_scans", "image-scan-{component}/scan.json"),
                    (
                        "compiler_graphs",
                        "compiler-{component}/compiler-graph-measurement.json",
                    ),
                )
            },
            authenticator=auth.return_value,
        )
        auth.assert_called_once_with(token="fixture-token", run_id=1234, run_attempt=2)
        before = {}
        for output, reports in (
            (
                self.args.output,
                {
                    "corresponding-source": self.report,
                    "corresponding-source-evidence": self.evidence,
                },
            ),
            (self.args.checks_output, self.checks),
        ):
            self.assertEqual(
                {path.name for path in output.iterdir()},
                {gate + ".json" for gate in reports},
            )
            for gate, report in reports.items():
                path = output / (gate + ".json")
                self.assertEqual(path.read_bytes(), json_bytes(report))
                before[path] = path.read_bytes()
        self.args.verify_only = True
        # Signed output verification does not reopen substantive scanner inputs.
        self.args.resolved_bases.unlink()
        command.run_command(self.args)
        self.assertEqual(
            {call.args[0] for call in auth.return_value.verify.call_args_list},
            {*command.CHECK_GATES, "corresponding-source"},
        )
        self.assertEqual({path: path.read_bytes() for path in before}, before)
        self.assertEqual(producer.call_count, 1)
        self.assertEqual(ci.call_count, 1)
        self.assertEqual(native.call_count, 1)
        self.assertEqual(source.call_count, 1)
        self.assertEqual(image.call_count, 1)
        self.assertEqual(prepared.call_count, 2)

    def test_paired_verification_rejects_missing_substituted_stale_and_changed_reports(
        self,
    ):
        prepared, auth, producer, ci, native, source, image = self.paired_mocks()
        command.run_command(self.args)
        self.args.verify_only = True
        path = self.args.checks_output / "source-ci.json"
        original = path.read_bytes()
        for mutation in (
            "missing",
            "substituted",
            "old-attempt",
            "old-run",
            "noncanonical",
            "linked",
            "extra",
        ):
            with self.subTest(mutation=mutation):
                changed = copy.deepcopy(self.checks["source-ci"])
                if mutation == "missing":
                    path.unlink()
                elif mutation == "linked":
                    path.unlink()
                    path.symlink_to(self.args.output / "corresponding-source.json")
                elif mutation == "extra":
                    (self.args.checks_output / "extra.json").write_bytes(b"{}")
                elif mutation == "noncanonical":
                    path.write_bytes(original + b" ")
                else:
                    if mutation == "substituted":
                        changed["details"]["jobs"]["ios"] = "failure"
                    elif mutation == "old-attempt":
                        changed["details"]["run_attempt"] -= 1
                    else:
                        changed["details"]["run_id"] += 1
                    path.write_bytes(json_bytes(changed))
                with self.assertRaises(InvalidRelease):
                    command.run_command(self.args)
                if path.is_symlink():
                    path.unlink()
                path.write_bytes(original)
                (self.args.checks_output / "extra.json").unlink(missing_ok=True)
        notice_path = self.args.checks_output / "runtime-notices.json"

        for gate in ("source-scanners", "final-image-scanners"):
            scanner_path = self.args.checks_output / (gate + ".json")
            scanner_original = scanner_path.read_bytes()
            for mutation in ("missing", "substituted", "unresolved"):
                with self.subTest(gate=gate, mutation=mutation):
                    changed = copy.deepcopy(self.checks[gate])
                    if mutation == "missing":
                        scanner_path.unlink()
                    else:
                        changed["details"]["scans"][0]["findings"] = [
                            {"disposition": "unresolved", "reason": "fixture finding"}
                        ]
                        scanner_path.write_bytes(json_bytes(changed))
                        if mutation == "unresolved":
                            self.checks[gate]["details"] = changed["details"]
                    with self.assertRaises(InvalidRelease):
                        command.run_command(self.args)
                    self.checks[gate]["details"] = read_json(scanner_original)[
                        "details"
                    ]
                    scanner_path.write_bytes(scanner_original)

        def change_paired_output(raw, binding):
            notice_path.write_bytes(notice_path.read_bytes() + b" ")

        auth.return_value.authenticate.side_effect = change_paired_output
        with self.assertRaisesRegex(
            InvalidRelease, "outputs changed during authentication"
        ):
            command.run_command(self.args)
        self.assertEqual(producer.call_count, 1)
        self.assertEqual(ci.call_count, 1)
        self.assertEqual(native.call_count, 1)
        self.assertEqual(source.call_count, 1)
        self.assertEqual(image.call_count, 1)

    def test_paired_generation_failure_and_existing_output_create_no_complete_reports(
        self,
    ):
        prepared, auth, producer, ci, native, source, image = self.paired_mocks()
        resolved_bases = self.args.resolved_bases
        self.args.resolved_bases = None
        with self.assertRaisesRegex(
            InvalidRelease, "require authenticated resolved bases"
        ):
            command.run_command(self.args)
        producer.assert_not_called()
        self.args.resolved_bases = resolved_bases
        self.args.checks_output.mkdir()
        with self.assertRaisesRegex(InvalidRelease, "must be a new directory"):
            command.run_command(self.args)
        producer.assert_not_called()
        self.args.checks_output.rmdir()
        ci.side_effect = InvalidRelease("Exact source CI is unfinished")
        with self.assertRaises(InvalidRelease):
            command.run_command(self.args)
        self.assertFalse(self.args.output.exists())
        self.assertFalse(self.args.checks_output.exists())
        ci.side_effect = None
        auth.return_value.authenticate.side_effect = InvalidRelease(
            "Resolved bases lack current workflow authentication"
        )
        with self.assertRaisesRegex(InvalidRelease, "Resolved bases"):
            command.run_command(self.args)
        producer.assert_not_called()
        source.assert_not_called()
        image.assert_not_called()
        self.assertFalse(self.args.output.exists())
        self.assertFalse(self.args.checks_output.exists())


if __name__ == "__main__":
    unittest.main()
