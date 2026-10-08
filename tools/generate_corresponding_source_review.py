"""Combine independently verified source inputs for exact final release images.

Individual application/backend/browser replays remain partial facts. The complete
producer also requires both authenticated runtime-source replays, every offered
asset, final notices and the committed policy. Publication still needs separate
distribution approval and delivery gates.
"""

from __future__ import annotations

import argparse
import gzip
import io
import os
from pathlib import Path
import tempfile

import browser_preferred_source_relationships as preferred
import backend_preferred_source_relationships as backend_preferred
import measure_browser_source_inventory as browser_inventory
import measure_native_browser_inputs as browser_inputs
import package_upstream_application_sources as upstream_inputs
import sqlite_vendoring
import final_image_notice_inventory as final_notices
from generate_distribution_review import committed_policy
from github_release_evidence import GhEvidenceVerifier

from aggregate_release_image_scans import checked_graph, load_authenticated, load_raw
from generate_release_gate_reports import (
    MeasurementAuthenticator,
    NativeSourceContext,
    aggregate_native_reports,
    checked_binding,
    runtime_inputs,
    source_asset_measurements,
    timestamp,
)
import verify_application_dependency_inputs as dependency_inputs
from publish_container_release import (
    Binding,
    SOURCE_NAME,
    SOURCE_COVERAGE,
    prepare_inputs,
    sha256,
    source_digest,
    source_review_details,
)
from release_artifacts import (
    COMMIT,
    InvalidRelease,
    create_output,
    DIGEST,
    PLATFORMS,
    VERSION,
    fields,
    git,
    matches,
    read_bounded_file,
    read_json,
    repository_name,
    json_bytes,
    require,
)

# Same bound as the native source scanner's Git snapshot. No extraction is needed.
MAX_APPLICATION_SOURCE_BYTES = 256 * 1024**2


def verify_runtime_source_records(
    binding: Binding,
    *,
    records: dict[str, Path],
    native: dict,
    runtime_packs: dict[str, Path],
    authenticator: MeasurementAuthenticator,
) -> tuple[dict, dict]:
    """Reuse authenticated substantive runtime replays, never caller approvals.

    The trusted native producer has already replayed original APK/Caddy/Go source
    and final OCI files. Bind those exact completed observations to the current
    source assets, pack, smoke execution and final images, without rerunning the
    source collector, signature tool or image build.
    """
    fields(records, set(PLATFORMS), "both authenticated runtime-source records")
    fields(runtime_packs, set(PLATFORMS), "both complete runtime packs")
    result, snapshots = {}, {}
    for platform in PLATFORMS:
        record, digest = load_authenticated(records[platform], binding, authenticator)
        fields(
            record,
            {
                "schema_version",
                "kind",
                "verified_at",
                "repository",
                "version",
                "revision",
                "platform",
                "runtime_source_asset_sha256",
                "runtime_pack_sha256",
                "native_smoke_report_sha256",
                "images",
                "coverage",
                "caddy_signature_verification",
                "runtime_source_inputs_verified",
                "application_source_verified",
                "apk_binary_signatures_verified",
                "source_publication_verified",
                "distribution_authorized",
            },
            "completed runtime-source replay",
        )
        context = NativeSourceContext(
            binding.repository, binding.version, binding.commit, platform
        )
        _, runtime = runtime_inputs(context, runtime_packs[platform])
        measurement = native[platform]
        require(
            type(record["schema_version"]) is int
            and record["schema_version"] == 1
            and record["kind"] == "runtime-source-completeness"
            and record["repository"] == binding.repository
            and record["version"] == binding.version
            and record["revision"] == binding.commit
            and record["platform"] == platform
            and runtime == measurement["runtime"]
            and record["runtime_source_asset_sha256"]
            == runtime["source_asset"]["digest"]
            and record["runtime_pack_sha256"] == runtime["runtime_pack_sha256"]
            and record["native_smoke_report_sha256"]
            == sha256(json_bytes(measurement["smoke"]))
            and record["images"] == measurement["images"],
            "Runtime-source replay differs from exact authenticated native inputs",
        )
        require(
            record["runtime_source_inputs_verified"] is True
            and all(
                record[key] is False
                for key in (
                    "application_source_verified",
                    "apk_binary_signatures_verified",
                    "source_publication_verified",
                    "distribution_authorized",
                )
            ),
            "Runtime-source replay is incomplete or claims unrelated approval",
        )
        timestamp(record["verified_at"])
        coverage = fields(
            record["coverage"],
            {
                "backend_origins",
                "web_origins",
                "retained_package_versions",
                "go_runtime",
            },
            "completed runtime source coverage",
        )
        require(
            all(
                type(coverage[key]) is int and coverage[key] > 0
                for key in (
                    "backend_origins",
                    "web_origins",
                    "retained_package_versions",
                )
            ),
            "Runtime source/package coverage is missing",
        )
        go = fields(
            coverage["go_runtime"],
            {"version", "commit", "archive_sha256", "executables"},
            "retained original Go runtime",
        )
        matches(go["commit"], COMMIT, "Original Go source revision missing")
        matches(go["archive_sha256"], DIGEST, "Original Go archive digest missing")
        require(
            isinstance(go["version"], str)
            and go["version"].startswith("go")
            and go["executables"]
            == {component: go["version"] for component in ("backend", "web")}
            and isinstance(record["caddy_signature_verification"], dict)
            and record["caddy_signature_verification"].get(
                "legacy_sigstore_signatures_verified"
            )
            is True,
            "Runtime replay lacks exact executable/source or Caddy signature coverage",
        )
        result[platform] = {"measurement_sha256": digest, "replay": record}
        snapshots[records[platform]] = digest
        snapshots[runtime_packs[platform] / "runtime-pack.json"] = runtime[
            "runtime_pack_sha256"
        ]
    return result, snapshots


