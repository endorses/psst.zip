"""Derive a final image scanner gate from authenticated execution facts.

This cannot accept disposition/reviewer flags. The only implemented dismissal is
that every authoritative affected Go package is absent from the exact compiled
dependency graph. Reachability or version-range approximations are not used.
The trusted workflow must sign the resulting gate; this module cannot publish.
"""

from __future__ import annotations

from pathlib import Path
import re

from collect_caddy_sources import go_source_policy
from generate_release_gate_reports import (
    MeasurementAuthenticator,
    NativeSourceContext,
    aggregate_native_reports,
    checked_binding,
    runtime_inputs,
    timestamp,
)
from measure_release_image_scans import (
    ASSETS,
    DATABASE_REPOSITORY,
    IDENTITY,
    ISSUER,
    SOURCE_COMMIT,
    TARGETS,
    VERSION,
    checked_report,
)
from measure_release_source_scans import GO_VERSION, graph_paths
from prepare_release_candidate import BASES
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
from verify_caddy_source_signatures import (
    COSIGN_COMMIT,
    COSIGN_SHA256,
    COSIGN_VERSION,
    ISSUER as CADDY_ISSUER,
    WORKFLOW as CADDY_WORKFLOW,
    verification_arguments,
)

GO_ID = re.compile(r"GO-[0-9]{4}-[0-9]+\Z")
FILE = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,199}\Z")
MODULE_SUM = re.compile(r"h1:[A-Za-z0-9+/]{43}=\Z")


def load_authenticated(
    path: Path, binding: Binding, authenticator: MeasurementAuthenticator
) -> tuple[dict, str]:
    raw = read_bounded_file(path)
    authenticator.authenticate(raw, binding)
    value = read_json(raw)
    require(isinstance(value, dict), "Authenticated measurement is not an object")
    return value, sha256(raw)


def load_raw(root: Path, files: dict, filename: object, expected: str) -> bytes:
    matches(filename, FILE, "Unsafe raw evidence filename")
    matches(expected, DIGEST, "Missing raw evidence checksum")
    require(
        isinstance(files, dict) and files.get(filename) == expected,
        "Raw evidence is not covered by authenticated measurement",
    )
    raw = read_bounded_file(root / filename)
    require(sha256(raw) == expected, "Raw evidence was substituted")
    return raw


