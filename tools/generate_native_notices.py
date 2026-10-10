#!/usr/bin/env python3
"""Resolve shipped native library variants and preserve their full licensing inputs.

The main resolution is the Android release runtime and all three iOS compile-klib
variants. Kotlin/Native and SKIE runtime notices are separately pinned because
compiler-injected runtime code does not appear in that Maven dependency graph.
Apple platform SDK/framework code is supplied by the OS, not copied into assets.
"""

from __future__ import annotations

import argparse
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import tempfile
import tomllib
import xml.etree.ElementTree as ET
import zipfile

ROOT = Path(__file__).resolve().parents[1]
PLATFORMS = {
    "android": "android/app/src/main/assets/licenses",
    "ios": "ios/Shared/LegalResources",
}
INPUTS = (
    "android/app/build.gradle.kts",
    "android/gradle/libs.versions.toml",
    "android/settings.gradle.kts",
    "shared/build.gradle.kts",
    "shared/gradle/libs.versions.toml",
    "shared/settings.gradle.kts",
    "shared/licenses/source-inventory.json",
    "tools/generate_native_notices.py",
)
MAX_LICENSE = 1024 * 1024
MAX_NESTED_JAR = 256 * 1024 * 1024
LEGAL_NAME = re.compile(
    r"(?:^|/)(?:LICENSE|LICENCE|NOTICE|COPYRIGHT|COPYING)(?:[^/]*)$", re.I
)

GRADLE_EXPORT = r"""
import groovy.json.JsonOutput
import org.gradle.api.artifacts.component.ModuleComponentIdentifier
import org.gradle.maven.MavenModule
import org.gradle.maven.MavenPomArtifact
allprojects { p ->
  tasks.register('psstExportLegalArtifacts') {
    doLast {
      def names = p.path == ':app' ? ['releaseRuntimeClasspath'] : p.path == ':' && p.rootProject.name == 'shared' ? ['iosArm64CompileKlibraries', 'iosSimulatorArm64CompileKlibraries', 'iosX64CompileKlibraries'] : []
      if (names.isEmpty()) return
      def records = []
      names.each { name ->
        def configuration = p.configurations.getByName(name)
        configuration.incoming.artifactView { componentFilter { it instanceof ModuleComponentIdentifier } }.artifacts.artifacts.each { artifact ->
          def id = artifact.id.componentIdentifier
          records.add([configuration:name, group:id.group, name:id.module, version:id.version, file:artifact.file.absolutePath])
        }
      }
      records = records.unique { [it.group, it.name, it.version, it.configuration, it.file] }
      def queryPom = { group, module, version ->
        def result = p.dependencies.createArtifactResolutionQuery().forModule(group,module,version).withArtifacts(MavenModule,MavenPomArtifact).execute()
        def artifacts = result.resolvedComponents.collectMany { it.getArtifacts(MavenPomArtifact) }
        def file = artifacts.find { it instanceof org.gradle.api.artifacts.result.ResolvedArtifactResult }?.file
        if (file == null) throw new GradleException("Missing native dependency POM ${group}:${module}:${version}")
        return file
      }
      records.each { record ->
        def file = queryPom(record.group,record.name,record.version)
        record.poms = []
        def seen = []
        for (def depth = 0; depth < 8; depth++) {
          if (seen.contains(file.absolutePath)) throw new GradleException('Cyclic native POM parent')
          seen.add(file.absolutePath)
          record.poms.add(file.absolutePath)
          def xml = new groovy.xml.XmlSlurper().parse(file)
          if (xml.parent.size() == 0) break
          if (depth == 7) throw new GradleException('Native POM parent depth exceeds limit')
          file = queryPom(xml.parent.groupId.text(), xml.parent.artifactId.text(), xml.parent.version.text())
        }
      }
      def output = new File(System.getenv('PSST_LEGAL_OUTPUT'), p.path == ':app' ? 'android.json' : 'ios.json')
      output.text = JsonOutput.prettyPrint(JsonOutput.toJson(records)) + '\n'
      println('Resolved native legal inputs: ' + records.size() + ' artifacts for ' + (p.path == ':app' ? 'Android' : 'iOS'))
    }
  }
}
"""


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def normalize(data: bytes) -> bytes:
    text = data.decode("utf-8")
    return (
        "\n".join(
            line.rstrip(" \t")
            for line in text.replace("\r\n", "\n").replace("\r", "\n").splitlines()
        ).rstrip("\n")
        + "\n"
    ).encode()