def corresponding_source_report(
    binding: Binding,
    *,
    root: Path,
    source_assets: dict[str, Path],
    native_measurements: dict[str, Path],
    runtime_packs: dict[str, Path],
    runtime_source_records: dict[str, Path],
    dependency_collections: dict[str, Path],
    source_scans: dict[str, Path],
    compiler_graphs: dict[str, Path],
    upstream_collection: Path,
    browser_measurements: dict[str, Path],
    captures: dict[str, Path],
    archives: dict[str, dict[str, Path]],
    authenticator: MeasurementAuthenticator,
) -> tuple[dict, dict]:
    """Return complete gate bytes and replay evidence; perform no publication.

    Invoke the substantive replays here rather than accepting caller-supplied
    completion flags. Keep canonical evidence behind each coverage digest for
    hosted retention/attestation. Runtime records come from the existing trusted
    original-source replay producer; both architectures remain mandatory.
    """
    subjects = checked_binding(binding)
    fields(archives, set(PLATFORMS), "both final native image archives")
    for pair in archives.values():
        fields(pair, {"backend", "web"}, "complete final native image pair")
    assets = source_asset_measurements(binding, source_assets)
    _, policy = committed_policy(root, binding)
    native = aggregate_native_reports(binding, native_measurements, authenticator)[
        "final-image-smoke"
    ]["details"]["native_measurements"]
    runtime, snapshots = verify_runtime_source_records(
        binding,
        records=runtime_source_records,
        native=native,
        runtime_packs=runtime_packs,
        authenticator=authenticator,
    )
    for values in (source_scans, compiler_graphs, browser_measurements):
        snapshots.update({path: source_digest(path) for path in values.values()})
    snapshots[upstream_collection / upstream_inputs.RECORD] = source_digest(
        upstream_collection / upstream_inputs.RECORD
    )
    snapshots.update(
        {
            collection
            / "dependency-collection.json": source_digest(
                collection / "dependency-collection.json"
            )
            for collection in dependency_collections.values()
        }
    )
    application_name = f"psst.zip-source-{binding.version}.tar.gz"
    require(
        application_name in source_assets, "Committed application source asset missing"
    )
    application = verify_application_source_archive(
        binding, root=root, source=source_assets[application_name]
    )
    backend = verify_backend_source_inputs(
        binding,
        root=root,
        native_measurements=native_measurements,
        runtime_packs=runtime_packs,
        dependency_collections=dependency_collections,
        source_scans=source_scans,
        compiler_graphs=compiler_graphs,
        upstream_collection=upstream_collection,
        authenticator=authenticator,
    )
    browser = verify_browser_source_inputs(
        binding,
        root=root,
        native_measurements=native_measurements,
        runtime_packs=runtime_packs,
        browser_measurements=browser_measurements,
        captures=captures,
        web_archives={p: pair["web"] for p, pair in archives.items()},
        upstream_collection=upstream_collection,
        authenticator=authenticator,
    )
    require(
        backend["binding_digest"] == browser["binding_digest"] == binding.digest
        and backend["upstream_inputs"] == browser["upstream_inputs"],
        "Backend/browser source offerings differ",
    )
    fields(
        backend["images"],
        {"backend-amd64", "backend-arm64"},
        "both backend source replays",
    )
    fields(browser["images"], {"web-amd64", "web-arm64"}, "both browser source replays")
    covered = {application_name, backend["upstream_inputs"]["asset"]["name"]}
    evidence, coverage, images = {}, {}, {}

    def retain(name, value):
        evidence[name] = value
        return {"status": "complete", "evidence_digest": sha256(json_bytes(value))}

    app_coverage = retain("application", application)
    for platform in PLATFORMS:
        arch = platform.split("/")[1]
        pack, runtime_input = runtime_inputs(
            NativeSourceContext(
                binding.repository, binding.version, binding.commit, platform
            ),
            runtime_packs[platform],
        )
        require(
            runtime_input == native[platform]["runtime"],
            "Runtime pack changed during complete source replay",
        )
        covered.add(runtime_input["source_asset"]["name"])
        runtime_coverage = retain("runtime-" + arch, runtime[platform])
        for component, partial in (("backend", backend), ("web", browser)):
            target = component + "-" + arch
            row = partial["images"][target]
            require(
                row["subject"] == subjects[target]
                and row["image"] == native[platform]["images"][component],
                "Source replay covers another final image",
            )
            coverage[target] = {
                "application": app_coverage,
                "runtime": runtime_coverage,
            }
            if component == "backend":
                covered.add(row["dependency_asset"]["name"])
                snapshots[
                    dependency_collections[platform] / "dependency-collection.json"
                ] = row["dependency_replay"]["collection_sha256"]
                coverage[target]["backend-modules"] = retain(target + "-modules", row)
                notices = final_notices.verify_backend_notices(
                    NativeSourceContext(
                        binding.repository, binding.version, binding.commit, platform
                    ),
                    archives[platform]["backend"],
                    image=row["image"],
                    pack=pack,
                    root=root,
                )
                require(
                    notices["files"]["dependency-inventory.json"]["sha256"]
                    == row["dependency_replay"]["backend_notice_sources"][
                        "inventory_sha256"
                    ],
                    "Final dependency notices differ from independently verified upstream notice sources",
                )
            else:
                coverage[target]["browser-packages"] = retain(
                    target + "-packages",
                    {
                        key: row[key]
                        for key in (
                            "subject",
                            "image",
                            "browser_measurement_sha256",
                            "browser_inputs_sha256",
                            "preferred_sources",
                            "source_associations",
                        )
                    },
                )
                coverage[target]["browser-generators"] = retain(
                    target + "-generators",
                    {
                        key: row[key]
                        for key in (
                            "subject",
                            "image",
                            "compiler_sources",
                            "generator_sources",
                        )
                    },
                )
                files = row["notice_files"]
                require(
                    isinstance(files, dict)
                    and {
                        "AGPL-3.0-only.txt",
                        "THIRD_PARTY_NOTICES.txt",
                        "dependency-inventory.json",
                        "release.json",
                        "backend/AGPL-3.0-only.txt",
                        "runtime/THIRD_PARTY_NOTICES.txt",
                        "runtime/SOURCE.txt",
                        "runtime/runtime-inventory.json",
                    }
                    <= set(files),
                    "Final web notice inventory is incomplete",
                )
                notices = {
                    "files": files,
                    "notice_inventory_digest": sha256(json_bytes(files)),
                }
                snapshots[captures[platform]] = row["browser_inputs_sha256"]
            evidence[target + "-notices"] = notices
            images[target] = {
                "subject": subjects[target],
                "notice_inventory_digest": notices["notice_inventory_digest"],
            }
            snapshots[archives[platform][component]] = row["image"]["archive_digest"]
        snapshots[native_measurements[platform]] = native[platform][
            "measurement_digest"
        ]
    require(
        covered == set(source_assets),
        "An offered source asset lacks substantive source coverage",
    )
    require(
        source_digest(upstream_collection / upstream_inputs.RECORD)
        == backend["upstream_inputs"]["collection_sha256"],
        "Upstream offering record changed during complete source replay",
    )
    require(
        source_asset_measurements(binding, source_assets) == assets,
        "Source assets changed during complete replay",
    )
    require(
        committed_policy(root, binding)[1] == policy,
        "Committed distribution policy changed during replay",
    )
    for path, digest in snapshots.items():
        require(
            source_digest(path) == digest,
            "Complete source evidence changed during replay",
        )
    details = {
        "schema_version": 1,
        "source_subjects": {
            key: value for key, value in subjects.items() if key.startswith("source:")
        },
        "images": images,
        "policy": policy,
        "coverage": coverage,
    }
    source_review_details(details, binding, distribution=False)
    return {
        "schema_version": 1,
        "gate": "corresponding-source",
        "binding_digest": binding.digest,
        "passed": True,
        "details": details,
    }, {
        "schema_version": 1,
        "kind": "complete-corresponding-source-replay-evidence",
        "binding_digest": binding.digest,
        "source_assets": assets,
        "coverage_evidence": evidence,
        "distribution_review_required": True,
        "byte_reproduction_verified": False,
        "publication_authorized": False,
    }


