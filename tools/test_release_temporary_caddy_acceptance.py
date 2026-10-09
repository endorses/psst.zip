"""Offline native fixtures exercise committed Caddy acceptance boundaries.

Fixture attestations supply trust only to disposable test bytes; they do not
approve a release or weaken the live GitHub evidence verifier.
"""

from __future__ import annotations

import copy
from dataclasses import replace
from datetime import datetime, timezone
import unittest
from unittest.mock import patch

import generate_distribution_review as distribution
from measure_release_source_scans import json_stream
import publish_container_release as publication
import temporary_caddy_acceptance as acceptance
import test_release_image_scan_gate as image_fixtures
import test_release_publication as publication_fixtures
from release_artifacts import InvalidRelease, json_bytes, read_json


class TemporaryCaddyAcceptanceChecks(unittest.TestCase):
    def setUp(self):
        self.fixture = image_fixtures.ImageScanGateChecks()
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.repository = self.fixture.native.fixture.fixture
        self.git_root = self.repository.root
        self.root = self.fixture.root
        self.now = datetime(2026, 10, 10, tzinfo=timezone.utc)
        self.clock = patch.object(acceptance, "utc_now", return_value=self.now)
        self.clock.start()
        self.addCleanup(self.clock.stop)
        policy = {
            "schema_version": 1,
            "kind": "container-distribution-review-policy",
            "environment": "container-release",
            "reviewers": ["fixture-owner"],
        }
        self.node = {
            "schema_version": 1,
            "kind": "temporary-caddy-vulnerability-acceptance",
            "id": "disposable-caddy-fixture",
            "repository": self.fixture.binding.repository,
            "authorized_by": "fixture-owner",
            "authorized_at": "2026-10-09T00:00:00Z",
            "expires_at": "2026-10-23T00:00:00Z",
            "reason": "Disposable test acceptance preserves affected code and exact scope.",
            "caddy_version": "v2.11.7",
            "source_revision": "f" * 40,
            "go_version": "go1.26.8",
            "base_image": "docker.io/library/caddy@sha256:" + "8" * 64,
            "platforms": {
                platform: {"base_digest": publication.sha256(platform.encode())}
                for platform in acceptance.PLATFORMS
            },
            # One advisory affects both modules, independently scoped. These
            # test identities remain valid after the live exception is removed.
            "findings": [
                {
                    "go_id": "GO-2026-6603",
                    "scanner_id": "CVE-2026-78659",
                    "module": module,
                    "installed_version": installed,
                    "fixed_version": fixed,
                }
                for module, installed, fixed in (
                    ("stdlib", "v1.26.8", "1.26.9, 1.27.2"),
                    ("golang.org/x/net", "v0.59.0", "0.60.0"),
                )
            ],
        }
        self.advisory = {
            "id": "GO-2026-6603",
            "modified": "2026-10-09T00:00:00Z",
            "aliases": ["CVE-2026-78659"],
            "affected": [
                {
                    "package": {"ecosystem": "Go", "name": module},
                    "ecosystem_specific": {"imports": [{"path": path}]},
                }
                for module, path in (
                    ("stdlib", "net/http"),
                    ("golang.org/x/net", "golang.org/x/net/http2"),
                )
            ],
        }
        advisory_raw = json_bytes(self.advisory)
        for row in self.node["findings"]:
            row["advisory_sha256"] = publication.sha256(advisory_raw)
        for platform in acceptance.PLATFORMS:
            target = "web-" + platform.split("/")[1]
            graph = self.fixture.graph_records[target]
            self.node["platforms"][platform]["binary_sha256"] = graph["binary"][
                "sha256"
            ]
            self.add_affected_graph(target, advisory_raw)
            _, scan = self.fixture.records[target]
            scan["Results"][1]["Vulnerabilities"] = [
                {
                    "VulnerabilityID": row["scanner_id"],
                    "VendorIDs": [row["go_id"]],
                    "PkgName": row["module"],
                    "InstalledVersion": row["installed_version"],
                    "FixedVersion": row["fixed_version"],
                    "Status": "affected",
                    "Severity": "HIGH",
                    "DataSource": {"ID": "govulndb"},
                }
                for row in self.node["findings"]
            ]
        policy["temporary_caddy_exception"] = self.node
        policy_path = self.git_root / publication.SOURCE_REVIEW_POLICY
        policy_path.parent.mkdir(exist_ok=True)
        policy_path.write_bytes(json_bytes(policy))
        self.repository.git("add", publication.SOURCE_REVIEW_POLICY)
        self.repository.git("commit", "-qm", "Disposable temporary Caddy policy")
        self.binding = replace(
            self.fixture.binding,
            commit=self.repository.git("rev-parse", "HEAD").decode().strip(),
        )
        self.fixture.binding = self.binding
        self.rebind_native_records()
        self.bases = self.root / "accepted-bases.json"
        self.bases.write_bytes(
            json_bytes(
                {
                    "source_commit": self.binding.commit,
                    "version": self.binding.version,
                    "base_images": {"caddy": self.node["base_image"]},
                    "base_platform_digests": {
                        "caddy": {
                            p: value["base_digest"]
                            for p, value in self.node["platforms"].items()
                        }
                    },
                }
            )
        )
        self.fixture.auth.trust(self.bases)
        self.policy, self.policy_fact = distribution.committed_policy(
            self.git_root, self.binding
        )

    def add_affected_graph(self, target, advisory_raw):
        graph = self.fixture.graph_records[target]
        raw = self.fixture.raw_graphs[target]
        selected = json_stream(raw["graph.json"])
        selected.extend(
            [
                {"ImportPath": "net/http", "Standard": True},
                {
                    "ImportPath": "golang.org/x/net/http2",
                    "Module": {"Path": "golang.org/x/net", "Version": "v0.59.0"},
                },
            ]
        )
        raw["graph.json"] = b"\n".join(json_bytes(item) for item in selected)
        source = graph["source_graph"]
        source["sha256"] = publication.sha256(raw["graph.json"])
        source["package_paths_sha256"] = publication.sha256(
            json_bytes(sorted({item["ImportPath"] for item in selected}))
        )
        dependency = {
            "Path": "golang.org/x/net",
            "Version": "v0.59.0",
            "Sum": "h1:" + "B" * 43 + "=",
        }
        info = graph["binary"]["build_info"]
        info["Deps"].append(dependency)
        graph["binary"]["build_info_sha256"] = publication.sha256(json_bytes(info))
        source["modules"].append(
            {
                "path": dependency["Path"],
                "version": dependency["Version"],
                "sum": dependency["Sum"],
            }
        )
        raw["GO-2026-6603.json"] = advisory_raw
        graph["advisories"]["GO-2026-6603"] = {
            "raw_file": "GO-2026-6603.json",
            "origin": "https://vuln.go.dev/ID/GO-2026-6603.json",
            "sha256": publication.sha256(advisory_raw),
        }

    def rebind_native_records(self):
        for platform in acceptance.PLATFORMS:
            pack_path = self.fixture.native.packs[platform] / "runtime-pack.json"
            pack = read_json(pack_path.read_bytes())
            pack["revision"] = self.binding.commit
            original = publication.sha256(("original-web-" + platform).encode())
            pack["bindings"]["web"]["original_image_id"] = original
            pack_path.write_bytes(json_bytes(pack))
            native_path = self.fixture.native_measurements[platform]
            native = read_json(native_path.read_bytes())
            native["source"]["commit"] = self.binding.commit
            native["smoke"]["revision"] = self.binding.commit
            digest = publication.sha256(pack_path.read_bytes())
            native["smoke"]["runtime_pack_sha256"] = digest
            native["runtime"]["runtime_pack_sha256"] = digest
            native_path.write_bytes(json_bytes(native))
            self.fixture.auth.trust(native_path)
            for component in ("backend", "web"):
                target = component + "-" + platform.split("/")[1]
                self.fixture.records[target][0]["source"][
                    "commit"
                ] = self.binding.commit
                graph = self.fixture.graph_records[target]
                graph["source"]["commit"] = self.binding.commit
                if component == "web":
                    proof = graph["signature_verification"]
                    proof["image_id"] = original
                    proof_raw = json_bytes(proof)
                    self.fixture.raw_graphs[target]["signature.json"] = proof_raw
                    graph["binary"]["correspondence"]["signature_proof_sha256"] = (
                        publication.sha256(proof_raw)
                    )
                self.fixture.save_graph(target)
                self.fixture.save_scan(target)

    def aggregate(self):
        return self.fixture.aggregate(
            repository_root=self.git_root, resolved_bases=self.bases
        )

    def verify_scanner(self, report, gate="final-image-scanners"):
        path = self.root / "scanner-gate.json"
        path.write_bytes(json_bytes(report))
        verifier = publication_fixtures.FixtureVerifier()
        verifier.details[gate] = report["details"]
        # Use the actual disposable committed Git policy; no caller disposition
        # flag or mocked policy content supplies the acceptance.
        real_loader = acceptance.committed_acceptance
        with patch.object(
            publication,
            "committed_acceptance",
            side_effect=lambda binding: real_loader(binding, self.git_root),
        ):
            return publication.verify_gates(
                {gate: path}, frozenset({gate}), self.binding, verifier
            )

    def test_exact_native_findings_derive_distinct_temporary_dispositions_and_consume_standalone(
        self,
    ):
        report = self.aggregate()
        for scan in report["details"]["scans"]:
            expected = (
                "temporarily-accepted"
                if scan["target"].startswith("web-")
                else "not-applicable"
            )
            self.assertTrue(
                all(row["disposition"] == expected for row in scan["findings"])
            )
            if expected == "temporarily-accepted":
                self.assertEqual(len(scan["findings"]), 2)
                self.assertEqual(
                    {row["finding"]["PkgName"] for row in scan["findings"]},
                    {"stdlib", "golang.org/x/net"},
                )
                for row in scan["findings"]:
                    self.assertEqual(row["acceptance"]["policy"], self.policy_fact)
                    self.assertEqual(row["finding"]["Status"], "affected")
                    self.assertEqual(row["finding"]["Severity"], "HIGH")
        self.assertEqual(set(self.verify_scanner(report)), {"final-image-scanners"})

    def test_uncommitted_policy_changes_do_not_extend_acceptance(self):
        policy_path = self.git_root / publication.SOURCE_REVIEW_POLICY
        policy = read_json(policy_path.read_bytes())
        policy["temporary_caddy_exception"]["findings"][0][
            "installed_version"
        ] = "v0.60.0"
        policy_path.write_bytes(json_bytes(policy))
        report = self.aggregate()
        self.assertEqual(
            report["details"]["temporary_caddy_exception_policy"], self.policy_fact
        )
        self.verify_scanner(report)

    def test_expiry_blocks_aggregation_and_consumer_without_affecting_public_readback(
        self,
    ):
        report = self.aggregate()
        with patch.object(
            acceptance, "utc_now", return_value=acceptance.date(self.node["expires_at"])
        ):
            with self.assertRaisesRegex(InvalidRelease, "expired"):
                self.aggregate()
            with self.assertRaisesRegex(InvalidRelease, "expired"):
                self.verify_scanner(report)
            verifier = publication_fixtures.FixtureVerifier()
            reports = {}
            for gate in publication.READBACK_GATES:
                reports[gate] = self.root / (gate + ".json")
                reports[gate].write_bytes(b'{"fixture":true}\n')
            with patch.object(
                publication,
                "committed_acceptance",
                side_effect=AssertionError(
                    "Existing release readback must not reopen temporary acceptance"
                ),
            ):
                result = publication.verify_gates(
                    reports, publication.READBACK_GATES, self.binding, verifier
                )
            self.assertEqual(set(result), set(publication.READBACK_GATES))

    def test_changed_advisory_bytes_and_unreviewed_findings_fail_even_when_authenticated(
        self,
    ):
        target = "web-amd64"
        graph, raw = self.fixture.graph_records[target], self.fixture.raw_graphs[target]
        original = raw["GO-2026-6603.json"]
        changed = copy.deepcopy(self.advisory)
        changed["modified"] = "2026-10-10T00:00:00Z"
        raw["GO-2026-6603.json"] = json_bytes(changed)
        graph["advisories"]["GO-2026-6603"]["sha256"] = publication.sha256(
            raw["GO-2026-6603.json"]
        )
        self.fixture.save_graph(target)
        with self.assertRaisesRegex(InvalidRelease, "outside the temporary acceptance"):
            self.aggregate()
        raw["GO-2026-6603.json"] = original
        graph["advisories"]["GO-2026-6603"]["sha256"] = publication.sha256(original)
        self.fixture.save_graph(target)
        _, scan = self.fixture.records[target]
        scan["Results"][1]["Vulnerabilities"][0]["FixedVersion"] = "0.61.0"
        self.fixture.save_scan(target)
        with self.assertRaisesRegex(InvalidRelease, "outside the temporary acceptance"):
            self.aggregate()
        new_advisory = copy.deepcopy(self.advisory)
        new_advisory["id"] = "GO-2026-9999"
        new_advisory["aliases"] = ["CVE-2026-9999"]
        raw["GO-2026-9999.json"] = json_bytes(new_advisory)
        graph["advisories"]["GO-2026-9999"] = {
            "raw_file": "GO-2026-9999.json",
            "origin": "https://vuln.go.dev/ID/GO-2026-9999.json",
            "sha256": publication.sha256(raw["GO-2026-9999.json"]),
        }
        self.fixture.save_graph(target)
        finding = scan["Results"][1]["Vulnerabilities"][0]
        finding.update(VulnerabilityID="CVE-2026-9999", VendorIDs=["GO-2026-9999"])
        self.fixture.save_scan(target)
        with self.assertRaisesRegex(InvalidRelease, "outside the temporary acceptance"):
            self.aggregate()

    def test_changed_base_binary_or_original_runtime_proof_fail_before_acceptance(self):
        bases = self.bases.read_bytes()
        changed = read_json(bases)
        changed["base_platform_digests"]["caddy"]["linux/amd64"] = "sha256:" + "0" * 64
        self.bases.write_bytes(json_bytes(changed))
        self.fixture.auth.trust(self.bases)
        with self.assertRaisesRegex(InvalidRelease, "Resolved Caddy"):
            self.aggregate()
        self.bases.write_bytes(bases)
        target = "web-amd64"
        graph = self.fixture.graph_records[target]
        original_proof = copy.deepcopy(graph["signature_verification"])
        graph["signature_verification"]["image_id"] = "sha256:" + "0" * 64
        proof_raw = json_bytes(graph["signature_verification"])
        self.fixture.raw_graphs[target]["signature.json"] = proof_raw
        graph["binary"]["correspondence"]["signature_proof_sha256"] = (
            publication.sha256(proof_raw)
        )
        self.fixture.save_graph(target)
        with self.assertRaisesRegex(InvalidRelease, "upstream proof differs"):
            self.aggregate()
        graph["signature_verification"] = original_proof
        proof_raw = json_bytes(original_proof)
        self.fixture.raw_graphs[target]["signature.json"] = proof_raw
        graph["binary"]["correspondence"]["signature_proof_sha256"] = (
            publication.sha256(proof_raw)
        )
        # A trusted producer can attest a different internally consistent binary;
        # it still must not inherit this exact binary's risk acceptance.
        different_binary = "sha256:" + "0" * 64
        graph["binary"]["sha256"] = different_binary
        graph["binary"]["correspondence"][
            "executable_archive_member_sha256"
        ] = different_binary
        pack_path = self.fixture.native.packs["linux/amd64"] / "runtime-pack.json"
        pack = read_json(pack_path.read_bytes())
        pack["bindings"]["web"]["binary_sha256"] = different_binary[7:]
        pack_path.write_bytes(json_bytes(pack))
        native_path = self.fixture.native_measurements["linux/amd64"]
        native = read_json(native_path.read_bytes())
        digest = publication.sha256(pack_path.read_bytes())
        native["smoke"]["runtime_pack_sha256"] = digest
        native["runtime"]["runtime_pack_sha256"] = digest
        native_path.write_bytes(json_bytes(native))
        self.fixture.auth.trust(native_path)
        self.fixture.save_graph(target)
        with self.assertRaisesRegex(InvalidRelease, "outside temporary acceptance"):
            self.aggregate()

    def test_affected_backend_packages_remain_blocked_by_real_aggregation(self):
        target = "backend-amd64"
        graph, raw = self.fixture.graph_records[target], self.fixture.raw_graphs[target]
        advisory = read_json(raw["GO-2026-1234.json"])
        advisory["affected"][0]["ecosystem_specific"]["imports"] = [
            {"path": "golang.org/x/crypto/argon2"}
        ]
        raw["GO-2026-1234.json"] = json_bytes(advisory)
        graph["advisories"]["GO-2026-1234"]["sha256"] = publication.sha256(
            raw["GO-2026-1234.json"]
        )
        self.fixture.save_graph(target)
        with self.assertRaisesRegex(InvalidRelease, "Backend findings"):
            self.aggregate()

    def test_source_backend_and_tampered_consumer_facts_cannot_use_acceptance(self):
        report = self.aggregate()
        accepted = next(
            scan for scan in report["details"]["scans"] if scan["target"] == "web-amd64"
        )["findings"][0]
        for mutation in ("backend", "binary", "raw-finding", "policy", "new-id"):
            changed = copy.deepcopy(report)
            scan = next(
                scan
                for scan in changed["details"]["scans"]
                if scan["target"] == "web-amd64"
            )
            row = scan["findings"][0]
            if mutation == "backend":
                backend = next(
                    item
                    for item in changed["details"]["scans"]
                    if item["target"] == "backend-amd64"
                )
                backend["findings"] = [copy.deepcopy(row)]
            elif mutation == "binary":
                row["binary_sha256"] = "sha256:" + "0" * 64
            elif mutation == "raw-finding":
                row["finding"]["Severity"] = "CRITICAL"
            elif mutation == "policy":
                row["acceptance"]["policy"]["git_blob"] = "0" * 40
            else:
                row["official_advisory"]["id"] = "GO-2026-9999"
            with self.subTest(mutation=mutation), self.assertRaises(InvalidRelease):
                self.verify_scanner(changed)
        source_details = (
            publication_fixtures.FixtureVerifier()
            .verify("source-scanners", self.bases, self.binding)
            .details
        )
        source_details["scans"][0]["findings"] = [accepted]
        with self.assertRaisesRegex(InvalidRelease, "Source findings"):
            self.verify_scanner({"details": source_details}, gate="source-scanners")


if __name__ == "__main__":
    unittest.main()
