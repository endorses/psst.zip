#!/usr/bin/env python3
"""Prepare one native release image pair privately; never publish or approve it.

Requires real record-build output and its paired Docker save, preloaded immutable
application/helper configurations, and the pinned Cosign executable. Collectors
retain original and lower-layer source versions. Final overlays, OCI exports,
native application smoke and independent source replay must all complete before
native-artifacts.json is written. Interrupted output is evidence, not a result.
"""

from __future__ import annotations

import argparse
import hashlib
import io
import os
from pathlib import Path
import re
import shutil
import tarfile
import uuid

import collect_caddy_sources
import measure_native_browser_inputs
import collect_runtime_notices
from assemble_release_oci import inspect_archive
from generate_release_gate_reports import (
    NativeSourceContext,
    collect_native_measurement,
    runtime_inputs,
    validate_smoke,
)
from github_release_transport import command
import package_runtime_sources
from prepare_release_candidate import validate_candidate
from release_artifacts import (
    DIGEST,
    InvalidRelease,
    PLATFORMS,
    create_output,
    fields,
    json_bytes,
    matches,
    read_bounded_file,
    read_json,
    require,
)
import verify_runtime_source_pack

COMPONENTS = {"backend", "web"}
CANDIDATE_FIELDS = {
    "schema_version",
    "kind",
    "candidate_only",
    "version",
    "source_commit",
    "platforms",
    "base_images",
    "base_platform_digests",
}
BUILD_FIELDS = {
    "checked_platform",
    "native_execution",
    "toolchain_output",
    "build_metadata",
    "image_archive",
    "limitations",
}
FILE_NAME = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9_.-]{0,150}\Z")
MAX_ARCHIVE = 8 * 1024**3


def file_record(path: Path, root: Path | None = None) -> dict:
    require(path.is_file() and not path.is_symlink(), "Artifact is not a regular file")
    require(0 < path.stat().st_size <= MAX_ARCHIVE, "Artifact size is invalid")
    with path.open("rb") as stream:
        digest = "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()
    return {
        "file": path.relative_to(root).as_posix() if root else path.name,
        "sha256": digest,
        "size": path.stat().st_size,
    }


def saved_pair(path: Path, configs: dict[str, str]) -> None:
    """Bind original paired save bytes to real Buildx configuration identities.

    Never extract caller archive members. Layer integrity/runtime-source coverage
    is subsequently checked against the preloaded exact images by collectors;
    final exported layers are independently verified by inspect_archive.
    """
    with tarfile.open(path) as saved:
        members = saved.getmembers()
        require(len(members) <= 20_000, "Original save has too many members")
        names = set()
        for member in members:
            collect_runtime_notices.safe_member(member.name)
            require(member.name not in names, "Duplicate original save member")
            names.add(member.name)

        def read(name):
            member = saved.getmember(name)
            require(
                member.isfile() and member.size <= 8 * 1024**2, "Invalid saved metadata"
            )
            return saved.extractfile(member).read()

        manifest = read_json(read("manifest.json"))
        require(
            isinstance(manifest, list) and len(manifest) == 2,
            "Original save must contain exactly the pair",
        )
        seen = set()
        for item in manifest:
            require(isinstance(item, dict), "Invalid original save entry")
            name = str(collect_runtime_notices.safe_member(item.get("Config", "")))
            raw = read(name)
            seen.add("sha256:" + hashlib.sha256(raw).hexdigest())
        require(
            len(seen) == 2 and seen == set(configs.values()),
            "Original save contains another image pair",
        )