def checked_scan(
    record: dict,
    raw: bytes,
    *,
    context: NativeSourceContext,
    component: str,
    image: dict,
) -> list[dict]:
    require(
        type(record.get("schema_version")) is int
        and record["schema_version"] == 1
        and record.get("kind") == "native-image-scanner-measurement"
        and record.get("source") == context.checked()
        and record.get("execution") == "native"
        and record.get("target") == component + "-" + context.platform.split("/")[1]
        and record.get("image") == image
        and record.get("publication_authorized") is False
        and record.get("findings_review_required") is True
        and record.get("final_image_scanners_gate_pending") is True
        and record.get("status") == "complete"
        and type(record.get("exit_code")) is int
        and record["exit_code"] == 0,
        "Scanner measurement is incomplete or examines another image/source",
    )
    require(
        record.get("raw_report_sha256") == sha256(raw),
        "Raw scanner report differs from authenticated measurement",
    )
    report = checked_report(
        raw,
        platform=context.platform,
        component=component,
        config=image["config_digest"],
    )
    tool = record.get("scanner", {})
    pins = ASSETS[context.platform]
    require(
        isinstance(tool, dict)
        and tool.get("name") == "Trivy"
        and tool.get("version") == VERSION
        and tool.get("source_commit") == SOURCE_COMMIT
        and tool.get("platform") == context.platform
        and tool.get("archive_sha256") == "sha256:" + pins["archive"]
        and tool.get("bundle_sha256") == "sha256:" + pins["bundle"]
        and tool.get("certificate_identity") == IDENTITY
        and tool.get("oidc_issuer") == ISSUER
        and tool.get("verification") == "online-cosign-bundle-sct-rekor",
        "Scanner tooling was not the authenticated pinned native release",
    )
    matches(tool.get("sha256"), DIGEST, "Missing authenticated scanner executable")
    if "binary" in pins:
        require(
            tool["sha256"] == "sha256:" + pins["binary"],
            "Scanner executable differs from pin",
        )
    verifier = tool.get("cosign", {})
    require(
        verifier
        == {
            "version": COSIGN_VERSION,
            "commit": COSIGN_COMMIT,
            "sha256": "sha256:" + COSIGN_SHA256[context.platform],
        },
        "Unexpected scanner signature verifier",
    )
    db = fields(
        record.get("database"),
        {
            "sha256",
            "metadata_sha256",
            "acquisition",
            "Version",
            "UpdatedAt",
            "NextUpdate",
            "DownloadedAt",
        },
        "owned scanner database",
    )
    for key in ("sha256", "metadata_sha256"):
        matches(db[key], DIGEST, "Missing frozen database identity")
    require(
        type(db["Version"]) is int and db["Version"] == 2,
        "Unexpected scanner database schema",
    )
    for key in ("UpdatedAt", "NextUpdate", "DownloadedAt"):
        timestamp(db[key])
    acquisition = db["acquisition"]
    require(
        isinstance(acquisition, dict)
        and acquisition.get("kind") == "owned-authenticated-trivy-download"
        and acquisition.get("repository") == DATABASE_REPOSITORY
        and acquisition.get("scanner_sha256") == tool["sha256"]
        and acquisition.get("database_sha256") == db["sha256"]
        and acquisition.get("metadata_sha256") == db["metadata_sha256"],
        "Trusted official database acquisition evidence is missing",
    )
    for key in ("started_at", "completed_at"):
        timestamp(acquisition.get(key))
    for key in ("started_at", "scanned_at", "completed_at"):
        timestamp(record.get(key))
    require(
        record["scanned_at"] == report["CreatedAt"],
        "Scanner timestamp differs from raw report",
    )
    findings = [
        {
            "target": result["Target"],
            "class": result["Class"],
            "finding_sha256": sha256(json_bytes(finding)),
            "finding": finding,
        }
        for result in report["Results"]
        for finding in result.get("Vulnerabilities", [])
    ]
    require(
        record.get("findings") == findings,
        "Authenticated scanner measurement omitted/changed findings",
    )
    # Preserve the raw analyzer class/type as well as the original full finding.
    for result in report["Results"]:
        if result.get("Vulnerabilities"):
            require(
                result["Class"] == "lang-pkgs"
                and result.get("Type") == "gobinary"
                and result["Target"] == TARGETS[component],
                "OS/non-application/non-Go findings require repair and a complete rescan",
            )
    return findings


