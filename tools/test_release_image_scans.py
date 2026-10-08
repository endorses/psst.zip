"""Native scanner measurements: authenticated tooling, identity and no approval."""

from __future__ import annotations

import copy
import io
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import measure_release_image_scans as scanner
import test_release_oci as oci_fixtures
from generate_release_gate_reports import NativeSourceContext
from publish_container_release import sha256
from release_artifacts import InvalidRelease, json_bytes


class ImageScannerChecks(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="psst-scanner-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.context = NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", "a" * 40, "linux/amd64"
        )
        self.archive = self.root / "image.tar"
        self.files, self.config = oci_fixtures.fixture()
        with tarfile.open(self.archive, "w:") as archive:
            for name, body in self.files.items():
                member = tarfile.TarInfo(name)
                member.size = len(body)
                archive.addfile(member, io.BytesIO(body))
        self.binary = (
            b"\x7fELF\x02\x01"
            + b"\0" * 12
            + (62).to_bytes(2, "little")
            + b"fixture Trivy"
        )
        self.tool_archive = self.root / "trivy.tar.gz"
        with tarfile.open(self.tool_archive, "w:gz") as archive:
            member = tarfile.TarInfo("trivy")
            member.size = len(self.binary)
            archive.addfile(member, io.BytesIO(self.binary))
        self.bundle = self.root / "bundle.json"
        self.bundle.write_bytes(b"fixture signature bundle")
        self.cosign = self.root / "cosign"
        self.cosign.write_bytes(b"fixture official Cosign")
        self.database = self.root / "db"
        self.database.mkdir()
        (self.database / "trivy.db").write_bytes(b"fixture schema2 database")
        self.metadata = {
            "Version": 2,
            "UpdatedAt": "2026-10-07T07:00:00Z",
            "NextUpdate": "2026-10-08T07:00:00Z",
            "DownloadedAt": "2026-10-07T08:00:00Z",
        }
        (self.database / "metadata.json").write_bytes(json_bytes(self.metadata))
        self.report = {
            "SchemaVersion": 2,
            "ArtifactType": "container_image",
            "Trivy": {"Version": scanner.VERSION},
            "CreatedAt": "2026-10-07T09:00:00Z",
            "Metadata": {
                "ImageID": self.config,
                "ImageConfig": {"os": "linux", "architecture": "amd64"},
                "OS": {"Family": "alpine"},
            },
            "Results": [
                {
                    "Target": "Alpine3.21",
                    "Class": "os-pkgs",
                    "Type": "alpine",
                    "Packages": [{"Name": "musl"}],
                    "Vulnerabilities": [],
                },
                {
                    "Target": "app/server",
                    "Class": "lang-pkgs",
                    "Type": "gobinary",
                    "Packages": [{"Name": "golang.org/x/crypto"}],
                    "Vulnerabilities": [
                        {
                            "VulnerabilityID": "GO-2026-fixture",
                            "PkgName": "golang.org/x/crypto",
                            "InstalledVersion": "v0.57.0",
                            "Severity": "UNKNOWN",
                            "Status": "affected",
                            "DataSource": {"ID": "govulndb"},
                        },
                        {
                            "VulnerabilityID": "CVE-fixture",
                            "PkgName": "stdlib",
                            "InstalledVersion": "v1.26.8",
                            "Severity": "HIGH",
                            "Status": "fixed",
                            "FixedVersion": "v1.26.9",
                        },
                    ],
                },
            ],
        }
        pins = {
            "linux/amd64": {
                "archive": sha256(self.tool_archive.read_bytes())[7:],
                "bundle": sha256(self.bundle.read_bytes())[7:],
                "binary": sha256(self.binary)[7:],
            }
        }
        self.addCleanup(patch.stopall)
        patch.object(scanner, "ASSETS", pins).start()
        patch.object(
            scanner,
            "COSIGN_SHA256",
            {"linux/amd64": sha256(self.cosign.read_bytes())[7:]},
        ).start()
        patch.object(scanner, "native_platform", return_value="linux/amd64").start()
        self.calls = []
        self.roots = []

    def execute(self, args, *, environment, timeout):
        self.calls.append(args)
        root = Path(environment["HOME"])
        self.roots.append(root)
        self.assertEqual(
            set(environment),
            {
                "PATH",
                "HOME",
                "TMPDIR",
                "XDG_CONFIG_HOME",
                "XDG_CACHE_HOME",
                "SIGSTORE_NO_CACHE",
            },
        )
        self.assertNotIn("TRIVY_IGNORE_UNFIXED", environment)
        self.assertNotIn("GH_TOKEN", environment)
        if args[1:] == ["version", "--json"]:
            self.assertEqual(timeout, 55)
            return json_bytes(
                {
                    "gitVersion": scanner.COSIGN_VERSION,
                    "gitCommit": scanner.COSIGN_COMMIT,
                    "gitTreeState": "clean",
                    "platform": self.context.platform,
                }
            )
        if "verify-blob" in args:
            self.assertEqual(
                args[args.index("--certificate-identity") + 1], scanner.IDENTITY
            )
            self.assertEqual(
                args[args.index("--certificate-github-workflow-sha") + 1],
                scanner.SOURCE_COMMIT,
            )
            for flag in (
                "--insecure-ignore-tlog=false",
                "--insecure-ignore-sct=false",
                "--offline=false",
                "--new-bundle-format=true",
            ):
                self.assertIn(flag, args)
            self.assertEqual(timeout, 55)
            self.assertFalse((root / "trivy").exists())
            return b"fixture signature verified"
        if "--version" in args:
            self.assertTrue(any("verify-blob" in call for call in self.calls))
            return json_bytes({"Version": scanner.VERSION})
        if "--download-db-only" in args:
            self.assertEqual(
                args[args.index("--db-repository") + 1], scanner.DATABASE_REPOSITORY
            )
            self.assertEqual(timeout, 330)
            db = Path(args[args.index("--cache-dir") + 1]) / "db"
            (db / "trivy.db").write_bytes((self.database / "trivy.db").read_bytes())
            (db / "metadata.json").write_bytes(json_bytes(self.metadata))
            return b"fixture downloaded official database"
        self.assertEqual(args[1], "image")
        self.assertEqual(timeout, 660)
        layout = Path(args[args.index("--input") + 1])
        self.assertTrue(layout.is_dir())
        self.assertEqual(
            {
                str(path.relative_to(layout)): path.read_bytes()
                for path in layout.rglob("*")
                if path.is_file()
            },
            self.files,
        )
        for flag in (
            "--list-all-pkgs=true",
            "--ignore-unfixed=false",
            "--ignorefile=",
            "--ignore-policy=",
            "--offline-scan",
            "--skip-db-update",
            "--skip-java-db-update",
            "--skip-vex-repo-update",
            "--show-suppressed",
        ):
            self.assertIn(flag, args)
        self.assertEqual(
            args[args.index("--severity") + 1], "UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL"
        )
        return json_bytes(self.report)

    def measure(self, execute=None, **overrides):
        return scanner.measure_image_scan(
            self.context,
            **{
                "component": "backend",
                "archive": self.archive,
                "tested_config": self.config,
                "tool_archive": self.tool_archive,
                "tool_bundle": self.bundle,
                "cosign": self.cosign,
                "database": self.database,
                "execute": execute or self.execute,
                **overrides,
            },
        )

    def test_real_invocation_keeps_all_findings_raw_hashes_and_never_approves(self):
        record, raw = self.measure()
        self.assertEqual(record["source"], self.context.checked())
        self.assertNotIn("binding_digest", record)
        self.assertEqual(record["raw_report_sha256"], sha256(raw))
        self.assertEqual(record["scanner"]["sha256"], sha256(self.binary))
        self.assertEqual(
            record["database"]["sha256"],
            sha256((self.database / "trivy.db").read_bytes()),
        )
        self.assertEqual(
            [item["finding"] for item in record["findings"]],
            self.report["Results"][1]["Vulnerabilities"],
        )
        for item in record["findings"]:
            self.assertNotIn("disposition", item)
            self.assertEqual(
                item["finding_sha256"], sha256(json_bytes(item["finding"]))
            )
        self.assertFalse(record["publication_authorized"])
        self.assertTrue(record["final_image_scanners_gate_pending"])
        self.assertTrue(record["findings_review_required"])
        self.assertNotIn("passed", record)
        self.assertNotIn("gate", record)
        self.assertTrue(all(not path.exists() for path in self.roots))

    def test_invalid_oci_or_tested_configuration_precedes_any_tool_execution(self):
        with self.assertRaisesRegex(InvalidRelease, "configuration"):
            self.measure(tested_config="sha256:" + "f" * 64)
        self.assertEqual(self.calls, [])
        self.archive.write_bytes(b"invalid archive")
        with self.assertRaises((InvalidRelease, tarfile.TarError)):
            self.measure()
        self.assertEqual(self.calls, [])

    def test_unpinned_tool_asset_and_failed_signature_never_run_trivy(self):
        self.tool_archive.write_bytes(b"substituted tool")
        with self.assertRaisesRegex(InvalidRelease, "pinned bytes"):
            self.measure()
        self.assertFalse(
            any("--version" in call or "image" in call for call in self.calls)
        )

    def test_signature_failure_stops_before_extraction_or_scan(self):
        def failed_signature(args, **kwargs):
            if "verify-blob" in args:
                raise InvalidRelease("signature failure")
            return self.execute(args, **kwargs)

        with self.assertRaisesRegex(InvalidRelease, "signature failure"):
            self.measure(execute=failed_signature)
        self.assertFalse(
            any("--version" in call or "image" in call for call in self.calls)
        )
        self.assertTrue(all(not path.exists() for path in self.roots))

    def test_wrong_scanned_image_missing_binary_inventory_and_malformed_report_fail(
        self,
    ):
        original = copy.deepcopy(self.report)
        changes = [
            lambda r: r["Metadata"].update(ImageID="sha256:" + "f" * 64),
            lambda r: r["Metadata"]["ImageConfig"].update(architecture="arm64"),
            lambda r: r.update(Results=r["Results"][:1]),
            lambda r: r["Results"][1].update(Packages=[]),
            lambda r: r["Results"][1]["Vulnerabilities"][0].update(Severity="hidden"),
            lambda r: r["Results"][1].update(
                ExperimentalModifiedFindings=[{"Status": "not_affected"}]
            ),
        ]
        for change in changes:
            self.report = copy.deepcopy(original)
            change(self.report)
            with self.subTest(change=change), self.assertRaises(InvalidRelease):
                self.measure()
        self.report = original
        with self.assertRaises((InvalidRelease, ValueError)):
            self.measure(
                execute=lambda args, **kwargs: (
                    b"invalid JSON" if "image" in args else self.execute(args, **kwargs)
                )
            )

    def test_scan_error_and_private_oci_database_or_tool_mutation_fail_closed(self):
        def failure(args, **kwargs):
            if "image" in args:
                raise InvalidRelease("scanner failure")
            return self.execute(args, **kwargs)

        with self.assertRaisesRegex(InvalidRelease, "scanner failure"):
            self.measure(execute=failure)
        for kind in ("layout", "database", "tool", "archive"):

            def mutate(args, **kwargs):
                raw = self.execute(args, **kwargs)
                if "image" in args:
                    root = Path(kwargs["environment"]["HOME"])
                    path = {
                        "layout": root / "layout/index.json",
                        "database": root / "cache/db/trivy.db",
                        "tool": root / "trivy",
                        "archive": self.archive,
                    }[kind]
                    path.chmod(0o600)
                    path.write_bytes(path.read_bytes() + b"changed")
                return raw

            archive_before = self.archive.read_bytes()
            with self.subTest(kind=kind), self.assertRaises(InvalidRelease):
                self.measure(execute=mutate)
            self.archive.write_bytes(archive_before)

    def test_zero_findings_is_still_unapproved_and_non_native_context_fails(self):
        self.report["Results"][1]["Vulnerabilities"] = []
        record, _ = self.measure()
        self.assertEqual(record["findings"], [])
        self.assertTrue(record["findings_review_required"])
        self.assertFalse(record["publication_authorized"])
        self.context = NativeSourceContext(
            self.context.repository,
            self.context.version,
            self.context.commit,
            "linux/arm64",
        )
        with self.assertRaisesRegex(InvalidRelease, "actual native"):
            self.measure()

    def test_database_schema_dates_and_json_bounds_fail(self):
        for metadata in (
            {**self.metadata, "Version": 1},
            {**self.metadata, "UpdatedAt": "undated"},
        ):
            (self.database / "metadata.json").write_bytes(json_bytes(metadata))
            with self.assertRaises(InvalidRelease):
                self.measure()
        with patch.object(scanner, "MAX_JSON", 8):
            with self.assertRaisesRegex(InvalidRelease, "bounds"):
                scanner.checked_report(
                    b"large JSON content",
                    platform="linux/amd64",
                    component="backend",
                    config=self.config,
                )

    def test_arm_tool_requires_its_own_pinned_assets_and_official_signature(self):
        self.context = NativeSourceContext(
            self.context.repository,
            self.context.version,
            self.context.commit,
            "linux/arm64",
        )
        self.binary = self.binary[:18] + (183).to_bytes(2, "little") + self.binary[20:]
        with tarfile.open(self.tool_archive, "w:gz") as archive:
            member = tarfile.TarInfo("trivy")
            member.size = len(self.binary)
            archive.addfile(member, io.BytesIO(self.binary))
        pins = {
            "linux/arm64": {
                "archive": sha256(self.tool_archive.read_bytes())[7:],
                "bundle": sha256(self.bundle.read_bytes())[7:],
            }
        }
        root = self.root / "arm-auth"
        root.mkdir()
        (root / "empty.yaml").write_text("{}\n")
        with (
            patch.object(scanner, "ASSETS", pins),
            patch.object(
                scanner,
                "COSIGN_SHA256",
                {"linux/arm64": sha256(self.cosign.read_bytes())[7:]},
            ),
        ):
            executable, record = scanner.authenticate_tool(
                root,
                platform="linux/arm64",
                archive=self.tool_archive,
                bundle=self.bundle,
                cosign=self.cosign,
                execute=self.execute,
            )
        self.assertEqual(record["platform"], "linux/arm64")
        self.assertEqual(record["sha256"], sha256(self.binary))
        self.assertTrue(any("verify-blob" in call for call in self.calls))
        self.assertEqual(executable.read_bytes(), self.binary)

    def test_cli_emits_exact_raw_and_measurement_atomically_without_overwrite(self):
        record, raw = self.measure()
        output = self.root / "completed-scan"
        argv = [
            "--repository",
            self.context.repository,
            "--version",
            self.context.version,
            "--commit",
            self.context.commit,
            "--platform",
            self.context.platform,
            "--component",
            "backend",
            "--tested-config",
            self.config,
        ]
        for name, value in (
            ("archive", self.archive),
            ("tool-archive", self.tool_archive),
            ("tool-bundle", self.bundle),
            ("cosign", self.cosign),
            ("database", self.database),
            ("output", output),
        ):
            argv.extend(["--" + name, str(value)])
        with patch.object(
            scanner, "measure_image_scan", return_value=(record, raw)
        ) as measure:
            scanner.main(argv)
            measure.assert_called_once()
            with self.assertRaisesRegex(InvalidRelease, "already exists"):
                scanner.main(argv)
            self.assertEqual(measure.call_count, 1)
        self.assertEqual((output / "scan.json").read_bytes(), raw)
        self.assertEqual((output / "measurement.json").read_bytes(), json_bytes(record))
        self.assertEqual(output.stat().st_mode & 0o777, 0o700)
        self.assertEqual((output / "measurement.json").stat().st_mode & 0o777, 0o400)
        output2 = self.root / "failed-scan"
        argv[-1] = str(output2)
        with patch.object(
            scanner, "measure_image_scan", side_effect=InvalidRelease("scanner failed")
        ):
            with self.assertRaisesRegex(InvalidRelease, "scanner failed"):
                scanner.main(argv)
        self.assertFalse(output2.exists())
        with (
            patch.object(scanner, "measure_image_scan", return_value=(record, raw)),
            patch.object(
                scanner.os, "link", side_effect=OSError("atomic write failed")
            ),
        ):
            with self.assertRaisesRegex(OSError, "atomic write failed"):
                scanner.main(argv)
        self.assertFalse(output2.exists())
        self.assertFalse(list(self.root.glob(".psst-scanner-output-*")))

    def test_owned_database_download_uses_authenticated_tool_and_records_exact_bytes(
        self,
    ):
        record, _ = self.measure(database=None)
        acquisition = record["database"]["acquisition"]
        self.assertEqual(acquisition["kind"], "owned-authenticated-trivy-download")
        self.assertEqual(acquisition["repository"], scanner.DATABASE_REPOSITORY)
        self.assertEqual(acquisition["scanner_sha256"], record["scanner"]["sha256"])
        self.assertEqual(acquisition["database_sha256"], record["database"]["sha256"])
        self.assertEqual(
            acquisition["metadata_sha256"], record["database"]["metadata_sha256"]
        )
        call = next(call for call in self.calls if "--download-db-only" in call)
        self.assertLess(
            next(i for i, c in enumerate(self.calls) if "verify-blob" in c),
            self.calls.index(call),
        )
        self.assertFalse(record["publication_authorized"])

        def failed_download(args, **kwargs):
            if "--download-db-only" in args:
                raise InvalidRelease("official database download failed")
            return self.execute(args, **kwargs)

        with self.assertRaisesRegex(InvalidRelease, "download failed"):
            self.measure(database=None, execute=failed_download)

    def test_retained_database_cannot_mint_an_acquisition_receipt(self):
        record, _ = self.measure()
        self.assertEqual(
            record["database"]["acquisition"],
            {
                "kind": "retained-unapproved-snapshot",
                "authenticated_acquisition_required": True,
            },
        )
        forged = {
            **self.metadata,
            "acquisition": {"kind": "owned-authenticated-trivy-download"},
        }
        (self.database / "metadata.json").write_bytes(json_bytes(forged))
        with self.assertRaisesRegex(InvalidRelease, "database metadata"):
            self.measure()


if __name__ == "__main__":
    unittest.main()