def export_saved_image(
    context: NativeSourceContext,
    archive: Path,
    alias: str,
    output: Path,
    component: str,
    tested_config: str,
) -> dict:
    """Package saved configuration/layers without reserializing tested bytes.

    Docker archive to OCI converters can reorder configuration JSON and thereby
    change the tested configuration digest. Preserve the original raw blob and
    layer streams instead. No extraction, executable image tool, registry,
    emulation or external cache is involved. inspect_archive then independently
    verifies the complete graph, source identity and every uncompressed diff ID.
    """
    context.checked()
    matches(tested_config, DIGEST, "Missing tested configuration for export")
    require(
        component in COMPONENTS and not output.exists(), "Invalid/existing OCI export"
    )
    before = file_record(archive)
    with tarfile.open(archive, mode="r:") as saved:
        members = saved.getmembers()
        require(len(members) <= 20_000, "Saved image has too many members")
        names = set()
        for member in members:
            collect_runtime_notices.safe_member(member.name)
            require(
                member.name not in names
                and (member.isfile() or member.isdir())
                and not member.sparse,
                "Duplicate/unsupported saved image member",
            )
            names.add(member.name)

        def metadata(name):
            require(isinstance(name, str), "Saved metadata path is invalid")
            member = saved.getmember(str(collect_runtime_notices.safe_member(name)))
            require(
                member.isfile() and 0 < member.size <= 8 * 1024**2,
                "Saved metadata exceeds bounds",
            )
            return saved.extractfile(member).read()

        manifest = read_json(metadata("manifest.json"))
        require(
            isinstance(manifest, list)
            and len(manifest) == 2
            and all(isinstance(item, dict) for item in manifest),
            "Expected saved native pair",
        )
        selected = [
            item
            for item in manifest
            if isinstance(item.get("RepoTags"), list) and alias in item["RepoTags"]
        ]
        require(len(selected) == 1, "Saved native image tag is missing/ambiguous")
        image = selected[0]
        config_raw = metadata(image.get("Config"))
        require(
            "sha256:" + hashlib.sha256(config_raw).hexdigest() == tested_config,
            "Saved configuration differs from tested configuration",
        )
        config = read_json(config_raw)
        layers = image.get("Layers")
        require(
            isinstance(layers, list)
            and 0 < len(layers) <= 128
            and isinstance(config, dict),
            "Saved image layers are invalid",
        )
        diff_ids = config.get("rootfs", {}).get("diff_ids")
        require(
            isinstance(diff_ids, list) and len(layers) == len(diff_ids),
            "Saved image layer count differs",
        )
        descriptors = []
        blob_members = {}
        total = 0
        for name in layers:
            require(isinstance(name, str), "Saved layer path is invalid")
            member = saved.getmember(str(collect_runtime_notices.safe_member(name)))
            require(
                member.isfile() and 0 < member.size <= 512 * 1024**2,
                "Saved image layer exceeds bounds",
            )
            total += member.size
            require(total <= MAX_ARCHIVE, "Saved layer total exceeds bounds")
            with saved.extractfile(member) as stream:
                prefix = stream.read(2)
                stream.seek(0)
                digest = "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()
            media = "application/vnd.oci.image.layer.v1.tar" + (
                "+gzip" if prefix == b"\x1f\x8b" else ""
            )
            descriptors.append(
                {"mediaType": media, "digest": digest, "size": member.size}
            )
            blob_members[digest] = member
        image_raw = json_bytes(
            {
                "schemaVersion": 2,
                "mediaType": "application/vnd.oci.image.manifest.v1+json",
                "config": {
                    "mediaType": "application/vnd.oci.image.config.v1+json",
                    "digest": tested_config,
                    "size": len(config_raw),
                },
                "layers": descriptors,
            }
        )
        image_digest = "sha256:" + hashlib.sha256(image_raw).hexdigest()
        documents = {
            "oci-layout": json_bytes({"imageLayoutVersion": "1.0.0"}),
            "index.json": json_bytes(
                {
                    "schemaVersion": 2,
                    "mediaType": "application/vnd.oci.image.index.v1+json",
                    "manifests": [
                        {
                            "mediaType": "application/vnd.oci.image.manifest.v1+json",
                            "digest": image_digest,
                            "size": len(image_raw),
                            "platform": {
                                "os": "linux",
                                "architecture": context.platform.split("/")[1],
                            },
                        }
                    ],
                }
            ),
            "blobs/sha256/" + tested_config[7:]: config_raw,
            "blobs/sha256/" + image_digest[7:]: image_raw,
        }
        # Exclusive output, deterministic metadata, content-addressed payloads.
        with tarfile.open(output, mode="x:", format=tarfile.USTAR_FORMAT) as exported:
            for name, raw in sorted(documents.items()):
                member = tarfile.TarInfo(name)
                member.mode, member.size = 0o644, len(raw)
                exported.addfile(member, io.BytesIO(raw))
            for digest, original in sorted(blob_members.items()):
                member = tarfile.TarInfo("blobs/sha256/" + digest[7:])
                member.mode, member.size = 0o644, original.size
                with saved.extractfile(original) as stream:
                    exported.addfile(member, stream)
    require(file_record(archive) == before, "Saved image changed during export")
    return inspect_archive(
        output,
        platform=context.platform,
        repository=context.repository,
        version=context.version,
        commit=context.commit,
        tested_config=tested_config,
        component=component,
    )