def verify_backend_source_inputs(
    binding: Binding,
    *,
    root: Path,
    native_measurements: dict[str, Path],
    runtime_packs: dict[str, Path],
    dependency_collections: dict[str, Path],
    source_scans: dict[str, Path],
    compiler_graphs: dict[str, Path],
    upstream_collection: Path,
    authenticator: MeasurementAuthenticator,
) -> dict:
    """Bind retained H1-verified module inputs to both actual native backend binaries.

    This replays existing compiler evidence, never rebuilds an image or rescans it.
    Preferred-form upstream source and the remaining release categories still
    require independent review before a corresponding-source gate can pass.
    """
    subjects = checked_binding(binding)
    native = aggregate_native_reports(binding, native_measurements, authenticator)[
        "final-image-smoke"
    ]["details"]["native_measurements"]
    for values, label in (
        (runtime_packs, "both native runtime packs"),
        (dependency_collections, "both dependency collections"),
        (source_scans, "both authenticated source scans"),
    ):
        fields(values, set(PLATFORMS), label)
    fields(compiler_graphs, {"backend-amd64", "backend-arm64"}, "both backend graphs")
    # Authenticate the complete small observation set before reading large originals.
    authenticated_sources = {
        platform: load_authenticated(source_scans[platform], binding, authenticator)
        for platform in PLATFORMS
    }
    authenticated_graphs = {
        target: load_authenticated(path, binding, authenticator)
        for target, path in compiler_graphs.items()
    }
    upstream, originals = upstream_inputs.verify_source_files(
        root,
        binding.repository,
        binding.version,
        binding.commit,
        upstream_collection,
        component="backend",
    )
    upstream_asset = upstream["asset"]
    require(
        subjects.get("source:" + upstream_asset["name"])
        == "file:" + upstream_asset["name"] + "@" + upstream_asset["digest"],
        "Backend upstream offering differs from exact publication binding",
    )
    application_digest = sha256(git(root, "archive", binding.commit))
    reports = {}
    snapshots = [
        (upstream_collection / upstream_inputs.RECORD, upstream["collection_sha256"]),
        (upstream_collection / upstream_asset["name"], upstream_asset["digest"]),
    ]
    comparator_source = git(root, "show", binding.commit + ":tools/sqlite_vendoring.go")
    preferred_cache = {}
    with sqlite_vendoring.build_verifier(comparator_source) as vendoring_verifier:
        for platform in PLATFORMS:
            context = NativeSourceContext(
                binding.repository, binding.version, binding.commit, platform
            )
            target = "backend-" + platform.split("/")[1]
            pack, runtime = runtime_inputs(context, runtime_packs[platform])
            require(
                runtime == native[platform]["runtime"],
                "Runtime pack/source bytes differ from authenticated native execution",
            )
            source, source_digest_value = authenticated_sources[platform]
            require(
                source.get("source") == context.checked(),
                "Backend source scan belongs to another native source context",
            )
            graph_path = compiler_graphs[target]
            graph, graph_digest = authenticated_graphs[target]
            checked_graph(
                graph,
                graph_path.parent,
                context=context,
                component="backend",
                image=native[platform]["images"]["backend"],
                runtime=runtime,
                pack=pack,
            )
            require(
                graph["source_inputs"]["application_archive_sha256"]
                == application_digest,
                "Backend compiler source archive differs from selected committed Git source",
            )
            replay, module_originals = dependency_inputs.verify_module_source_files(
                context,
                root,
                dependency_collections[platform],
                source_scans[platform],
                modules=frozenset(
                    {
                        "modernc.org/sqlite",
                        "modernc.org/libc",
                        "modernc.org/cc/v4",
                        "modernc.org/ccgo/v4",
                        "modernc.org/fileutil",
                    }
                ),
            )
            require(
                replay.get("source") == context.checked()
                and replay.get("package_inputs_replayed") is True
                and replay.get("backend_notice_sources_verified") is True
                and replay.get("source_measurement_sha256") == source_digest_value,
                "Dependency inputs differ from authenticated native source scan",
            )
            notice_sources = fields(
                replay.get("backend_notice_sources"),
                {"inventory_sha256", "generator_sha256", "modules"},
                "independently replayed upstream module notices",
            )
            for key in ("inventory_sha256", "generator_sha256"):
                matches(
                    notice_sources[key],
                    DIGEST,
                    "Upstream notice evidence digest missing",
                )
            asset_name = f"psst.zip-dependency-inputs-{binding.version}-{platform.split('/')[1]}.tar.gz"
            archive_digest = matches(
                replay.get("archive_sha256"), DIGEST, "Dependency asset digest missing"
            )
            require(
                subjects.get("source:" + asset_name)
                == "file:" + asset_name + "@" + archive_digest,
                "Backend dependency asset differs from exact publication binding",
            )
            retained = replay.get("go_module_inputs")
            require(
                isinstance(retained, list)
                and len(retained) == replay.get("go_modules"),
                "Verified Go module input inventory missing",
            )
            modules = {}
            for row in retained:
                fields(
                    row, {"module", "version", "sum", "zip_sha256"}, "verified module"
                )
                require(
                    isinstance(row["module"], str)
                    and row["module"]
                    and row["module"] not in modules,
                    "Duplicate or malformed verified module input",
                )
                matches(
                    row["zip_sha256"], DIGEST, "Verified module archive digest missing"
                )
                modules[row["module"]] = row
            dependencies = []
            for dep in graph["binary"]["build_info"]["Deps"]:
                retained_dep = modules.get(dep["Path"])
                require(
                    retained_dep is not None
                    and retained_dep["version"] == dep["Version"]
                    and retained_dep["sum"] == dep["Sum"],
                    "Actual backend module version/checksum lacks retained verified source inputs",
                )
                dependencies.append(retained_dep)
            # Both architectures may share source inputs. H1 replay still occurs on
            # each actual native collection; reuse only identical origin/ZIP facts.
            preferred_key = json_bytes(
                {
                    module: {"record": value["record"], "origin": value.get("origin")}
                    for module, value in sorted(module_originals.items())
                }
            )
            if preferred_key not in preferred_cache:
                preferred_cache[preferred_key] = backend_preferred.verify_relationships(
                    originals, module_originals, vendoring_verifier=vendoring_verifier
                )
            preferred_sources = preferred_cache[preferred_key]
            reports[target] = {
                "subject": subjects[target],
                "image": native[platform]["images"]["backend"],
                "binary_sha256": graph["binary"]["sha256"],
                "rebuilt_sha256": graph["binary"]["rebuilt_sha256"],
                "compiler_measurement_sha256": graph_digest,
                "application_archive_sha256": application_digest,
                "dependency_asset": {"name": asset_name, "sha256": archive_digest},
                "dependency_replay": replay,
                "binary_module_inputs": dependencies,
                "preferred_sources": preferred_sources,
            }
            snapshots.extend(
                [
                    (
                        native_measurements[platform],
                        native[platform]["measurement_digest"],
                    ),
                    (source_scans[platform], source_digest_value),
                    (graph_path, graph_digest),
                    (
                        dependency_collections[platform] / "dependency-collection.json",
                        replay["collection_sha256"],
                    ),
                    (dependency_collections[platform] / asset_name, archive_digest),
                ]
            )
            for scan in source["scans"]:
                name = (
                    "go-modules.json"
                    if scan["target"] == "backend-source"
                    else "npm-lock-graph.json"
                )
                raw = dependency_inputs.scanner_raw(source_scans[platform], scan, name)
                snapshots.append(
                    (source_scans[platform].parent / scan["target"] / name, sha256(raw))
                )
    for path, digest in snapshots:
        require(
            source_digest(path) == digest, "Backend source input changed during replay"
        )
    for platform in PLATFORMS:
        context = NativeSourceContext(
            binding.repository, binding.version, binding.commit, platform
        )
        require(
            runtime_inputs(context, runtime_packs[platform])[1]
            == native[platform]["runtime"],
            "Runtime input changed during backend source replay",
        )
        graph_path = compiler_graphs["backend-" + platform.split("/")[1]]
        graph_raw = read_bounded_file(graph_path)
        require(
            sha256(graph_raw)
            == reports["backend-" + platform.split("/")[1]][
                "compiler_measurement_sha256"
            ],
            "Compiler measurement changed during replay",
        )
        graph = read_json(graph_raw)
        load_raw(
            graph_path.parent,
            graph["raw_files"],
            graph["source_graph"]["raw_file"],
            graph["source_graph"]["sha256"],
        )
    return {
        "schema_version": 1,
        "kind": "native-backend-source-input-replay",
        "binding_digest": binding.digest,
        "images": reports,
        "backend_source_inputs_verified": True,
        "upstream_inputs": upstream,
        "vendoring_verifier_source_sha256": sha256(comparator_source),
        "preferred_source_review_required": True,
        "corresponding_source_completeness_verified": False,
        "publication_authorized": False,
    }


