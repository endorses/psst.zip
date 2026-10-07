#!/usr/bin/env python3
"""Retain complete, unsigned native candidate inputs through an explicit allowlist.

No archive is extracted and no credential, signing or publication action occurs.
Downloaded measurements still require independent authentication and gate review.
"""

from __future__ import annotations

import argparse
import hashlib
from pathlib import Path, PurePosixPath
import re

from generate_release_gate_reports import (
    NativeSourceContext,
    runtime_inputs,
    validate_smoke,
)
from release_artifacts import (
    DIGEST,
    PLATFORMS,
    create_output,
    fields,
    json_bytes,
    matches,
    read_json,
    require,
)

METADATA = "candidate-transfer.json"
SOURCE_KINDS = {"version-tag", "planned-main-dispatch"}
MAX_FILE = 8 * 1024**3
MAX_TOTAL = 32 * 1024**3
MAX_JSON = 16 * 1024**2
MAX_RAW = 32 * 1024**2
MAX_FILES = 10_000
COMPONENTS = {"backend", "web"}
ARTIFACTS = {
    "build_record",
    "original_archive",
    "final_archive",
    "native_measurement",
    "smoke_report",
    "source_verification",
    "runtime_pack",
    "runtime_source",
    "backend_archive",
    "web_archive",
}
DESCRIPTOR_FIELDS = {
    "schema_version",
    "kind",
    "source",
    "original_tested_configs",
    "tested_configs",
    "source_helper_config",
    "oci_exporter",
    "artifacts",
    "publication_authorized",
    "measurement_authentication_required",
}
GO_RAW = {
    "go-version.txt",
    "go-env.json",
    "scanner-module.json",
    "scanner-binary-sha256.txt",
    "scanner-build.json",
    "go-roots.json",
    "go-all-graph.json",
    "go-server-graph.json",
    "go-modules.json",
    "govulncheck.json",
    "govulncheck.stderr",
    "govulncheck.exit",
    "govulncheck.txt",
    "govulncheck-convert.stderr",
    "govulncheck-convert.exit",
    "go-execution.complete",
    "execution.stderr",
}
NODE_RAW = {
    "node-version.txt",
    "npm-version.txt",
    "npm-lock-graph.json",
    "npm-graph.stderr",
    "npm-audit.json",
    "npm-audit.stderr",
    "npm-audit.exit",
    "node-execution.complete",
    "execution.stderr",
}
COMPILER_RAW = {
    "binary-build-info.json",
    "compiler-environment.json",
    "compiler-execution.complete",
    "compiler-graph.json",
    "execution.stderr",
    "go-version.txt",
}


def safe_name(name: object) -> str:
    require(isinstance(name, str) and bool(name), "Missing transfer path")
    path = PurePosixPath(name)
    require(
        not path.is_absolute()
        and str(path) == name
        and all(part not in {".", ".."} for part in path.parts)
        and "\\" not in name
        and not any(ord(char) < 32 or ord(char) == 127 for char in name),
        "Unsafe transfer path",
    )
    return name


def regular(root: Path, name: str) -> Path:
    name = safe_name(name)
    require(
        root.is_dir() and not any(p.is_symlink() for p in (root, *root.parents)),
        "Unsafe transfer root",
    )
    path = root / name
    require(
        path.is_file() and not any(p.is_symlink() for p in (path, *path.parents)),
        "Transfer input must be a regular file under real directories",
    )
    return path


def fingerprint(root: Path, name: str, maximum: int = MAX_FILE) -> dict:
    path = regular(root, name)
    before = path.stat()
    require(0 <= before.st_size <= maximum, "Transfer input exceeds bounds")
    checksum, size = hashlib.sha256(), 0
    with path.open("rb") as stream:
        while chunk := stream.read(1024**2):
            size += len(chunk)
            require(size <= maximum, "Transfer input grew beyond bounds")
            checksum.update(chunk)
    after = path.stat()
    require(
        size == before.st_size
        and (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns)
        == (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns),
        "Transfer input changed while hashing",
    )
    return {"sha256": "sha256:" + checksum.hexdigest(), "size": size}


def json_record(root: Path, name: str) -> dict:
    with regular(root, name).open("rb") as stream:
        raw = stream.read(MAX_JSON + 1)
    require(0 < len(raw) <= MAX_JSON, "Transfer JSON exceeds bounds")
    record = read_json(raw)
    require(isinstance(record, dict), "Transfer record must be an object")
    return record


