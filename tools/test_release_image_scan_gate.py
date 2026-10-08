"""Fixture-authenticated facts test the gate; fixtures never authorize a release."""

from __future__ import annotations

import copy
from dataclasses import replace
from pathlib import Path
import unittest
from unittest.mock import patch

import aggregate_release_image_scans as aggregate
import generate_corresponding_source_review as corresponding
import test_release_gate_reports as native_fixtures
from generate_release_gate_reports import NativeSourceContext
from publish_container_release import sha256
from release_artifacts import InvalidRelease, json_bytes, read_json
from verify_caddy_source_signatures import verification_arguments


class FixtureAuthenticator:
    def __init__(self):
        self.trusted = set()

    def trust(self, path):
        self.trusted.add(path.read_bytes())

    def authenticate(self, content, binding):
        if content not in self.trusted:
            raise InvalidRelease("unsigned fixture evidence")


class ImageScanGateChecks(unittest.TestCase):
    def setUp(self):
        self.native = native_fixtures.GateReports()
        self.native.setUp()
        self.addCleanup(self.native.doCleanups)
        self.binding, self.root = self.native.binding, self.native.root
        self.auth = FixtureAuthenticator()
        self.scans, self.raw_scans, self.graphs, self.raw_graphs = {}, {}, {}, {}
        self.records, self.graph_records = {}, {}
        for platform in aggregate.PLATFORMS:
            pack = self.native.packs[platform] / "runtime-pack.json"
            record = read_json(pack.read_bytes())
            record["bindings"] = {
                c: {"binary_sha256": sha256((c + platform).encode())[7:]}
                for c in aggregate.TARGETS
            }
            pack.write_bytes(json_bytes(record))
            self.native.smokes[platform]["runtime_pack_sha256"] = sha256(
                pack.read_bytes()
            )
        self.native_measurements = self.native.collect_both()
        for path in self.native_measurements.values():
            self.auth.trust(path)
        for platform in aggregate.PLATFORMS:
            native = read_json(self.native_measurements[platform].read_bytes())
            for component in aggregate.TARGETS:
                target = component + "-" + platform.split("/")[1]
                image = native["images"][component]
                context = NativeSourceContext(
                    self.binding.repository,
                    self.binding.version,
                    self.binding.commit,
                    platform,
                )
                self.create_scan(target, context, component, image)
                self.create_graph(target, context, component, image, native["runtime"])

    def save_scan(self, target):
        record, raw = self.records[target]
        contents = json_bytes(raw)
        record["raw_report_sha256"] = sha256(contents)
        record["findings"] = [
            {
                "target": result["Target"],
                "class": result["Class"],
                "finding_sha256": sha256(json_bytes(f)),
                "finding": f,
            }
            for result in raw["Results"]
            for f in result.get("Vulnerabilities", [])
        ]
        self.raw_scans[target].write_bytes(contents)
        self.scans[target].write_bytes(json_bytes(record))
        self.auth.trust(self.scans[target])

    def create_scan(self, target, context, component, image):
        root = self.root / target
        root.mkdir()
        self.scans[target], self.raw_scans[target] = (
            root / "measurement.json",
            root / "scan.json",
        )
        binary = aggregate.ASSETS[context.platform].get("binary", "b" * 64)
        tool = {
            "name": "Trivy",
            "version": aggregate.VERSION,
            "source_commit": aggregate.SOURCE_COMMIT,
            "platform": context.platform,
            "sha256": "sha256:" + binary,
            "archive_sha256": "sha256:" + aggregate.ASSETS[context.platform]["archive"],
            "bundle_sha256": "sha256:" + aggregate.ASSETS[context.platform]["bundle"],
            "certificate_identity": aggregate.IDENTITY,
            "oidc_issuer": aggregate.ISSUER,
            "verification": "online-cosign-bundle-sct-rekor",
            "cosign": {
                "version": aggregate.COSIGN_VERSION,
                "commit": aggregate.COSIGN_COMMIT,
                "sha256": "sha256:" + aggregate.COSIGN_SHA256[context.platform],
            },
        }
        db = {
            "sha256": "sha256:" + "d" * 64,
            "metadata_sha256": "sha256:" + "e" * 64,
            "Version": 2,
            "UpdatedAt": "2026-10-07T07:00:00Z",
            "NextUpdate": "2026-10-08T07:00:00Z",
            "DownloadedAt": "2026-10-07T08:00:00Z",
        }
        db["acquisition"] = {
            "kind": "owned-authenticated-trivy-download",
            "repository": aggregate.DATABASE_REPOSITORY,
            "scanner_sha256": tool["sha256"],
            "database_sha256": db["sha256"],
            "metadata_sha256": db["metadata_sha256"],
            "started_at": "2026-10-07T07:59:00Z",
            "completed_at": "2026-10-07T08:00:00Z",
        }
        finding = {
            "VulnerabilityID": "GO-2026-1234",
            "PkgName": "golang.org/x/crypto",
            "InstalledVersion": "v0.57.0",
            "Status": "affected",
            "Severity": "UNKNOWN",
            "DataSource": {"ID": "govulndb"},
        }
        raw = {
            "SchemaVersion": 2,
            "ArtifactType": "container_image",
            "Trivy": {"Version": aggregate.VERSION},
            "CreatedAt": "2026-10-07T09:00:00Z",
            "Metadata": {
                "ImageID": image["config_digest"],
                "ImageConfig": {
                    "os": "linux",
                    "architecture": context.platform.split("/")[1],
                },
                "OS": {"Family": "alpine"},
            },
            "Results": [
                {
                    "Class": "os-pkgs",
                    "Type": "alpine",
                    "Target": "Alpine",
                    "Packages": [{"Name": "musl"}],
                },
                {
                    "Class": "lang-pkgs",
                    "Type": "gobinary",
                    "Target": aggregate.TARGETS[component],
                    "Packages": [{"Name": "golang.org/x/crypto"}],
                    "Vulnerabilities": [finding],
                },
            ],
        }
        record = {
            "schema_version": 1,
            "kind": "native-image-scanner-measurement",
            "source": context.checked(),
            "execution": "native",
            "target": target,
            "image": image,
            "scanner": tool,
            "database": db,
            "started_at": "2026-10-07T08:59:00Z",
            "scanned_at": raw["CreatedAt"],
            "completed_at": "2026-10-07T09:01:00Z",
            "status": "complete",
            "exit_code": 0,
            "publication_authorized": False,
            "findings_review_required": True,
            "final_image_scanners_gate_pending": True,
        }
        self.records[target] = record, raw
        self.save_scan(target)

    def save_graph(self, target):
        graph = self.graph_records[target]
        for name, body in self.raw_graphs[target].items():
            (self.graphs[target].parent / name).write_bytes(body)
            graph["raw_files"][name] = sha256(body)
        self.graphs[target].write_bytes(json_bytes(graph))
        self.auth.trust(self.graphs[target])

    def create_graph(self, target, context, component, image, runtime):
        folder = self.root / (target + "-graph")
        folder.mkdir()
        self.graphs[target] = folder / "compiler-measurement.json"
        module_sum = "h1:" + "A" * 43 + "="
        main = "zip.psst/backend" if component == "backend" else "caddy"
        root_import = main + "/cmd/server" if component == "backend" else "caddy"
        selected = [
            {"ImportPath": root_import, "Module": {"Path": main}},
            {
                "ImportPath": "golang.org/x/crypto/argon2",
                "Module": {"Path": "golang.org/x/crypto", "Version": "v0.57.0"},
            },
            {"ImportPath": "fmt", "Standard": True},
        ]
        raw = b"\n".join(json_bytes(p) for p in selected)
        paths = {p["ImportPath"] for p in selected}
        env = {
            "GOOS": "linux",
            "GOARCH": context.platform.split("/")[1],
            "CGO_ENABLED": "0",
            "GOTOOLCHAIN": "local",
            "GOFLAGS": "" if component == "backend" else "-mod=vendor",
            "GOPATH": "/go",
            "GOMODCACHE": "/go/pkg/mod",
        }
        settings = [
            {"Key": key, "Value": env[key]} for key in ("GOOS", "GOARCH", "CGO_ENABLED")
        ]
        if component == "web":
            settings.extend(
                [
                    {"Key": "-tags", "Value": "nobadger,nomysql,nopgx"},
                    {"Key": "vcs.revision", "Value": "f" * 40},
                ]
            )
        info = {
            "GoVersion": aggregate.GO_VERSION,
            "Path": root_import,
            "Main": {"Path": main, "Version": "(devel)"},
            "Deps": [
                {"Path": "golang.org/x/crypto", "Version": "v0.57.0", "Sum": module_sum}
            ],
            "Settings": settings,
        }
        binary = (
            "sha256:"
            + read_json(
                (self.native.packs[context.platform] / "runtime-pack.json").read_bytes()
            )["bindings"][component]["binary_sha256"]
        )
        graph = {
            "schema_version": 1,
            "kind": "native-compiler-graph-measurement",
            "source": context.checked(),
            "execution": "native",
            "component": component,
            "executable_target": aggregate.TARGETS[component],
            "image": image,
            "builder": {
                "reference": aggregate.BASES["golang"].rsplit(":", 1)[0]
                + "@sha256:"
                + "1" * 64,
                "config_digest": "sha256:" + "2" * 64,
                "architecture": context.platform.split("/")[1],
                "rootfs_layers": ["sha256:" + "3" * 64],
            },
            "binary": {
                "sha256": binary,
                "rebuilt_sha256": binary if component == "backend" else None,
                "build_info": info,
                "build_info_sha256": sha256(json_bytes(info)),
            },
            "source_graph": {
                "raw_file": "graph.json",
                "sha256": sha256(raw),
                "root_import_path": root_import,
                "package_paths_sha256": sha256(json_bytes(sorted(paths))),
                "modules": [
                    {
                        "path": "golang.org/x/crypto",
                        "version": "v0.57.0",
                        "sum": module_sum,
                    }
                ],
                "build_environment": env,
                "rebuilt_sha256": binary if component == "backend" else None,
            },
            "source_inputs": {
                "runtime_source_asset_sha256": runtime["source_asset"]["digest"],
                "application_archive_sha256": "sha256:" + "a" * 64,
            },
            "advisories": {
                "GO-2026-1234": {
                    "raw_file": "GO-2026-1234.json",
                    "origin": "https://vuln.go.dev/ID/GO-2026-1234.json",
                }
            },
            "raw_files": {},
            "status": "complete",
            "exit_code": 0,
            "completed_at": "2026-10-07T09:02:00Z",
            "publication_authorized": False,
            "finding_dispositions_authorized": False,
        }
        advisory = {
            "id": "GO-2026-1234",
            "modified": "2026-10-07T07:00:00Z",
            "aliases": ["CVE-2026-1234"],
            "affected": [
                {
                    "package": {"ecosystem": "Go", "name": "golang.org/x/crypto"},
                    "ecosystem_specific": {
                        "imports": [{"path": "golang.org/x/crypto/ssh"}]
                    },
                }
            ],
        }
        advisory_raw = json_bytes(advisory)
        graph["advisories"]["GO-2026-1234"]["sha256"] = sha256(advisory_raw)
        raw_files = {"graph.json": raw, "GO-2026-1234.json": advisory_raw}
        graph["binary"]["correspondence"] = {
            "kind": "reproduced-in-release-builder",
            "actual_sha256": binary,
            "rebuilt_sha256": binary,
        }
        if component == "web":
            version, revision = "v2.11.7", "f" * 40
            source, checksums, archive = (
                "caddy_2.11.7_buildable-artifact.tar.gz",
                "caddy_2.11.7_checksums.txt",
                "caddy_2.11.7_linux_" + context.platform.split("/")[1] + ".tar.gz",
            )
            profile = verification_arguments(
                Path("cosign"),
                version,
                revision,
                Path("artifact"),
                Path("certificate"),
                Path("signature"),
            )[1:-5]
            proof = {
                "version": version,
                "source_revision": revision,
                "signer_identity": aggregate.CADDY_WORKFLOW + "@refs/tags/" + version,
                "oidc_issuer": aggregate.CADDY_ISSUER,
                "rekor_url": "https://rekor.sigstore.dev",
                "legacy_sigstore_signatures_verified": True,
                "certificate_transparency_verification_required": True,
                "rekor_verification_required": True,
                "verifier": {
                    "version": aggregate.COSIGN_VERSION,
                    "commit": aggregate.COSIGN_COMMIT,
                    "platform": context.platform,
                    "sha256": aggregate.COSIGN_SHA256[context.platform],
                },
                "verifications": [
                    {
                        "artifact": source,
                        "sha256": "4" * 64,
                        "exit_code": 0,
                        "arguments": profile,
                    },
                    {
                        "artifact": checksums,
                        "sha256": "5" * 64,
                        "exit_code": 0,
                        "arguments": profile,
                    },
                ],
                "signed_sha512_bindings": [
                    {"file": source, "sha512": "6" * 128},
                    {"file": archive, "sha512": "7" * 128},
                ],
            }
            proof_raw = json_bytes(proof)
            graph["source_inputs"].update(
                wrapper_revision=revision,
                authenticated_wrapper_sha256="sha256:" + "4" * 64,
            )
            graph["signature_verification"] = proof
            graph["binary"]["correspondence"] = {
                "kind": "upstream-signed-source-and-binary",
                "signature_proof_file": "signature.json",
                "signature_proof_sha256": sha256(proof_raw),
                "source_asset": {"file": source, "sha512": "6" * 128},
                "checksum_asset": {"file": checksums, "sha512": "8" * 128},
                "binary_archive": {"file": archive, "sha512": "7" * 128},
                "executable_archive_member_sha256": binary,
            }
            raw_files["signature.json"] = proof_raw
        self.graph_records[target], self.raw_graphs[target] = graph, raw_files
        self.save_graph(target)

    def aggregate(self, **overrides):
        return aggregate.aggregate_image_scans(
            self.binding,
            **{
                "native_measurements": self.native_measurements,
                "runtime_packs": self.native.packs,
                "scans": self.scans,
                "raw_scans": self.raw_scans,
                "compiler_graphs": self.graphs,
                "authenticator": self.auth,
                **overrides,
            },
        )

    def test_all_four_native_scans_preserve_findings_and_derive_only_proven_absence(
        self,
    ):
        result = self.aggregate()
        self.assertEqual(result["gate"], "final-image-scanners")
        self.assertEqual(result["binding_digest"], self.binding.digest)
        self.assertEqual(len(result["details"]["scans"]), 4)
        for scan in result["details"]["scans"]:
            self.assertEqual(
                scan["subject"], dict(self.binding.subjects)[scan["target"]]
            )
            finding = scan["findings"][0]
            self.assertEqual(finding["finding"]["Severity"], "UNKNOWN")
            self.assertEqual(finding["finding"]["Status"], "affected")
            self.assertEqual(finding["disposition"], "not-applicable")
            self.assertEqual(finding["affected_packages"], ["golang.org/x/crypto/ssh"])
            self.assertIn("official_advisory", finding)
            self.assertIn("binary_sha256", finding)

    def test_missing_arch_unsigned_measurement_and_changed_raw_scan_fail(self):
        subset = {k: v for k, v in self.scans.items() if k != "backend-arm64"}
        with self.assertRaises(InvalidRelease):
            self.aggregate(scans=subset)
        path = self.scans["backend-amd64"]
        before = path.read_bytes()
        path.write_bytes(before + b"\n")
        with self.assertRaisesRegex(InvalidRelease, "unsigned"):
            self.aggregate()
        path.write_bytes(before)
        raw = self.raw_scans["web-amd64"]
        raw.write_bytes(raw.read_bytes() + b"\n")
        with self.assertRaisesRegex(InvalidRelease, "Raw scanner"):
            self.aggregate()

    def test_wrong_source_image_tool_or_retained_database_even_authenticated_fails(
        self,
    ):
        record, _ = self.records["backend-amd64"]
        original = copy.deepcopy(record)
        cases = [
            lambda r: r["source"].update(commit="b" * 40),
            lambda r: r["image"].update(config_digest="sha256:" + "f" * 64),
            lambda r: r["scanner"].update(archive_sha256="sha256:" + "f" * 64),
            lambda r: r["database"].update(
                acquisition={"kind": "retained-unapproved-snapshot"}
            ),
        ]
        for change in cases:
            record.clear()
            record.update(copy.deepcopy(original))
            change(record)
            self.save_scan("backend-amd64")
            with self.subTest(change=change), self.assertRaises(InvalidRelease):
                self.aggregate()

    def test_missing_graph_present_affected_package_uncertain_advisory_and_version_fail(
        self,
    ):
        with self.assertRaisesRegex(InvalidRelease, "compiler evidence"):
            self.aggregate(
                compiler_graphs={
                    k: v for k, v in self.graphs.items() if k != "backend-amd64"
                }
            )
        target = "backend-amd64"
        graph, raw = self.graph_records[target], self.raw_graphs[target]
        original_graph, original_raw = copy.deepcopy(graph), copy.deepcopy(raw)
        for change in (
            "present",
            "missing-imports",
            "wrong-alias",
            "withdrawn",
            "version",
            "binary",
            "rebuilt",
            "tagged-builder",
            "experiment",
        ):
            graph.clear()
            graph.update(copy.deepcopy(original_graph))
            raw.clear()
            raw.update(copy.deepcopy(original_raw))
            if change in {"present", "missing-imports", "wrong-alias", "withdrawn"}:
                advisory = read_json(raw["GO-2026-1234.json"])
                if change == "present":
                    advisory["affected"][0]["ecosystem_specific"]["imports"][0][
                        "path"
                    ] = "golang.org/x/crypto/argon2"
                if change == "missing-imports":
                    advisory["affected"][0]["ecosystem_specific"]["imports"] = []
                if change == "wrong-alias":
                    advisory["id"] = "GO-2026-9999"
                if change == "withdrawn":
                    advisory["withdrawn"] = "2026-10-07T07:30:00Z"
                raw["GO-2026-1234.json"] = json_bytes(advisory)
                graph["advisories"]["GO-2026-1234"]["sha256"] = sha256(
                    raw["GO-2026-1234.json"]
                )
            if change == "version":
                graph["source_graph"]["modules"][0]["version"] = "v0.56.0"
            if change == "binary":
                graph["binary"]["sha256"] = "sha256:" + "f" * 64
            if change == "rebuilt":
                graph["binary"]["rebuilt_sha256"] = "sha256:" + "f" * 64
            if change == "tagged-builder":
                graph["builder"]["reference"] = (
                    aggregate.BASES["golang"] + "@sha256:" + "1" * 64
                )
            if change == "experiment":
                graph["binary"]["build_info"]["Settings"].append(
                    {"Key": "GOEXPERIMENT", "Value": "boringcrypto"}
                )
                graph["binary"]["build_info_sha256"] = sha256(
                    json_bytes(graph["binary"]["build_info"])
                )
            self.save_graph(target)
            with self.subTest(change=change), self.assertRaises(InvalidRelease):
                self.aggregate()

    def test_os_non_go_and_generic_review_flags_do_not_authorize(self):
        target = "backend-amd64"
        record, raw = self.records[target]
        finding = raw["Results"][1]["Vulnerabilities"][0]
        finding.update(disposition="not-applicable", reason="caller says so")
        raw["Results"][0]["Vulnerabilities"] = [copy.deepcopy(finding)]
        self.save_scan(target)
        with self.assertRaisesRegex(InvalidRelease, "OS/non-application"):
            self.aggregate()

    def test_upstream_typed_correspondence_rejects_weak_signature_but_not_nonreproducibility(
        self,
    ):
        self.assertIsNone(self.graph_records["web-amd64"]["binary"]["rebuilt_sha256"])
        self.aggregate()
        target = "web-amd64"
        graph, raw = self.graph_records[target], self.raw_graphs[target]
        proof = read_json(raw["signature.json"])
        proof["verifications"][0]["arguments"].append("--insecure-ignore-tlog=true")
        raw["signature.json"] = json_bytes(proof)
        graph["signature_verification"] = proof
        graph["binary"]["correspondence"]["signature_proof_sha256"] = sha256(
            raw["signature.json"]
        )
        self.save_graph(target)
        with self.assertRaisesRegex(InvalidRelease, "verified profile"):
            self.aggregate()

    def test_empty_scans_need_no_dismissals_but_still_all_native_authenticated_targets(
        self,
    ):
        for target in self.scans:
            self.records[target][1]["Results"][1]["Vulnerabilities"] = []
            self.save_scan(target)
        result = self.aggregate(compiler_graphs={})
        self.assertTrue(
            all(scan["findings"] == [] for scan in result["details"]["scans"])
        )
        self.aggregate()  # Valid optional measured graphs remain compatible.

    def test_raw_graph_substitution_and_wrong_source_asset_fail(self):
        target = "backend-amd64"
        file = self.graphs[target].parent / "graph.json"
        before = file.read_bytes()
        file.write_bytes(before + b"\n")
        with self.assertRaisesRegex(InvalidRelease, "substituted"):
            self.aggregate()
        file.write_bytes(before)
        self.graph_records[target]["source_inputs"]["runtime_source_asset_sha256"] = (
            "sha256:" + "f" * 64
        )
        self.save_graph(target)
        with self.assertRaisesRegex(InvalidRelease, "source asset differs"):
            self.aggregate()

    def test_backend_source_binds_both_actual_binaries_to_verified_retained_modules(
        self,
    ):
        git_archive = b"committed fixture archive"
        source_scans, collections, replays = {}, {}, {}
        subjects = dict(self.binding.subjects)
        for platform in aggregate.PLATFORMS:
            arch = platform.split("/")[1]
            folder = self.root / ("dependency-" + arch)
            folder.mkdir()
            collections[platform] = folder
            asset_name = (
                f"psst.zip-dependency-inputs-{self.binding.version}-{arch}.tar.gz"
            )
            asset = b"independently replayed fixture dependency originals"
            (folder / asset_name).write_bytes(asset)
            (folder / "dependency-collection.json").write_bytes(b"fixture collection")
            scan = folder / "source-scan-measurement.json"
            scan.write_bytes(
                json_bytes(
                    {
                        "source": corresponding.NativeSourceContext(
                            self.binding.repository,
                            self.binding.version,
                            self.binding.commit,
                            platform,
                        ).checked(),
                        "scans": [],
                    }
                )
            )
            source_scans[platform] = scan
            self.auth.trust(scan)
            subjects["source:" + asset_name] = (
                "file:" + asset_name + "@" + sha256(asset)
            )
            target = "backend-" + arch
            self.graph_records[target]["source_inputs"][
                "application_archive_sha256"
            ] = sha256(git_archive)
            self.save_graph(target)
            replays[platform] = {
                "source": read_json(scan.read_bytes())["source"],
                "package_inputs_replayed": True,
                "source_measurement_sha256": sha256(scan.read_bytes()),
                "archive_sha256": sha256(asset),
                "collection_sha256": sha256(b"fixture collection"),
                "go_modules": 2,
                "go_module_inputs": [
                    {
                        "module": "golang.org/x/crypto",
                        "version": "v0.57.0",
                        "sum": "h1:" + "A" * 43 + "=",
                        "zip_sha256": sha256(b"crypto"),
                    },
                    {
                        "module": "build-only-module",
                        "version": "v1.0.0",
                        "sum": "h1:" + "B" * 43 + "=",
                        "zip_sha256": sha256(b"build tool"),
                    },
                ],
            }
        upstream_collection = self.root / "upstream-originals"
        upstream_collection.mkdir()
        upstream_name = "psst.zip-upstream-inputs-v1.2.3.tar.gz"
        upstream_raw = b"independently replayed preferred source originals"
        (upstream_collection / upstream_name).write_bytes(upstream_raw)
        (upstream_collection / corresponding.upstream_inputs.RECORD).write_bytes(
            b"fixture upstream record"
        )
        upstream = {
            "asset": {"name": upstream_name, "digest": sha256(upstream_raw)},
            "collection_sha256": sha256(b"fixture upstream record"),
        }
        subjects["source:" + upstream_name] = (
            "file:" + upstream_name + "@" + sha256(upstream_raw)
        )
        binding = replace(self.binding, subjects=tuple(sorted(subjects.items())))

        def replay(context, root, collection, scan, *, modules):
            self.assertEqual(
                modules,
                frozenset(
                    {
                        "modernc.org/sqlite",
                        "modernc.org/libc",
                        "modernc.org/cc/v4",
                        "modernc.org/ccgo/v4",
                        "modernc.org/fileutil",
                    }
                ),
            )
            self.assertEqual(
                (root, collection, scan),
                (
                    self.root,
                    collections[context.platform],
                    source_scans[context.platform],
                ),
            )
            return replays[context.platform], {}

        def verify(**overrides):
            return corresponding.verify_backend_source_inputs(
                binding,
                **{
                    "root": self.root,
                    "native_measurements": self.native_measurements,
                    "runtime_packs": self.native.packs,
                    "dependency_collections": collections,
                    "source_scans": source_scans,
                    "compiler_graphs": {
                        key: value
                        for key, value in self.graphs.items()
                        if key.startswith("backend-")
                    },
                    "upstream_collection": upstream_collection,
                    "authenticator": self.auth,
                    **overrides,
                },
            )

        with patch.object(
            corresponding, "git", return_value=git_archive
        ) as git_read, patch.object(
            corresponding.dependency_inputs,
            "verify_module_source_files",
            side_effect=replay,
        ), patch.object(
            corresponding.upstream_inputs,
            "verify_source_files",
            return_value=(upstream, {"fixture": "originals"}),
        ) as offering, patch.object(
            corresponding.backend_preferred,
            "verify_relationships",
            return_value={"associations": [], "pending": ["fixture translation"]},
        ) as preferred:
            result = verify()
            self.assertEqual(
                git_read.call_args_list,
                [
                    unittest.mock.call(self.root, "archive", binding.commit),
                    unittest.mock.call(
                        self.root, "show", binding.commit + ":tools/sqlite_vendoring.go"
                    ),
                ],
            )
            offering.assert_called_once_with(
                self.root,
                binding.repository,
                binding.version,
                binding.commit,
                upstream_collection,
                component="backend",
            )
            self.assertEqual(preferred.call_count, 1)
            self.assertEqual(set(result["images"]), {"backend-amd64", "backend-arm64"})
            self.assertTrue(result["backend_source_inputs_verified"])
            self.assertFalse(result["corresponding_source_completeness_verified"])
            self.assertFalse(result["publication_authorized"])
            self.assertEqual(
                len(result["images"]["backend-amd64"]["binary_module_inputs"]), 1
            )
            offering.reset_mock()
            with self.assertRaisesRegex(InvalidRelease, "unsigned"):
                verify(authenticator=FixtureAuthenticator())
            offering.assert_not_called()
            upstream["asset"]["digest"] = sha256(b"substituted offering")
            with self.assertRaisesRegex(
                InvalidRelease, "upstream offering.*publication binding"
            ):
                verify()
            upstream["asset"]["digest"] = sha256(upstream_raw)

            def mutate(*args, **kwargs):
                (upstream_collection / upstream_name).write_bytes(
                    b"changed after preferred replay"
                )
                return {"fixture": "mapped"}

            preferred.side_effect = mutate
            with self.assertRaisesRegex(InvalidRelease, "changed during replay"):
                verify()
            (upstream_collection / upstream_name).write_bytes(upstream_raw)
            preferred.side_effect = None
            for path in (self.graphs["backend-amd64"], source_scans["linux/amd64"]):
                content = path.read_bytes()
                self.auth.trusted.remove(content)
                with self.subTest(unsigned=path.name), self.assertRaisesRegex(
                    InvalidRelease, "unsigned"
                ):
                    verify()
                self.auth.trusted.add(content)
            for inputs in (
                "source_scans",
                "compiler_graphs",
                "dependency_collections",
                "runtime_packs",
            ):
                with self.subTest(missing=inputs), self.assertRaises(InvalidRelease):
                    verify(**{inputs: {}})
            target = "backend-amd64"
            original = copy.deepcopy(self.graph_records[target])
            self.graph_records[target]["source_inputs"][
                "application_archive_sha256"
            ] = sha256(b"wrong commit archive")
            self.save_graph(target)
            with self.assertRaisesRegex(InvalidRelease, "source archive differs"):
                verify()
            self.graph_records[target] = original
            self.save_graph(target)
            original_replay = copy.deepcopy(replays["linux/amd64"])
            replays["linux/amd64"]["archive_sha256"] = sha256(b"other asset")
            with self.assertRaisesRegex(InvalidRelease, "publication binding"):
                verify()
            replays["linux/amd64"] = copy.deepcopy(original_replay)
            replays["linux/amd64"]["source"]["platform"] = "linux/arm64"
            with self.assertRaisesRegex(InvalidRelease, "native source scan"):
                verify()
            replays["linux/amd64"] = copy.deepcopy(original_replay)
            for key in ("version", "sum"):
                replays["linux/amd64"]["go_module_inputs"][0][key] = "different"
                with self.subTest(module_field=key), self.assertRaisesRegex(
                    InvalidRelease, "version/checksum"
                ):
                    verify()
                replays["linux/amd64"] = copy.deepcopy(original_replay)
            replays["linux/amd64"]["go_module_inputs"].pop(0)
            replays["linux/amd64"]["go_modules"] = 1
            with self.assertRaisesRegex(InvalidRelease, "version/checksum"):
                verify()


if __name__ == "__main__":
    unittest.main()