def verify_browser_source_inputs(
    binding: Binding,
    *,
    root: Path,
    native_measurements: dict[str, Path],
    runtime_packs: dict[str, Path],
    browser_measurements: dict[str, Path],
    captures: dict[str, Path],
    web_archives: dict[str, Path],
    upstream_collection: Path,
    authenticator: MeasurementAuthenticator,
) -> dict:
    """Replay authenticated browser inputs against both final native OCI images.

    Reuse the retained build, Git, npm and OCI evidence; never rebuild or fetch.
    Replay the common offering once, then map each image's rendered package and
    compiler and generator inputs. Source associations do not prove byte regeneration.
    """
    subjects = checked_binding(binding)
    for values, label in (
        (runtime_packs, "both browser runtime packs"),
        (browser_measurements, "both authenticated browser measurements"),
        (captures, "both browser captures"),
        (web_archives, "both final web OCI archives"),
    ):
        fields(values, set(PLATFORMS), label)
    native = aggregate_native_reports(binding, native_measurements, authenticator)[
        "final-image-smoke"
    ]["details"]["native_measurements"]
    observations, snapshots = {}, []
    # Authenticate both reports before reading potentially large source archives.
    for platform in PLATFORMS:
        value, digest = load_authenticated(
            browser_measurements[platform], binding, authenticator
        )
        context = NativeSourceContext(
            binding.repository, binding.version, binding.commit, platform
        )
        require(
            value.get("kind") == "native-browser-input-measurement"
            and value.get("source") == context.checked()
            and value.get("image") == native[platform]["images"]["web"]
            and value.get("tested_web_config")
            == native[platform]["smoke"]["tested_configs"]["web"],
            "Browser observation differs from authenticated final image/source",
        )
        matches(value.get("builder_config"), DIGEST, "Missing browser builder config")
        observations[platform] = (value, digest)
        snapshots.extend(
            [
                (browser_measurements[platform], digest),
                (native_measurements[platform], native[platform]["measurement_digest"]),
            ]
        )
    upstream, originals = upstream_inputs.verify_source_files(
        root, binding.repository, binding.version, binding.commit, upstream_collection
    )
    asset = upstream["asset"]
    require(
        subjects.get("source:" + asset["name"])
        == "file:" + asset["name"] + "@" + asset["digest"],
        "Browser preferred source offering differs from publication binding",
    )
    snapshots.extend(
        [
            (upstream_collection / asset["name"], asset["digest"]),
            (
                upstream_collection / upstream_inputs.RECORD,
                upstream["collection_sha256"],
            ),
        ]
    )
    reports = {}
    for platform in PLATFORMS:
        context = NativeSourceContext(
            binding.repository, binding.version, binding.commit, platform
        )
        measured, digest = observations[platform]
        pack, runtime = runtime_inputs(context, runtime_packs[platform])
        require(
            runtime == native[platform]["runtime"],
            "Browser runtime inputs differ from authenticated native execution",
        )
        replayed = browser_inputs.replay(
            context,
            captures[platform],
            web_archives[platform],
            measured["tested_web_config"],
            pack,
            source_root=root,
        )
        require(
            measured == {**replayed, "builder_config": measured["builder_config"]},
            "Browser observation differs from independent Git/npm/OCI replay",
        )
        require(
            replayed["source_associations"]["unresolved_javascript"] == [],
            "Unattributed final JavaScript output lacks a preferred source association",
        )
        with tempfile.TemporaryDirectory(prefix="psst-browser-preferred-") as temporary:
            tree = Path(temporary)
            # Small capture extraction only; do not repeat Git/npm/OCI replay.
            browser_inputs.unpack(captures[platform], tree)
            inventory = browser_inventory.json_record(
                browser_inventory.read_file(tree / "build", browser_inventory.INVENTORY)
            )
            mappings = preferred.verify_preferred_relationships(
                inventory, tree, replayed["npm_archives"], originals
            )
            plan = browser_inputs.recipe_plan(tree, inventory)
            compiler = preferred.verify_captured_compiler_inputs(
                tree, replayed["npm_archives"], originals, plan["recipes"]
            )
            generators = preferred.verify_generator_relationships(
                inventory,
                tree,
                replayed["npm_archives"],
                originals,
                plan,
                mappings,
                replayed["git_inputs"],
            )
        target = "web-" + platform.split("/")[1]
        reports[target] = {
            "subject": subjects[target],
            "image": replayed["image"],
            "browser_measurement_sha256": digest,
            "browser_inputs_sha256": replayed["browser_inputs_sha256"],
            "preferred_sources": mappings,
            "compiler_sources": compiler,
            "generator_sources": generators,
            "source_associations": replayed["source_associations"],
            "notice_files": {
                path.removeprefix("licenses/"): fact
                for path, fact in replayed["final_static_files"].items()
                if path.startswith("licenses/")
            },
        }
        snapshots.extend(
            [
                (captures[platform], replayed["browser_inputs_sha256"]),
                (web_archives[platform], replayed["image"]["archive_digest"]),
            ]
        )
    for path, digest in snapshots:
        require(
            source_digest(path) == digest, "Browser source input changed during replay"
        )
    for platform in PLATFORMS:
        context = NativeSourceContext(
            binding.repository, binding.version, binding.commit, platform
        )
        require(
            runtime_inputs(context, runtime_packs[platform])[1]
            == native[platform]["runtime"],
            "Runtime input changed during browser source replay",
        )
    return {
        "schema_version": 1,
        "kind": "native-browser-preferred-source-replay",
        "binding_digest": binding.digest,
        "upstream_inputs": upstream,
        "images": reports,
        "browser_source_inputs_verified": True,
        "generated_source_associations_verified": True,
        "byte_reproduction_verified": False,
        "corresponding_source_completeness_verified": False,
        "publication_authorized": False,
    }