def build_inputs(
    context: NativeSourceContext, record: object, archive: Path
) -> tuple[dict, dict]:
    source = context.checked()
    record = fields(record, CANDIDATE_FIELDS | BUILD_FIELDS, "native build record")
    candidate = validate_candidate({key: record[key] for key in CANDIDATE_FIELDS})
    require(
        candidate["version"] == source["version"]
        and candidate["source_commit"] == source["commit"],
        "Build record belongs to another source",
    )
    require(
        record["checked_platform"] == context.platform
        and record["native_execution"] is True,
        "Build record is not this native platform",
    )
    toolchain = fields(
        record["toolchain_output"],
        {"go", "node", "docker", "compose", "buildx", "builder"},
        "actual native toolchains",
    )
    require(
        all(
            isinstance(value, str) and 0 < len(value) <= 8192
            for value in toolchain.values()
        ),
        "Actual toolchain output is missing",
    )
    require(
        re.fullmatch(
            r"go version go1\.26\.8 " + re.escape(context.platform), toolchain["go"]
        )
        is not None
        and re.fullmatch(r"v22\.\d+\.\d+", toolchain["node"]) is not None,
        "Native compiler/runtime output differs",
    )
    require(
        isinstance(record["limitations"], list)
        and all(isinstance(item, str) for item in record["limitations"]),
        "Missing candidate limitations",
    )
    metadata = fields(record["build_metadata"], COMPONENTS, "original Buildx pair")
    configs = {}
    for component, value in metadata.items():
        require(isinstance(value, dict), "Invalid Buildx metadata")
        configs[component] = matches(
            value.get("containerimage.config.digest"),
            DIGEST,
            "Missing original configuration",
        )
        matches(
            value.get("containerimage.digest"),
            DIGEST,
            "Missing original built manifest",
        )
    require(len(set(configs.values())) == 2, "Original configurations must be distinct")
    saved = fields(
        record["image_archive"], {"name", "sha256", "size"}, "original paired save"
    )
    matches(saved["name"], FILE_NAME, "Unsafe original save filename")
    require(
        saved["name"].endswith(".tar") and saved["name"] == archive.name,
        "Original save filename differs",
    )
    actual = file_record(archive)
    require(
        type(saved["size"]) is int
        and saved["size"] == actual["size"]
        and "sha256:" + str(saved["sha256"]) == actual["sha256"],
        "Original paired save bytes differ",
    )
    saved_pair(archive, configs)
    return candidate, configs


def checked_image(
    info: dict, config: str, context: NativeSourceContext, *, application: bool
) -> None:
    require(
        info.get("Id") == config
        and info.get("Os") == "linux"
        and info.get("Architecture") == context.platform.split("/")[1],
        "Preloaded immutable image/platform differs",
    )
    if application:
        labels = info.get("Config", {}).get("Labels", {})
        require(
            isinstance(labels, dict)
            and labels.get("org.opencontainers.image.version") == context.version
            and labels.get("org.opencontainers.image.revision") == context.commit
            and labels.get("org.opencontainers.image.source")
            == "https://github.com/" + context.repository,
            "Preloaded application source identity differs",
        )
    else:
        require(
            info.get("Config", {}).get("User") == "65532:65532",
            "Source helper must be the unprivileged build recipe",
        )


