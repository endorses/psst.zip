"""Derive source scanner facts from authenticated native measurements, never publish.

The trusted workflow authenticates inputs against the complete release binding
and signs the resulting gate. Caller dispositions cannot replace raw evidence.
"""

from __future__ import annotations

from datetime import datetime
from pathlib import Path, PurePosixPath
import re
import tempfile

from generate_release_gate_reports import (
    MeasurementAuthenticator,
    NativeSourceContext,
    checked_binding,
    timestamp,
)
from measure_release_source_scans import (
    GO_VERSION,
    MAX_RAW,
    analyze_go,
    analyze_npm,
    graph_paths,
    json_stream,
    run,
    source_snapshot,
)
from prepare_release_candidate import validate_candidate
from publish_container_release import Binding, sha256
from release_artifacts import (
    DIGEST,
    PLATFORMS,
    fields,
    json_bytes,
    matches,
    read_bounded_file,
    read_json,
    require,
)

TARGETS = {"backend-source", "web-source"}
FILE = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,199}\Z")
MODULE_SUM = re.compile(r"h1:[A-Za-z0-9+/]{43}=\Z")
GO_ID = re.compile(r"GO-[0-9]{4}-[0-9]+\Z")


def authenticated(
    path: Path, binding: Binding, authenticator: MeasurementAuthenticator
):
    raw = read_bounded_file(path)
    authenticator.authenticate(raw, binding)
    value = read_json(raw)
    require(
        isinstance(value, dict), "Authenticated source measurement is not an object"
    )
    return value, sha256(raw)


def raw_files(root: Path, inventory: object) -> dict[str, bytes]:
    require(
        isinstance(inventory, dict) and 0 < len(inventory) <= 1024,
        "Missing or oversized authenticated raw file inventory",
    )
    require(root.is_dir() and not root.is_symlink(), "Unsafe source evidence directory")
    result = {}
    total = 0
    for name, expected in inventory.items():
        matches(name, FILE, "Unsafe source evidence filename")
        matches(expected, DIGEST, "Missing authenticated raw source checksum")
        path = root / name
        require(
            path.is_file() and not path.is_symlink() and path.stat().st_size <= MAX_RAW,
            "Missing, substituted or oversized raw source evidence",
        )
        with path.open("rb") as stream:
            raw = stream.read(MAX_RAW + 1)
        total += len(raw)
        require(
            len(raw) <= MAX_RAW and total <= 512 * 1024**2 and sha256(raw) == expected,
            "Raw source evidence checksum/bounds differ",
        )
        result[name] = raw
    return result


def checked_builder(builder: dict, *, reference: str, context: NativeSourceContext):
    fields(
        builder,
        {
            "reference",
            "config_digest",
            "architecture",
            "rootfs_layers",
            "docker_engine_architecture",
        },
        "actual source builder",
    )
    arch = context.platform.split("/")[1]
    require(
        builder["reference"] == reference
        and builder["architecture"] == arch
        and builder["docker_engine_architecture"]
        in ({"amd64", "x86_64"} if arch == "amd64" else {"arm64", "aarch64"}),
        "Source builder differs from authenticated native base resolution",
    )
    matches(
        builder["config_digest"], DIGEST, "Missing actual source builder configuration"
    )
    require(
        isinstance(builder["rootfs_layers"], list) and builder["rootfs_layers"],
        "Missing source builder layers",
    )
    for digest in builder["rootfs_layers"]:
        matches(digest, DIGEST, "Invalid source builder layer")


