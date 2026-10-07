"""Unsigned transfer tests retain real tiny Docker/OCI/archive fixture bytes."""

from __future__ import annotations

import copy
import gzip
import io
import shutil
import tarfile
import sys
import unittest
from unittest.mock import patch

import prepare_candidate_transfer as transfer
import test_release_recovery_measurement as recovery_fixtures
from generate_release_gate_reports import runtime_inputs
from publish_container_release import sha256
from release_artifacts import InvalidRelease, json_bytes, read_json


class CandidateTransfer(unittest.TestCase):
    def setUp(self):
        self.fixture = recovery_fixtures.RecoveryMeasurements()
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.context = self.fixture.context
        self.private = self.fixture.folder / "private"
        self.private.mkdir()
        self.output = self.fixture.folder / "transfer"
        self.native = self.private / "native"
        self.native.mkdir()
        original_descriptor = self.fixture.natives[self.context.platform]
        descriptor = read_json(original_descriptor.read_bytes())
        for key, record in descriptor["artifacts"].items():
            old = original_descriptor.parent / record["file"]
            names = {
                "build_record": "build-record.json",
                "original_archive": "original/" + old.name,
                "final_archive": "final-images.docker.tar",
                "native_measurement": "native-measurement.json",
                "smoke_report": "smoke-report.json",
                "source_verification": "source-completeness-verification.json",
                "runtime_pack": "pack/runtime-pack.json",
                "runtime_source": "pack/" + old.name,
                "backend_archive": "export/backend-amd64.oci.tar",
                "web_archive": "export/web-amd64.oci.tar",
            }
            new = self.native / names[key]
            new.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(old, new)
            record["file"] = names[key]
        pack_path = self.native / "pack/runtime-pack.json"
        pack = read_json(pack_path.read_bytes())
        pack.update(
            publication_pending=True,
            distribution_review_required=True,
            additional_files={"backend": {}, "web": {}},
        )
        for component, files in pack["overlays"].items():
            for name in files:
                payload = (component + ": fixture notice " + name).encode()
                path = self.native / "pack/overlays" / component / "runtime" / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(payload)
                files[name] = sha256(payload)[7:]
        pack_path.write_bytes(json_bytes(pack))
        _, runtime = runtime_inputs(self.context, self.native / "pack")
        measurement = self.read("native/native-measurement.json")
        measurement["runtime"] = runtime
        measurement["smoke"]["runtime_pack_sha256"] = runtime["runtime_pack_sha256"]
        self.write("native/native-measurement.json", measurement)
        self.write("native/smoke-report.json", measurement["smoke"])
        replay = self.read("native/source-completeness-verification.json")
        replay.update(
            runtime_pack_sha256=runtime["runtime_pack_sha256"],
            native_smoke_report_sha256=sha256(
                (self.native / "smoke-report.json").read_bytes()
            ),
        )
        self.write("native/source-completeness-verification.json", replay)
        self.descriptor = descriptor
        self.refresh_descriptor()
        self.images = measurement["images"]
        scans = []
        for target, allowed in (
            ("backend-source", transfer.GO_RAW),
            ("web-source", transfer.NODE_RAW),
        ):
            raw = {}
            for name in allowed:
                payload = b"" if name.endswith(".stderr") else b"Fixture receipt\n"
                path = self.private / "source-scans" / target / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(payload)
                raw[name] = sha256(payload)
            scans.append(
                {
                    "target": target,
                    "subject": "git:"
                    + self.context.repository
                    + "@"
                    + self.context.commit,
                    "status": "complete",
                    "exit_code": 0,
                    "raw_files": raw,
                }
            )
        self.write(
            "source-scans/source-scan-measurement.json",
            {
                "schema_version": 1,
                "kind": "native-source-scanner-measurement",
                "source": self.context.checked(),
                "execution": "native",
                "scans": scans,
                "publication_authorized": False,
                "source_scanners_gate_pending": True,
                "findings_review_required": True,
            },
        )
        for component in transfer.COMPONENTS:
            directory = "compiler-" + component
            allowed = transfer.COMPILER_RAW | (
                {"initial-build-info.json", "rebuilt-backend-sha256.txt"}
                if component == "backend"
                else {"caddy-signature-verification.json"}
            )
            raw = {}
            for name in allowed:
                payload = (
                    b""
                    if name.endswith(".stderr")
                    else json_bytes({"kind": "unsigned fixture", "name": name})
                )
                path = self.private / directory / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(payload)
                raw[name] = sha256(payload)
            self.write(
                directory + "/compiler-graph-measurement.json",
                {
                    "schema_version": 1,
                    "kind": "native-compiler-graph-measurement",
                    "source": self.context.checked(),
                    "execution": "native",
                    "component": component,
                    "image": self.images[component],
                    "status": "complete",
                    "exit_code": 0,
                    "finding_dispositions_authorized": False,
                    "publication_authorized": False,
                    "advisories": {},
                    "raw_files": raw,
                    "source_graph": {
                        "raw_file": "compiler-graph.json",
                        "sha256": raw["compiler-graph.json"],
                    },
                },
            )
            scan_directory = "image-scan-" + component
            self.write(
                scan_directory + "/scan.json",
                {
                    "Metadata": {"ImageID": descriptor["tested_configs"][component]},
                    "Results": [],
                },
            )
            self.write(
                scan_directory + "/measurement.json",
                {
                    "schema_version": 1,
                    "kind": "native-image-scanner-measurement",
                    "source": self.context.checked(),
                    "execution": "native",
                    "target": component + "-amd64",
                    "image": self.images[component],
                    "status": "complete",
                    "exit_code": 0,
                    "publication_authorized": False,
                    "findings_review_required": True,
                    "final_image_scanners_gate_pending": True,
                    "raw_report_sha256": sha256(
                        (self.private / scan_directory / "scan.json").read_bytes()
                    ),
                },
            )
        asset = "psst.zip-dependency-inputs-" + self.context.version + "-amd64.tar.gz"
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode="w:") as archive:
            member = tarfile.TarInfo("SOURCE.md")
            payload = b"Unsigned dependency fixture\n"
            member.size = len(payload)
            archive.addfile(member, io.BytesIO(payload))
        raw = gzip.compress(stream.getvalue(), mtime=0)
        path = self.private / "application-dependencies" / asset
        path.parent.mkdir()
        path.write_bytes(raw)
        self.write(
            "application-dependencies/dependency-collection.json",
            {
                "schema_version": 1,
                "kind": "application-dependency-collection",
                "repository": self.context.repository,
                "version": self.context.version,
                "source_commit": self.context.commit,
                "platform": self.context.platform,
                "publication_authorized": False,
                "package_inputs_verified": True,
                "preferred_source_review_required": True,
                "asset": {"name": asset, "sha256": sha256(raw), "size": len(raw)},
            },
        )

    def write(self, name, value):
        path = self.private / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(json_bytes(value))

    def read(self, name):
        return read_json((self.private / name).read_bytes())

    def refresh_descriptor(self):
        for record in self.descriptor["artifacts"].values():
            value = transfer.fingerprint(self.native, record["file"])
            record.update(value)
        self.write("native/native-artifacts.json", self.descriptor)

    def stage(self, **changes):
        return transfer.stage_native(
            self.context,
            self.private,
            changes.get("output", self.output),
            changes.get("source_kind", "planned-main-dispatch"),
        )

    def test_actual_archive_bytes_and_empty_declared_receipts_relocate_exactly(self):
        result = self.stage()
        self.assertIs(result["publication_authorized"], False)
        self.assertIs(result["measurement_authentication_required"], True)
        for name, record in result["files"].items():
            self.assertEqual(
                (self.private / name).read_bytes(), (self.output / name).read_bytes()
            )
            self.assertEqual(record, transfer.fingerprint(self.output, name))
        relocated = self.fixture.folder / "relocated"
        self.output.rename(relocated)
        self.assertEqual(
            result,
            transfer.verify_native(self.context, relocated, "planned-main-dispatch"),
        )
        with tarfile.open(relocated / "native/final-images.docker.tar") as archive:
            self.assertIn("manifest.json", archive.getnames())

    def test_unreferenced_private_tools_diagnostics_cookies_and_cache_are_excluded(
        self,
    ):
        for name in (
            "tools/cosign",
            "native/collections/private",
            "operator.env",
            "cookie",
            "build.stdout",
            "compiler-backend/runtime-binary",
            "compiler-web/rebuilt-backend",
            "image-scan-backend/cache/trivy.db",
        ):
            path = self.private / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"Protected fixture, never transfer")
        result = self.stage(source_kind="version-tag")
        self.assertTrue(
            all(
                not (self.output / name).exists()
                for name in (
                    "tools/cosign",
                    "operator.env",
                    "compiler-backend/runtime-binary",
                )
            )
        )
        self.assertEqual(
            result, transfer.verify_native(self.context, self.output, "version-tag")
        )

    def test_missing_raw_overlay_or_dependency_archive_fails_before_writing(self):
        paths = (
            "source-scans/backend-source/govulncheck.json",
            "compiler-web/compiler-graph.json",
            "native/pack/overlays/backend/runtime/SOURCE.txt",
            "application-dependencies/"
            + self.read("application-dependencies/dependency-collection.json")["asset"][
                "name"
            ],
        )
        for name in paths:
            path = self.private / name
            raw = path.read_bytes()
            path.unlink()
            with self.subTest(name=name), self.assertRaises(InvalidRelease):
                self.stage()
            self.assertFalse(self.output.exists())
            path.write_bytes(raw)

    def test_wrong_source_emulation_authorization_configuration_and_raw_hash_refuse(
        self,
    ):
        cases = (
            (
                "native/native-artifacts.json",
                lambda value: value["artifacts"]["final_archive"].update(sha256=None),
            ),
            (
                "application-dependencies/dependency-collection.json",
                lambda value: value["asset"].update(sha256=None),
            ),
            (
                "compiler-backend/compiler-graph-measurement.json",
                lambda value: value.update(execution="emulated"),
            ),
            (
                "image-scan-web/measurement.json",
                lambda value: value.update(publication_authorized=True),
            ),
            (
                "source-scans/source-scan-measurement.json",
                lambda value: value["source"].update(commit="0" * 40),
            ),
            (
                "compiler-web/compiler-graph-measurement.json",
                lambda value: value["raw_files"].update(
                    {"compiler-graph.json": "sha256:" + "0" * 64}
                ),
            ),
            (
                "image-scan-backend/measurement.json",
                lambda value: value["image"].update(config_digest="sha256:" + "0" * 64),
            ),
            (
                "image-scan-backend/measurement.json",
                lambda value: value.update(raw_report_sha256=None),
            ),
            (
                "compiler-web/compiler-graph-measurement.json",
                lambda value: value["raw_files"].update({"compiler-graph.json": None}),
            ),
        )
        for name, mutate in cases:
            original = self.read(name)
            changed = copy.deepcopy(original)
            mutate(changed)
            self.write(name, changed)
            with self.subTest(name=name), self.assertRaises(InvalidRelease):
                self.stage()
            self.assertFalse(self.output.exists())
            self.write(name, original)

    def test_symlink_traversal_and_referenced_protected_raw_files_refuse(self):
        path = self.private / "compiler-backend/compiler-graph.json"
        path.rename(path.with_suffix(".saved"))
        path.symlink_to(path.with_suffix(".saved"))
        with self.assertRaises(InvalidRelease):
            self.stage()
        path.unlink()
        path.with_suffix(".saved").rename(path)
        old = copy.deepcopy(self.descriptor)
        self.descriptor["artifacts"]["final_archive"]["file"] = "../cookie"
        self.write("native/native-artifacts.json", self.descriptor)
        with self.assertRaises(InvalidRelease):
            self.stage()
        self.descriptor = old
        self.write("native/native-artifacts.json", old)
        graph = self.read("compiler-backend/compiler-graph-measurement.json")
        graph["raw_files"]["runtime-binary"] = "sha256:" + "0" * 64
        self.write("compiler-backend/compiler-graph-measurement.json", graph)
        with self.assertRaises(InvalidRelease):
            self.stage()
        self.assertFalse(self.output.exists())

    def test_changed_input_during_copy_never_writes_terminal_transfer_metadata(self):
        real_copy = transfer.copy_file
        altered = []

        def change(source, target):
            real_copy(source, target)
            if not altered:
                source.write_bytes(source.read_bytes() + b"changed")
                altered.append(source)

        with patch.object(transfer, "copy_file", side_effect=change):
            with self.assertRaisesRegex(InvalidRelease, "changed during transfer"):
                self.stage()
        self.assertFalse((self.output / transfer.METADATA).exists())

    def test_download_extra_file_empty_directory_symlink_and_missing_payload_refuse(
        self,
    ):
        self.stage()
        extra = self.output / "cookie"
        extra.write_text("Extra")
        with self.assertRaisesRegex(InvalidRelease, "Extra or missing"):
            transfer.verify_native(self.context, self.output, "planned-main-dispatch")
        extra.unlink()
        extra.mkdir()
        with self.assertRaisesRegex(InvalidRelease, "Extra or missing"):
            transfer.verify_native(self.context, self.output, "planned-main-dispatch")
        extra.rmdir()
        extra.symlink_to(self.private / "native/native-artifacts.json")
        with self.assertRaisesRegex(InvalidRelease, "Linked"):
            transfer.verify_native(self.context, self.output, "planned-main-dispatch")
        extra.unlink()
        (self.output / "compiler-web/compiler-graph.json").unlink()
        with self.assertRaises(InvalidRelease):
            transfer.verify_native(self.context, self.output, "planned-main-dispatch")

    def test_stale_context_source_kind_boolean_size_and_terminal_flags_refuse(self):
        self.stage()
        with self.assertRaises(InvalidRelease):
            transfer.verify_native(self.context, self.output, "version-tag")
        with self.assertRaises(InvalidRelease):
            transfer.verify_native(self.context, self.output, "other")
        path = self.output / transfer.METADATA
        original = read_json(path.read_bytes())
        for change in (
            lambda value: value.update(publication_authorized=True),
            lambda value: value["source"].update(platform="linux/arm64"),
            lambda value: next(iter(value["files"].values())).update(size=True),
        ):
            value = copy.deepcopy(original)
            change(value)
            path.write_bytes(json_bytes(value))
            with self.assertRaises(InvalidRelease):
                transfer.verify_native(
                    self.context, self.output, "planned-main-dispatch"
                )
        path.write_bytes(json_bytes(original))

    def test_cli_stage_and_verify_write_distinct_unsigned_outputs(self):
        context_args = [
            "--repository",
            self.context.repository,
            "--version",
            self.context.version,
            "--commit",
            self.context.commit,
            "--platform",
            self.context.platform,
            "--source-kind",
            "planned-main-dispatch",
        ]
        with patch.object(
            sys,
            "argv",
            [
                "transfer",
                "stage",
                *context_args,
                "--private",
                str(self.private),
                "--output",
                str(self.output),
            ],
        ):
            transfer.main()
        receipt = self.fixture.folder / "verified-transfer.json"
        with patch.object(
            sys,
            "argv",
            [
                "transfer",
                "verify",
                *context_args,
                "--transfer",
                str(self.output),
                "--output",
                str(receipt),
            ],
        ):
            transfer.main()
        self.assertEqual(
            read_json(receipt.read_bytes()),
            read_json((self.output / transfer.METADATA).read_bytes()),
        )
        self.assertIs(read_json(receipt.read_bytes())["publication_authorized"], False)

    def test_metadata_limits_and_reusing_output_fail_closed(self):
        self.stage()
        with self.assertRaisesRegex(InvalidRelease, "new real directory"):
            self.stage()
        with patch.object(transfer, "MAX_JSON", 32):
            with self.assertRaises(InvalidRelease):
                transfer.verify_native(
                    self.context, self.output, "planned-main-dispatch"
                )


if __name__ == "__main__":
    unittest.main()
