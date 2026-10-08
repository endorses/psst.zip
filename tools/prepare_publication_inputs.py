"""Map exact downloaded release artifacts; this grants no publication authority.

Only bounded metadata and regular paths are inspected. The publication driver
independently snapshots, hashes and authenticates all selected bytes before any
remote mutation. No build, source replay, signature verification or API runs here.
"""

from __future__ import annotations

import argparse
from pathlib import Path

from generate_corresponding_source_review import prepared_inventory
from generate_release_gate_reports import NativeSourceContext
from measure_browser_source_inventory import local_file, root_directory
from publish_container_release import GATES, image_subjects, SOURCE_NAME
from release_artifacts import (
    DIGEST,
    fields,
    json_bytes,
    matches,
    read_bounded_file,
    read_json,
    require,
    validate_manifest,
)


def publication_inputs(
    *, inputs_root: Path, repository: str, version: str, commit: str
) -> dict:
    require(inputs_root.is_absolute(), "Publication input root must be absolute")
    root_directory(inputs_root)
    NativeSourceContext(repository, version, commit, "linux/amd64").checked()
    prepared = inputs_root / "prepared"
    _, record, _, manifest, bundle_name, sources, indexes, _ = prepared_inventory(
        prepared=prepared, repository=repository, version=version, commit=commit
    )
    validate_manifest(manifest, repository)
    require(
        manifest["version"] == version
        and manifest["source"]["commit"] == commit
        and manifest["payload_profile"] == "deployment-ready",
        "Publication manifest differs from selected tagged context",
    )
    matches(record["binding_sha256"], DIGEST, "Invalid declared release binding")
    subjects = fields(
        record["subjects"],
        set(image_subjects(manifest))
        | {"manifest", "bundle"}
        | {"source:" + name for name in sources},
        "declared publication subjects",
    )
    require(
        all(
            subjects[name] == value for name, value in image_subjects(manifest).items()
        ),
        "Declared image subjects differ from manifest",
    )
    for name, digest in record["assets"].items():
        subject = (
            "manifest"
            if name == "release-manifest.json"
            else "bundle" if name == bundle_name else "source:" + name
        )
        require(
            subjects[subject] == "file:" + name + "@" + digest,
            "Declared file subjects differ from prepared asset inventory",
        )
    require(
        record["assets"][bundle_name] == "sha256:" + manifest["bundle"]["sha256"],
        "Declared bundle digest differs from manifest",
    )
    archives = {
        component
        + "-"
        + arch: local_file(
            inputs_root / arch, f"native/export/{component}-{arch}.oci.tar"
        )
        for component in ("backend", "web")
        for arch in ("amd64", "arm64")
    }
    reports = {
        gate: local_file(
            inputs_root / "source",
            (
                "candidate-source-review/corresponding-source.json"
                if gate == "corresponding-source"
                else "candidate-check-reports/" + gate + ".json"
            ),
        )
        for gate in GATES - {"upgrade-recovery", "distribution-review"}
    }
    reports.update(
        {
            "upgrade-recovery": local_file(
                inputs_root / "recovery", "recovery-gate/upgrade-recovery.json"
            ),
            "distribution-review": local_file(
                inputs_root / "distribution",
                "distribution-review/reports/distribution-review.json",
            ),
        }
    )
    for gate, path in reports.items():
        value = fields(
            read_json(read_bounded_file(path)),
            {"schema_version", "gate", "binding_digest", "passed", "details"},
            "declared publication gate",
        )
        require(
            type(value["schema_version"]) is int
            and value["schema_version"] == 1
            and value["gate"] == gate
            and value["binding_digest"] == record["binding_sha256"]
            and value["passed"] is True
            and isinstance(value["details"], dict),
            "Declared gate differs from selected release binding",
        )
    return {
        "manifest": str(prepared / "release-manifest.json"),
        "bundle": str(prepared / bundle_name),
        **{
            name: {key: str(path) for key, path in sorted(paths.items())}
            for name, paths in (
                ("indexes", indexes),
                ("archives", archives),
                ("source_assets", sources),
                ("reports", reports),
            )
        },
    }


def main(argv=None) -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inputs-root", type=Path, required=True)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    require(args.output.is_absolute(), "Publication map output must be absolute")
    root_directory(args.output.parent)
    matches(args.output.name, SOURCE_NAME, "Unsafe publication map filename")
    require(
        not args.output.resolve().is_relative_to(args.inputs_root.resolve()),
        "Publication map must be outside downloaded artifacts",
    )
    result = publication_inputs(
        inputs_root=args.inputs_root,
        repository=args.repository,
        version=args.version,
        commit=args.commit,
    )
    with args.output.open("xb") as output:
        output.write(json_bytes(result))


if __name__ == "__main__":
    main()
