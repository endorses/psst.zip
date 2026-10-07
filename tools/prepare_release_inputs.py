#!/usr/bin/env python3
"""Assemble both native candidates into immutable inputs, without approving release.

Run after the native matrix, before authenticated gate aggregation. The supplied
measurements describe actual local checks; this structural preparation does not
authenticate them. The publisher must still authenticate all required gates.
"""

from __future__ import annotations

import argparse
import gzip
import io
from pathlib import Path
import re
import shutil

from assemble_release_oci import assemble, inspect_archive
from generate_release_gate_reports import (
    NativeSourceContext,
    runtime_inputs,
    validate_smoke,
)
from prepare_release_candidate import validate_candidate
from publish_container_release import (
    bind_reviewed_source,
    prepare_inputs,
    source_digest,
    sha256,
)
from release_artifacts import (
    PLATFORMS,
    REQUIREMENTS,
    InvalidRelease,
    assignments,
    build_bundle,
    create_output,
    fields,
    git,
    json_bytes,
    read_bounded_file,
    read_json,
    require,
    validate_bundle,
    validate_manifest,
)
from verify_application_dependency_inputs import verify as verify_dependencies
from package_upstream_application_sources import verify as verify_upstream


def toolchains(candidate: dict, records: dict[str, dict]) -> dict[str, str]:
    """Use actual pinned builder outputs from both native jobs, never defaults."""
    fields(records, set(PLATFORMS), "both native build records")
    versions = {}
    for platform, record in records.items():
        require(isinstance(record, dict), "Missing native build record")
        require(
            {name: record.get(name) for name in candidate} == candidate,
            "Native build used different resolved bases/source",
        )
        require(
            record.get("checked_platform") == platform
            and record.get("native_execution") is True,
            "Build record is not native for its selected platform",
        )
        output = record.get("toolchain_output")
        require(isinstance(output, dict), "Missing actual builder toolchains")
        go = re.fullmatch(
            r"go version (go[0-9]+\.[0-9]+\.[0-9]+) " + platform, str(output.get("go"))
        )
        node = re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", str(output.get("node")))
        require(
            go is not None and node is not None, "Malformed actual Go/Node versions"
        )
        versions[platform] = {"go": go.group(1), "node": node.group(0)}
    require(
        versions[PLATFORMS[0]] == versions[PLATFORMS[1]],
        "Native builders used different Go/Node versions",
    )
    return versions[PLATFORMS[0]]