def checked_upstream_correspondence(
    graph: dict, root: Path, *, binary_sha: str, settings: dict, platform: str
) -> None:
    binary = graph["binary"]
    correspondence = binary.get("correspondence", {})
    require(
        correspondence.get("kind") == "upstream-signed-source-and-binary"
        and correspondence.get("executable_archive_member_sha256") == binary_sha,
        "Missing exact upstream source/executable correspondence",
    )
    raw = load_raw(
        root,
        graph["raw_files"],
        correspondence.get("signature_proof_file"),
        correspondence.get("signature_proof_sha256"),
    )
    proof = read_json(raw)
    require(
        proof == graph.get("signature_verification") and isinstance(proof, dict),
        "Upstream signature proof was substituted",
    )
    version, revision = proof.get("version"), proof.get("source_revision")
    require(
        isinstance(version, str)
        and re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", version) is not None
        and revision == settings.get("vcs.revision")
        and revision == graph["source_inputs"].get("wrapper_revision"),
        "Upstream source revision differs from actual executable",
    )
    require(
        proof.get("signer_identity") == CADDY_WORKFLOW + "@refs/tags/" + version
        and proof.get("oidc_issuer") == CADDY_ISSUER
        and proof.get("rekor_url") == "https://rekor.sigstore.dev"
        and proof.get("legacy_sigstore_signatures_verified") is True
        and proof.get("certificate_transparency_verification_required") is True
        and proof.get("rekor_verification_required") is True,
        "Upstream proof lacks exact official signer/transparency policy",
    )
    verifier = proof.get("verifier", {})
    require(
        verifier
        == {
            "version": COSIGN_VERSION,
            "commit": COSIGN_COMMIT,
            "platform": platform,
            "sha256": COSIGN_SHA256[platform],
        },
        "Upstream proof used another signature verifier",
    )
    short = version[1:]
    expected_names = {
        "source_asset": f"caddy_{short}_buildable-artifact.tar.gz",
        "checksum_asset": f"caddy_{short}_checksums.txt",
        "binary_archive": f"caddy_{short}_linux_{platform.split('/')[1]}.tar.gz",
    }
    for key, name in expected_names.items():
        asset = correspondence.get(key, {})
        require(
            isinstance(asset, dict)
            and asset.get("file") == name
            and isinstance(asset.get("sha512"), str)
            and re.fullmatch(r"[0-9a-f]{128}", asset["sha512"]) is not None,
            "Upstream correspondence asset/checksum identity differs",
        )
    signed = proof.get("signed_sha512_bindings")
    require(
        isinstance(signed, list)
        and len(signed) == 2
        and {item.get("file"): item.get("sha512") for item in signed}
        == {
            correspondence[key]["file"]: correspondence[key]["sha512"]
            for key in ("source_asset", "binary_archive")
        },
        "Signed checksum list does not bind both original source and executable",
    )
    verifications = proof.get("verifications")
    require(
        isinstance(verifications, list) and len(verifications) == 2,
        "Missing upstream signature executions",
    )
    verified = {item.get("artifact"): item for item in verifications}
    require(
        set(verified)
        == {expected_names["source_asset"], expected_names["checksum_asset"]},
        "Wrong upstream signed artifacts",
    )
    require(
        "sha256:" + str(verified[expected_names["source_asset"]].get("sha256"))
        == graph["source_inputs"].get("authenticated_wrapper_sha256"),
        "Compiler wrapper source differs from authenticated upstream artifact",
    )
    for evidence in verifications:
        require(
            type(evidence.get("exit_code")) is int and evidence["exit_code"] == 0,
            "Upstream signature execution failed",
        )
        args = evidence.get("arguments")
        expected_args = verification_arguments(
            Path("cosign"),
            version,
            revision,
            Path("artifact"),
            Path("certificate"),
            Path("signature"),
        )[1:-5]
        require(
            args == expected_args,
            "Upstream signature execution differs from the exact verified profile",
        )
        require(
            isinstance(args, list)
            and all(
                flag in args
                for flag in (
                    "--insecure-ignore-tlog=false",
                    "--insecure-ignore-sct=false",
                    "--private-infrastructure=false",
                    "--offline=false",
                    "--new-bundle-format=false",
                )
            ),
            "Upstream signature execution weakened transparency/trust",
        )
        for flag, expected in (
            ("--certificate-identity", proof["signer_identity"]),
            ("--certificate-oidc-issuer", CADDY_ISSUER),
            ("--certificate-github-workflow-sha", revision),
            ("--certificate-github-workflow-ref", "refs/tags/" + version),
            ("--certificate-github-workflow-repository", "caddyserver/caddy"),
        ):
            require(
                args.count(flag) == 1
                and args.index(flag) + 1 < len(args)
                and args[args.index(flag) + 1] == expected,
                "Upstream signature execution used another identity/source",
            )