def legal_files(data: bytes, *, nested: bool = False) -> dict[str, bytes]:
    result = {}
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        if len(archive.infolist()) > 100_000:
            raise ValueError("Excessive native archive entry count")
        for entry in archive.infolist():
            name = entry.filename
            if entry.is_dir():
                continue
            if LEGAL_NAME.search(name) and not name.lower().endswith(
                (
                    ".class",
                    ".jar",
                    ".so",
                    ".pdf",
                    ".png",
                    ".zip",
                    ".dex",
                    ".kotlin_module",
                )
            ):
                if entry.file_size > MAX_LICENSE:
                    raise ValueError(f"Oversized native legal input: {name}")
                payload = archive.read(entry)
                if payload.strip():
                    result[name] = payload
            elif not nested and name.endswith(".jar"):
                if entry.file_size > MAX_NESTED_JAR:
                    raise ValueError(f"Oversized nested native archive: {name}")
                for child, payload in legal_files(
                    archive.read(entry), nested=True
                ).items():
                    result[f"{name}!/{child}"] = payload
    return result


def pom_metadata(
    paths: list[str],
) -> tuple[list[dict], str | None, list[dict], list[str]]:
    licenses = []
    source = None
    parents = []
    copyrights = []
    for file in paths:
        path = Path(file)
        data = path.read_bytes()
        if len(data) > MAX_LICENSE:
            raise ValueError("Oversized native POM")
        tree = ET.fromstring(data)
        parents.append({"name": path.name, "sha256": sha(data)})
        current = [
            {
                "name": item.findtext("{*}name") or "",
                "url": item.findtext("{*}url") or "",
            }
            for item in tree.findall("{*}licenses/{*}license")
        ]
        if not licenses and current:
            licenses = current
        if not source:
            source = tree.findtext("{*}scm/{*}url") or tree.findtext("{*}url")
        for comment in re.findall(r"<!--([\s\S]*?)-->", data.decode("utf-8")):
            if re.search(r"copyright", comment, re.I):
                copyrights.append(normalize(comment.encode()).decode().strip())
    if not licenses:
        raise ValueError("Native dependency has no effective declared license")
    return licenses, source, parents, copyrights


def load_sources(root: Path) -> tuple[dict, dict[str, bytes]]:
    inventory = json.loads(
        (root / "shared/licenses/source-inventory.json").read_bytes()
    )
    versions = tomllib.loads((root / "shared/gradle/libs.versions.toml").read_text())[
        "versions"
    ]
    if any(
        inventory[f"{name}_version"] != versions[name] for name in ("kotlin", "skie")
    ):
        raise ValueError(
            "Native compiler/runtime notice snapshots need an explicit version refresh"
        )
    texts = {}
    for item in inventory["sources"]:
        path = root / "shared/licenses/upstream" / item["file"]
        relative = PurePosixPath(item["file"])
        if (
            relative.is_absolute()
            or ".." in relative.parts
            or path.is_symlink()
            or not path.resolve().is_relative_to(
                (root / "shared/licenses/upstream").resolve()
            )
        ):
            raise ValueError("Unsafe native source snapshot path")
        data = path.read_bytes()
        if not data.strip() or len(data) > MAX_LICENSE or sha(data) != item["sha256"]:
            raise ValueError(f"Native upstream notice drift: {item['file']}")
        if not item["source"].startswith("https://"):
            raise ValueError("Native source snapshots require HTTPS provenance")
        texts[item["file"]] = data
    return inventory, texts