def verify_application_source_archive(
    binding: Binding, *, root: Path, source: Path
) -> dict:
    """Match retained bytes to the selected committed archive, ignoring worktree edits."""
    repository_name(binding.repository)
    matches(binding.version, VERSION, "Invalid application source version")
    matches(binding.commit, COMMIT, "Invalid application source commit")
    name = f"psst.zip-source-{binding.version}.tar.gz"
    require(source.name == name, "Application source asset was renamed")
    require(
        source.is_file()
        and not any(path.is_symlink() for path in (source, *source.parents)),
        "Application source asset must be a regular file",
    )
    require(
        0 < source.stat().st_size <= MAX_APPLICATION_SOURCE_BYTES,
        "Application source asset exceeds bounds",
    )
    with source.open("rb") as stream:
        retained = stream.read(MAX_APPLICATION_SOURCE_BYTES + 1)
    require(
        0 < len(retained) <= MAX_APPLICATION_SOURCE_BYTES,
        "Application source asset exceeds bounds",
    )
    digest = sha256(retained)
    subject = "file:" + name + "@" + digest
    require(
        len(dict(binding.subjects)) == len(binding.subjects)
        and dict(binding.subjects).get("source:" + name) == subject,
        "Application source asset differs from exact publication binding",
    )
    # Replay the recipe in prepare_release_inputs, using Git object bytes rather
    # than checked-out files. Comparing the complete canonical tar also covers
    # commit metadata, paths, file modes, archive attributes and omitted files.
    archive = git(
        root,
        "archive",
        "--format=tar",
        f"--prefix=psst.zip-{binding.version}/",
        binding.commit,
    )
    require(
        0 < len(archive) <= MAX_APPLICATION_SOURCE_BYTES,
        "Canonical application source archive exceeds bounds",
    )
    timestamp = int(git(root, "show", "-s", "--format=%ct", binding.commit))
    buffer = io.BytesIO()
    with gzip.GzipFile(
        fileobj=buffer, mode="wb", filename="", mtime=timestamp
    ) as zipped:
        zipped.write(archive)
    require(
        retained == buffer.getvalue(),
        "Application source archive differs from selected committed Git source",
    )
    require(source_digest(source) == digest, "Application source changed during replay")
    return {
        "schema_version": 1,
        "kind": "committed-application-source-replay",
        "source": {
            "repository": binding.repository,
            "version": binding.version,
            "commit": binding.commit,
        },
        "asset": {
            "name": name,
            "sha256": digest,
            "size": len(retained),
            "subject": subject,
        },
        "git_archive_sha256": sha256(archive),
        "source_commit_archive_verified": True,
        "corresponding_source_completeness_verified": False,
        "publication_authorized": False,
    }


