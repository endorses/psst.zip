"""Source gate fixture authentication is a test boundary, never live approval."""

from __future__ import annotations

import copy
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import aggregate_release_source_scans as gate
import measure_release_source_scans as producer
from generate_release_gate_reports import NativeSourceContext
from prepare_release_candidate import BASES
from publish_container_release import Binding, sha256
from release_artifacts import InvalidRelease, PLATFORMS, json_bytes
from test_release_source_scans import go_fixture, stream


class FixtureAuthenticator:
    def __init__(self, binding):
        self.binding = binding
        self.contents = set()

    def authenticate(self, content, binding):
        if binding != self.binding or sha256(content) not in self.contents:
            raise InvalidRelease(
                "Fixture authentication rejected substituted bytes/binding"
            )


class SourceGateTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="psst-source-gate-fixture-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.sum = "h1:" + "a" * 43 + "="
        self.lock = json_bytes(
            {
                "name": "web",
                "version": "1.0.0",
                "lockfileVersion": 3,
                "packages": {"": {"name": "web", "version": "1.0.0"}},
            }
        )
        files = {
            "backend/go.mod": b"module example\n\ngo 1.23.0\nrequire dependency v1.0.0\n",
            "backend/go.sum": f"dependency v1.0.0 {self.sum}\n".encode(),
            "backend/cmd/server/main.go": b"package main\nfunc main() {}\n",
            "web/package.json": b'{"name":"web","version":"1.0.0"}\n',
            "web/package-lock.json": self.lock,
        }
        for name, raw in files.items():
            p = self.repo / name
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_bytes(raw)
        self.git("init", "-q")
        self.git("add", ".")
        self.git(
            "-c",
            "user.name=Fixture",
            "-c",
            "user.email=fixture@example.invalid",
            "commit",
            "-qm",
            "Fixture source",
        )
        commit = self.git("rev-parse", "HEAD").decode().strip()
        subjects = tuple(
            (name, "sha256:" + "f" * 64)
            for name in [
                "manifest",
                "bundle",
                "backend-index",
                "web-index",
                "backend-amd64",
                "backend-arm64",
                "web-amd64",
                "web-arm64",
                "source:fixture.tar.gz",
            ]
        )
        self.binding = Binding("endorses/psst.zip", "v1.0.0", commit, subjects)
        self.auth = FixtureAuthenticator(self.binding)
        source = self.root / "snapshot"
        source.mkdir()
        self.archive_sha = producer.source_snapshot(
            self.repo, commit, source, producer.run
        )
        self.inputs = {
            str(p.relative_to(source)): sha256(p.read_bytes())
            for name in ["backend", "web"]
            for p in (source / name).rglob("*")
            if p.is_file()
        }
        self.bases = {
            "schema_version": 1,
            "kind": "release-candidate",
            "candidate_only": True,
            "version": self.binding.version,
            "source_commit": commit,
            "platforms": PLATFORMS,
            "base_images": {
                name: tagged.rsplit(":", 1)[0] + "@sha256:" + "b" * 64
                for name, tagged in BASES.items()
            },
            "base_platform_digests": {
                name: {
                    "linux/amd64": "sha256:" + "c" * 64,
                    "linux/arm64": "sha256:" + "d" * 64,
                }
                for name in BASES
            },
        }
        self.base_path = self.root / "bases.json"
        self.authorize(self.base_path, self.bases)
        self.paths = {}
        self.records = {}
        self.raw = {}
        for platform in PLATFORMS:
            arch = platform.split("/")[1]
            root = self.root / arch
            root.mkdir()
            raw_go, messages = go_fixture()
            for m in messages:
                if "SBOM" in m:
                    m["SBOM"]["roots"] = ["example/cmd/server"]
                if "osv" in m:
                    m["osv"]["modified"] = "2026-10-07T00:00:00Z"
            raw_go["govulncheck.json"] = stream(messages)
            for filename in [
                "go-roots.json",
                "go-all-graph.json",
                "go-server-graph.json",
            ]:
                graph = producer.json_stream(raw_go[filename])
                for row in graph:
                    if row["ImportPath"] == "example/main":
                        row["ImportPath"] = "example/cmd/server"
                    if row.get("Module", {}).get("Path") == "dependency":
                        row["Module"]["Sum"] = self.sum
                raw_go[filename] = stream(graph)
            raw_go["go-modules.json"] = stream(
                [
                    {"Path": "example", "Main": True},
                    {"Path": "dependency", "Version": "v1.0.0", "Sum": self.sum},
                ]
            )
            environment = json.loads(raw_go["go-env.json"])
            environment.update(
                {
                    "CGO_ENABLED": "0",
                    "GOARCH": arch,
                    "GOAMD64": "v1" if arch == "amd64" else "",
                    "GOARM64": "v8.0" if arch == "arm64" else "",
                }
            )
            raw_go["go-env.json"] = json_bytes(environment)
            raw_go["go-version.txt"] = (
                f"go version {producer.GO_VERSION} {platform}\n".encode()
            )
            raw_npm = {
                "node-execution.complete": b"complete\n",
                "node-version.txt": b"v26.10.0\n",
                "npm-version.txt": b"11.19.1\n",
                "npm-audit.exit": b"0\n",
                "npm-audit.json": json_bytes(
                    {
                        "auditReportVersion": 2,
                        "vulnerabilities": {},
                        "metadata": {"vulnerabilities": {"total": 0}},
                    }
                ),
                "npm-lock-graph.json": json_bytes({"name": "web", "version": "1.0.0"}),
            }
            self.raw[platform] = {"backend-source": raw_go, "web-source": raw_npm}
            context = NativeSourceContext(
                self.binding.repository, self.binding.version, commit, platform
            )
            record = {
                "schema_version": 1,
                "kind": "native-source-scanner-measurement",
                "source": context.checked(),
                "execution": "native",
                "source_archive_sha256": self.archive_sha,
                "source_inputs": self.inputs,
                "scans": [],
                "completed_at": "2026-10-07T03:00:00Z",
                "publication_authorized": False,
                "source_scanners_gate_pending": True,
                "findings_review_required": True,
            }
            for target in sorted(gate.TARGETS):
                kind = "golang" if target == "backend-source" else "node"
                calculated = (
                    producer.analyze_go(raw_go, platform=platform)
                    if kind == "golang"
                    else producer.analyze_npm(raw_npm, self.lock)
                )
                calculated.update(
                    {
                        "target": target,
                        "subject": "git:" + self.binding.repository + "@" + commit,
                        "started_at": "2026-10-07T01:00:00Z",
                        "scanned_at": "2026-10-07T02:00:00Z",
                        "builder": {
                            "reference": self.bases["base_images"][kind],
                            "config_digest": "sha256:" + "e" * 64,
                            "architecture": arch,
                            "rootfs_layers": ["sha256:" + "a" * 64],
                            "docker_engine_architecture": arch,
                        },
                        "raw_files": {
                            name: sha256(value)
                            for name, value in self.raw[platform][target].items()
                        },
                    }
                )
                record["scans"].append(calculated)
                directory = root / target
                directory.mkdir()
                for name, value in self.raw[platform][target].items():
                    (directory / name).write_bytes(value)
            self.paths[platform] = root / "source-scan-measurement.json"
            self.records[platform] = record
            self.authorize(self.paths[platform], record)

    def git(self, *args):
        return subprocess.run(
            ["git", "-C", str(self.repo), *args],
            capture_output=True,
            check=True,
            env={
                "PATH": os.defpath,
                "GIT_CONFIG_NOSYSTEM": "1",
                "GIT_CONFIG_GLOBAL": "/dev/null",
            },
        ).stdout

    def authorize(self, path, value):
        raw = json_bytes(value)
        path.write_bytes(raw)
        self.auth.contents.add(sha256(raw))

    def aggregate(self):
        return gate.aggregate_source_scans(
            self.binding,
            native_measurements=self.paths,
            repository_root=self.repo,
            resolved_bases=self.base_path,
            authenticator=self.auth,
        )

    def change_go(self, platform, mutate):
        raw = self.raw[platform]["backend-source"]
        mutate(raw)
        scan = next(
            s
            for s in self.records[platform]["scans"]
            if s["target"] == "backend-source"
        )
        facts = producer.analyze_go(raw, platform=platform)
        scan.update(facts)
        self.write_raw(platform, "backend-source")

    def write_raw(self, platform, target):
        raw = self.raw[platform][target]
        scan = next(s for s in self.records[platform]["scans"] if s["target"] == target)
        scan["raw_files"] = {name: sha256(value) for name, value in raw.items()}
        for name, value in raw.items():
            (self.paths[platform].parent / target / name).write_bytes(value)
        self.authorize(self.paths[platform], self.records[platform])

    def test_complete_fixture_authentication_preserves_both_native_findings(self):
        report = self.aggregate()
        self.assertEqual(report["binding_digest"], self.binding.digest)
        self.assertTrue(report["passed"])
        rows = {row["target"]: row for row in report["details"]["scans"]}
        self.assertEqual(set(rows), gate.TARGETS)
        self.assertEqual(len(rows["backend-source"]["findings"]), 2)
        self.assertEqual(
            set(rows["backend-source"]["native_measurements"]), set(PLATFORMS)
        )
        self.assertEqual(rows["web-source"]["findings"], [])
        self.assertEqual(
            report["details"]["release_subjects"], dict(self.binding.subjects)
        )

    def test_missing_arm_or_bypassed_authentication_fails(self):
        self.paths.pop("linux/arm64")
        with self.assertRaises(InvalidRelease):
            self.aggregate()
        self.paths["linux/arm64"] = self.root / "arm64/source-scan-measurement.json"
        self.base_path.write_bytes(json_bytes(self.bases) + b" ")
        with self.assertRaisesRegex(InvalidRelease, "authentication"):
            self.aggregate()

    def test_changed_raw_or_committed_source_or_claimed_pass_fails(self):
        platform = "linux/amd64"
        path = self.paths[platform].parent / "backend-source/go-all-graph.json"
        original = path.read_bytes()
        path.write_bytes(original + b" ")
        with self.assertRaisesRegex(InvalidRelease, "checksum"):
            self.aggregate()
        path.write_bytes(original)
        record = self.records[platform]
        record["source_inputs"]["web/package-lock.json"] = "sha256:" + "0" * 64
        self.authorize(self.paths[platform], record)
        with self.assertRaisesRegex(InvalidRelease, "substituted"):
            self.aggregate()
        record["source_inputs"] = {
            str(p.relative_to(self.repo)): sha256(p.read_bytes())
            for name in ["backend", "web"]
            for p in (self.repo / name).rglob("*")
            if p.is_file()
        }
        record["passed"] = True
        self.authorize(self.paths[platform], record)
        with self.assertRaises(InvalidRelease):
            self.aggregate()

    def test_caller_disposition_and_omitted_findings_cannot_approve(self):
        platform = "linux/amd64"
        scan = self.records[platform]["scans"][0]
        scan["findings"] = []
        self.authorize(self.paths[platform], self.records[platform])
        with self.assertRaisesRegex(InvalidRelease, "omitted/changed"):
            self.aggregate()

    def test_affected_path_only_on_arm_fails_instead_of_reusing_amd64(self):
        def mutate(raw):
            rows = producer.json_stream(raw["go-all-graph.json"])
            rows.append(
                {
                    "ImportPath": "dependency/ssh",
                    "Module": {
                        "Path": "dependency",
                        "Version": "v1.0.0",
                        "Sum": self.sum,
                    },
                }
            )
            raw["go-all-graph.json"] = stream(rows)

        self.change_go("linux/arm64", mutate)
        with self.assertRaisesRegex(InvalidRelease, "present or uncertain"):
            self.aggregate()

    def test_withdrawn_or_wrong_module_paths_fail_even_when_parser_claims_absence(self):
        original = copy.deepcopy(self.raw["linux/amd64"]["backend-source"])
        for change in [
            {"withdrawn": "2026-10-07T00:00:00Z"},
            {
                "affected": [
                    {
                        "package": {"name": "dependency", "ecosystem": "Go"},
                        "ecosystem_specific": {
                            "imports": [{"path": "dependency/invalid path"}]
                        },
                    }
                ]
            },
            {
                "affected": [
                    {
                        "package": {"name": "dependency", "ecosystem": "Go"},
                        "ecosystem_specific": {"imports": [{"path": "other/ssh"}]},
                    }
                ]
            },
        ]:
            self.raw["linux/amd64"]["backend-source"] = copy.deepcopy(original)

            def mutate(raw):
                messages = producer.json_stream(raw["govulncheck.json"])
                for message in messages:
                    if "osv" in message:
                        message["osv"].update(change)
                raw["govulncheck.json"] = stream(messages)

            self.change_go("linux/amd64", mutate)
            with self.assertRaises(InvalidRelease):
                self.aggregate()

    def test_package_symbol_missing_affected_paths_or_wrong_finding_version_fail(self):
        original = copy.deepcopy(self.raw["linux/amd64"]["backend-source"])
        for field in ["package", "function"]:
            self.raw["linux/amd64"]["backend-source"] = copy.deepcopy(original)

            def mutate(raw):
                messages = producer.json_stream(raw["govulncheck.json"])
                for message in messages:
                    if "finding" in message:
                        message["finding"]["trace"][0][field] = "dependency/ssh"
                raw["govulncheck.json"] = stream(messages)

            self.change_go("linux/amd64", mutate)
            with self.assertRaisesRegex(InvalidRelease, "Package/symbol"):
                self.aggregate()

        def wrong_version(raw):
            messages = producer.json_stream(raw["govulncheck.json"])
            for message in messages:
                if "finding" in message:
                    message["finding"]["trace"][0]["version"] = "v99.0.0"
            raw["govulncheck.json"] = stream(messages)

        self.raw["linux/amd64"]["backend-source"] = copy.deepcopy(original)
        self.change_go("linux/amd64", wrong_version)
        with self.assertRaisesRegex(InvalidRelease, "module/version"):
            self.aggregate()

        def missing_paths(raw):
            messages = producer.json_stream(raw["govulncheck.json"])
            for message in messages:
                if "osv" in message:
                    message["osv"]["affected"][0]["ecosystem_specific"]["imports"] = []
            raw["govulncheck.json"] = stream(messages)

        self.raw["linux/amd64"]["backend-source"] = copy.deepcopy(original)
        self.change_go("linux/amd64", missing_paths)
        with self.assertRaisesRegex(InvalidRelease, "complete official import"):
            self.aggregate()

    def test_failed_process_and_missing_receipts_cannot_pass_signed_wrapper(self):
        platform = "linux/amd64"
        raw = self.raw[platform]["backend-source"]
        raw["govulncheck.exit"] = b"1\n"
        self.write_raw(platform, "backend-source")
        with self.assertRaisesRegex(InvalidRelease, "failed"):
            self.aggregate()
        raw["govulncheck.exit"] = b"0\n"
        raw.pop("go-execution.complete")
        self.write_raw(platform, "backend-source")
        with self.assertRaisesRegex(InvalidRelease, "incomplete"):
            self.aggregate()

    def test_unsafe_symlink_and_escaping_raw_filename_fail(self):
        platform = "linux/amd64"
        path = self.paths[platform].parent / "backend-source/go-version.txt"
        saved = self.root / "saved-go-version"
        path.rename(saved)
        path.symlink_to(saved)
        with self.assertRaisesRegex(InvalidRelease, "substituted"):
            self.aggregate()
        path.unlink()
        saved.rename(path)
        self.records[platform]["scans"][0]["raw_files"]["../outside"] = (
            "sha256:" + "0" * 64
        )
        self.authorize(self.paths[platform], self.records[platform])
        with self.assertRaisesRegex(InvalidRelease, "Unsafe"):
            self.aggregate()

    def test_builder_resolution_and_source_checksums_are_required(self):
        original = copy.deepcopy(self.bases)
        for field, value in [("source_commit", "0" * 40), ("version", "v99.0.0")]:
            modified = copy.deepcopy(original)
            modified[field] = value
            self.authorize(self.base_path, modified)
            with self.assertRaisesRegex(InvalidRelease, "another release"):
                self.aggregate()
        modified = copy.deepcopy(original)
        modified["base_images"]["golang"] = (
            modified["base_images"]["golang"].split("@")[0] + "@sha256:" + "0" * 64
        )
        self.authorize(self.base_path, modified)
        with self.assertRaisesRegex(InvalidRelease, "base resolution"):
            self.aggregate()
        self.authorize(self.base_path, original)

        def wrong_sum(raw):
            modules = producer.json_stream(raw["go-modules.json"])
            modules[1]["Sum"] = "h1:" + "b" * 43 + "="
            raw["go-modules.json"] = stream(modules)

        self.change_go("linux/amd64", wrong_sum)
        with self.assertRaisesRegex(InvalidRelease, "committed GoSumDB"):
            self.aggregate()

    def test_npm_findings_fail_and_cannot_be_wrapped_as_passed(self):
        platform = "linux/amd64"
        raw = self.raw[platform]["web-source"]
        raw["npm-audit.exit"] = b"1\n"
        raw["npm-audit.json"] = json_bytes(
            {
                "auditReportVersion": 2,
                "vulnerabilities": {"problem": {"name": "problem", "severity": "high"}},
                "metadata": {"vulnerabilities": {"total": 1}},
            }
        )
        scan = next(
            s for s in self.records[platform]["scans"] if s["target"] == "web-source"
        )
        scan.update(producer.analyze_npm(raw, self.lock))
        scan["raw_files"] = {name: sha256(value) for name, value in raw.items()}
        for name, value in raw.items():
            (self.paths[platform].parent / "web-source" / name).write_bytes(value)
        self.authorize(self.paths[platform], self.records[platform])
        with self.assertRaisesRegex(InvalidRelease, "npm source findings"):
            self.aggregate()

    def test_changed_base_or_wrong_native_builder_or_time_fails(self):
        platform = "linux/arm64"
        record = self.records[platform]
        for mutation in [
            lambda r: r.update({"execution": "emulated"}),
            lambda r: r["scans"][0]["builder"].update(
                {"docker_engine_architecture": "amd64"}
            ),
            lambda r: r["scans"][0].update({"scanned_at": "2026-10-07T04:00:00Z"}),
        ]:
            changed = copy.deepcopy(record)
            mutation(changed)
            self.authorize(self.paths[platform], changed)
            with self.assertRaises(InvalidRelease):
                self.aggregate()


if __name__ == "__main__":
    unittest.main()