class Inventory:
    def __init__(self, root: Path):
        self.root, self.files = root, {}

    def add(
        self,
        name: str,
        digest: str | None = None,
        size: int | None = None,
        *,
        maximum=MAX_FILE,
    ):
        actual = fingerprint(self.root, name, maximum)
        if digest is not None:
            matches(digest, DIGEST, "Invalid referenced transfer checksum")
            require(actual["sha256"] == digest, "Referenced transfer bytes differ")
        if size is not None:
            require(
                type(size) is int and actual["size"] == size,
                "Referenced transfer size differs",
            )
        if name in self.files:
            require(
                self.files[name] == actual, "Transfer input changed during inventory"
            )
        self.files[name] = actual
        require(
            len(self.files) <= MAX_FILES
            and sum(v["size"] for v in self.files.values()) <= MAX_TOTAL,
            "Transfer inventory exceeds bounds",
        )
        return actual

    def json(self, name: str) -> dict:
        self.add(name, maximum=MAX_JSON)
        return json_record(self.root, name)

    def raw(self, directory: str, raw_files: object, allowed: set[str]):
        require(
            isinstance(raw_files, dict)
            and bool(raw_files)
            and set(raw_files) == allowed,
            "Declared raw evidence inventory is incomplete or unsupported",
        )
        for name, digest in raw_files.items():
            matches(digest, DIGEST, "Missing declared raw evidence checksum")
            require(
                safe_name(name) == PurePosixPath(name).name,
                "Raw evidence requires a simple filename",
            )
            self.add(directory + "/" + name, digest, maximum=MAX_RAW)


def checked_measurement(
    record: dict, context: NativeSourceContext, kind: str, schema=1
):
    require(
        type(record.get("schema_version")) is int
        and record["schema_version"] == schema
        and record.get("kind") == kind
        and record.get("source") == context.checked()
        and record.get("publication_authorized") is False,
        "Transfer measurement source/schema/approval boundary differs",
    )