PREPARED_RECORD_FIELDS = {
    "schema_version",
    "kind",
    "source_kind",
    "tagged_source_ci_gate_verified",
    "signer_identity_verified",
    "repository",
    "version",
    "source_commit",
    "binding_sha256",
    "assets",
    "subjects",
    "dependency_replays",
    "upstream_replay",
    "publication_authorized",
    "measurement_authentication_required",
}
MAX_COMMAND_OUTPUT = 16 * 1024**2


def command_inputs(
    *, root: Path, prepared: Path, repository: str, version: str, commit: str
):
    """Bind only tagged, exact prepared files; preparation flags confer no trust."""
    browser_inventory.root_directory(root)
    browser_inventory.root_directory(prepared)
    record_path = prepared / "release-inputs.json"
    record_raw = read_bounded_file(record_path)
    record = fields(
        read_json(record_raw), PREPARED_RECORD_FIELDS, "prepared release record"
    )
    require(
        type(record["schema_version"]) is int
        and record["schema_version"] == 1
        and record["kind"] == "prepared-release-inputs"
        and record["source_kind"] == "version-tag"
        and record["repository"] == repository
        and record["version"] == version
        and record["source_commit"] == commit
        and record["tagged_source_ci_gate_verified"] is False
        and record["signer_identity_verified"] is False
        and record["publication_authorized"] is False
        and record["measurement_authentication_required"] is True
        and isinstance(record["dependency_replays"], dict)
        and set(record["dependency_replays"]) == set(PLATFORMS)
        and isinstance(record["upstream_replay"], dict),
        "Source command requires exact tagged release preparation",
    )
    manifest_path = prepared / "release-manifest.json"
    manifest_raw = read_bounded_file(manifest_path)
    manifest = read_json(manifest_raw)
    require(
        isinstance(manifest, dict) and isinstance(manifest.get("bundle"), dict),
        "Prepared manifest bundle is missing",
    )
    bundle_name = matches(
        manifest["bundle"].get("name"), SOURCE_NAME, "Invalid prepared bundle name"
    )
    assets = record["assets"]
    require(
        isinstance(assets, dict) and 2 < len(assets) <= 64,
        "Prepared source assets are missing or oversized",
    )
    for name, checksum in assets.items():
        matches(name, SOURCE_NAME, "Unsafe prepared asset name")
        matches(checksum, DIGEST, "Invalid prepared asset hash")
    require(
        {"release-manifest.json", bundle_name} <= assets.keys(),
        "Prepared manifest or bundle asset is missing",
    )
    expected_names = set(assets) | {
        "release-inputs.json",
        "oci-correspondence.json",
        "backend-index.json",
        "web-index.json",
    }
    actual_names = set()
    for path in prepared.iterdir():
        require(
            path.is_file() and not path.is_symlink(),
            "Prepared release contains a linked or nested input",
        )
        actual_names.add(path.name)
        require(len(actual_names) <= 68, "Prepared release directory exceeds bounds")
    require(
        actual_names == expected_names, "Prepared release file set differs from record"
    )
    sources = {
        name: prepared / name
        for name in assets
        if name not in {"release-manifest.json", bundle_name}
    }
    indexes = {
        component: prepared / (component + "-index.json")
        for component in ("backend", "web")
    }
    inputs = prepare_inputs(
        root=root,
        repository=repository,
        ref="refs/tags/" + version,
        event_sha=commit,
        reviewed_commit=commit,
        manifest_path=manifest_path,
        bundle=prepared / bundle_name,
        indexes=indexes,
        source_assets=sources,
    )
    require(
        inputs.binding.repository == repository
        and inputs.binding.version == version
        and inputs.binding.commit == commit
        and record["binding_sha256"] == inputs.binding.digest
        and record["subjects"] == dict(inputs.binding.subjects)
        and assets == dict(inputs.assets),
        "Prepared record differs from measured tagged release binding",
    )
    # Large source payloads are rechecked by the substantive producer. Retain
    # only small assembly metadata/bundle snapshots here, avoiding another full
    # dependency/source archive hash pass after its independent replay.
    snapshots = {
        path: source_digest(path)
        for path in (
            manifest_path,
            prepared / bundle_name,
            *indexes.values(),
            prepared / "oci-correspondence.json",
        )
    }
    require(
        read_bounded_file(manifest_path) == manifest_raw
        and read_bounded_file(record_path) == record_raw,
        "Prepared metadata changed during binding",
    )
    return inputs.binding, sources, record_raw, expected_names, snapshots


