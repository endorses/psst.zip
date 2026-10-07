"""Replay corresponding-source inputs; partial facts never grant release approval.

The application archive check establishes the exact retained Git source tree and
release packaging recipe. It does not establish dependency, runtime, generator,
or preferred-form source completeness, and cannot produce a passed source gate.
"""

from __future__ import annotations

import gzip
import io
from pathlib import Path

from aggregate_release_image_scans import checked_graph, load_authenticated, load_raw
from generate_release_gate_reports import (
    MeasurementAuthenticator,
    NativeSourceContext,
    aggregate_native_reports,
    checked_binding,
    runtime_inputs,
)
import verify_application_dependency_inputs as dependency_inputs
from publish_container_release import Binding, sha256, source_digest
from release_artifacts import (
    COMMIT,
    DIGEST,
    PLATFORMS,
    VERSION,
    fields,
    git,
    matches,
    read_bounded_file,
    read_json,
    repository_name,
    require,
)

# Same bound as the native source scanner's Git snapshot. No extraction is needed.
MAX_APPLICATION_SOURCE_BYTES = 256 * 1024**2


def verify_backend_source_inputs(
    binding: Binding,
    *,
    root: Path,
    native_measurements: dict[str, Path],
    runtime_packs: dict[str, Path],
    dependency_collections: dict[str, Path],
    source_scans: dict[str, Path],
    compiler_graphs: dict[str, Path],
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
    application_digest = sha256(git(root, "archive", binding.commit))
    reports = {}
    snapshots = []
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
        source, source_digest_value = load_authenticated(
            source_scans[platform], binding, authenticator
        )
        require(
            source.get("source") == context.checked(),
            "Backend source scan belongs to another native source context",
        )
        graph_path = compiler_graphs[target]
        graph, graph_digest = load_authenticated(graph_path, binding, authenticator)
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
            graph["source_inputs"]["application_archive_sha256"] == application_digest,
            "Backend compiler source archive differs from selected committed Git source",
        )
        replay = dependency_inputs.verify(
            context, root, dependency_collections[platform], source_scans[platform]
        )
        require(
            replay.get("source") == context.checked()
            and replay.get("package_inputs_replayed") is True
            and replay.get("source_measurement_sha256") == source_digest_value,
            "Dependency inputs differ from authenticated native source scan",
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
            isinstance(retained, list) and len(retained) == replay.get("go_modules"),
            "Verified Go module input inventory missing",
        )
        modules = {}
        for row in retained:
            fields(row, {"module", "version", "sum", "zip_sha256"}, "verified module")
            require(
                isinstance(row["module"], str)
                and row["module"]
                and row["module"] not in modules,
                "Duplicate or malformed verified module input",
            )
            matches(row["zip_sha256"], DIGEST, "Verified module archive digest missing")
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
        }
        snapshots.extend(
            [
                (native_measurements[platform], native[platform]["measurement_digest"]),
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
        "preferred_source_review_required": True,
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