def collect_inventory(context: NativeSourceContext, root: Path) -> dict[str, dict]:
    """Validate producer identities and referenced bytes; never approve findings."""
    context.checked()
    inventory, arch = Inventory(root), context.platform.split("/")[1]
    descriptor = fields(
        inventory.json("native/native-artifacts.json"),
        DESCRIPTOR_FIELDS,
        "native descriptor",
    )
    checked_measurement(descriptor, context, "native-release-artifacts")
    require(
        descriptor["measurement_authentication_required"] is True
        and descriptor["oci_exporter"] == "docker-save-byte-preserving-oci-v1",
        "Native transfer requires unsigned exact OCI exports",
    )
    for pair in ("original_tested_configs", "tested_configs"):
        values = fields(descriptor[pair], COMPONENTS, "native configurations")
        for value in values.values():
            matches(value, DIGEST, "Missing native image configuration")
        require(len(set(values.values())) == 2, "Native image configurations overlap")
    matches(
        descriptor["source_helper_config"],
        DIGEST,
        "Missing native helper configuration",
    )
    artifacts = fields(descriptor["artifacts"], ARTIFACTS, "all native artifacts")
    expected_paths = {
        "build_record": "build-record.json",
        "final_archive": "final-images.docker.tar",
        "native_measurement": "native-measurement.json",
        "smoke_report": "smoke-report.json",
        "source_verification": "source-completeness-verification.json",
        "runtime_pack": "pack/runtime-pack.json",
        "backend_archive": "export/backend-" + arch + ".oci.tar",
        "web_archive": "export/web-" + arch + ".oci.tar",
    }
    for key, record in artifacts.items():
        record = fields(record, {"file", "sha256", "size"}, "native artifact")
        matches(record["sha256"], DIGEST, "Missing native artifact checksum")
        name = safe_name(record["file"])
        if key in expected_paths:
            require(
                name == expected_paths[key],
                "Native artifact path differs from producer",
            )
        elif key == "original_archive":
            require(
                name.startswith("original/")
                and len(PurePosixPath(name).parts) == 2
                and name.endswith(".tar"),
                "Unsafe original save path",
            )
        else:
            require(
                name.startswith("pack/")
                and len(PurePosixPath(name).parts) == 2
                and name.endswith(".tar.gz"),
                "Unsafe runtime source path",
            )
        require(
            type(record["size"]) is int and record["size"] > 0,
            "Empty/invalid native artifact",
        )
        inventory.add("native/" + name, record["sha256"], record["size"])
    pack, runtime = runtime_inputs(context, root / "native/pack")
    require(
        artifacts["runtime_source"]["file"]
        == "pack/" + runtime["source_asset"]["name"],
        "Runtime source descriptor differs from pack",
    )
    require(
        pack.get("publication_pending") is True
        and pack.get("distribution_review_required") is True,
        "Runtime pack approval boundary differs",
    )
    for component, files in pack["overlays"].items():
        for name, digest in files.items():
            inventory.add(
                "native/pack/overlays/" + component + "/runtime/" + safe_name(name),
                "sha256:" + digest,
                maximum=MAX_RAW,
            )
    additional = fields(
        pack.get("additional_files"), COMPONENTS, "additional runtime offer files"
    )
    for component, files in additional.items():
        require(
            isinstance(files, dict)
            and (
                not files
                or component == "web"
                and set(files) == {"/srv/web/licenses/release.json"}
            ),
            "Unsupported extra overlay file",
        )
        for digest in files.values():
            inventory.add(
                "native/pack/overlays/web/release.json",
                "sha256:" + digest,
                maximum=MAX_JSON,
            )
    measurement = inventory.json("native/native-measurement.json")
    checked_measurement(measurement, context, "native-release-measurement", 2)
    require(measurement.get("runtime") == runtime, "Native measurement runtime differs")
    smoke = validate_smoke(
        measurement.get("smoke"), context, runtime["runtime_pack_sha256"]
    )
    require(
        smoke["tested_configs"] == descriptor["tested_configs"]
        and inventory.json("native/smoke-report.json") == smoke,
        "Native smoke configuration/report differs",
    )
    images = fields(measurement.get("images"), COMPONENTS, "native image measurements")
    for component, image in images.items():
        require(
            isinstance(image, dict)
            and image.get("platform") == context.platform
            and image.get("config_digest") == descriptor["tested_configs"][component]
            and image.get("archive_digest")
            == artifacts[component + "_archive"]["sha256"],
            "Native image/export identity differs",
        )
    build = inventory.json("native/build-record.json")
    original = artifacts["original_archive"]
    require(
        build.get("version") == context.version
        and build.get("source_commit") == context.commit
        and build.get("checked_platform") == context.platform
        and build.get("native_execution") is True,
        "Original build source/platform differs",
    )
    require(
        build.get("image_archive")
        == {
            "name": PurePosixPath(original["file"]).name,
            "sha256": original["sha256"][7:],
            "size": original["size"],
        },
        "Original build save differs",
    )
    for component in COMPONENTS:
        require(
            build.get("build_metadata", {})
            .get(component, {})
            .get("containerimage.config.digest")
            == descriptor["original_tested_configs"][component],
            "Original configuration differs",
        )
    replay = inventory.json("native/source-completeness-verification.json")
    require(
        replay.get("schema_version") == 1
        and replay.get("kind") == "runtime-source-completeness"
        and replay.get("repository") == context.repository
        and replay.get("version") == context.version
        and replay.get("revision") == context.commit
        and replay.get("platform") == context.platform
        and replay.get("distribution_authorized") is False
        and replay.get("runtime_source_inputs_verified") is True
        and replay.get("runtime_pack_sha256") == runtime["runtime_pack_sha256"]
        and replay.get("runtime_source_asset_sha256")
        == runtime["source_asset"]["digest"]
        and replay.get("images") == images
        and replay.get("native_smoke_report_sha256")
        == inventory.files["native/smoke-report.json"]["sha256"],
        "Runtime replay evidence differs",
    )
    sources = inventory.json("source-scans/source-scan-measurement.json")
    checked_measurement(sources, context, "native-source-scanner-measurement")
    require(
        sources.get("execution") == "native"
        and sources.get("source_scanners_gate_pending") is True
        and sources.get("findings_review_required") is True,
        "Source scanners require native pending review",
    )
    scans = sources.get("scans")
    require(
        isinstance(scans, list)
        and len(scans) == 2
        and all(isinstance(row, dict) for row in scans)
        and {row.get("target") for row in scans} == {"backend-source", "web-source"},
        "Source scanner target coverage differs",
    )
    for scan in scans:
        target = scan["target"]
        require(
            scan.get("subject") == "git:" + context.repository + "@" + context.commit
            and scan.get("status") == "complete"
            and type(scan.get("exit_code")) is int
            and scan["exit_code"] in ({0} if target == "backend-source" else {0, 1}),
            "Source scan failed or source differs",
        )
        inventory.raw(
            "source-scans/" + target,
            scan.get("raw_files"),
            GO_RAW if target == "backend-source" else NODE_RAW,
        )
    for component in COMPONENTS:
        directory = "compiler-" + component
        graph = inventory.json(directory + "/compiler-graph-measurement.json")
        checked_measurement(graph, context, "native-compiler-graph-measurement")
        require(
            graph.get("execution") == "native"
            and graph.get("component") == component
            and graph.get("image") == images[component]
            and graph.get("status") == "complete"
            and type(graph.get("exit_code")) is int
            and graph["exit_code"] == 0
            and graph.get("finding_dispositions_authorized") is False,
            "Compiler graph source/image/execution differs",
        )
        allowed = COMPILER_RAW | (
            {"initial-build-info.json", "rebuilt-backend-sha256.txt"}
            if component == "backend"
            else {"caddy-signature-verification.json"}
        )
        advisories = graph.get("advisories")
        require(isinstance(advisories, dict), "Missing compiler advisory inventory")
        for identifier, advisory in advisories.items():
            require(
                isinstance(identifier, str)
                and re.fullmatch(r"GO-[0-9]{4}-[0-9]+", identifier) is not None
                and isinstance(advisory, dict)
                and advisory.get("raw_file") == identifier + ".json"
                and advisory.get("origin")
                == "https://vuln.go.dev/ID/" + identifier + ".json",
                "Unsafe compiler advisory reference",
            )
            allowed.add(identifier + ".json")
            matches(
                advisory.get("sha256"), DIGEST, "Missing compiler advisory checksum"
            )
            inventory.add(
                directory + "/" + identifier + ".json",
                advisory.get("sha256"),
                maximum=MAX_RAW,
            )
        inventory.raw(directory, graph.get("raw_files"), allowed)
        require(
            graph.get("source_graph", {}).get("raw_file") == "compiler-graph.json"
            and graph["source_graph"].get("sha256")
            == graph["raw_files"]["compiler-graph.json"],
            "Compiler graph raw binding differs",
        )
        directory = "image-scan-" + component
        scan = inventory.json(directory + "/measurement.json")
        checked_measurement(scan, context, "native-image-scanner-measurement")
        require(
            scan.get("execution") == "native"
            and scan.get("target") == component + "-" + arch
            and scan.get("image") == images[component]
            and scan.get("status") == "complete"
            and type(scan.get("exit_code")) is int
            and scan["exit_code"] == 0
            and scan.get("findings_review_required") is True
            and scan.get("final_image_scanners_gate_pending") is True,
            "Image scanner source/target/execution differs",
        )
        matches(
            scan.get("raw_report_sha256"), DIGEST, "Missing image scanner raw checksum"
        )
        inventory.add(
            directory + "/scan.json", scan.get("raw_report_sha256"), maximum=MAX_JSON
        )
        raw = inventory.json(directory + "/scan.json")
        require(
            raw.get("Metadata", {}).get("ImageID")
            == descriptor["tested_configs"][component],
            "Image scanner raw configuration differs",
        )
    dependencies = inventory.json("application-dependencies/dependency-collection.json")
    require(
        type(dependencies.get("schema_version")) is int
        and dependencies["schema_version"] == 1
        and dependencies.get("kind") == "application-dependency-collection"
        and dependencies.get("repository") == context.repository
        and dependencies.get("version") == context.version
        and dependencies.get("source_commit") == context.commit
        and dependencies.get("platform") == context.platform
        and dependencies.get("publication_authorized") is False
        and dependencies.get("package_inputs_verified") is True
        and dependencies.get("preferred_source_review_required") is True,
        "Dependency collection source/approval boundary differs",
    )
    asset = fields(
        dependencies.get("asset"), {"name", "sha256", "size"}, "dependency asset"
    )
    matches(asset["sha256"], DIGEST, "Missing dependency archive checksum")
    require(
        type(asset["size"]) is int and asset["size"] > 0,
        "Invalid dependency archive size",
    )
    require(
        asset["name"]
        == "psst.zip-dependency-inputs-" + context.version + "-" + arch + ".tar.gz",
        "Dependency asset architecture/name differs",
    )
    inventory.add(
        "application-dependencies/" + asset["name"], asset["sha256"], asset["size"]
    )
    return dict(sorted(inventory.files.items()))