def verify_command_output(
    output: Path,
    binding: Binding,
    authenticator: GhEvidenceVerifier,
    sources: dict[str, Path],
) -> tuple[dict, dict]:
    """Authenticate already produced bytes without repeating substantive replays."""
    browser_inventory.root_directory(output)
    names = {"corresponding-source.json", "corresponding-source-evidence.json"}
    require(
        {path.name for path in output.iterdir()} == names
        and all(path.is_file() and not path.is_symlink() for path in output.iterdir()),
        "Source verification requires exactly two regular output files",
    )
    report_path, evidence_path = (
        output / "corresponding-source.json",
        output / "corresponding-source-evidence.json",
    )
    report_raw, evidence_raw = read_bounded_file(report_path), read_bounded_file(
        evidence_path
    )
    require(
        0 < len(report_raw) + len(evidence_raw) <= MAX_COMMAND_OUTPUT,
        "Source verification output exceeds bounds",
    )
    receipt = authenticator.verify("corresponding-source", report_path, binding)
    require(
        receipt.gate == "corresponding-source"
        and receipt.binding_digest == binding.digest
        and receipt.report_digest == sha256(report_raw)
        and receipt.passed is True,
        "Authenticated source report receipt differs",
    )
    report = fields(
        read_json(report_raw),
        {"schema_version", "gate", "binding_digest", "passed", "details"},
        "authenticated source command report",
    )
    require(
        type(report["schema_version"]) is int
        and report["schema_version"] == 1
        and report["gate"] == receipt.gate
        and report["binding_digest"] == receipt.binding_digest
        and report["passed"] is True
        and report["details"] == receipt.details,
        "Source report content differs from authenticated receipt",
    )
    source_review_details(receipt.details, binding, distribution=False)
    authenticator.authenticate(evidence_raw, binding)
    evidence = fields(
        read_json(evidence_raw),
        {
            "schema_version",
            "kind",
            "binding_digest",
            "source_assets",
            "coverage_evidence",
            "distribution_review_required",
            "byte_reproduction_verified",
            "publication_authorized",
        },
        "authenticated corresponding-source replay evidence",
    )
    require(
        type(evidence["schema_version"]) is int
        and evidence["schema_version"] == 1
        and evidence["kind"] == "complete-corresponding-source-replay-evidence"
        and evidence["binding_digest"] == binding.digest
        and evidence["distribution_review_required"] is True
        and evidence["byte_reproduction_verified"] is False
        and evidence["publication_authorized"] is False,
        "Source replay evidence identity or approval boundary differs",
    )
    assets = fields(
        evidence["source_assets"],
        {
            "schema_version",
            "kind",
            "binding_digest",
            "assets",
            "corresponding_source_completeness_verified",
            "distribution_review_required",
            "publication_authorized",
        },
        "source evidence asset inventory",
    )
    require(
        type(assets["schema_version"]) is int
        and assets["schema_version"] == 1
        and assets["kind"] == "release-source-asset-measurements"
        and assets["binding_digest"] == binding.digest
        and assets["corresponding_source_completeness_verified"] is False
        and assets["distribution_review_required"] is True
        and assets["publication_authorized"] is False,
        "Source evidence asset boundary differs",
    )
    rows = fields(assets["assets"], set(sources), "all source evidence assets")
    subjects = dict(binding.subjects)
    for name, row in rows.items():
        fields(row, {"digest", "size"}, "source evidence asset")
        require(
            type(row["size"]) is int
            and row["size"] > 0
            and row["size"] == sources[name].stat().st_size
            and subjects.get("source:" + name)
            == "file:" + name + "@" + str(row["digest"]),
            "Source replay evidence asset differs from tagged binding",
        )
    retained = evidence["coverage_evidence"]
    require(
        isinstance(retained, dict)
        and 0 < len(retained) <= 32
        and all(
            isinstance(name, str) and isinstance(value, dict)
            for name, value in retained.items()
        ),
        "Source coverage evidence is missing or oversized",
    )
    hashes = {sha256(json_bytes(value)) for value in retained.values()}
    for target in SOURCE_COVERAGE:
        for row in receipt.details["coverage"][target].values():
            require(
                row["evidence_digest"] in hashes,
                "Authenticated report coverage lacks exact retained evidence",
            )
        notices = retained.get(target + "-notices")
        require(
            isinstance(notices, dict)
            and isinstance(notices.get("files"), dict)
            and bool(notices["files"])
            and notices.get("notice_inventory_digest")
            == sha256(json_bytes(notices["files"]))
            == receipt.details["images"][target]["notice_inventory_digest"],
            "Authenticated report notices lack exact retained inventory",
        )
    require(
        report_raw == json_bytes(report) and evidence_raw == json_bytes(evidence),
        "Source command outputs must retain canonical producer bytes",
    )
    require(
        read_bounded_file(report_path) == report_raw
        and read_bounded_file(evidence_path) == evidence_raw
        and {path.name for path in output.iterdir()} == names,
        "Source outputs changed during authentication",
    )
    return report, evidence


