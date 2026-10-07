"""Replay retained package inputs independently; never authorize distribution."""

from __future__ import annotations

import copy
import gzip
import hashlib
import io
from pathlib import Path
import re
import tarfile
import tempfile

from assemble_release_oci import StrictTarInfo
from generate_release_gate_reports import NativeSourceContext
import package_application_dependencies as package
from publish_container_release import sha256
from release_artifacts import (
    DIGEST,
    fields,
    git,
    matches,
    read_bounded_file,
    read_json,
    require,
)

LOCKS = ("backend/go.mod", "backend/go.sum", "web/package-lock.json")
COLLECTION = (
    "go-downloads.json",
    "go-modules.json",
    "go-environment.json",
    "go-version.txt",
)
ENVIRONMENT = {
    "GOTOOLCHAIN": "local",
    "GOPROXY": "https://proxy.golang.org",
    "GOSUMDB": "sum.golang.org",
    "GOPRIVATE": "",
    "GONOPROXY": "",
    "GONOSUMDB": "",
    "GOFLAGS": "-mod=readonly",
}


def archive_files(raw: bytes) -> dict[str, bytes]:
    """Read bounded regular USTAR payloads; no archive filenames are extracted."""
    files, total = {}, 0
    with tarfile.open(
        fileobj=io.BytesIO(raw), mode="r:gz", tarinfo=StrictTarInfo
    ) as archive:
        for entry in archive:
            package.safe_name(entry.name)
            require(
                entry.isfile()
                and not entry.issparse()
                and entry.name not in files
                and len(files) < package.MAX_MEMBERS
                and 0 < entry.size <= package.MAX_PACKAGE,
                "Unsafe/duplicate dependency archive payload",
            )
            total += entry.size
            require(
                total <= package.MAX_TOTAL,
                "Dependency archive expansion exceeds bounds",
            )
            stream = archive.extractfile(entry)
            require(stream is not None, "Dependency archive payload missing")
            body = stream.read(entry.size + 1)
            require(len(body) == entry.size, "Truncated dependency archive payload")
            files[entry.name] = body
    require(files, "Empty dependency archive")
    return files


def selected_modules(records: list[dict]) -> list[tuple[str, str, bool]]:
    result = []
    for row in records:
        require(
            isinstance(row.get("Path"), str) and row["Path"],
            "Invalid selected module path",
        )
        main = row.get("Main") is True
        version = row.get("Version", "")
        require(
            isinstance(version, str) and (main or version),
            "Selected module version missing",
        )
        result.append((row["Path"], version, main))
    require(
        len(set(result)) == len(result) and sum(v[2] for v in result) == 1,
        "Duplicate/incomplete selected module graph",
    )
    return sorted(result)


def canonical_payload(raw: bytes, files: dict[str, bytes], timestamp: int) -> None:
    """Reject hidden/trailing archive bytes as well as noncanonical metadata."""
    expected = hashlib.sha256()

    class Sink:
        def write(self, content):
            expected.update(content)
            return len(content)

    with tarfile.open(
        fileobj=Sink(), mode="w|", format=tarfile.USTAR_FORMAT
    ) as archive:
        for name, body in sorted(files.items()):
            entry = tarfile.TarInfo(name)
            entry.size, entry.mtime, entry.mode = len(body), timestamp, 0o644
            archive.addfile(entry, io.BytesIO(body))
    actual, count = hashlib.sha256(), 0
    maximum = package.MAX_TOTAL + package.MAX_MEMBERS * 1024 + 10240
    with gzip.GzipFile(fileobj=io.BytesIO(raw), mode="rb") as stream:
        while chunk := stream.read(1024**2):
            count += len(chunk)
            require(
                count <= maximum, "Dependency archive hidden expansion exceeds bounds"
            )
            actual.update(chunk)
    require(
        actual.digest() == expected.digest(),
        "Dependency archive has hidden payload or changed canonical metadata",
    )


