#!/usr/bin/env python3
"""Measure exact committed source with pinned native builders; never authorize release."""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import re
import signal
import subprocess
import tarfile
import tempfile
import time

from generate_release_gate_reports import NativeSourceContext, timestamp
from prepare_release_candidate import BASES
from publish_container_release import sha256
from release_artifacts import (
    DIGEST,
    InvalidRelease,
    json_bytes,
    matches,
    read_json,
    require,
)

SCANNER_VERSION = "v1.8.0"
SCANNER_SUM = "h1:clG4qBU6zH5VKjti8n5j8BBuYzoSha392xXMkXS351U="
GO_VERSION = "go1.26.8"
MAX_RAW = 32 * 1024**2
SOURCE_LIMIT = 256 * 1024**2
GO_SCRIPT = r"""set -eu
export PATH=/tmp/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export GOTOOLCHAIN=local GOPATH=/tmp/go GOMODCACHE=/tmp/modules GOCACHE=/tmp/build GOBIN=/tmp/bin
export GOWORK=off GOFLAGS=-mod=readonly GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org
export GOPRIVATE= GONOPROXY= GONOSUMDB= GOVULNDB=https://vuln.go.dev
cd /tmp
go version > /reports/go-version.txt
go env -json GOOS GOARCH CGO_ENABLED GOAMD64 GOARM64 GOFLAGS GOTOOLCHAIN GOSUMDB GOPROXY > /reports/go-env.json
go mod download -json golang.org/x/vuln@v1.8.0 > /reports/scanner-module.json
grep -F '"Sum": "h1:clG4qBU6zH5VKjti8n5j8BBuYzoSha392xXMkXS351U="' /reports/scanner-module.json >/dev/null
go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
sha256sum /tmp/bin/govulncheck > /reports/scanner-binary-sha256.txt
go version -m -json /tmp/bin/govulncheck > /reports/scanner-build.json
cd /source/backend
go list -json ./... > /reports/go-roots.json
go list -deps -json ./... > /reports/go-all-graph.json
go list -deps -json ./cmd/server > /reports/go-server-graph.json
go list -m -json all > /reports/go-modules.json
set +e
govulncheck -db https://vuln.go.dev -json ./... > /reports/govulncheck.json 2> /reports/govulncheck.stderr
scan_status=$?
printf '%s\n' "$scan_status" > /reports/govulncheck.exit
govulncheck -mode convert -show verbose < /reports/govulncheck.json > /reports/govulncheck.txt 2> /reports/govulncheck-convert.stderr
printf '%s\n' "$?" > /reports/govulncheck-convert.exit
set -e
printf 'complete\n' > /reports/go-execution.complete
"""
NODE_SCRIPT = r"""set -eu
export PATH=/usr/local/bin:/usr/bin:/bin
export npm_config_cache=/tmp/npm-cache npm_config_userconfig=/tmp/empty-npmrc npm_config_globalconfig=/tmp/global-npmrc
export npm_config_registry=https://registry.npmjs.org/ npm_config_ignore_scripts=true npm_config_audit=true
export npm_config_omit= npm_config_include=dev npm_config_workspaces=false
: > /tmp/empty-npmrc
: > /tmp/global-npmrc
node --version > /reports/node-version.txt
npm --version > /reports/npm-version.txt
cd /source/web
npm ls --package-lock-only --all --json > /reports/npm-lock-graph.json 2> /reports/npm-graph.stderr
set +e
npm audit --package-lock-only --ignore-scripts --json --registry=https://registry.npmjs.org/ > /reports/npm-audit.json 2> /reports/npm-audit.stderr
printf '%s\n' "$?" > /reports/npm-audit.exit
set -e
printf 'complete\n' > /reports/node-execution.complete
"""


def native_platform() -> str:
    require(platform.system() == "Linux", "Source scans require Linux native execution")
    machine = platform.machine()
    require(machine in {"x86_64", "aarch64", "arm64"}, "Unsupported source scan host")
    return "linux/amd64" if machine == "x86_64" else "linux/arm64"


def run(
    args: list[str], *, timeout: int = 1200, diagnostics: Path | None = None
) -> bytes:
    with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
        process = subprocess.Popen(
            args,
            env={"PATH": os.defpath},
            stdout=stdout,
            stderr=stderr,
            start_new_session=True,
        )
        start = time.monotonic()
        try:
            while process.poll() is None:
                require(
                    time.monotonic() - start < timeout, "Source scan command timed out"
                )
                require(
                    os.fstat(stdout.fileno()).st_size
                    + os.fstat(stderr.fileno()).st_size
                    <= MAX_RAW,
                    "Source command output exceeds bounds",
                )
                if diagnostics is not None:
                    files = list(diagnostics.parent.iterdir())
                    require(
                        len(files) <= 1024
                        and all(
                            path.is_file()
                            and not path.is_symlink()
                            and path.stat().st_size <= 128 * 1024**2
                            for path in files
                        ),
                        "Source fixture output exceeds bounds",
                    )
                    require(
                        sum(path.stat().st_size for path in files) <= 512 * 1024**2,
                        "Total source fixture output exceeds bounds",
                    )
                time.sleep(0.05)
            if diagnostics is not None:
                stderr.seek(0)
                diagnostics.write_bytes(stderr.read(MAX_RAW + 1))
            require(
                process.returncode == 0,
                "Source scan command failed; no complete measurement",
            )
            require(
                os.fstat(stdout.fileno()).st_size <= MAX_RAW,
                "Source command output exceeds bounds",
            )
            stdout.seek(0)
            return stdout.read(MAX_RAW + 1)
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
            process.wait()