def verify_native(
    context: NativeSourceContext, transfer: Path, source_kind: str
) -> dict:
    require(source_kind in SOURCE_KINDS, "Unknown candidate source kind")
    fingerprint(transfer, METADATA, MAX_JSON)
    metadata = json_record(transfer, METADATA)
    fields(
        metadata,
        {
            "schema_version",
            "kind",
            "source",
            "source_kind",
            "files",
            "publication_authorized",
            "measurement_authentication_required",
        },
        "candidate transfer metadata",
    )
    checked_measurement(metadata, context, "native-candidate-transfer")
    require(
        metadata["source_kind"] == source_kind
        and metadata["measurement_authentication_required"] is True,
        "Candidate transfer source kind/authentication differs",
    )
    files = collect_inventory(context, transfer)
    require(
        metadata["files"] == files
        and all(
            type(record.get("size")) is int for record in metadata["files"].values()
        ),
        "Candidate transfer inventory/hash differs",
    )
    expected_files = set(files) | {METADATA}
    expected_dirs = {
        parent.as_posix()
        for name in expected_files
        for parent in PurePosixPath(name).parents
        if parent != PurePosixPath(".")
    }
    actual_files, actual_dirs = set(), set()
    for path in transfer.rglob("*"):
        require(not path.is_symlink(), "Linked candidate transfer payload")
        name = path.relative_to(transfer).as_posix()
        if path.is_dir():
            actual_dirs.add(name)
        else:
            require(path.is_file(), "Special candidate transfer payload")
            actual_files.add(name)
    require(
        actual_files == expected_files and actual_dirs == expected_dirs,
        "Extra or missing candidate transfer payload",
    )
    return metadata


