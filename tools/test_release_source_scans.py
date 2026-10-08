"""Native source measurement coverage, scanner failure and bounded applicability."""

from __future__ import annotations

import copy
from contextlib import redirect_stderr, redirect_stdout
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import measure_release_source_scans as source
from generate_release_gate_reports import NativeSourceContext
from release_artifacts import InvalidRelease, json_bytes


def stream(records):
    return b"\n".join(json_bytes(record) for record in records)


def go_fixture():
    roots = [
        {"ImportPath": "example/main", "Module": {"Path": "example", "Main": True}}
    ]
    imported = {
        "ImportPath": "dependency/safe",
        "Module": {"Path": "dependency", "Version": "v1.0.0", "Sum": "h1:module"},
    }
    config = {
        "protocol_version": "v1.0.0",
        "scanner_name": "govulncheck",
        "scanner_version": source.SCANNER_VERSION,
        "db": "https://vuln.go.dev",
        "db_last_modified": "2026-10-07T01:00:00Z",
        "go_version": source.GO_VERSION,
        "scan_level": "symbol",
        "scan_mode": "source",
    }
    advisory = {
        "id": "GO-2026-1234",
        "affected": [
            {
                "package": {"name": "dependency", "ecosystem": "Go"},
                "ecosystem_specific": {"imports": [{"path": "dependency/ssh"}]},
            }
        ],
    }
    finding = {
        "osv": advisory["id"],
        "trace": [{"module": "dependency", "version": "v1.0.0"}],
    }
    messages = [
        {"config": config},
        {
            "SBOM": {
                "go_version": source.GO_VERSION,
                "roots": ["example/main"],
                "modules": [
                    {"path": "example"},
                    {"path": "dependency", "version": "v1.0.0"},
                ],
            }
        },
        {"osv": advisory},
        {"osv": copy.deepcopy(advisory)},
        {"finding": finding},
    ]
    raw = {
        "go-execution.complete": b"complete\n",
        "govulncheck.exit": b"0\n",
        "govulncheck-convert.exit": b"0\n",
        "govulncheck.txt": b"No vulnerable symbols\n",
        "govulncheck.json": stream(messages),
        "go-version.txt": b"go version go1.26.8 linux/amd64\n",
        "go-env.json": json_bytes(
            {
                "GOOS": "linux",
                "GOARCH": "amd64",
                "GOTOOLCHAIN": "local",
                "GOFLAGS": "-mod=readonly",
                "GOPROXY": "https://proxy.golang.org",
                "GOSUMDB": "sum.golang.org",
            }
        ),
        "scanner-module.json": json_bytes(
            {
                "Path": "golang.org/x/vuln",
                "Version": source.SCANNER_VERSION,
                "Sum": source.SCANNER_SUM,
            }
        ),
        "scanner-build.json": json_bytes(
            {
                "GoVersion": source.GO_VERSION,
                "Main": {
                    "Path": "golang.org/x/vuln",
                    "Version": source.SCANNER_VERSION,
                    "Sum": source.SCANNER_SUM,
                },
            }
        ),
        "scanner-binary-sha256.txt": b"a" * 64 + b"  /tmp/bin/govulncheck\n",
        "go-roots.json": stream(roots),
        "go-all-graph.json": stream(roots + [imported]),
        "go-server-graph.json": stream(roots + [imported]),
        "go-modules.json": stream([{"Path": "example"}]),
    }
    return raw, messages