def json_stream(raw: bytes) -> list[dict]:
    require(0 < len(raw) <= MAX_RAW, "Missing or oversized JSON stream")
    text = raw.decode()
    decoder = json.JSONDecoder()
    offset = 0
    records = []
    while offset < len(text):
        while offset < len(text) and text[offset].isspace():
            offset += 1
        if offset == len(text):
            break
        value, offset = decoder.raw_decode(text, offset)
        require(
            isinstance(value, dict) and len(records) < 100_000,
            "Malformed JSON stream object",
        )
        records.append(value)
    require(bool(records), "Empty JSON stream")
    return records


def graph_paths(raw: bytes) -> tuple[set[str], set[tuple[str, str]]]:
    paths, modules = set(), set()
    for record in json_stream(raw):
        require(
            not record.get("Incomplete")
            and not record.get("Error")
            and not record.get("DepsErrors")
            and not record.get("ForTest"),
            "Go graph is incomplete or includes test packages",
        )
        name = record.get("ImportPath")
        require(
            isinstance(name, str) and name and name not in paths,
            "Duplicate/missing imported package",
        )
        paths.add(name)
        module = record.get("Module")
        if module:
            require(
                isinstance(module, dict)
                and not module.get("Replace")
                and not module.get("Error"),
                "Go module replacement/error requires separate review",
            )
            modules.add((module["Path"], module.get("Version", "")))
    return paths, modules


def analyze_go(raw: dict[str, bytes], *, platform: str) -> dict:
    require(
        raw["go-execution.complete"] == b"complete\n"
        and int(raw["govulncheck.exit"]) == 0,
        "Go source scanner failed or execution incomplete",
    )
    require(
        raw["go-version.txt"].decode().strip()
        == "go version " + GO_VERSION + " " + platform,
        "Source Go toolchain differs from release builder",
    )
    env = read_json(raw["go-env.json"])
    require(
        env.get("GOOS") == "linux"
        and env.get("GOARCH") == platform.split("/")[1]
        and env.get("GOTOOLCHAIN") == "local"
        and env.get("GOFLAGS") == "-mod=readonly"
        and env.get("GOPROXY") == "https://proxy.golang.org"
        and env.get("GOSUMDB") == "sum.golang.org",
        "Go source build configuration changed",
    )
    module = read_json(raw["scanner-module.json"])
    require(
        module.get("Path") == "golang.org/x/vuln"
        and module.get("Version") == SCANNER_VERSION
        and module.get("Sum") == SCANNER_SUM
        and not module.get("Error"),
        "govulncheck source module checksum differs",
    )
    build = read_json(raw["scanner-build.json"])
    require(
        build.get("GoVersion") == GO_VERSION
        and build.get("Main", {}).get("Path") == "golang.org/x/vuln"
        and build["Main"].get("Version") == SCANNER_VERSION
        and build["Main"].get("Sum") == SCANNER_SUM,
        "Unexpected govulncheck build identity",
    )
    scanner_binary = raw["scanner-binary-sha256.txt"].decode().split()[0]
    matches(
        "sha256:" + scanner_binary, DIGEST, "Missing actual govulncheck binary digest"
    )
    roots, _ = graph_paths(raw["go-roots.json"])
    all_paths, all_modules = graph_paths(raw["go-all-graph.json"])
    server_paths, _ = graph_paths(raw["go-server-graph.json"])
    require(
        roots <= all_paths and server_paths <= all_paths,
        "Source graphs do not cover scanned root/server targets",
    )
    messages = json_stream(raw["govulncheck.json"])
    require(set(messages[0]) == {"config"}, "Missing first govulncheck config")
    configs = [v["config"] for v in messages if "config" in v]
    sboms = [v["SBOM"] for v in messages if "SBOM" in v]
    require(
        len(configs) == len(sboms) == 1,
        "Missing/duplicate scanner config or source coverage SBOM",
    )
    config = configs[0]
    sbom = sboms[0]
    require(
        config.get("protocol_version") == "v1.0.0"
        and config.get("scanner_name") == "govulncheck"
        and config.get("scanner_version") == SCANNER_VERSION
        and config.get("go_version") == GO_VERSION
        and config.get("scan_mode") == "source"
        and config.get("scan_level") == "symbol"
        and config.get("db") == "https://vuln.go.dev",
        "Unexpected govulncheck protocol/configuration",
    )
    timestamp(config.get("db_last_modified"))
    require(
        sbom.get("go_version") == GO_VERSION and set(sbom.get("roots", [])) == roots,
        "Scanner root coverage differs from actual source graph",
    )
    scanned_modules = {
        (m["path"], m.get("version", "")) for m in sbom.get("modules", [])
    }
    require(
        all_modules <= scanned_modules, "Scanner SBOM omits imported module versions"
    )
    advisories = {}
    findings = []
    for message in messages:
        require(
            len(message) == 1
            and set(message) <= {"config", "progress", "SBOM", "osv", "finding"},
            "Unknown or ambiguous govulncheck message",
        )
        if "osv" in message:
            advisory = message["osv"]
            identifier = advisory.get("id")
            require(
                isinstance(identifier, str)
                and (
                    identifier not in advisories or advisories[identifier] == advisory
                ),
                "Duplicate/missing advisory record",
            )
            advisories[identifier] = advisory
        if "finding" in message:
            findings.append(message["finding"])
    assessed = []
    for finding in findings:
        identifier = finding.get("osv")
        trace = finding.get("trace")
        require(
            identifier in advisories and isinstance(trace, list) and trace,
            "Finding lacks authoritative advisory/trace",
        )
        advisory = advisories[identifier]
        frame = trace[0]
        affected = set()
        uncertain = False
        for entry in advisory.get("affected", []):
            package = entry.get("package", {})
            if package.get("ecosystem") == "Go" and package.get("name") == frame.get(
                "module"
            ):
                imports = entry.get("ecosystem_specific", {}).get("imports", [])
                if not imports:
                    uncertain = True
                for imported in imports:
                    name = imported.get("path")
                    if not isinstance(name, str) or not name:
                        uncertain = True
                    else:
                        affected.add(name)
        absent = (
            bool(affected)
            and not uncertain
            and not affected & (all_paths | server_paths)
        )
        module_only = (
            len(trace) == 1
            and not frame.get("package")
            and not frame.get("function")
            and not frame.get("position")
        )
        disposition = "not-applicable" if absent and module_only else "unresolved"
        assessed.append(
            {
                "id": identifier,
                "finding": finding,
                "finding_sha256": sha256(json_bytes(finding)),
                "advisory_sha256": sha256(json_bytes(advisory)),
                "affected_packages": sorted(affected),
                "absent_from_all_root_and_server_graphs": absent,
                "disposition": disposition,
                "reason": (
                    "All authoritative affected Go package paths are absent from both exact native source import graphs; this is a module-only finding."
                    if disposition == "not-applicable"
                    else "Affected/package/symbol/uncertain finding requires review; no suppression applied."
                ),
            }
        )
    convert = int(raw["govulncheck-convert.exit"])
    require(
        convert in {0, 3} and bool(raw["govulncheck.txt"]),
        "govulncheck text conversion failed or produced no report",
    )
    require(
        convert == 0 or any(f["disposition"] == "unresolved" for f in assessed),
        "Nonzero scanner text result cannot be justified by absent module paths",
    )
    return {
        "scanner": {
            "name": "govulncheck",
            "version": SCANNER_VERSION,
            "module_sum": SCANNER_SUM,
            "binary_sha256": "sha256:" + scanner_binary,
            "build": build,
        },
        "database": {
            "url": config["db"],
            "last_modified": config["db_last_modified"],
            "advisories_sha256": sha256(json_bytes(advisories)),
        },
        "build_environment": env,
        "coverage": {
            "root_packages": len(roots),
            "all_imported_packages": len(all_paths),
            "server_imported_packages": len(server_paths),
            "scanned_modules": len(scanned_modules),
        },
        "graphs": {
            k: sha256(raw[k])
            for k in [
                "go-roots.json",
                "go-all-graph.json",
                "go-server-graph.json",
                "go-modules.json",
            ]
        },
        "status": "complete",
        "exit_code": 0,
        "text_conversion_exit_code": convert,
        "raw_report_sha256": sha256(raw["govulncheck.json"]),
        "findings": assessed,
    }