class Operations:
    """Real adapters only; CLI exposes no supplied-success or skip-check path."""

    def run(self, *args: str, timeout: int = 1200) -> bytes:
        environment = {
            key: value
            for key, value in os.environ.items()
            if key
            in {
                "PATH",
                "HOME",
                "DOCKER_HOST",
                "DOCKER_CONTEXT",
                "DOCKER_CONFIG",
                "DOCKER_CERT_PATH",
                "DOCKER_TLS_VERIFY",
                "XDG_RUNTIME_DIR",
            }
        }
        return command(list(args), environment=environment, timeout=timeout)

    def image(self, config: str) -> dict:
        values = read_json(self.run("docker", "image", "inspect", config))
        require(
            isinstance(values, list) and len(values) == 1, "Preloaded image is absent"
        )
        return values[0]

    def preflight(self, context: NativeSourceContext) -> None:
        architecture = (
            self.run("docker", "info", "--format", "{{.Architecture}}").decode().strip()
        )
        architecture = {"x86_64": "amd64", "aarch64": "arm64"}.get(
            architecture, architecture
        )
        require(
            context.platform == "linux/" + architecture,
            "Native release preparation forbids emulation",
        )
        builder = self.run("docker", "buildx", "inspect", "default").decode()
        require(
            re.search(r"^Driver:\s+docker\s*$", builder, re.M) is not None,
            "Local overlay builder must use Docker driver",
        )

    def collect_apk(self, image: str, helper: str, output: Path) -> None:
        collect_runtime_notices.collect(image, helper, output)

    def collect_caddy(self, image: str, base: str, output: Path) -> None:
        collect_caddy_sources.collect(image, base, output)

    def package(
        self,
        collections: Path,
        cosign: Path,
        context: NativeSourceContext,
        output: Path,
    ) -> dict:
        return package_runtime_sources.package(
            collections / "backend",
            collections / "web",
            collections / "caddy",
            cosign,
            context.version,
            context.commit,
            f"https://github.com/{context.repository}/releases/download/{context.version}",
            output,
        )

    def overlay(
        self,
        image: str,
        alias: str,
        output_alias: str,
        folder: Path,
        platform: str,
        iidfile: Path,
    ) -> str:
        self.run("docker", "image", "tag", image, alias)
        self.run(
            "docker",
            "buildx",
            "build",
            "--builder",
            "default",
            "--network",
            "none",
            "--pull=false",
            "--platform",
            platform,
            "--load",
            "--provenance=false",
            "--sbom=false",
            "--tag",
            output_alias,
            "--iidfile",
            str(iidfile),
            "--build-arg",
            "ORIGINAL_IMAGE=" + alias,
            str(folder),
        )
        return matches(
            read_bounded_file(iidfile).decode().strip(),
            DIGEST,
            "Overlay configuration was not recorded",
        )

    def overlays(self, pack: Path, configs: dict[str, str]) -> dict:
        return package_runtime_sources.verify_overlays(
            pack, configs["backend"], configs["web"]
        )

    def save(self, aliases: dict[str, str], output: Path) -> None:
        self.run(
            "docker",
            "image",
            "save",
            "--output",
            str(output),
            aliases["backend"],
            aliases["web"],
        )

    def export(
        self,
        context: NativeSourceContext,
        archive: Path,
        alias: str,
        output: Path,
        component: str,
        tested_config: str,
    ) -> dict:
        return export_saved_image(
            context, archive, alias, output, component, tested_config
        )

    def measure(
        self, context: NativeSourceContext, pack: Path, archives: dict, configs: dict
    ) -> dict:
        return collect_native_measurement(
            context, pack=pack, archives=archives, tested_configs=configs
        )

    def browser(
        self,
        context,
        builder_config,
        source_root,
        dependency_collection,
        archive,
        tested_config,
        runtime_pack,
        output,
    ):
        return measure_native_browser_inputs.collect(
            self,
            context,
            builder_config,
            source_root,
            dependency_collection,
            archive,
            tested_config,
            runtime_pack,
            output,
        )

    def replay(
        self,
        context: NativeSourceContext,
        pack: Path,
        archives: dict,
        smoke: Path,
        cosign: Path,
        asset_digest: str,
    ) -> dict:
        return verify_runtime_source_pack.verify(
            pack,
            archives,
            smoke,
            cosign,
            repository=context.repository,
            version=context.version,
            revision=context.commit,
            source_sha256=asset_digest,
        )

    def cleanup(self, aliases: list[str]) -> None:
        # Remove only unique task aliases, never shared/configuration IDs or cache.
        for alias in aliases:
            try:
                self.run("docker", "image", "rm", alias, timeout=120)
            except InvalidRelease:
                pass  # A failed build may not have created its alias.