def go_facts(raw: dict[str, bytes], source: Path, platform: str) -> list[dict]:
    """Independently derive absence; never consume a measurement's dispositions."""
    env = read_json(raw["go-env.json"])
    arch = platform.split("/")[1]
    require(
        env.get("CGO_ENABLED") == "0"
        and not env.get("GOEXPERIMENT")
        and env.get("GOAMD64" if arch == "amd64" else "GOARM64")
        == ("v1" if arch == "amd64" else "v8.0"),
        "Source graph uses unsupported native package-selection settings",
    )
    module_line = re.search(
        rb"(?m)^module\s+(\S+)\s*$", (source / "backend/go.mod").read_bytes()
    )
    require(module_line is not None, "Committed backend module identity missing")
    main = module_line.group(1).decode()
    all_paths, all_selected = graph_paths(raw["go-all-graph.json"])
    server_paths, server_selected = graph_paths(raw["go-server-graph.json"])
    require(
        main + "/cmd/server" in server_paths,
        "Server graph omits exact committed executable root",
    )
    modules = {}
    for module in json_stream(raw["go-modules.json"]):
        require(
            isinstance(module.get("Path"), str)
            and module["Path"] not in modules
            and not module.get("Replace")
            and not module.get("Error"),
            "Missing, duplicate or replaced source module",
        )
        modules[module["Path"]] = module
    require(
        modules.get(main, {}).get("Main") is True,
        "Go module graph differs from committed module",
    )
    sums = {
        tuple(line.split())
        for line in (source / "backend/go.sum").read_text().splitlines()
    }
    for name, version in all_selected | server_selected:
        require(
            name in modules and modules[name].get("Version", "") == version,
            "Imported module version differs from selected module graph",
        )
        if name == main:
            continue
        matches(
            modules[name].get("Sum"), MODULE_SUM, "Imported module checksum missing"
        )
        require(
            (name, version, modules[name]["Sum"]) in sums,
            "Imported module differs from committed GoSumDB sum",
        )
    for filename in ["go-all-graph.json", "go-server-graph.json"]:
        for package in json_stream(raw[filename]):
            module = package.get("Module")
            if module and module["Path"] != main:
                require(
                    module.get("Sum") == modules[module["Path"]].get("Sum"),
                    "Compiler source package checksum differs from selected module",
                )
    advisories, findings = {}, []
    for message in json_stream(raw["govulncheck.json"]):
        if "osv" in message:
            advisories[message["osv"]["id"]] = message["osv"]
        if "finding" in message:
            findings.append(message["finding"])
    result = []
    for finding in findings:
        identifier = matches(
            finding.get("osv"), GO_ID, "Finding lacks official Go advisory ID"
        )
        advisory = advisories[identifier]
        require(
            not advisory.get("withdrawn"),
            "Withdrawn advisory cannot establish an absence disposition",
        )
        timestamp(advisory.get("modified"))
        trace = finding.get("trace")
        require(
            isinstance(trace, list)
            and len(trace) == 1
            and isinstance(trace[0], dict)
            and set(trace[0]) <= {"module", "version"},
            "Package/symbol/uncertain source findings require repair and rescan",
        )
        frame = trace[0]
        module, version = frame.get("module"), frame.get("version")
        require(
            isinstance(module, str)
            and isinstance(version, str)
            and (
                (module == "stdlib" and version == "v" + GO_VERSION.removeprefix("go"))
                or (module in modules and modules[module].get("Version") == version)
            ),
            "Finding module/version differs from exact selected native source graph",
        )
        affected = set()
        entries = advisory.get("affected")
        require(
            isinstance(entries, list) and entries,
            "Official affected source paths missing",
        )
        for entry in entries:
            require(
                isinstance(entry, dict) and isinstance(entry.get("package"), dict),
                "Malformed official affected source entry",
            )
            package = entry["package"]
            if package.get("ecosystem") != "Go" or package.get("name") != module:
                continue
            imports = entry.get("ecosystem_specific", {}).get("imports")
            require(
                isinstance(imports, list) and imports,
                "Affected module lacks complete official import paths",
            )
            for imported in imports:
                path = imported.get("path") if isinstance(imported, dict) else None
                require(
                    isinstance(path, str)
                    and path
                    and re.fullmatch(r"[A-Za-z0-9_~.+/-]+", path) is not None
                    and str(PurePosixPath(path)) == path
                    and not PurePosixPath(path).is_absolute()
                    and ".." not in PurePosixPath(path).parts
                    and "\\" not in path
                    and (
                        module == "stdlib"
                        or path == module
                        or path.startswith(module + "/")
                    ),
                    "Official affected source import path is uncertain",
                )
                affected.add(path)
        require(
            affected and not affected.intersection(all_paths | server_paths),
            "Affected source packages are present or uncertain; repair/rescan required",
        )
        result.append(
            {
                "id": identifier,
                "finding": finding,
                "finding_sha256": sha256(json_bytes(finding)),
                "advisory_sha256": sha256(json_bytes(advisory)),
                "official_advisory": {
                    "id": identifier,
                    "origin": "https://vuln.go.dev",
                    "modified": advisory["modified"],
                    "raw_report_sha256": sha256(raw["govulncheck.json"]),
                },
                "affected_packages": sorted(affected),
                "disposition": "not-applicable",
                "reason": "Every official affected Go package across all advisory ranges is absent from both exact native all-root and server graphs; this is a module-only finding.",
                "all_root_graph_sha256": sha256(raw["go-all-graph.json"]),
                "server_graph_sha256": sha256(raw["go-server-graph.json"]),
            }
        )
    return result