def checked_graph(
    graph: dict,
    root: Path,
    *,
    context: NativeSourceContext,
    component: str,
    image: dict,
    runtime: dict,
    pack: dict,
) -> tuple[set[str], dict[str, str]]:
    require(
        type(graph.get("schema_version")) is int
        and graph["schema_version"] == 1
        and graph.get("kind") == "native-compiler-graph-measurement"
        and graph.get("source") == context.checked()
        and graph.get("execution") == "native"
        and graph.get("component") == component
        and graph.get("executable_target") == TARGETS[component]
        and graph.get("image") == image
        and graph.get("publication_authorized") is False
        and graph.get("finding_dispositions_authorized") is False
        and graph.get("status") == "complete"
        and type(graph.get("exit_code")) is int
        and graph["exit_code"] == 0,
        "Compiler measurement is incomplete or examines another image/source",
    )
    timestamp(graph.get("completed_at"))
    inputs = graph.get("source_inputs", {})
    require(
        inputs.get("runtime_source_asset_sha256") == runtime["source_asset"]["digest"],
        "Compiler source asset differs from exact native runtime source",
    )
    builder = graph.get("builder", {})
    require(
        isinstance(builder.get("reference"), str)
        and builder["reference"].startswith(
            BASES["golang"].rsplit(":", 1)[0] + "@sha256:"
        )
        and builder.get("architecture") == context.platform.split("/")[1],
        "Compiler graph did not use the pinned native builder",
    )
    matches(
        builder["reference"].split("@")[-1], DIGEST, "Compiler builder digest missing"
    )
    matches(
        builder.get("config_digest"),
        DIGEST,
        "Actual compiler builder configuration missing",
    )
    require(
        isinstance(builder.get("rootfs_layers"), list) and builder["rootfs_layers"],
        "Actual compiler builder layers missing",
    )
    for digest in builder["rootfs_layers"]:
        matches(digest, DIGEST, "Invalid compiler builder layer")
    binary = graph.get("binary", {})
    expected_binary = "sha256:" + str(
        pack.get("bindings", {}).get(component, {}).get("binary_sha256")
    )
    matches(expected_binary, DIGEST, "Native runtime executable identity missing")
    require(
        binary.get("sha256") == expected_binary
        and sha256(json_bytes(binary.get("build_info")))
        == binary.get("build_info_sha256"),
        "Compiler binary/build-info differs from actual smoke-checked executable",
    )
    info = binary["build_info"]
    require(
        isinstance(info, dict)
        and isinstance(info.get("GoVersion"), str)
        and info["GoVersion"]
        == pack.get("bindings", {}).get(component, {}).get("go_version")
        and isinstance(info.get("Settings"), list),
        "Compiler toolchain metadata missing/different",
    )
    if component == "backend":
        require(
            info["GoVersion"] == GO_VERSION,
            "Backend compiler differs from pinned producer",
        )
    else:
        # Caddy keeps its original compiler, bound to this exact runtime binary.
        go_source_policy(info["GoVersion"])
    settings = {}
    for setting in info["Settings"]:
        require(
            isinstance(setting, dict)
            and isinstance(setting.get("Key"), str)
            and setting["Key"] not in settings
            and isinstance(setting.get("Value"), str),
            "Duplicate/incomplete actual executable build settings",
        )
        settings[setting["Key"]] = setting["Value"]
    require(
        settings.get("GOOS") == "linux"
        and settings.get("GOARCH") == context.platform.split("/")[1],
        "Compiler executable platform differs",
    )
    require(
        settings.get("GOEXPERIMENT", "") == ""
        and settings.get("-compiler", "gc") == "gc"
        and settings.get("-buildmode", "exe") == "exe",
        "Unhandled compiler/package-selection settings require exact reviewed proof",
    )
    source_graph = graph.get("source_graph", {})
    raw = load_raw(
        root,
        graph.get("raw_files"),
        source_graph.get("raw_file"),
        source_graph.get("sha256"),
    )
    paths, selected = graph_paths(raw)
    require(
        source_graph.get("root_import_path") == info.get("Path")
        and info.get("Path") in paths
        and source_graph.get("package_paths_sha256")
        == sha256(json_bytes(sorted(paths))),
        "Compiler graph root/package inventory differs",
    )
    env = source_graph.get("build_environment", {})
    require(
        env.get("GOTOOLCHAIN") == "local"
        and all(
            env.get(key) == settings.get(key)
            for key in ("GOOS", "GOARCH", "CGO_ENABLED")
        ),
        "Compiler graph build settings differ from actual binary",
    )
    for key in ("GOAMD64", "GOARM64"):
        if key in settings:
            require(
                env.get(key) == settings[key], "Compiler architecture variant differs"
            )
    modules = {}
    declared = source_graph.get("modules")
    require(
        isinstance(declared, list) and isinstance(info.get("Deps"), list),
        "Compiler dependency module identity missing",
    )
    for dep in info["Deps"]:
        require(
            isinstance(dep, dict)
            and not dep.get("Replace")
            and isinstance(dep.get("Path"), str)
            and dep["Path"] not in modules
            and isinstance(dep.get("Version"), str),
            "Unknown/replaced/duplicate executable module",
        )
        matches(dep.get("Sum"), MODULE_SUM, "Executable module checksum missing")
        modules[dep["Path"]] = dep["Version"]
    require(
        declared
        == [
            {"path": d["Path"], "version": d["Version"], "sum": d["Sum"]}
            for d in info["Deps"]
        ],
        "Compiler module checksum/version correspondence differs",
    )
    main = info.get("Main", {})
    require(
        {item for item in selected if item[0] != main.get("Path")}
        == set(modules.items()),
        "Compiler graph selects different module versions than actual executable",
    )
    correspondence = binary.get("correspondence", {})
    if component == "backend":
        require(
            settings.get("-tags", "") == "",
            "Backend compiler graph did not use default Dockerfile tags",
        )
        require(
            correspondence
            == {
                "kind": "reproduced-in-release-builder",
                "actual_sha256": expected_binary,
                "rebuilt_sha256": expected_binary,
            }
            and binary.get("rebuilt_sha256") == expected_binary
            and source_graph.get("rebuilt_sha256") == expected_binary
            and env.get("GOFLAGS") == ""
            and env.get("GOPATH") == "/go"
            and env.get("GOMODCACHE") == "/go/pkg/mod",
            "Backend graph lacks byte-identical actual release-builder reproduction",
        )
        matches(
            inputs.get("application_archive_sha256"),
            DIGEST,
            "Exact application source archive missing",
        )
    else:
        require(
            env.get("GOFLAGS") == "-mod=vendor"
            and settings.get("-tags") == "nobadger,nomysql,nopgx",
            "Caddy graph did not use exact original vendor/build tags",
        )
        checked_upstream_correspondence(
            graph,
            root,
            binary_sha=expected_binary,
            settings=settings,
            platform=context.platform,
        )
    modules["stdlib"] = info["GoVersion"].removeprefix("go")
    return paths, modules