def analyze_npm(raw: dict[str, bytes], lock: bytes) -> dict:
    require(
        raw["node-execution.complete"] == b"complete\n",
        "Web source scan execution incomplete",
    )
    node = raw["node-version.txt"].decode().strip()
    npm = raw["npm-version.txt"].decode().strip()
    require(
        re.fullmatch(r"v22\.[0-9]+\.[0-9]+", node)
        and re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", npm),
        "Unexpected bundled Node/npm version",
    )
    code = int(raw["npm-audit.exit"])
    report = read_json(raw["npm-audit.json"])
    graph = read_json(raw["npm-lock-graph.json"])
    state = read_json(lock)
    require(
        code in {0, 1}
        and report.get("auditReportVersion") == 2
        and not report.get("error")
        and isinstance(report.get("vulnerabilities"), dict)
        and isinstance(report.get("metadata", {}).get("vulnerabilities"), dict),
        "npm audit failed or report incomplete",
    )
    require(
        not graph.get("error")
        and not graph.get("problems")
        and state.get("lockfileVersion") == 3
        and isinstance(state.get("packages"), dict)
        and state["packages"],
        "npm lock graph is invalid or unresolved",
    )
    findings = [
        {
            "id": name,
            "finding": value,
            "finding_sha256": sha256(json_bytes(value)),
            "disposition": "unresolved",
            "reason": "npm advisory requires exact affected dependency review; retained without suppression.",
        }
        for name, value in sorted(report["vulnerabilities"].items())
    ]
    total = report["metadata"]["vulnerabilities"].get("total")
    require(
        type(total) is int
        and total == len(findings)
        and ((code == 0 and total == 0) or (code == 1 and total > 0)),
        "npm exit status/findings accounting differs",
    )
    return {
        "scanner": {"name": "npm audit", "version": npm, "node": node},
        "database": {
            "url": "https://registry.npmjs.org/-/npm/v1/security/advisories/bulk",
            "response_sha256": sha256(raw["npm-audit.json"]),
            "snapshot_version_available": False,
        },
        "lock_sha256": sha256(lock),
        "lock_graph_sha256": sha256(raw["npm-lock-graph.json"]),
        "dependency_packages": len(state["packages"]) - 1,
        "status": "complete",
        "exit_code": code,
        "raw_report_sha256": sha256(raw["npm-audit.json"]),
        "findings": findings,
    }