def aggregate_source_scans(
    binding: Binding,
    *,
    native_measurements: dict[str, Path],
    repository_root: Path,
    resolved_bases: Path,
    authenticator: MeasurementAuthenticator,
) -> dict:
    """Return an unsigned gate only from complete authenticated native facts."""
    subjects = checked_binding(binding)
    fields(native_measurements, set(PLATFORMS), "both native source measurements")
    bases, bases_digest = authenticated(resolved_bases, binding, authenticator)
    validate_candidate(bases)
    require(
        bases["version"] == binding.version
        and bases["source_commit"] == binding.commit,
        "Source builder resolution belongs to another release",
    )
    measured = {target: {} for target in TARGETS}
    combined_findings = {target: [] for target in TARGETS}
    with tempfile.TemporaryDirectory(prefix="psst-source-gate-") as folder:
        source = Path(folder)
        archive_digest = source_snapshot(repository_root, binding.commit, source, run)
        inputs = {
            str(path.relative_to(source)): sha256(path.read_bytes())
            for directory in ["backend", "web"]
            for path in sorted((source / directory).rglob("*"))
            if path.is_file()
        }
        for platform in PLATFORMS:
            context = NativeSourceContext(
                binding.repository, binding.version, binding.commit, platform
            )
            record, digest = authenticated(
                native_measurements[platform], binding, authenticator
            )
            fields(
                record,
                {
                    "schema_version",
                    "kind",
                    "source",
                    "execution",
                    "source_archive_sha256",
                    "source_inputs",
                    "scans",
                    "completed_at",
                    "publication_authorized",
                    "source_scanners_gate_pending",
                    "findings_review_required",
                },
                "native source measurement",
            )
            require(
                type(record["schema_version"]) is int
                and record["schema_version"] == 1
                and record["kind"] == "native-source-scanner-measurement"
                and record["source"] == context.checked()
                and record["execution"] == "native"
                and record["source_archive_sha256"] == archive_digest
                and record["source_inputs"] == inputs
                and record["publication_authorized"] is False
                and record["source_scanners_gate_pending"] is True
                and record["findings_review_required"] is True,
                "Source measurement is stale, substituted, emulated or belongs to another release",
            )
            timestamp(record["completed_at"])
            scans = record["scans"]
            require(
                isinstance(scans, list)
                and len(scans) == 2
                and all(isinstance(scan, dict) for scan in scans)
                and {scan.get("target") for scan in scans} == TARGETS,
                "Native source scanner coverage missing/duplicated",
            )
            for scan in scans:
                target = scan["target"]
                kind = "golang" if target == "backend-source" else "node"
                checked_builder(
                    scan.get("builder"),
                    reference=bases["base_images"][kind],
                    context=context,
                )
                for key in ["started_at", "scanned_at"]:
                    timestamp(scan.get(key))
                times = [
                    datetime.fromisoformat(scan[key].replace("Z", "+00:00"))
                    for key in ["started_at", "scanned_at"]
                ]
                require(
                    times[0]
                    <= times[1]
                    <= datetime.fromisoformat(
                        record["completed_at"].replace("Z", "+00:00")
                    ),
                    "Source measurement timestamps are inconsistent",
                )
                raw = raw_files(
                    native_measurements[platform].parent / target, scan.get("raw_files")
                )
                required = (
                    {
                        "go-execution.complete",
                        "govulncheck.exit",
                        "go-version.txt",
                        "go-env.json",
                        "scanner-module.json",
                        "scanner-build.json",
                        "scanner-binary-sha256.txt",
                        "go-roots.json",
                        "go-all-graph.json",
                        "go-server-graph.json",
                        "go-modules.json",
                        "govulncheck.json",
                        "govulncheck-convert.exit",
                        "govulncheck.txt",
                    }
                    if target == "backend-source"
                    else {
                        "node-execution.complete",
                        "node-version.txt",
                        "npm-version.txt",
                        "npm-audit.exit",
                        "npm-audit.json",
                        "npm-lock-graph.json",
                    }
                )
                require(
                    required <= set(raw),
                    "Authenticated source execution receipts are incomplete",
                )
                calculated = (
                    analyze_go(raw, platform=platform)
                    if target == "backend-source"
                    else analyze_npm(
                        raw, (source / "web/package-lock.json").read_bytes()
                    )
                )
                metadata = {
                    "target",
                    "subject",
                    "started_at",
                    "scanned_at",
                    "builder",
                    "raw_files",
                }
                fields(scan, set(calculated) | metadata, "complete source scan")
                require(
                    scan["subject"]
                    == "git:" + binding.repository + "@" + binding.commit
                    and {key: scan[key] for key in calculated} == calculated,
                    "Source scanner measurement omitted/changed raw findings or tool/database/graph facts",
                )
                findings = (
                    go_facts(raw, source, platform)
                    if target == "backend-source"
                    else []
                )
                require(
                    target != "web-source" or not calculated["findings"],
                    "npm source findings require repair and rescan",
                )
                measured[target][platform] = {
                    **calculated,
                    "findings": findings,
                    "builder": scan["builder"],
                    "started_at": scan["started_at"],
                    "scanned_at": scan["scanned_at"],
                    "completed_at": record["completed_at"],
                    "measurement_sha256": digest,
                    "raw_files": scan["raw_files"],
                }
                combined_findings[target].extend(
                    {**finding, "platform": platform} for finding in findings
                )
        return {
            "schema_version": 1,
            "gate": "source-scanners",
            "binding_digest": binding.digest,
            "passed": True,
            "details": {
                "scans": [
                    {
                        "target": target,
                        "subject": "git:" + binding.repository + "@" + binding.commit,
                        "status": "complete",
                        "exit_code": 0,
                        "scanner": (
                            "govulncheck" if target == "backend-source" else "npm audit"
                        ),
                        "version": ",".join(
                            sorted(
                                {
                                    row["scanner"]["version"]
                                    for row in measured[target].values()
                                }
                            )
                        ),
                        "database": sha256(
                            json_bytes(
                                {
                                    platform: row["database"]
                                    for platform, row in measured[target].items()
                                }
                            )
                        ),
                        "scanned_at": max(
                            (row["scanned_at"] for row in measured[target].values()),
                            key=lambda value: datetime.fromisoformat(
                                value.replace("Z", "+00:00")
                            ),
                        ),
                        "findings": combined_findings[target],
                        "native_measurements": measured[target],
                    }
                    for target in sorted(TARGETS)
                ],
                "release_subjects": subjects,
                "source_archive_sha256": archive_digest,
                "source_inputs": inputs,
                "resolved_bases_sha256": bases_digest,
                "derivation_policy": "authenticated-both-native-source-graphs-official-Go-package-absence-and-zero-npm-v1",
            },
        }