def prepare(
    *,
    root: Path,
    repository: str,
    ref: str,
    event_sha: str,
    reviewed_commit: str,
    candidate: Path,
    builds: dict[str, Path],
    measurements: dict[str, Path],
    packs: dict[str, Path],
    archives: dict[str, Path],
    dependency_collections: dict[str, Path],
    source_scans: dict[str, Path],
    upstream_collection: Path,
    output: Path,
    migration_notes: str,
    rollback_notes: str,
) -> dict:
    version, commit = bind_reviewed_source(
        root, repository, ref, event_sha, reviewed_commit
    )
    require(
        not output.exists() and not output.is_symlink(),
        "Release output must be new; partial preparations require manual inspection",
    )
    resolved = validate_candidate(read_json(read_bounded_file(candidate)))
    require(
        resolved["version"] == version and resolved["source_commit"] == commit,
        "Resolved bases belong to another source",
    )
    for pair in (builds, measurements, packs, dependency_collections, source_scans):
        fields(pair, set(PLATFORMS), "both native artifact inputs")
    fields(
        archives,
        {
            component + "-" + arch
            for component in ("backend", "web")
            for arch in ("amd64", "arm64")
        },
        "four native OCI exports",
    )
    actual_tools = toolchains(
        resolved,
        {
            platform: read_json(read_bounded_file(path))
            for platform, path in builds.items()
        },
    )
    tested, sources, source_hashes, dependency_replays = {}, {}, {}, {}
    for platform in PLATFORMS:
        context = NativeSourceContext(repository, version, commit, platform)
        _, runtime = runtime_inputs(context, packs[platform])
        record = fields(
            read_json(read_bounded_file(measurements[platform])),
            {
                "schema_version",
                "kind",
                "source",
                "smoke",
                "images",
                "runtime",
                "publication_authorized",
            },
            "native measurements",
        )
        require(
            type(record["schema_version"]) is int
            and record["schema_version"] == 2
            and record["kind"] == "native-release-measurement"
            and record["source"] == context.checked()
            and record["runtime"] == runtime
            and record["publication_authorized"] is False,
            "Native measurement source/runtime differs",
        )
        smoke = validate_smoke(record["smoke"], context, runtime["runtime_pack_sha256"])
        require(smoke["execution"] == "native", "Both native executions are required")
        fields(record["images"], {"backend", "web"}, "measured image pair")
        for component in ("backend", "web"):
            key = component + "-" + platform.split("/")[1]
            tested[key] = smoke["tested_configs"][component]
            actual = inspect_archive(
                archives[key],
                platform=platform,
                repository=repository,
                version=version,
                commit=commit,
                tested_config=tested[key],
                component=component,
            )
            require(
                actual == record["images"][component],
                "Native OCI bytes differ from the measured image",
            )
        asset = runtime["source_asset"]
        require(asset["name"] not in sources, "Native source asset names collide")
        sources[asset["name"]] = packs[platform] / asset["name"]
        source_hashes[asset["name"]] = asset["digest"]
        replay = verify_dependencies(
            context,
            root,
            dependency_collections[platform],
            source_scans[platform],
        )
        require(
            replay["source"] == context.checked()
            and replay["package_inputs_replayed"] is True
            and replay["preferred_source_review_required"] is True
            and replay["corresponding_source_completeness_verified"] is False
            and replay["publication_authorized"] is False,
            "Dependency replay did not preserve its unapproved source boundary",
        )
        collection_path = (
            dependency_collections[platform] / "dependency-collection.json"
        )
        collection_raw = read_bounded_file(collection_path)
        require(
            sha256(collection_raw) == replay["collection_sha256"],
            "Dependency collection changed after replay",
        )
        collection = read_json(collection_raw)
        name = collection["asset"]["name"]
        require(name not in sources, "Dependency source asset names collide")
        sources[name] = dependency_collections[platform] / name
        source_hashes[name] = replay["archive_sha256"]
        dependency_replays[platform] = replay
    upstream = verify_upstream(
        root=root,
        repository=repository,
        version=version,
        commit=commit,
        collection=upstream_collection,
    )
    require(
        upstream["source"]
        == {
            "repository": repository,
            "version": version,
            "commit": commit,
        }
        and upstream["package_inputs_replayed"] is True
        and upstream["corresponding_source_completeness_verified"] is False
        and upstream["publication_authorized"] is False,
        "Upstream replay did not preserve its unapproved source boundary",
    )
    upstream_asset = fields(
        upstream["asset"], {"name", "digest", "size"}, "upstream source asset"
    )
    require(
        upstream_asset["name"] not in sources, "Upstream source asset name collides"
    )
    sources[upstream_asset["name"]] = upstream_collection / upstream_asset["name"]
    source_hashes[upstream_asset["name"]] = upstream_asset["digest"]
    # All four exports and local measurements were checked before writing outputs.
    oci = assemble(
        archives,
        tested,
        repository=repository,
        version=version,
        commit=commit,
        output=output,
    )
    bundle = build_bundle(root, output, version, "deployment-ready")
    value = {
        "schema_version": 1,
        "version": version,
        "payload_profile": "deployment-ready",
        "source": {
            "repository": repository,
            "commit": commit,
            "archive_url": f"https://github.com/{repository}/archive/{commit}.tar.gz",
        },
        "platforms": PLATFORMS,
        "images": oci["images"],
        "bundle": {"name": bundle.name, "sha256": source_digest(bundle)[7:]},
        "requirements": REQUIREMENTS,
        "notes": {
            "migration": migration_notes,
            "rollback": rollback_notes,
            "checkpoint_required": True,
        },
        "build": {"base_images": resolved["base_images"], "toolchains": actual_tools},
    }
    validate_manifest(value, repository)
    validate_bundle(value, bundle)
    manifest = output / "release-manifest.json"
    create_output(manifest, json_bytes(value))
    # Exact tracked tree includes application/build/install sources, no untracked
    # operator files or research. Match gzip time to the tagged Git commit.
    name = f"psst.zip-source-{version}.tar.gz"
    require(name not in sources, "Application source asset name collides")
    buffer = io.BytesIO()
    with gzip.GzipFile(
        fileobj=buffer,
        mode="wb",
        filename="",
        mtime=int(git(root, "show", "-s", "--format=%ct", commit)),
    ) as zipped:
        zipped.write(
            git(
                root, "archive", "--format=tar", f"--prefix=psst.zip-{version}/", commit
            )
        )
    create_output(output / name, buffer.getvalue())
    sources[name] = output / name
    # Prepared inputs must retain their actual payloads, not only references to
    # matrix-local paths that disappear when a runner exits.
    for name, path in list(sources.items()):
        if path == output / name:
            continue
        before = source_digest(path)
        require(
            before == source_hashes[name], "Source payload changed after verification"
        )
        destination = output / name
        require(not destination.exists(), "Source payload destination already exists")
        with path.open("rb") as source, destination.open("xb") as retained:
            shutil.copyfileobj(source, retained, length=1024**2)
        require(
            source_digest(destination) == before == source_digest(path),
            "Source payload changed while retaining release inputs",
        )
        sources[name] = destination
    inputs = prepare_inputs(
        root=root,
        repository=repository,
        ref=ref,
        event_sha=event_sha,
        reviewed_commit=reviewed_commit,
        manifest_path=manifest,
        bundle=bundle,
        indexes={c: output / f"{c}-index.json" for c in ("backend", "web")},
        source_assets=sources,
    )
    result = {
        "schema_version": 1,
        "kind": "prepared-release-inputs",
        "repository": repository,
        "version": version,
        "source_commit": commit,
        "binding_sha256": inputs.binding.digest,
        "assets": dict(inputs.assets),
        "subjects": dict(inputs.binding.subjects),
        "dependency_replays": dependency_replays,
        "upstream_replay": upstream,
        "publication_authorized": False,
        "measurement_authentication_required": True,
    }
    create_output(output / "release-inputs.json", json_bytes(result))
    return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("root", "candidate", "output", "upstream"):
        parser.add_argument("--" + name, required=True, type=Path)
    for name in (
        "repository",
        "ref",
        "event-sha",
        "reviewed-commit",
        "migration-notes",
        "rollback-notes",
    ):
        parser.add_argument("--" + name, required=True)
    for name in (
        "build",
        "measurement",
        "pack",
        "archive",
        "dependencies",
        "source-scan",
    ):
        parser.add_argument(
            "--" + name, action="append", default=[], metavar="KEY=PATH"
        )
    args = parser.parse_args()
    try:
        prepare(
            root=args.root,
            repository=args.repository,
            ref=args.ref,
            event_sha=args.event_sha,
            reviewed_commit=args.reviewed_commit,
            candidate=args.candidate,
            output=args.output,
            builds={k: Path(v) for k, v in assignments(args.build).items()},
            measurements={k: Path(v) for k, v in assignments(args.measurement).items()},
            packs={k: Path(v) for k, v in assignments(args.pack).items()},
            archives={k: Path(v) for k, v in assignments(args.archive).items()},
            dependency_collections={
                k: Path(v) for k, v in assignments(args.dependencies).items()
            },
            source_scans={k: Path(v) for k, v in assignments(args.source_scan).items()},
            upstream_collection=args.upstream,
            migration_notes=args.migration_notes,
            rollback_notes=args.rollback_notes,
        )
    except (InvalidRelease, OSError, ValueError) as error:
        parser.exit(1, f"Release input preparation rejected: {error}\n")


if __name__ == "__main__":
    main()