def source_snapshot(repository_root: Path, commit: str, target: Path, execute) -> str:
    raw = execute(["git", "-C", str(repository_root), "archive", commit], timeout=60)
    require(0 < len(raw) <= SOURCE_LIMIT, "Source snapshot exceeds bounds")
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:") as archive:
        seen = set()
        expanded = 0
        for member in archive:
            name = Path(member.name)
            require(
                not name.is_absolute()
                and ".." not in name.parts
                and member.name not in seen
                and len(seen) < 100_000
                and (member.isfile() or member.isdir()),
                "Unsafe source snapshot member",
            )
            seen.add(member.name)
            expanded += member.size
            require(expanded <= SOURCE_LIMIT, "Expanded source snapshot exceeds bounds")
            destination = target / name
            if member.isdir():
                destination.mkdir(parents=True, exist_ok=True)
            else:
                destination.parent.mkdir(parents=True, exist_ok=True)
                with destination.open("xb") as stream:
                    stream.write(archive.extractfile(member).read())
    return sha256(raw)


def builder_identity(
    reference: str, kind: str, context: NativeSourceContext, execute
) -> dict:
    require(
        reference.startswith(BASES[kind].rsplit(":", 1)[0] + "@sha256:"),
        "Unexpected or mutable release builder",
    )
    matches(reference.rsplit("@", 1)[1], DIGEST, "Invalid immutable builder digest")
    daemon_arch = execute(
        ["docker", "info", "--format", "{{json .Architecture}}"], timeout=30
    )
    daemon_arch = read_json(daemon_arch)
    expected_arches = (
        {"amd64", "x86_64"}
        if context.platform.endswith("amd64")
        else {"arm64", "aarch64"}
    )
    require(
        daemon_arch in expected_arches,
        "Docker daemon would emulate this source architecture",
    )
    execute(["docker", "pull", "--platform", context.platform, reference], timeout=300)
    records = read_json(execute(["docker", "image", "inspect", reference], timeout=30))
    require(
        isinstance(records, list) and len(records) == 1,
        "Missing actual builder configuration",
    )
    image = records[0]
    require(
        image.get("Os") == "linux"
        and image.get("Architecture") == context.platform.split("/")[1],
        "Builder is not native source architecture",
    )
    matches(image.get("Id"), DIGEST, "Invalid actual builder identity")
    return {
        "reference": reference,
        "config_digest": image["Id"],
        "architecture": image["Architecture"],
        "rootfs_layers": image["RootFS"]["Layers"],
        "docker_engine_architecture": daemon_arch,
    }


def docker_scan_args(
    image: str, source: Path, output: Path, context: NativeSourceContext, script: str
) -> list[str]:
    return [
        "docker",
        "run",
        "--rm",
        "--platform",
        context.platform,
        "--read-only",
        "--cap-drop",
        "ALL",
        "--security-opt",
        "no-new-privileges",
        "--user",
        str(os.getuid()) + ":" + str(os.getgid()),
        "--pids-limit",
        "256",
        "--memory",
        "3g",
        "--cpus",
        "2",
        "--tmpfs",
        "/tmp:rw,exec,nosuid,nodev,size=2g",
        "--env",
        "HOME=/tmp",
        "--env",
        "TMPDIR=/tmp",
        "--mount",
        f"type=bind,src={source},dst=/source,readonly",
        "--mount",
        f"type=bind,src={output},dst=/reports",
        "--entrypoint",
        "/bin/sh",
        image,
        "-ec",
        script,
    ]


def execute_fixture(
    arguments: list[str], root: Path, output: Path, execute, *, timeout: int
) -> None:
    cidfile = root / (output.name + ".cid")
    arguments[2:2] = ["--cidfile", str(cidfile)]
    try:
        execute(arguments, timeout=timeout, diagnostics=output / "execution.stderr")
    finally:
        if cidfile.exists():
            cid = cidfile.read_text().strip()
            require(
                re.fullmatch(r"[a-f0-9]{64}", cid) is not None,
                "Invalid owned source fixture ID",
            )
            alive = execute(
                [
                    "docker",
                    "container",
                    "ls",
                    "--all",
                    "--no-trunc",
                    "--filter",
                    "id=" + cid,
                    "--format",
                    "{{.ID}}",
                ],
                timeout=30,
            ).strip()
            if alive:
                require(alive.decode() == cid, "Owned source fixture selection differs")
                execute(["docker", "container", "rm", "--force", cid], timeout=30)
            cidfile.unlink()