def derive_absence(
    row: dict, graph: dict, root: Path, paths: set[str], modules: dict[str, str]
) -> dict:
    finding = row["finding"]
    require(
        row["class"] == "lang-pkgs"
        and finding.get("DataSource", {}).get("ID") == "govulndb",
        "Finding does not have an authoritative Go database source",
    )
    module = finding["PkgName"]
    version = (
        finding["InstalledVersion"].removeprefix("v")
        if module == "stdlib"
        else finding["InstalledVersion"]
    )
    require(
        modules.get(module) == version,
        "Finding module version differs from exact compiled executable",
    )
    identifiers = {finding["VulnerabilityID"], *finding.get("VendorIDs", [])}
    advisories = graph.get("advisories")
    require(isinstance(advisories, dict), "Official advisory evidence missing")
    selected = [
        identifier
        for identifier in identifiers
        if isinstance(identifier, str) and GO_ID.fullmatch(identifier)
    ]
    require(
        len(selected) == 1 and selected[0] in advisories,
        "Finding lacks an unambiguous official Go advisory",
    )
    identifier = selected[0]
    entry = advisories[identifier]
    require(
        isinstance(entry, dict)
        and entry.get("origin") == f"https://vuln.go.dev/ID/{identifier}.json",
        "Advisory was not collected from the exact official endpoint",
    )
    raw = load_raw(root, graph["raw_files"], entry.get("raw_file"), entry.get("sha256"))
    advisory = read_json(raw)
    require(
        isinstance(advisory, dict)
        and advisory.get("id") == identifier
        and not advisory.get("withdrawn")
        and finding["VulnerabilityID"] in {identifier, *advisory.get("aliases", [])},
        "Official advisory ID/alias or withdrawal is uncertain",
    )
    timestamp(advisory.get("modified"))
    entries = advisory.get("affected")
    require(
        isinstance(entries, list) and entries,
        "Official advisory has no affected package data",
    )
    affected = set()
    for affected_entry in entries:
        require(
            isinstance(affected_entry, dict)
            and isinstance(affected_entry.get("package"), dict),
            "Malformed official affected package",
        )
        package = affected_entry["package"]
        if package.get("ecosystem") != "Go" or package.get("name") != module:
            continue
        imports = affected_entry.get("ecosystem_specific", {}).get("imports")
        require(
            isinstance(imports, list) and imports,
            "Affected module lacks complete authoritative import paths",
        )
        for imported in imports:
            require(
                isinstance(imported, dict)
                and isinstance(imported.get("path"), str)
                and imported["path"]
                and not Path(imported["path"]).is_absolute()
                and ".." not in Path(imported["path"]).parts
                and "\\" not in imported["path"]
                and "//" not in imported["path"]
                and (
                    module == "stdlib"
                    or imported["path"] == module
                    or imported["path"].startswith(module + "/")
                ),
                "Affected import path is uncertain",
            )
            affected.add(imported["path"])
    require(
        affected and not affected.intersection(paths),
        "Affected/uncertain Go packages are present in the exact compiler graph; repair/rescan required",
    )
    return {
        **row,
        "disposition": "not-applicable",
        "reason": "Every official affected Go import path (conservatively across all advisory ranges) is absent from the exact native compiler dependency graph matching this executable.",
        "official_advisory": {
            "id": identifier,
            "sha256": entry["sha256"],
            "origin": entry["origin"],
            "modified": advisory["modified"],
        },
        "affected_packages": sorted(affected),
        "graph_sha256": graph["source_graph"]["sha256"],
        "binary_sha256": graph["binary"]["sha256"],
        "source_inputs": graph["source_inputs"],
    }