def effective_license(coordinate: str, licenses: list[dict]) -> str:
    names = {item["name"] for item in licenses}
    if len(names) != 1:
        raise ValueError(f"Review multiple native licenses: {coordinate}")
    name = next(iter(names))
    if name in {
        "Apache-2.0",
        "Apache 2.0",
        "Apache License, Version 2.0",
        "The Apache License, Version 2.0",
        "The Apache Software License, Version 2.0",
    }:
        return "Apache-2.0"
    if coordinate == "org.slf4j:slf4j-api:2.0.19" and name == "MIT":
        return "MIT"
    raise ValueError(
        f"Unreviewed native dependency license/copyright: {coordinate}: {name}"
    )


def generate(root: Path, platform: str, records: list[dict]) -> dict[str, bytes]:
    source_inventory, source_texts = load_sources(root)
    input_hashes = {name: sha((root / name).read_bytes()) for name in INPUTS}
    outputs = {"AGPL-3.0-only.txt": (root / "LICENSE").read_bytes()}
    notices = [
        "psst.zip native third-party notices",
        f"Scope: {platform}; exact resolved library artifact variants below, plus pinned compiler-injected runtimes.",
        "License snapshots normalize line endings and trailing spaces only; source hashes retain the original upstream bytes.",
    ]
    components = []
    seen = set()
    for record in sorted(
        records,
        key=lambda v: (
            v["group"],
            v["name"],
            v["version"],
            v["configuration"],
            Path(v["file"]).name,
        ),
    ):
        coordinate = f"{record['group']}:{record['name']}:{record['version']}"
        artifact = Path(record["file"])
        identity = (coordinate, record["configuration"], artifact.name)
        if identity in seen:
            raise ValueError(f"Duplicate native artifact variant: {identity}")
        seen.add(identity)
        licenses, source, poms, copyrights = pom_metadata(record["poms"])
        spdx = effective_license(coordinate, licenses)
        legal = legal_files(artifact.read_bytes())
        component = {
            "coordinate": coordinate,
            "configuration": record["configuration"],
            "artifact": artifact.name,
            "artifact_sha256": sha(artifact.read_bytes()),
            "license": spdx,
            "declared_licenses": licenses,
            "source": source,
            "pom_inputs": poms,
            "embedded_notices": [],
        }
        notices += [
            "",
            coordinate,
            f"Artifact: {artifact.name}",
            f"License: {spdx}",
            f"Source: {source or 'See published Maven POM'}",
        ]
        notices.extend(copyrights)
        for name, payload in sorted(legal.items()):
            component["embedded_notices"].append(
                {
                    "path": name,
                    "sha256": sha(normalize(payload)),
                    "upstream_sha256": sha(payload),
                }
            )
            notices += [
                f"Embedded upstream legal file: {name}",
                normalize(payload).decode(),
            ]
        if spdx == "MIT":
            notices += [
                "Full upstream MIT license and copyright:",
                source_texts["slf4j-2.0.19/LICENSE.txt"].decode(),
            ]
        components.append(component)
    notices += [
        "",
        "Full POM-declared Apache License, Version 2.0:",
        source_texts["apache-2.0/LICENSE.txt"].decode(),
    ]
    extra_sources = []
    for item in source_inventory["sources"]:
        if item["file"].startswith("kotlin-") or (
            platform == "ios" and item["file"].startswith(("kotlin-native-", "skie-"))
        ):
            extra_sources.append(item)
            notices += [
                "",
                item["component"],
                f"Upstream legal file: {item['file']}",
                f"Exact source: {item['source']}",
                source_texts[item["file"]].decode(),
            ]
    inventory = {
        "schema_version": 1,
        "platform": platform,
        "scope": (
            "Android release runtime"
            if platform == "android"
            else "iOS arm64, simulator arm64 and x64 compile klibs; Kotlin/Native and SKIE runtime"
        ),
        "input_sha256": input_hashes,
        "components": components,
        "runtime_notice_sources": extra_sources,
        "notices_complete_for_scope": True,
        "notices_sha256": sha(normalize("\n".join(notices).encode())),
        "project_license_sha256": sha(outputs["AGPL-3.0-only.txt"]),
        "excluded": [
            "Project Shared framework (covered by AGPL)",
            "Build-only compiler/plugin code",
            "Apple/Android platform SDK frameworks supplied by the operating system",
        ],
    }
    outputs["dependency-inventory.json"] = (
        json.dumps(inventory, indent=2, ensure_ascii=False) + "\n"
    ).encode()
    outputs["THIRD_PARTY_NOTICES.txt"] = normalize("\n".join(notices).encode())
    return outputs