def measure_source_scans(
    context: NativeSourceContext,
    *,
    repository_root: Path,
    go_image: str,
    node_image: str,
    output: Path,
    execute=run,
) -> dict:
    identity = context.checked()
    require(
        context.platform == native_platform(), "Source scan execution must be native"
    )
    require(
        not output.exists() and not output.is_symlink(),
        "Never replace source scan artifacts",
    )
    output.mkdir(parents=True, mode=0o700)
    with tempfile.TemporaryDirectory(prefix="psst-source-scan-") as temporary:
        root = Path(temporary)
        source = root / "source"
        source.mkdir()
        archive_digest = source_snapshot(
            repository_root, context.commit, source, execute
        )
        inputs = {
            str(p.relative_to(source)): sha256(p.read_bytes())
            for name in ["backend", "web"]
            for p in sorted((source / name).rglob("*"))
            if p.is_file()
        }
        require(
            "backend/go.mod" in inputs
            and "backend/go.sum" in inputs
            and "web/package-lock.json" in inputs
            and "web/package.json" in inputs,
            "Source dependencies missing",
        )
        builders = {
            "backend-source": builder_identity(go_image, "golang", context, execute),
            "web-source": builder_identity(node_image, "node", context, execute),
        }
        records = []
        for target, script in [
            ("backend-source", GO_SCRIPT),
            ("web-source", NODE_SCRIPT),
        ]:
            raw_root = output / target
            raw_root.mkdir(mode=0o700)
            started = datetime.now(timezone.utc).isoformat()
            execute_fixture(
                docker_scan_args(
                    builders[target]["config_digest"], source, raw_root, context, script
                ),
                root,
                raw_root,
                execute,
                timeout=1200,
            )
            raw = {}
            for path in raw_root.iterdir():
                require(
                    path.is_file()
                    and not path.is_symlink()
                    and path.stat().st_size <= MAX_RAW,
                    "Scanner raw output exceeds bounds",
                )
                raw[path.name] = path.read_bytes()
            scan = (
                analyze_go(raw, platform=context.platform)
                if target == "backend-source"
                else analyze_npm(raw, (source / "web/package-lock.json").read_bytes())
            )
            scan.update(
                {
                    "target": target,
                    "subject": "git:" + context.repository + "@" + context.commit,
                    "started_at": started,
                    "scanned_at": datetime.now(timezone.utc).isoformat(),
                    "builder": builders[target],
                    "raw_files": {
                        name: sha256(data) for name, data in sorted(raw.items())
                    },
                }
            )
            records.append(scan)
        require(
            inputs
            == {
                str(p.relative_to(source)): sha256(p.read_bytes())
                for name in ["backend", "web"]
                for p in sorted((source / name).rglob("*"))
                if p.is_file()
            },
            "Read-only source fixture changed during scans",
        )
        result = {
            "schema_version": 1,
            "kind": "native-source-scanner-measurement",
            "source": identity,
            "execution": "native",
            "source_archive_sha256": archive_digest,
            "source_inputs": inputs,
            "scans": records,
            "completed_at": datetime.now(timezone.utc).isoformat(),
            "publication_authorized": False,
            "source_scanners_gate_pending": True,
            "findings_review_required": True,
        }
        (output / "source-scan-measurement.json").write_bytes(json_bytes(result))
        return result


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--mode", choices=["source-scans", "compiler-graph"], default="source-scans"
    )
    for name in [
        "repository",
        "version",
        "revision",
        "platform",
        "go-image",
    ]:
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--repository-root", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--node-image")
    parser.add_argument("--component", choices=["backend", "web"])
    parser.add_argument("--runtime-pack", type=Path)
    parser.add_argument("--runtime-source-sha256")
    parser.add_argument("--image-archive", type=Path)
    parser.add_argument("--tested-config")
    parser.add_argument("--advisory-id", action="append", default=[])
    parser.add_argument("--cosign", type=Path)
    args = parser.parse_args(argv)
    compiler_fields = [
        "component",
        "runtime_pack",
        "runtime_source_sha256",
        "image_archive",
        "tested_config",
    ]
    if args.mode == "source-scans":
        if not args.node_image:
            parser.error("source-scans requires --node-image")
        if (
            any(getattr(args, name) for name in compiler_fields)
            or args.cosign
            or args.advisory_id
        ):
            parser.error("compiler inputs require --mode compiler-graph")
    else:
        for name in compiler_fields:
            if getattr(args, name) is None:
                parser.error("compiler-graph requires --" + name.replace("_", "-"))
        if args.component == "web" and args.cosign is None:
            parser.error("web compiler-graph requires --cosign")
        if args.node_image:
            parser.error("--node-image applies only to source-scans")
    try:
        context = NativeSourceContext(
            args.repository, args.version, args.revision, args.platform
        )
        if args.mode == "source-scans":
            result = measure_source_scans(
                context,
                repository_root=args.repository_root,
                go_image=args.go_image,
                node_image=args.node_image,
                output=args.output,
            )
            summary = {
                "status": "complete",
                "source_scanners_gate_pending": True,
                "findings": {s["target"]: len(s["findings"]) for s in result["scans"]},
            }
        else:
            result = measure_compiler_graph(
                context,
                component=args.component,
                repository_root=args.repository_root,
                runtime_pack=args.runtime_pack,
                runtime_source_sha256=args.runtime_source_sha256,
                image_archive=args.image_archive,
                tested_config=args.tested_config,
                go_image=args.go_image,
                output=args.output,
                advisory_ids=tuple(args.advisory_id),
                cosign=args.cosign,
            )
            summary = {
                "status": "complete",
                "component": result["component"],
                "package_count": result["source_graph"]["package_count"],
                "binary_sha256": result["binary"]["sha256"],
                "publication_authorized": False,
                "finding_dispositions_authorized": False,
            }
        print(json.dumps(summary))
    except (
        InvalidRelease,
        OSError,
        ValueError,
        KeyError,
        TypeError,
        tarfile.TarError,
        subprocess.TimeoutExpired,
    ) as error:
        parser.exit(1, f"Source measurement failed: {error}\n")