def copy_file(source: Path, target: Path):
    target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    size = 0
    with source.open("rb") as reader, target.open("xb") as writer:
        while chunk := reader.read(1024**2):
            size += len(chunk)
            require(size <= MAX_FILE, "Candidate copy grew beyond bounds")
            writer.write(chunk)


def stage_native(
    context: NativeSourceContext, private: Path, output: Path, source_kind: str
) -> dict:
    require(source_kind in SOURCE_KINDS, "Unknown candidate source kind")
    require(
        not output.exists()
        and not output.is_symlink()
        and output.parent.is_dir()
        and not any(p.is_symlink() for p in (output.parent, *output.parent.parents)),
        "Candidate transfer output must be a new real directory",
    )
    files = collect_inventory(context, private)
    output.mkdir(mode=0o700)
    for name, record in files.items():
        require(
            fingerprint(private, name) == record,
            "Candidate input changed before transfer",
        )
        copy_file(regular(private, name), output / name)
        require(
            fingerprint(output, name) == record == fingerprint(private, name),
            "Candidate input changed during transfer",
        )
    require(
        collect_inventory(context, private) == files
        and collect_inventory(context, output) == files,
        "Candidate evidence changed during transfer",
    )
    result = {
        "schema_version": 1,
        "kind": "native-candidate-transfer",
        "source": context.checked(),
        "source_kind": source_kind,
        "files": files,
        "publication_authorized": False,
        "measurement_authentication_required": True,
    }
    create_output(output / METADATA, json_bytes(result))
    return verify_native(context, output, source_kind)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("stage", "verify"))
    for name in ("repository", "version", "commit", "platform", "source-kind"):
        parser.add_argument(
            "--" + name,
            required=True,
            choices=(
                sorted(SOURCE_KINDS)
                if name == "source-kind"
                else PLATFORMS if name == "platform" else None
            ),
        )
    parser.add_argument("--private", type=Path)
    parser.add_argument("--transfer", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    context = NativeSourceContext(
        args.repository, args.version, args.commit, args.platform
    )
    if args.mode == "stage":
        require(
            args.private is not None and args.transfer is None,
            "Stage requires only private input and new output directory",
        )
        stage_native(context, args.private, args.output, args.source_kind)
    else:
        require(
            args.transfer is not None and args.private is None,
            "Verify requires only transfer input and new output report",
        )
        result = verify_native(context, args.transfer, args.source_kind)
        create_output(args.output, json_bytes(result))


if __name__ == "__main__":
    main()