class SourceScannerTests(unittest.TestCase):
    def cli_arguments(self):
        return [
            "--repository",
            "endorses/psst.zip",
            "--version",
            "v0.0.0",
            "--revision",
            "a" * 40,
            "--platform",
            "linux/amd64",
            "--repository-root",
            "/source",
            "--go-image",
            "go@sha256:" + "b" * 64,
            "--output",
            "/private/reports",
        ]

    def test_existing_source_scan_cli_remains_default(self):
        with patch.object(
            source,
            "measure_source_scans",
            return_value={"scans": [{"target": "backend-source", "findings": []}]},
        ) as measure, redirect_stdout(io.StringIO()) as output:
            source.main(
                self.cli_arguments() + ["--node-image", "node@sha256:" + "c" * 64]
            )
        self.assertTrue(json.loads(output.getvalue())["source_scanners_gate_pending"])
        self.assertEqual(
            measure.call_args.kwargs["node_image"], "node@sha256:" + "c" * 64
        )

    def test_compiler_cli_dispatches_bound_inputs_and_repeated_advisories(self):
        arguments = self.cli_arguments() + [
            "--mode",
            "compiler-graph",
            "--component",
            "web",
            "--runtime-pack",
            "/private/pack",
            "--runtime-source-sha256",
            "sha256:" + "d" * 64,
            "--image-archive",
            "/private/web.oci.tar",
            "--tested-config",
            "sha256:" + "e" * 64,
            "--cosign",
            "/private/cosign",
            "--advisory-id",
            "GO-2026-5932",
            "--advisory-id",
            "GO-2026-1234",
        ]
        result = {
            "component": "web",
            "source_graph": {"package_count": 970},
            "binary": {"sha256": "sha256:" + "f" * 64},
        }
        with patch.object(
            source, "measure_compiler_graph", return_value=result
        ) as measure, redirect_stdout(io.StringIO()) as output:
            source.main(arguments)
        self.assertEqual(
            measure.call_args.kwargs["advisory_ids"], ("GO-2026-5932", "GO-2026-1234")
        )
        self.assertEqual(
            measure.call_args.kwargs["image_archive"], Path("/private/web.oci.tar")
        )
        self.assertFalse(json.loads(output.getvalue())["publication_authorized"])
        with patch.object(
            source,
            "measure_compiler_graph",
            side_effect=InvalidRelease("replay failed"),
        ), redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as error:
            source.main(arguments)
        self.assertEqual(error.exception.code, 1)

    def test_mode_required_inputs_fail_before_measurement(self):
        with patch.object(source, "measure_source_scans") as scans, patch.object(
            source, "measure_compiler_graph"
        ) as compiler:
            for extra in [
                [],
                ["--mode", "compiler-graph"],
                ["--node-image", "node", "--component", "web"],
                [
                    "--mode",
                    "compiler-graph",
                    "--component",
                    "web",
                    "--runtime-pack",
                    "/pack",
                    "--runtime-source-sha256",
                    "sha256:" + "d" * 64,
                    "--image-archive",
                    "/image",
                    "--tested-config",
                    "sha256:" + "e" * 64,
                ],
            ]:
                with self.subTest(extra=extra), redirect_stderr(
                    io.StringIO()
                ), self.assertRaises(SystemExit) as error:
                    source.main(self.cli_arguments() + extra)
                self.assertEqual(error.exception.code, 2)
            scans.assert_not_called()
            compiler.assert_not_called()

    def test_full_finding_and_official_affected_paths_are_retained(self):
        raw, messages = go_fixture()
        report = source.analyze_go(raw, platform="linux/amd64")
        self.assertEqual(report["findings"][0]["finding"], messages[-1]["finding"])
        self.assertEqual(report["findings"][0]["disposition"], "not-applicable")
        self.assertEqual(report["findings"][0]["affected_packages"], ["dependency/ssh"])
        self.assertEqual(report["coverage"]["root_packages"], 1)

    def test_imported_affected_packages_and_package_symbol_findings_remain_unresolved(
        self,
    ):
        for alteration in [
            "import",
            "package",
            "symbol",
            "missing-imports",
            "extra-affected-range",
        ]:
            raw, messages = go_fixture()
            if alteration == "import":
                raw["go-all-graph.json"] += json_bytes(
                    {
                        "ImportPath": "dependency/ssh",
                        "Module": {
                            "Path": "dependency",
                            "Version": "v1.0.0",
                            "Sum": "h1:module",
                        },
                    }
                )
            elif alteration == "package":
                messages[-1]["finding"]["trace"][0]["package"] = "dependency/ssh"
            elif alteration == "symbol":
                messages[-1]["finding"]["trace"][0]["function"] = "Affected"
            elif alteration == "missing-imports":
                messages[2]["osv"]["affected"][0]["ecosystem_specific"]["imports"] = []
                messages[3]["osv"] = copy.deepcopy(messages[2]["osv"])
            else:
                messages[2]["osv"]["affected"].append(
                    {"package": {"name": "dependency", "ecosystem": "Go"}}
                )
                messages[3]["osv"] = copy.deepcopy(messages[2]["osv"])
            raw["govulncheck.json"] = stream(messages)
            with self.subTest(alteration=alteration):
                self.assertEqual(
                    source.analyze_go(raw, platform="linux/amd64")["findings"][0][
                        "disposition"
                    ],
                    "unresolved",
                )

    def test_scanner_errors_empty_partial_streams_and_wrong_toolchains_fail(self):
        for key, value in [
            ("govulncheck.exit", b"1\n"),
            ("govulncheck.json", b""),
            ("govulncheck.json", b"{"),
            ("go-version.txt", b"go version go1.27.1 linux/amd64\n"),
            ("go-execution.complete", b""),
            ("govulncheck-convert.exit", b"1\n"),
        ]:
            raw, _ = go_fixture()
            raw[key] = value
            with self.subTest(key=key), self.assertRaises((InvalidRelease, ValueError)):
                source.analyze_go(raw, platform="linux/amd64")

    def test_conflicting_duplicate_advisories_and_missing_source_coverage_fail(self):
        raw, messages = go_fixture()
        messages[3]["osv"]["affected"] = []
        raw["govulncheck.json"] = stream(messages)
        with self.assertRaisesRegex(InvalidRelease, "advisory record"):
            source.analyze_go(raw, platform="linux/amd64")
        raw, messages = go_fixture()
        messages[1]["SBOM"]["roots"] = []
        raw["govulncheck.json"] = stream(messages)
        with self.assertRaisesRegex(InvalidRelease, "root coverage"):
            source.analyze_go(raw, platform="linux/amd64")

    def test_incomplete_go_graph_and_replacement_versions_cannot_dispose_findings(self):
        for record in [
            {"ImportPath": "example/main", "Incomplete": True},
            {"ImportPath": "example/main", "DepsErrors": [{"Err": "failed load"}]},
            {
                "ImportPath": "example/main",
                "Module": {"Path": "dependency", "Replace": {"Path": "replacement"}},
            },
        ]:
            with self.assertRaises(InvalidRelease):
                source.graph_paths(stream([record]))

    def npm_fixture(self, finding=False):
        report = {
            "auditReportVersion": 2,
            "vulnerabilities": (
                {"dependency": {"severity": "high", "via": ["GO-fixture"]}}
                if finding
                else {}
            ),
            "metadata": {"vulnerabilities": {"total": 1 if finding else 0}},
        }
        return {
            "node-execution.complete": b"complete\n",
            "node-version.txt": b"v22.23.3\n",
            "npm-version.txt": b"10.9.4\n",
            "npm-audit.exit": b"1\n" if finding else b"0\n",
            "npm-audit.json": json_bytes(report),
            "npm-lock-graph.json": json_bytes({"name": "web", "dependencies": {}}),
        }, json_bytes(
            {"lockfileVersion": 3, "packages": {"": {}, "node_modules/dependency": {}}}
        )

    def test_npm_exit_and_all_findings_are_explicit_not_approval(self):
        raw, lock = self.npm_fixture()
        self.assertEqual(source.analyze_npm(raw, lock)["findings"], [])
        raw, lock = self.npm_fixture(True)
        result = source.analyze_npm(raw, lock)
        self.assertEqual(result["exit_code"], 1)
        self.assertEqual(result["findings"][0]["disposition"], "unresolved")
        raw["npm-audit.exit"] = b"0\n"
        with self.assertRaisesRegex(InvalidRelease, "accounting differs"):
            source.analyze_npm(raw, lock)

    def test_npm_api_error_or_other_toolchain_is_failure(self):
        raw, lock = self.npm_fixture()
        raw["npm-audit.json"] = json_bytes({"error": {"code": "ENETWORK"}})
        with self.assertRaisesRegex(InvalidRelease, "audit failed"):
            source.analyze_npm(raw, lock)
        raw, lock = self.npm_fixture()
        raw["node-version.txt"] = b"v26.10.0\n"
        with self.assertRaisesRegex(InvalidRelease, "Node/npm"):
            source.analyze_npm(raw, lock)

    def test_read_only_scanner_fixtures_never_mount_socket_and_disable_scripts(self):
        context = NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", "a" * 40, "linux/amd64"
        )
        argv = source.docker_scan_args(
            "sha256:" + "b" * 64,
            Path("/private/source"),
            Path("/private/output"),
            context,
            source.NODE_SCRIPT,
        )
        self.assertIn("--read-only", argv)
        self.assertIn("--cap-drop", argv)
        self.assertIn("no-new-privileges", argv)
        self.assertIn("type=bind,src=/private/source,dst=/source,readonly", argv)
        self.assertNotIn("docker.sock", " ".join(argv))
        self.assertIn("--ignore-scripts", source.NODE_SCRIPT)
        self.assertIn("GOTOOLCHAIN=local", source.GO_SCRIPT)
        self.assertIn(source.SCANNER_SUM, source.GO_SCRIPT)

    def compiler_fixture(self):
        binary = b"actual backend"
        checksum = source.sha256(binary)
        build = {
            "GoVersion": source.GO_VERSION,
            "Path": "example/cmd/server",
            "Main": {"Path": "example", "Version": "(devel)"},
            "Deps": [
                {
                    "Path": "dependency",
                    "Version": "v1.0.0",
                    "Sum": "h1:module",
                    "Sum": "h1:module",
                }
            ],
            "Settings": [
                {"Key": key, "Value": value}
                for key, value in {
                    "GOOS": "linux",
                    "GOARCH": "amd64",
                    "GOAMD64": "v1",
                    "CGO_ENABLED": "0",
                }.items()
            ],
        }
        environment = {
            "GOOS": "linux",
            "GOARCH": "amd64",
            "GOAMD64": "v1",
            "CGO_ENABLED": "0",
            "GOTOOLCHAIN": "local",
            "GOFLAGS": "",
            "GOPATH": "/go",
            "GOMODCACHE": "/go/pkg/mod",
        }
        raw = {
            "compiler-execution.complete": b"complete\n",
            "binary-build-info.json": json_bytes(build),
            "go-version.txt": b"go version go1.26.8 linux/amd64\n",
            "compiler-environment.json": json_bytes(environment),
            "compiler-graph.json": stream(
                [
                    {
                        "ImportPath": "example/cmd/server",
                        "Module": {"Path": "example", "Main": True},
                    },
                    {
                        "ImportPath": "dependency/safe",
                        "Module": {
                            "Path": "dependency",
                            "Version": "v1.0.0",
                            "Sum": "h1:module",
                        },
                    },
                ]
            ),
            "rebuilt-backend-sha256.txt": checksum[7:].encode()
            + b"  /reports/rebuilt-backend\n",
        }
        return raw, binary, build

    def test_backend_same_builder_binary_rebuild_is_required(self):
        raw, binary, build = self.compiler_fixture()
        result = source.compiler_correspondence(
            raw,
            component="backend",
            actual_binary=binary,
            expected_build=build,
            platform="linux/amd64",
        )
        self.assertEqual(result["rebuilt_sha256"], source.sha256(binary))
        raw["rebuilt-backend-sha256.txt"] = b"f" * 64 + b"  /reports/rebuilt-backend\n"
        with self.assertRaisesRegex(InvalidRelease, "byte-match"):
            source.compiler_correspondence(
                raw,
                component="backend",
                actual_binary=binary,
                expected_build=build,
                platform="linux/amd64",
            )

    def test_compiler_graph_module_set_and_actual_target_flags_must_match(self):
        raw, binary, build = self.compiler_fixture()
        raw["compiler-graph.json"] = stream(
            [
                {
                    "ImportPath": "example/cmd/server",
                    "Module": {"Path": "example", "Main": True},
                }
            ]
        )
        with self.assertRaisesRegex(InvalidRelease, "dependency set"):
            source.compiler_correspondence(
                raw,
                component="backend",
                actual_binary=binary,
                expected_build=build,
                platform="linux/amd64",
            )
        raw, binary, build = self.compiler_fixture()
        env = json.loads(raw["compiler-environment.json"])
        env["CGO_ENABLED"] = "1"
        raw["compiler-environment.json"] = json_bytes(env)
        with self.assertRaisesRegex(InvalidRelease, "build settings"):
            source.compiler_correspondence(
                raw,
                component="backend",
                actual_binary=binary,
                expected_build=build,
                platform="linux/amd64",
            )

    def test_caddy_uses_vendor_and_exact_native_flags_without_a_rebuild_floor(self):
        script = source.compiler_script(
            "web",
            {
                "CGO_ENABLED": "0",
                "GOOS": "linux",
                "GOARCH": "amd64",
                "GOAMD64": "v1",
                "-tags": "nobadger,nomysql,nopgx",
            },
        )
        self.assertIn("-mod=vendor", script)
        self.assertIn("-tags=nobadger,nomysql,nopgx", script)
        self.assertNotIn("go build", script)
        with self.assertRaisesRegex(InvalidRelease, "build tags"):
            source.compiler_script(
                "web",
                {
                    "CGO_ENABLED": "0",
                    "GOOS": "linux",
                    "GOARCH": "amd64",
                    "-tags": "custom",
                },
            )

    def test_unhandled_experiments_and_package_selection_flags_fail(self):
        raw, binary, build = self.compiler_fixture()
        settings = {s["Key"]: s["Value"] for s in build["Settings"]}
        for key, value in [
            ("GOEXPERIMENT", "custom"),
            ("-race", "true"),
            ("-gcflags", "all=-N"),
        ]:
            with self.subTest(key=key), self.assertRaisesRegex(
                InvalidRelease, "package-selection"
            ):
                source.compiler_script("backend", settings | {key: value})
        environment = json.loads(raw["compiler-environment.json"])
        environment["GOEXPERIMENT"] = "custom"
        raw["compiler-environment.json"] = json_bytes(environment)
        with self.assertRaisesRegex(InvalidRelease, "experiments"):
            source.compiler_correspondence(
                raw,
                component="backend",
                actual_binary=binary,
                expected_build=build,
                platform="linux/amd64",
            )

    def test_builder_rejects_emulated_daemon_before_pulling(self):
        calls = []

        def execute(args, **kwargs):
            calls.append(args)
            return b'"aarch64"'

        context = NativeSourceContext(
            "endorses/psst.zip", "v0.0.0", "a" * 40, "linux/amd64"
        )
        with self.assertRaisesRegex(InvalidRelease, "emulate"):
            source.builder_identity(
                source.BASES["golang"].rsplit(":", 1)[0] + "@sha256:" + "b" * 64,
                "golang",
                context,
                execute,
            )
        self.assertEqual(len(calls), 1)

    def test_candidate_canonical_builder_names_match_actual_inspected_config(self):
        import prepare_release_candidate as candidate

        context = NativeSourceContext(
            "endorses/psst.zip", "v0.0.0", "a" * 40, "linux/amd64"
        )
        index = (
            "sha256:" + "b" * 64,
            {"linux/amd64": "sha256:" + "e" * 64, "linux/arm64": "sha256:" + "f" * 64},
        )
        with patch.object(candidate, "run", return_value=b"{}"), patch.object(
            candidate, "index_record", return_value=index
        ):
            candidate_refs = candidate.resolve_bases(context.version, context.commit)[
                "base_images"
            ]
        for kind in ["golang", "node"]:
            reference = candidate_refs[kind]
            calls = []

            def execute(args, **kwargs):
                calls.append(args)
                if args[1] == "info":
                    return b'"x86_64"'
                if "inspect" in args:
                    return json_bytes(
                        [
                            {
                                "Os": "linux",
                                "Architecture": "amd64",
                                "Id": "sha256:" + "c" * 64,
                                "RootFS": {"Layers": ["sha256:" + "d" * 64]},
                            }
                        ]
                    )
                return b""

            measured = source.builder_identity(reference, kind, context, execute)
            self.assertEqual(measured["reference"], reference)
            self.assertEqual(measured["config_digest"], "sha256:" + "c" * 64)
            self.assertEqual(
                calls[1], ["docker", "pull", "--platform", "linux/amd64", reference]
            )
            for wrong in [
                source.BASES[kind],
                source.BASES[kind] + "@sha256:" + "b" * 64,
                "docker.io/other/" + kind + "@sha256:" + "b" * 64,
                reference + ":tag",
            ]:
                with self.subTest(reference=wrong), self.assertRaises(InvalidRelease):
                    source.builder_identity(wrong, kind, context, execute)

    def test_failed_fixture_cleans_only_its_exact_owned_container(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            output = root / "reports"
            output.mkdir()
            calls = []
            cid = "a" * 64

            def execute(args, **kwargs):
                calls.append(args)
                if args[1] == "run":
                    Path(args[3]).write_text(cid)
                    raise InvalidRelease("fixture failed")
                if "ls" in args:
                    return cid.encode()
                return b""

            with self.assertRaisesRegex(InvalidRelease, "fixture failed"):
                source.execute_fixture(
                    ["docker", "run", "image"], root, output, execute, timeout=1
                )
            self.assertEqual(calls[-1], ["docker", "container", "rm", "--force", cid])
            self.assertFalse((root / "reports.cid").exists())


if __name__ == "__main__":
    unittest.main()