def aggregate_image_scans(
    binding: Binding,
    *,
    native_measurements: dict[str, Path],
    runtime_packs: dict[str, Path],
    scans: dict[str, Path],
    raw_scans: dict[str, Path],
    compiler_graphs: dict[str, Path],
    authenticator: MeasurementAuthenticator,
) -> dict:
    """Authenticate all exact facts; return unsigned gate bytes only if proven."""
    subjects = checked_binding(binding)
    native = aggregate_native_reports(binding, native_measurements, authenticator)[
        "final-image-smoke"
    ]["details"]["native_measurements"]
    fields(runtime_packs, set(PLATFORMS), "both native runtime packs")
    targets = {
        component + "-" + p.split("/")[1] for p in PLATFORMS for component in TARGETS
    }
    fields(scans, targets, "all four authenticated image scans")
    fields(raw_scans, targets, "all four raw image scan reports")
    require(
        isinstance(compiler_graphs, dict) and set(compiler_graphs) <= targets,
        "Unexpected compiler graph target",
    )
    reports = []
    for platform in PLATFORMS:
        context = NativeSourceContext(
            binding.repository, binding.version, binding.commit, platform
        )
        pack, runtime = runtime_inputs(context, runtime_packs[platform])
        require(
            runtime == native[platform]["runtime"],
            "Runtime pack/source bytes differ from authenticated native execution",
        )
        for component in TARGETS:
            target = component + "-" + platform.split("/")[1]
            image = native[platform]["images"][component]
            record, digest = load_authenticated(scans[target], binding, authenticator)
            raw = read_bounded_file(raw_scans[target])
            findings = checked_scan(
                record, raw, context=context, component=component, image=image
            )
            assessed, graph_digest = [], None
            if findings or target in compiler_graphs:
                require(
                    target in compiler_graphs,
                    "Affected image lacks authenticated exact compiler evidence",
                )
                graph_path = compiler_graphs[target]
                graph, graph_digest = load_authenticated(
                    graph_path, binding, authenticator
                )
                paths, modules = checked_graph(
                    graph,
                    graph_path.parent,
                    context=context,
                    component=component,
                    image=image,
                    runtime=runtime,
                    pack=pack,
                )
                assessed = [
                    derive_absence(row, graph, graph_path.parent, paths, modules)
                    for row in findings
                ]
            reports.append(
                {
                    "target": target,
                    "subject": subjects[target],
                    "status": "complete",
                    "exit_code": 0,
                    "scanner": record["scanner"]["name"],
                    "version": record["scanner"]["version"],
                    "database": record["database"]["sha256"],
                    "scanned_at": record["scanned_at"],
                    "findings": assessed,
                    "measurement_sha256": digest,
                    "raw_report_sha256": record["raw_report_sha256"],
                    "compiler_measurement_sha256": graph_digest,
                    "scanner_evidence": record["scanner"],
                    "database_evidence": record["database"],
                    "image": image,
                }
            )
    return {
        "schema_version": 1,
        "gate": "final-image-scanners",
        "binding_digest": binding.digest,
        "passed": True,
        "details": {
            "scans": reports,
            "derivation_policy": "exact-native-binary-compiler-graph-authoritative-Go-package-absence-v1",
        },
    }