def compiler_script(component: str, build_settings: dict[str, str]) -> str:
    require(component in {"backend", "web"}, "Unknown compiler graph component")
    require(
        not build_settings.get("GOEXPERIMENT")
        and build_settings.get("-buildmode", "exe") == "exe"
        and build_settings.get("-compiler", "gc") == "gc"
        and all(
            key in {"-buildmode", "-compiler", "-tags", "-trimpath", "-ldflags"}
            for key in build_settings
            if key.startswith("-")
        ),
        "Unhandled executable package-selection settings",
    )
    require(
        build_settings.get("CGO_ENABLED") == "0"
        and build_settings.get("GOOS") == "linux"
        and build_settings.get("GOARCH") in {"amd64", "arm64"},
        "Unsupported exact executable build configuration",
    )
    architecture = build_settings["GOARCH"]
    architecture_value = (
        build_settings.get("GOAMD64", "v1")
        if architecture == "amd64"
        else build_settings.get("GOARM64", "v8.0")
    )
    require(
        architecture_value == ("v1" if architecture == "amd64" else "v8.0"),
        "Unsupported native executable architecture variant",
    )
    tags = build_settings.get("-tags", "")
    require(
        tags == ("nobadger,nomysql,nopgx" if component == "web" else ""),
        "Unreviewed executable build tags",
    )
    script = """set -eu
export PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export GOTOOLCHAIN=local GOPATH=/go GOMODCACHE=/go/pkg/mod GOCACHE=/tmp/build GOWORK=off
export GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org GOPRIVATE= GONOPROXY= GONOSUMDB=
export CGO_ENABLED=0 GOOS=linux GOFLAGS= GOEXPERIMENT=
cd /build
go version > /reports/go-version.txt
go version -m -json /reports/runtime-binary > /reports/binary-build-info.json
"""
    script += (
        "export GOARCH="
        + architecture
        + " "
        + ("GOAMD64=" if architecture == "amd64" else "GOARM64=")
        + architecture_value
        + "\n"
    )
    if component == "web":
        script += "export GOFLAGS=-mod=vendor\ngo list -deps -json -buildvcs=false -tags=nobadger,nomysql,nopgx . > /reports/compiler-graph.json\n"
    else:
        script += "go mod download\ngo list -deps -json ./cmd/server > /reports/compiler-graph.json\ngo build -o /reports/rebuilt-backend ./cmd/server\nsha256sum /reports/rebuilt-backend > /reports/rebuilt-backend-sha256.txt\n"
    script += 'go env -json GOOS GOARCH CGO_ENABLED GOAMD64 GOARM64 GOFLAGS GOEXPERIMENT GOTOOLCHAIN GOPATH GOMODCACHE GOSUMDB GOPROXY > /reports/compiler-environment.json\nprintf "complete\\n" > /reports/compiler-execution.complete\n'
    return script


def compiler_correspondence(
    raw: dict[str, bytes],
    *,
    component: str,
    actual_binary: bytes,
    expected_build: dict,
    platform: str,
    vendor_sum: bytes | None = None,
    vendor_modules: bytes | None = None,
) -> dict:
    import collect_caddy_sources as caddy_sources

    require(
        raw["compiler-execution.complete"] == b"complete\n",
        "Compiler graph execution incomplete",
    )
    actual = read_json(raw["binary-build-info.json"])
    require(
        actual == expected_build and actual["GoVersion"] == GO_VERSION,
        "Executable build metadata does not correspond to graph target",
    )
    settings = {s["Key"]: s["Value"] for s in actual["Settings"]}
    compiler_script(component, settings)
    require(
        settings.get("GOARCH") == platform.split("/")[1]
        and settings.get("GOOS") == "linux",
        "Executable target platform differs",
    )
    environment = read_json(raw["compiler-environment.json"])
    require(
        not environment.get("GOEXPERIMENT"),
        "Unhandled compiler package-selection experiments",
    )
    for key in ["GOOS", "GOARCH", "CGO_ENABLED"]:
        require(
            environment.get(key) == settings.get(key),
            "Compiler environment differs from executable build settings",
        )
    require(
        raw["go-version.txt"].decode().strip()
        == "go version " + GO_VERSION + " " + platform
        and environment.get("GOTOOLCHAIN") == "local",
        "Compiler toolchain differs from executable",
    )
    for key in ["GOAMD64", "GOARM64"]:
        if key in settings:
            require(
                environment.get(key) == settings[key],
                "Compiler architecture variant differs",
            )
    paths, modules = graph_paths(raw["compiler-graph.json"])
    embedded = {(d["Path"], d["Version"]) for d in actual.get("Deps", [])}
    require(
        not any(d.get("Replace") for d in actual.get("Deps", [])),
        "Executable replacement modules require review",
    )
    main = (actual["Main"]["Path"], actual["Main"].get("Version", ""))
    graph_selected = {m for m in modules if m[0] != main[0]}
    require(
        graph_selected == embedded,
        "Compiler-selected module versions differ from executable dependency set",
    )
    if component == "web":
        require(
            vendor_sum is not None and vendor_modules is not None,
            "Authenticated vendor checksum/source declarations missing",
        )
        caddy_sources.verify_modules(actual, vendor_sum, vendor_modules)
        require(
            environment.get("GOFLAGS") == "-mod=vendor"
            and settings.get("-tags") == "nobadger,nomysql,nopgx",
            "Caddy graph was not resolved from exact vendor/tags",
        )
    else:
        resolved_sums = {
            record["Module"]["Path"]: record["Module"].get("Sum")
            for record in json_stream(raw["compiler-graph.json"])
            if record.get("Module")
        }
        require(
            all(
                resolved_sums.get(module["Path"]) == module.get("Sum")
                for module in actual.get("Deps", [])
            ),
            "Actual backend module checksums differ from binary metadata",
        )
        rebuilt = raw["rebuilt-backend-sha256.txt"].decode().split()[0]
        require(
            "sha256:" + rebuilt == sha256(actual_binary),
            "Rebuilt backend does not byte-match actual final OCI executable",
        )
        require(
            environment.get("GOFLAGS") == ""
            and environment.get("GOPATH") == "/go"
            and environment.get("GOMODCACHE") == "/go/pkg/mod",
            "Backend rebuild diverges from Dockerfile builder layout/flags",
        )
    module_records = [
        {"path": d["Path"], "version": d["Version"], "sum": d.get("Sum", "")}
        for d in actual.get("Deps", [])
    ]
    require(
        all(m["sum"].startswith("h1:") for m in module_records),
        "Executable dependency module checksums absent",
    )
    return {
        "root_target": "./cmd/server" if component == "backend" else ".",
        "root_import_path": actual["Path"],
        "raw_file": "compiler-graph.json",
        "sha256": sha256(raw["compiler-graph.json"]),
        "package_paths_sha256": sha256(json_bytes(sorted(paths))),
        "package_count": len(paths),
        "modules": module_records,
        "build_environment": environment,
        "rebuilt_sha256": (
            "sha256:" + raw["rebuilt-backend-sha256.txt"].decode().split()[0]
            if component == "backend"
            else None
        ),
        "executable_correspondence": (
            "byte-identical-same-builder-rebuild"
            if component == "backend"
            else "authenticated-original-vendor-source-and-signed-executable"
        ),
    }