def run_command(args) -> tuple[dict, dict]:
    """Produce new local gate/evidence files; never sign, publish or rebuild."""
    repository_name(args.repository)
    matches(args.version, VERSION, "Invalid source command version")
    matches(args.commit, COMMIT, "Invalid source command commit")
    require(
        type(args.run_id) is int
        and args.run_id > 0
        and type(args.run_attempt) is int
        and args.run_attempt > 0,
        "Source command requires a positive workflow run ID and attempt",
    )
    browser_inventory.root_directory(args.output.parent)
    verify_only = getattr(args, "verify_only", False)
    require(type(verify_only) is bool, "Invalid source command mode")
    if not verify_only:
        require(
            not args.output.exists() and not args.output.is_symlink(),
            "Source command output must be a new directory",
        )
    binding, sources, record_raw, expected_names, snapshots = command_inputs(
        root=args.root,
        prepared=args.prepared,
        repository=args.repository,
        version=args.version,
        commit=args.commit,
    )
    trees = {"linux/amd64": args.amd64_inputs, "linux/arm64": args.arm64_inputs}
    for tree in (*trees.values(), args.upstream):
        browser_inventory.root_directory(tree)
    authenticator = GhEvidenceVerifier(
        token=os.environ.get("GH_TOKEN"),
        run_id=args.run_id,
        run_attempt=args.run_attempt,
    )
    if verify_only:
        report, evidence = verify_command_output(
            args.output, binding, authenticator, sources
        )
        require(
            read_bounded_file(args.prepared / "release-inputs.json") == record_raw
            and {path.name for path in args.prepared.iterdir()} == expected_names
            and all(
                source_digest(path) == checksum for path, checksum in snapshots.items()
            ),
            "Prepared release changed during source authentication",
        )
        return report, evidence
    report, evidence = corresponding_source_report(
        binding,
        root=args.root,
        source_assets=sources,
        native_measurements={
            p: t / "native/native-measurement.json" for p, t in trees.items()
        },
        runtime_packs={p: t / "native/pack" for p, t in trees.items()},
        runtime_source_records={
            p: t / "native/source-completeness-verification.json"
            for p, t in trees.items()
        },
        dependency_collections={
            p: t / "application-dependencies" for p, t in trees.items()
        },
        source_scans={
            p: t / "source-scans/source-scan-measurement.json" for p, t in trees.items()
        },
        compiler_graphs={
            "backend-"
            + p.split("/")[1]: t / "compiler-backend/compiler-graph-measurement.json"
            for p, t in trees.items()
        },
        upstream_collection=args.upstream,
        browser_measurements={
            p: t / "native/browser/browser-verification.json" for p, t in trees.items()
        },
        captures={p: t / "native/browser/browser-inputs.tar" for p, t in trees.items()},
        archives={
            p: {
                component: t
                / ("native/export/" + component + "-" + p.split("/")[1] + ".oci.tar")
                for component in ("backend", "web")
            }
            for p, t in trees.items()
        },
        authenticator=authenticator,
    )
    require(
        read_bounded_file(args.prepared / "release-inputs.json") == record_raw
        and {path.name for path in args.prepared.iterdir()} == expected_names,
        "Prepared release record/tree changed during source replay",
    )
    require(
        all(source_digest(path) == checksum for path, checksum in snapshots.items()),
        "Prepared release bytes changed during source replay",
    )
    payloads = {
        "corresponding-source.json": json_bytes(report),
        "corresponding-source-evidence.json": json_bytes(evidence),
    }
    require(
        all(0 < len(raw) <= MAX_COMMAND_OUTPUT for raw in payloads.values())
        and sum(map(len, payloads.values())) <= MAX_COMMAND_OUTPUT,
        "Source command evidence exceeds bounds",
    )
    # Create only after all substantive checks/rechecks finish. Existing or
    # partially written output is never reused or overwritten on a retry.
    browser_inventory.root_directory(args.output.parent)
    args.output.mkdir(mode=0o700)
    for name, raw in payloads.items():
        create_output(args.output / name, raw)
    return report, evidence


def main(argv=None) -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in (
        "root",
        "prepared",
        "amd64-inputs",
        "arm64-inputs",
        "upstream",
        "output",
    ):
        parser.add_argument("--" + name, type=Path, required=True)
    for name in ("repository", "version", "commit"):
        parser.add_argument("--" + name, required=True)
    for name in ("run-id", "run-attempt"):
        parser.add_argument("--" + name, type=int, required=True)
    parser.add_argument("--verify-only", action="store_true")
    args = parser.parse_args(argv)
    try:
        run_command(args)
    except (InvalidRelease, OSError, ValueError, RecursionError):
        # Network/tool errors may contain credentials or large diagnostics. The
        # trusted verifier supplies the precise policy boundary internally.
        parser.exit(
            1,
            "Corresponding-source command failed; publication remains unauthorized.\n",
        )


if __name__ == "__main__":
    main()