def resolve(root: Path, *, offline: bool) -> dict[str, list[dict]]:
    with tempfile.TemporaryDirectory(
        prefix="psst-native-license-resolution-"
    ) as directory:
        work = Path(directory)
        init = work / "export.gradle"
        init.write_text(GRADLE_EXPORT)
        environment = os.environ.copy()
        environment["PSST_LEGAL_OUTPUT"] = directory
        command = [
            str(root / "android/gradlew"),
            "-p",
            str(root / "android"),
            ":app:psstExportLegalArtifacts",
            ":shared:psstExportLegalArtifacts",
            "-I",
            str(init),
            "--no-daemon",
        ]
        if offline:
            command.append("--offline")
        subprocess.run(command, env=environment, check=True, cwd=root)
        return {
            platform: json.loads((work / f"{platform}.json").read_bytes())
            for platform in PLATFORMS
        }


def check_snapshot_inputs(root: Path) -> None:
    load_sources(root)
    for platform, folder in PLATFORMS.items():
        output = root / folder
        inventory = json.loads((output / "dependency-inventory.json").read_bytes())
        expected = {name: sha((root / name).read_bytes()) for name in INPUTS}
        if inventory["input_sha256"] != expected:
            raise ValueError(f"Stale native inventory build inputs: {platform}")
        if (
            sha((output / "THIRD_PARTY_NOTICES.txt").read_bytes())
            != inventory["notices_sha256"]
        ):
            raise ValueError(f"Native aggregate notices drift: {platform}")
        if (output / "AGPL-3.0-only.txt").read_bytes() != (
            root / "LICENSE"
        ).read_bytes():
            raise ValueError(f"Native project license drift: {platform}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    parser.add_argument(
        "--offline",
        action="store_true",
        help="Use only artifacts and POMs already present in the Gradle cache",
    )
    parser.add_argument(
        "--check-inputs-only",
        action="store_true",
        help="Check build/snapshot freshness without claiming resolved-artifact verification",
    )
    parser.add_argument(
        "--resolved-inputs",
        type=Path,
        help="Directory with trusted Android/iOS Gradle-export JSON files (tests/bootstrap)",
    )
    args = parser.parse_args()
    if args.check_inputs_only:
        check_snapshot_inputs(ROOT)
        print(
            "Native notice source/build input freshness passed (artifact resolution not run)"
        )
        return
    records = (
        {
            platform: json.loads(
                (args.resolved_inputs / f"{platform}.json").read_bytes()
            )
            for platform in PLATFORMS
        }
        if args.resolved_inputs
        else resolve(ROOT, offline=args.offline)
    )
    for platform, folder in PLATFORMS.items():
        outputs = generate(ROOT, platform, records[platform])
        destination = ROOT / folder
        destination.mkdir(parents=True, exist_ok=True)
        for name, data in outputs.items():
            path = destination / name
            if args.check:
                if not path.exists() or path.read_bytes() != data:
                    raise ValueError(f"Stale native notices: {platform}/{name}")
            else:
                path.write_bytes(data)
        print(
            f"Native notices {'verified' if args.check else 'generated'}: {platform}, {len(records[platform])} exact artifact variants"
        )


if __name__ == "__main__":
    main()