def prepare(
    *,
    context: NativeSourceContext,
    build_record: Path,
    original_archive: Path,
    helper_config: str,
    cosign: Path,
    web_builder_config: str,
    source_root: Path,
    dependency_collection: Path,
    output: Path,
    operations: Operations | None = None,
) -> dict:
    context.checked()
    matches(
        web_builder_config, DIGEST, "Browser builder must be an immutable configuration"
    )
    matches(helper_config, DIGEST, "Source helper must be an immutable configuration")
    require(
        cosign.is_file() and not cosign.is_symlink(),
        "Pinned Cosign executable is missing",
    )
    raw = read_bounded_file(build_record)
    candidate, originals = build_inputs(context, read_json(raw), original_archive)
    original_snapshot = file_record(original_archive)
    require(
        not output.exists() and not output.is_symlink(),
        "Native output must be a new directory",
    )
    output = output.resolve()
    require(
        not any(character in str(output) for character in [",", "\n", "\r"]),
        "Native output path is unsafe for container mounts",
    )
    operations = operations or Operations()
    for config in originals.values():
        checked_image(operations.image(config), config, context, application=True)
    checked_image(
        operations.image(helper_config), helper_config, context, application=False
    )
    operations.preflight(context)
    output.mkdir(mode=0o700, parents=True)
    create_output(output / "build-record.json", raw)
    # Preserve exact original filename even if it would otherwise collide with
    # our stable layout. The enclosing directory prevents all collisions.
    original_folder = output / "original"
    original_folder.mkdir(mode=0o700)
    copied_original = original_folder / original_archive.name
    shutil.copyfile(original_archive, copied_original)
    require(
        file_record(copied_original) == original_snapshot,
        "Original save changed while copying",
    )
    collections = output / "collections"
    collections.mkdir(mode=0o700)
    export_folder = output / "export"
    export_folder.mkdir(mode=0o700)
    pack = output / "pack"
    nonce = uuid.uuid4().hex
    inputs = {
        component: f"psst-native-{nonce}-{component}:input" for component in COMPONENTS
    }
    aliases = {
        component: f"psst-native-{nonce}-{component}:final" for component in COMPONENTS
    }
    try:
        for component in sorted(COMPONENTS):
            operations.collect_apk(
                originals[component], helper_config, collections / component
            )
        operations.collect_caddy(
            originals["web"], candidate["base_images"]["caddy"], collections / "caddy"
        )
        operations.package(collections, cosign, context, pack)
        manifest, runtime = runtime_inputs(context, pack)
        require(
            {
                component: manifest["bindings"][component]["original_image_id"]
                for component in COMPONENTS
            }
            == originals,
            "Pack original configurations differ",
        )
        configs = {}
        for component in sorted(COMPONENTS):
            configs[component] = operations.overlay(
                originals[component],
                inputs[component],
                aliases[component],
                pack / "overlays" / component,
                context.platform,
                output / (component + "-overlay.id"),
            )
            checked_image(
                operations.image(configs[component]),
                configs[component],
                context,
                application=True,
            )
        create_output(
            output / "overlay-verification.json",
            json_bytes(operations.overlays(pack, configs)),
        )
        final_save = output / "final-images.docker.tar"
        operations.save(aliases, final_save)
        saved_pair(final_save, configs)
        final_snapshot = file_record(final_save)
        archives = {
            component: export_folder
            / f"{component}-{context.platform.split('/')[1]}.oci.tar"
            for component in COMPONENTS
        }
        for component in sorted(COMPONENTS):
            operations.export(
                context,
                final_save,
                aliases[component],
                archives[component],
                component,
                configs[component],
            )
        measurement = operations.measure(context, pack, archives, configs)
        require(
            measurement.get("schema_version") == 2
            and measurement.get("kind") == "native-release-measurement"
            and measurement.get("source") == context.checked()
            and measurement.get("publication_authorized") is False
            and measurement.get("runtime") == runtime,
            "Final measurement source/runtime identity differs",
        )
        smoke = validate_smoke(
            measurement["smoke"], context, runtime["runtime_pack_sha256"]
        )
        require(
            smoke["tested_configs"] == configs,
            "Final smoke tested another configuration pair",
        )
        smoke_path = output / "smoke-report.json"
        create_output(smoke_path, json_bytes(smoke))
        create_output(output / "native-measurement.json", json_bytes(measurement))
        replay = operations.replay(
            context,
            pack,
            archives,
            smoke_path,
            cosign,
            runtime["source_asset"]["digest"],
        )
        require(
            replay.get("runtime_source_inputs_verified") is True
            and replay.get("distribution_authorized") is False
            and replay.get("images") == measurement["images"]
            and replay.get("runtime_pack_sha256") == runtime["runtime_pack_sha256"],
            "Independent source replay differs from final native measurement",
        )
        create_output(
            output / "source-completeness-verification.json", json_bytes(replay)
        )
        require(
            runtime_inputs(context, pack)[1] == runtime
            and file_record(original_archive) == original_snapshot
            and read_bounded_file(build_record) == raw
            and file_record(copied_original) == original_snapshot
            and file_record(final_save) == final_snapshot,
            "Native preparation inputs changed",
        )
        for component, archive in archives.items():
            require(
                measurement["images"][component]["config_digest"] == configs[component]
                and measurement["images"][component]["archive_digest"]
                == file_record(archive)["sha256"],
                "Native OCI bytes differ from measured configurations",
            )
        browser_result = operations.browser(
            context,
            web_builder_config,
            source_root,
            dependency_collection,
            archives["web"],
            configs["web"],
            manifest,
            output / "browser",
        )
        require(
            browser_result.get("tested_web_config") == configs["web"]
            and browser_result.get("source") == context.checked()
            and browser_result.get("builder_config") == web_builder_config
            and browser_result.get("oci_image_verified") is True
            and browser_result.get("git_source_binding_verified") is True
            and browser_result.get("image") == measurement["images"]["web"]
            and browser_result.get("npm_member_integrity_verified") is True
            and browser_result.get("browser_module_closure_verified") is False
            and browser_result.get("source_reproduction_verified") is False
            and browser_result.get("publication_authorized") is False
            and browser_result.get("distribution_authorized") is False,
            "Native browser evidence identity/approval boundary differs",
        )
        artifact_paths = {
            "browser_inputs": output / "browser/browser-inputs.tar",
            "browser_verification": output / "browser/browser-verification.json",
            "build_record": output / "build-record.json",
            "original_archive": copied_original,
            "final_archive": final_save,
            "native_measurement": output / "native-measurement.json",
            "smoke_report": smoke_path,
            "source_verification": output / "source-completeness-verification.json",
            "runtime_pack": pack / "runtime-pack.json",
            "runtime_source": pack / runtime["source_asset"]["name"],
            **{component + "_archive": path for component, path in archives.items()},
        }
        require(
            file_record(original_archive) == original_snapshot
            and read_bounded_file(build_record) == raw
            and file_record(copied_original) == original_snapshot
            and file_record(final_save) == final_snapshot,
            "Native evidence inputs changed during browser verification",
        )
        result = {
            "schema_version": 2,
            "kind": "native-release-artifacts",
            "source": context.checked(),
            "original_tested_configs": originals,
            "tested_configs": configs,
            "source_helper_config": helper_config,
            "oci_exporter": "docker-save-byte-preserving-oci-v1",
            "artifacts": {
                key: file_record(path, output) for key, path in artifact_paths.items()
            },
            "publication_authorized": False,
            "measurement_authentication_required": True,
        }
        create_output(output / "native-artifacts.json", json_bytes(result))
        return result
    finally:
        operations.cleanup([*inputs.values(), *aliases.values()])


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--platform", choices=PLATFORMS, required=True)
    parser.add_argument("--helper-config", required=True)
    parser.add_argument("--web-builder-config", required=True)
    for name in [
        "build-record",
        "original-archive",
        "cosign",
        "source-root",
        "dependency-collection",
        "output",
    ]:
        parser.add_argument("--" + name, type=Path, required=True)
    args = parser.parse_args()
    try:
        result = prepare(
            context=NativeSourceContext(
                args.repository, args.version, args.commit, args.platform
            ),
            build_record=args.build_record,
            original_archive=args.original_archive,
            helper_config=args.helper_config,
            cosign=args.cosign,
            web_builder_config=args.web_builder_config,
            source_root=args.source_root,
            dependency_collection=args.dependency_collection,
            output=args.output,
        )
        print(json_bytes(result).decode(), end="")
    except (InvalidRelease, OSError, ValueError, KeyError, tarfile.TarError) as error:
        raise SystemExit(f"Native preparation refused: {error}") from error


if __name__ == "__main__":
    main()