def scanner_raw(measurement_path: Path, row: dict, name: str) -> bytes:
    matches(name, re.compile(r"[a-zA-Z0-9_.-]+\Z"), "Unsafe source scanner filename")
    target = row["target"]
    require(
        target in {"backend-source", "web-source"}, "Unexpected source scanner target"
    )
    root = measurement_path.parent / target
    require(
        root.is_dir() and not root.is_symlink(), "Invalid source scanner raw directory"
    )
    body = read_bounded_file(root / name)
    require(
        row.get("raw_files", {}).get(name) == sha256(body),
        "Source scanner raw graph changed",
    )
    return body


def verify(
    context: NativeSourceContext,
    repository_root: Path,
    collection_dir: Path,
    source_measurement: Path,
) -> dict:
    """Return unapproved checksum replay facts; caller authenticates inputs separately."""
    identity = context.checked()
    origin = git(repository_root, "remote", "get-url", "origin").decode().strip()
    require(
        origin
        in {
            f"https://github.com/{context.repository}{suffix}"
            for suffix in ("", ".git")
        }
        | {f"git@github.com:{context.repository}{suffix}" for suffix in ("", ".git")},
        "Dependency replay checkout origin differs",
    )
    require(
        collection_dir.is_dir() and not collection_dir.is_symlink(),
        "Invalid dependency collection directory",
    )
    collection_bytes = read_bounded_file(collection_dir / "dependency-collection.json")
    collection = read_json(collection_bytes)
    require(isinstance(collection, dict), "Invalid dependency collection record")
    for value, kind in ((collection, "application-dependency-collection"),):
        require(
            type(value.get("schema_version")) is int
            and value["schema_version"] == 1
            and value.get("kind") == kind
            and value.get("repository") == context.repository
            and value.get("version") == context.version
            and value.get("source_commit") == context.commit
            and value.get("platform") == context.platform
            and value.get("publication_authorized") is False
            and value.get("preferred_source_review_required") is True
            and value.get("package_inputs_verified") is True,
            "Dependency collection source/platform differs",
        )
    name = f"psst.zip-dependency-inputs-{context.version}-{context.platform.split('/')[1]}.tar.gz"
    asset = fields(
        collection.get("asset"), {"name", "sha256", "size"}, "dependency asset"
    )
    require(
        asset["name"] == name and type(asset["size"]) is int,
        "Dependency asset filename/size differs",
    )
    archive = package.bounded(collection_dir / name, package.MAX_TOTAL)
    require(
        asset["sha256"] == sha256(archive) and asset["size"] == len(archive),
        "Dependency archive checksum differs",
    )
    files = archive_files(archive)
    canonical_payload(
        archive,
        files,
        int(git(repository_root, "show", "-s", "--format=%ct", context.commit)),
    )
    require("dependency-inputs.json" in files, "Dependency input record missing")
    record = read_json(files["dependency-inputs.json"])
    require(
        isinstance(record, dict)
        and type(record.get("schema_version")) is int
        and record["schema_version"] == 1
        and record.get("kind") == "application-dependency-inputs"
        and record.get("repository") == context.repository
        and record.get("version") == context.version
        and record.get("source_commit") == context.commit
        and record.get("publication_authorized") is False
        and record.get("preferred_source_review_required") is True
        and record.get("package_inputs_verified") is True,
        "Dependency input record source differs",
    )
    originals = {
        name: git(repository_root, "show", context.commit + ":" + name)
        for name in LOCKS
    }
    require(
        record.get("inputs") == {name: sha256(body) for name, body in originals.items()}
        and all(
            files.get("inputs/" + name) == body for name, body in originals.items()
        ),
        "Original dependency locks differ from exact Git source",
    )
    measurement_bytes = read_bounded_file(source_measurement)
    measurement = read_json(measurement_bytes)
    require(
        isinstance(measurement, dict)
        and type(measurement.get("schema_version")) is int
        and measurement["schema_version"] == 1
        and measurement.get("kind") == "native-source-scanner-measurement"
        and measurement.get("source") == identity
        and measurement.get("execution") == "native"
        and measurement.get("publication_authorized") is False
        and measurement.get("source_archive_sha256")
        == sha256(git(repository_root, "archive", context.commit))
        and all(
            measurement.get("source_inputs", {}).get(name) == sha256(body)
            for name, body in originals.items()
        ),
        "Source scanner measurement differs from exact Git source",
    )
    scans = measurement.get("scans")
    require(
        isinstance(scans, list)
        and len(scans) == 2
        and all(isinstance(row, dict) for row in scans),
        "Source scanner coverage incomplete",
    )
    scans = {row.get("target"): row for row in scans}
    fields(scans, {"backend-source", "web-source"}, "both source scans")
    require(
        all(
            row.get("status") == "complete"
            and type(row.get("exit_code")) is int
            and row["exit_code"] in {0, 1}
            for row in scans.values()
        ),
        "Source scanner execution incomplete",
    )
    collector = record.get("go_collector", {})
    require(
        isinstance(collector, dict)
        and re.fullmatch(
            r"docker.io/library/golang@sha256:[0-9a-f]{64}", collector.get("image", "")
        )
        and collector.get("platform") == context.platform
        and collector.get("version") == package.GO_VERSION
        and collector.get("environment") == ENVIRONMENT,
        "Dependency collector immutable builder/settings differ",
    )
    matches(
        collector.get("config"), DIGEST, "Dependency collector configuration missing"
    )
    builder = scans["backend-source"].get("builder", {})
    require(
        builder.get("reference") == collector["image"]
        and builder.get("config_digest") == collector["config"]
        and builder.get("architecture") == context.platform.split("/")[1],
        "Collector and scanner actual builders differ",
    )
    require(
        files.get("collection/go-version.txt", b"").decode().strip()
        == f"go version {package.GO_VERSION} {context.platform}"
        and read_json(files.get("collection/go-environment.json", b"null"))
        == ENVIRONMENT,
        "Dependency collector raw version/settings differ",
    )
    require("inputs/authenticated-go.sum" in files, "Authenticated graph sums missing")
    added = package.additional_sums(
        originals["backend/go.sum"], files["inputs/authenticated-go.sum"]
    )
    require(
        added == record.get("additional_authenticated_sums"),
        "Additional authenticated sum inventory differs",
    )
    selected = package.stream_json(files.get("collection/go-modules.json", b""))
    source_modules = scanner_raw(
        source_measurement, scans["backend-source"], "go-modules.json"
    )
    require(
        scans["backend-source"].get("graphs", {}).get("go-modules.json")
        == sha256(source_modules)
        and selected_modules(selected)
        == selected_modules(package.stream_json(source_modules)),
        "Selected Go modules differ from source scanner graph",
    )
    downloads = package.stream_json(files.get("collection/go-downloads.json", b""))
    retained_go = record.get("go_modules")
    require(isinstance(retained_go, list), "Dependency module inventory missing")
    inventory = {
        (row.get("module"), row.get("version")): row
        for row in retained_go
        if isinstance(row, dict)
    }
    require(len(inventory) == len(retained_go), "Duplicate dependency module inventory")
    # Reuse the original checksum primitive while generating every cache path ourselves.
    # No downloaded metadata/archive path is written to the host filesystem.
    with tempfile.TemporaryDirectory(prefix="psst-dependency-replay-") as temporary:
        cache = Path(temporary)
        rewritten = copy.deepcopy(downloads)
        for index, row in enumerate(rewritten):
            key = (row.get("Path"), row.get("Version"))
            require(
                key in inventory, "Downloaded module lacks retained input inventory"
            )
            retained = inventory[key].get("inputs")
            fields(retained, {"zip", "mod", "info"}, "retained module inputs")
            basename = hashlib.sha256((key[0] + "@" + key[1]).encode()).hexdigest()
            for field, suffix in (("Zip", "zip"), ("GoMod", "mod"), ("Info", "info")):
                original_path = row.get(field)
                require(
                    isinstance(original_path, str)
                    and original_path.startswith("/reports/cache/")
                    and ".." not in Path(original_path).parts
                    and "\\" not in original_path,
                    "Downloaded module metadata escaped original private cache",
                )
                entry = fields(
                    retained[suffix],
                    {"file", "sha256", "size"},
                    "retained module input",
                )
                filename = "go/" + basename + "." + suffix
                require(
                    entry["file"] == filename and filename in files,
                    "Retained module filename differs",
                )
                body = files[filename]
                require(
                    entry["sha256"] == sha256(body)
                    and type(entry["size"]) is int
                    and entry["size"] == len(body),
                    "Retained module input checksum differs",
                )
                local = f"{index}.{suffix}"
                (cache / local).write_bytes(body)
                row[field] = "/reports/cache/" + local
        go_files, replayed = package.module_inputs(
            rewritten, selected, cache, files["inputs/authenticated-go.sum"]
        )
    require(replayed == retained_go, "Replayed Go module inventory differs")
    retained_npm = record.get("npm_packages")
    require(isinstance(retained_npm, list), "Dependency npm inventory missing")
    by_url = {}
    for row in retained_npm:
        require(
            isinstance(row, dict) and isinstance(row.get("resolved"), str),
            "Malformed retained npm input",
        )
        filename = row.get("file")
        require(
            isinstance(filename, str) and filename in files,
            "Retained npm payload missing",
        )
        body = files[filename]
        require(
            row["resolved"] not in by_url or by_url[row["resolved"]] == body,
            "Conflicting npm registry inputs",
        )
        by_url[row["resolved"]] = body

    def fetch(url):
        require(url in by_url, "Locked npm package source missing")
        return by_url[url]

    npm_files, replayed_npm = package.npm_inputs(
        originals["web/package-lock.json"], fetch
    )
    require(replayed_npm == retained_npm, "Replayed npm inventory differs")
    web = scans["web-source"]
    npm_graph = scanner_raw(source_measurement, web, "npm-lock-graph.json")
    require(
        web.get("lock_sha256") == sha256(originals["web/package-lock.json"])
        and web.get("lock_graph_sha256") == sha256(npm_graph)
        and web.get("dependency_packages") == len(replayed_npm)
        and not read_json(npm_graph).get("error")
        and not read_json(npm_graph).get("problems"),
        "npm source scanner lock graph differs",
    )
    expected = {
        **go_files,
        **npm_files,
        **{"inputs/" + k: v for k, v in originals.items()},
        "inputs/authenticated-go.sum": files["inputs/authenticated-go.sum"],
        **{"collection/" + name: files["collection/" + name] for name in COLLECTION},
        "dependency-inputs.json": files["dependency-inputs.json"],
        "SOURCE.md": files.get("SOURCE.md"),
    }
    require(
        expected.get("SOURCE.md") and files == expected,
        "Dependency archive contains missing/extra payloads",
    )
    require(
        type(collection.get("go_modules")) is int
        and collection["go_modules"] == len(replayed)
        and type(collection.get("npm_packages")) is int
        and collection["npm_packages"] == len(replayed_npm),
        "External dependency coverage counts differ",
    )
    return {
        "schema_version": 1,
        "kind": "application-dependency-replay",
        "source": identity,
        "collection_sha256": sha256(collection_bytes),
        "archive_sha256": sha256(archive),
        "record_sha256": sha256(files["dependency-inputs.json"]),
        "source_measurement_sha256": sha256(measurement_bytes),
        "selected_go_graph_sha256": sha256(source_modules),
        "npm_lock_graph_sha256": sha256(npm_graph),
        "go_modules": len(replayed),
        "npm_packages": len(replayed_npm),
        "additional_sums": len(added),
        "package_inputs_replayed": True,
        "preferred_source_review_required": True,
        "corresponding_source_completeness_verified": False,
        "publication_authorized": False,
    }