def measure_compiler_graph(
    context: NativeSourceContext,
    *,
    component: str,
    repository_root: Path,
    runtime_pack: Path,
    runtime_source_sha256: str,
    image_archive: Path,
    tested_config: str,
    go_image: str,
    output: Path,
    advisory_ids: tuple[str, ...],
    cosign: Path | None = None,
    execute=run,
) -> dict:
    import verify_runtime_source_pack as replay
    import package_runtime_sources as source_pack
    import collect_caddy_sources as caddy_sources
    from collect_runtime_notices import read_url

    identity = context.checked()
    require(
        context.platform == native_platform(),
        "Compiler graph measurement requires native execution",
    )
    require(
        component in {"backend", "web"}
        and not output.exists()
        and not output.is_symlink(),
        "Invalid/replaced compiler measurement output",
    )
    output.mkdir(parents=True, mode=0o700)
    with tempfile.TemporaryDirectory(prefix="psst-compiler-graph-") as temporary:
        root = Path(temporary)
        sources = root / "runtime"
        sources.mkdir()
        manifest = read_json(source_pack.read(runtime_pack, "runtime-pack.json"))
        require(
            manifest["version"] == context.version
            and manifest["revision"] == context.commit
            and manifest["architecture"] == context.platform.split("/")[1],
            "Runtime source pack belongs to another native source context",
        )
        require(
            "sha256:" + manifest["source_asset"]["sha256"] == runtime_source_sha256,
            "Compiler source asset differs from external binding",
        )
        asset = runtime_pack / source_pack.safe_member(manifest["source_asset"]["file"])
        replay.extract_source_asset(asset, sources, runtime_source_sha256)
        inventory = read_json(
            source_pack.read(
                sources / (component + "-runtime"), "runtime-inventory.json"
            )
        )
        binding = manifest["bindings"][component]
        image, binary = replay.replay_image(
            image_archive,
            component,
            binding,
            inventory,
            manifest["overlays"][component],
            manifest["additional_files"][component],
            {
                "repository": context.repository,
                "version": context.version,
                "revision": context.commit,
                "architecture": manifest["architecture"],
                "source_root": sources,
            },
            tested_config,
        )
        (output / "runtime-binary").write_bytes(binary)
        builder = builder_identity(go_image, "golang", context, execute)
        source_identity = {"runtime_source_asset_sha256": runtime_source_sha256}
        proof = None
        vendor_sum = None
        vendor_modules = None
        if component == "backend":
            source = root / "application"
            source.mkdir()
            source_identity["application_archive_sha256"] = source_snapshot(
                repository_root, context.commit, source, execute
            )
            build_root = source / "backend"
            # ELF build information is read by the exact compiler, never execute it.
            initial = "set -eu; /usr/local/go/bin/go version -m -json /reports/runtime-binary > /reports/initial-build-info.json"
            execute_fixture(
                docker_scan_args(
                    builder["config_digest"], build_root, output, context, initial
                ),
                root,
                output,
                execute,
                timeout=60,
            )
            expected_build = read_json(
                (output / "initial-build-info.json").read_bytes()
            )
        else:
            require(
                cosign is not None,
                "Caddy graph requires actual upstream signature verifier",
            )
            caddy_inventory, proof, _, _ = replay.verify_caddy_collection(
                sources / "caddy",
                cosign,
                binary,
                {**binding, "architecture": manifest["architecture"]},
            )
            expected_build = read_json(
                source_pack.read(sources / "caddy", "build-info.json")
            )
            wrapped = (
                sources
                / "caddy"
                / f"caddy_{caddy_inventory['version'][1:]}_buildable-artifact.tar.gz"
            )
            build_root = root / "wrapper"
            build_root.mkdir()
            # Signed wrapper contains vendor symlinks in some future versions:
            # unsupported special members fail closed rather than extracting them.
            with tarfile.open(wrapped) as archive:
                seen = set()
                expanded = 0
                for member in archive:
                    path = Path(member.name)
                    require(
                        not path.is_absolute()
                        and ".." not in path.parts
                        and member.name not in seen
                        and len(seen) < 100_000
                        and (member.isfile() or member.isdir()),
                        "Unsafe authenticated wrapper source member",
                    )
                    seen.add(member.name)
                    expanded += member.size
                    require(
                        expanded <= SOURCE_LIMIT,
                        "Authenticated wrapper source expansion exceeds bounds",
                    )
                    destination = build_root / path
                    if member.isdir():
                        destination.mkdir(parents=True, exist_ok=True)
                    else:
                        destination.parent.mkdir(parents=True, exist_ok=True)
                        with destination.open("xb") as stream:
                            stream.write(archive.extractfile(member).read())
            vendor_sum = (build_root / "go.sum").read_bytes()
            vendor_modules = (build_root / "vendor/modules.txt").read_bytes()
            source_identity.update(
                {
                    "authenticated_wrapper_sha256": sha256(wrapped.read_bytes()),
                    "wrapper_revision": caddy_inventory["source_revision"],
                    "vendor_modules_sha256": sha256(vendor_modules),
                }
            )
        for filename in ["go.mod", "go.sum"]:
            source_identity[filename.replace(".", "_") + "_sha256"] = sha256(
                (build_root / filename).read_bytes()
            )
        settings = {s["Key"]: s["Value"] for s in expected_build["Settings"]}
        args = docker_scan_args(
            builder["config_digest"],
            build_root,
            output,
            context,
            compiler_script(component, settings),
        )
        mount = args.index("--mount")
        args[mount + 1] = f"type=bind,src={build_root},dst=/build,readonly"
        args[2:2] = ["--tmpfs", "/go:rw,nosuid,nodev,size=1g"]
        execute_fixture(args, root, output, execute, timeout=900)
        raw = {
            p.name: p.read_bytes()
            for p in output.iterdir()
            if p.is_file() and p.name not in {"runtime-binary", "rebuilt-backend"}
        }
        require(
            all(len(value) <= MAX_RAW for value in raw.values()),
            "Compiler raw graph exceeds bounds",
        )
        graph = compiler_correspondence(
            raw,
            component=component,
            actual_binary=binary,
            expected_build=expected_build,
            platform=context.platform,
            vendor_sum=vendor_sum,
            vendor_modules=vendor_modules,
        )
        advisories = {}
        for identifier in advisory_ids:
            require(
                re.fullmatch(r"GO-[0-9]{4}-[0-9]+", identifier) is not None
                and identifier not in advisories,
                "Invalid/duplicate official advisory identifier",
            )
            origin = "https://vuln.go.dev/ID/" + identifier + ".json"
            body = read_url(origin, 1024**2)
            require(
                read_json(body).get("id") == identifier, "Official advisory ID differs"
            )
            filename = identifier + ".json"
            (output / filename).write_bytes(body)
            advisories[identifier] = {
                "raw_file": filename,
                "sha256": sha256(body),
                "origin": origin,
            }
        correspondence = {
            "kind": "reproduced-in-release-builder",
            "actual_sha256": sha256(binary),
            "rebuilt_sha256": graph["rebuilt_sha256"],
        }
        if component == "web":
            proof_file = "caddy-signature-verification.json"
            (output / proof_file).write_bytes(json_bytes(proof))
            signed_bindings = {
                item["file"]: item["sha512"] for item in proof["signed_sha512_bindings"]
            }
            short = caddy_inventory["version"][1:]
            source_asset = f"caddy_{short}_buildable-artifact.tar.gz"
            checksum_asset = f"caddy_{short}_checksums.txt"
            binary_archive = f"caddy_{short}_linux_{manifest['architecture']}.tar.gz"
            correspondence = {
                "kind": "upstream-signed-source-and-binary",
                "signature_proof_file": proof_file,
                "signature_proof_sha256": sha256(json_bytes(proof)),
                "source_asset": {
                    "file": source_asset,
                    "sha512": signed_bindings[source_asset],
                },
                "checksum_asset": {
                    "file": checksum_asset,
                    "sha512": hashlib.sha512(
                        (sources / "caddy" / checksum_asset).read_bytes()
                    ).hexdigest(),
                },
                "binary_archive": {
                    "file": binary_archive,
                    "sha512": signed_bindings[binary_archive],
                },
                "executable_archive_member_sha256": sha256(binary),
            }
        raw_files = {
            p.name: sha256(p.read_bytes())
            for p in output.iterdir()
            if p.is_file() and p.name not in {"runtime-binary", "rebuilt-backend"}
        }
        require(
            "sha256:" + source_pack.file_hash(asset) == runtime_source_sha256,
            "Compiler source asset changed during analysis",
        )
        require(
            "sha256:" + source_pack.file_hash(image_archive) == image["archive_digest"],
            "OCI executable input changed during analysis",
        )
        result = {
            "schema_version": 1,
            "kind": "native-compiler-graph-measurement",
            "source": identity,
            "execution": "native",
            "component": component,
            "executable_target": (
                "app/server" if component == "backend" else "usr/bin/caddy"
            ),
            "builder": builder,
            "image": image,
            "binary": {
                "sha256": sha256(binary),
                "rebuilt_sha256": graph["rebuilt_sha256"],
                "correspondence": correspondence,
                "build_info": expected_build,
                "build_info_sha256": sha256(json_bytes(expected_build)),
            },
            "source_graph": graph,
            "source_inputs": source_identity,
            "signature_verification": proof,
            "advisories": advisories,
            "raw_files": raw_files,
            "status": "complete",
            "exit_code": 0,
            "completed_at": datetime.now(timezone.utc).isoformat(),
            "publication_authorized": False,
            "finding_dispositions_authorized": False,
        }
        (output / "compiler-graph-measurement.json").write_bytes(json_bytes(result))
        return result


if __name__ == "__main__":
    main()
