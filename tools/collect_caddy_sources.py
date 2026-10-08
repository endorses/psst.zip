#!/usr/bin/env python3
"""Retain Caddy's exact official buildable source, distribution assets and notices.

This produces review inputs, never publication approval. The selected immutable
official image supplies the Docker recipe revision and release binary checksum.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import tarfile
import tempfile
import urllib.error
import urllib.parse
import urllib.request

from collect_runtime_notices import (
    EXPANDED_LIMIT,
    LIMIT,
    NOTICE_NAME,
    RECIPE_LIMIT,
    command,
    file_hash,
    image_info,
    read_url,
    safe_member,
    source_notices,
)
from release_artifacts import (
    COMMIT,
    DIGEST,
    InvalidRelease,
    json_bytes,
    matches,
    require,
)

OFFICIAL = re.compile(
    r"(?:docker\.io/library/)?caddy(?::[a-zA-Z0-9_.-]+)?@(sha256:[a-f0-9]{64})\Z"
)
RECIPE = re.compile(
    r"https://github\.com/caddyserver/caddy-docker\.git#([a-f0-9]{40}):([0-9.]+/alpine)\Z"
)
VERSION = re.compile(r"v[0-9]+\.[0-9]+\.[0-9]+\Z")
GO_VERSION = re.compile(r"go[0-9]+\.[0-9]+\.[0-9]+\Z")
GO_SOURCE_POLICY = Path(__file__).resolve().with_name("go-runtime-sources.json")
DOWNLOAD_HOSTS = {
    "github.com",
    "codeload.github.com",
    "objects.githubusercontent.com",
    "release-assets.githubusercontent.com",
}


class PublicRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, new_url):
        return None


def public_download(url: str, limit: int = LIMIT) -> bytes:
    """Allow only public GitHub download redirects and never attach credentials."""
    for _ in range(4):
        parsed = urllib.parse.urlsplit(url)
        require(
            parsed.scheme == "https"
            and parsed.hostname in DOWNLOAD_HOSTS
            and not parsed.username
            and not parsed.password
            and parsed.port in {None, 443},
            "Untrusted public source download",
        )
        request = urllib.request.Request(
            url, headers={"User-Agent": "psst.zip-runtime-source-collector"}
        )
        try:
            with urllib.request.build_opener(PublicRedirect()).open(
                request, timeout=60
            ) as response:
                data = response.read(limit + 1)
                require(len(data) <= limit, "Caddy source download exceeds limit")
                return data
        except urllib.error.HTTPError as error:
            if error.code not in {301, 302, 303, 307, 308}:
                raise
            location = error.headers.get("Location")
            error.close()
            require(bool(location), "Public source redirect lacks location")
            url = urllib.parse.urljoin(url, location)
    raise InvalidRelease("Excessive public source redirects")


def recipe_identity(index: dict, base: str, arch: str) -> tuple[str, str]:
    pinned = matches(base, OFFICIAL, "Caddy base must be an immutable official image")
    require(
        index.get("digest") == OFFICIAL.fullmatch(pinned).group(1),
        "Caddy index digest differs",
    )
    descriptors = [
        item
        for item in index.get("manifests", [])
        if item.get("platform", {}).get("os") == "linux"
        and item.get("platform", {}).get("architecture") == arch
    ]
    require(len(descriptors) == 1, "Caddy index native descriptor is ambiguous")
    annotations = descriptors[0].get("annotations", {})
    value = matches(
        annotations.get("org.opencontainers.image.source"),
        RECIPE,
        "Official Caddy recipe source is unbound",
    )
    commit, directory = RECIPE.fullmatch(value).groups()
    require(
        annotations.get("org.opencontainers.image.revision") == commit,
        "Caddy recipe revision differs",
    )
    return commit, directory


def archive_member(
    archive: Path, suffix: str, limit: int = RECIPE_LIMIT, *, exact: bool = False
) -> bytes:
    with tarfile.open(archive) as source:
        found = []
        count = 0
        for entry in source:
            safe_member(entry.name)
            count += 1
            require(count <= 100_000, "Caddy source entry count exceeds limit")
            name = str(safe_member(entry.name))
            if entry.isfile() and (
                name == suffix or (not exact and name.endswith("/" + suffix))
            ):
                require(entry.size <= limit, "Caddy source member exceeds limit")
                found.append(source.extractfile(entry).read())
        require(len(found) == 1, "Missing or ambiguous Caddy source member")
        return found[0]


def verify_modules(info: dict, go_sum: bytes, modules: bytes) -> None:
    sums = set(go_sum.decode().splitlines())
    declarations = set()
    for line in modules.decode().splitlines():
        fields = line.split()
        if len(fields) == 3 and fields[0] == "#":
            declarations.add((fields[1], fields[2]))
    dependencies = info.get("Deps")
    require(
        isinstance(dependencies, list) and dependencies,
        "Caddy binary dependency information missing",
    )
    for dependency in dependencies:
        require(
            not dependency.get("Replace"),
            "Caddy replacement requires separate source review",
        )
        path, version = dependency.get("Path"), dependency.get("Version")
        require(
            (path, version) in declarations, "Caddy vendored module differs from binary"
        )
        if dependency.get("Sum"):
            require(
                f"{path} {version} {dependency['Sum']}" in sums,
                "Caddy source module checksum differs from binary",
            )


def go_source_contents(
    archive: Path, version: str, commit: str
) -> tuple[dict[str, bytes], str | None]:
    """Read the complete original tree, without executing or opening test archives."""
    matches(version, GO_VERSION, "Unsupported Go runtime source version")
    matches(commit, COMMIT, "Invalid Go runtime source commit")
    require(
        archive.is_file()
        and not any(path.is_symlink() for path in (archive, *archive.parents))
        and 0 < archive.stat().st_size <= LIMIT,
        "Unsafe or oversized Go runtime source archive",
    )
    prefix = "go-" + commit
    required = {
        "LICENSE",
        "PATENTS",
        "src/go.mod",
        "src/runtime/proc.go",
        "src/cmd/compile/main.go",
        "src/make.bash",
    }
    seen, originals, total, notice_total, version_digest = set(), {}, 0, 0, None
    with tarfile.open(archive, "r:gz") as source:
        for entry in source:
            name = entry.name.rstrip("/") if entry.isdir() else entry.name
            path = safe_member(name)
            require(
                str(path) == name
                and path.parts[0] == prefix
                and name not in seen
                and len(seen) < 100_000
                and not any(
                    ord(character) < 32 or ord(character) == 127 for character in name
                ),
                "Unsafe or duplicate Go runtime source member",
            )
            seen.add(name)
            require(
                (entry.isfile() or entry.isdir())
                and not entry.sparse
                and 0 <= entry.size <= LIMIT,
                "Unsupported Go runtime source member",
            )
            total += entry.size
            require(
                total <= EXPANDED_LIMIT, "Expanded Go runtime source exceeds bounds"
            )
            if not entry.isfile():
                continue
            relative = path.relative_to(prefix).as_posix()
            if relative in required:
                require(entry.size > 0, "Empty required Go runtime source member")
                required.remove(relative)
            if relative == "VERSION" or NOTICE_NAME.fullmatch(path.name):
                require(
                    entry.size <= RECIPE_LIMIT,
                    "Go runtime source notice exceeds bounds",
                )
                data = source.extractfile(entry).read()
                require(len(data) == entry.size, "Truncated Go runtime source member")
                if relative == "VERSION":
                    require(
                        data.splitlines() and data.splitlines()[0] == version.encode(),
                        "Go runtime source VERSION differs from executable",
                    )
                    version_digest = hashlib.sha256(data).hexdigest()
                else:
                    notice_total += len(data)
                    require(
                        notice_total <= 64 * 1024**2,
                        "Go runtime source notices exceed bounds",
                    )
                    originals[name] = data
            # Nested archives are unchanged source-tree test fixtures. They are
            # retained in the original archive, not recursively interpreted as
            # additional libraries or exempted malformed dependency inputs.
    require(not required, "Required Go runtime sources are missing")
    return originals, version_digest


def go_source_policy(version: str) -> dict:
    require(
        GO_SOURCE_POLICY.is_file()
        and not GO_SOURCE_POLICY.is_symlink()
        and 0 < GO_SOURCE_POLICY.stat().st_size <= RECIPE_LIMIT,
        "Missing bounded committed Go runtime source policy",
    )
    policy = json.loads(GO_SOURCE_POLICY.read_bytes())
    require(
        isinstance(policy, dict)
        and set(policy) == {"schema_version", "kind", "sources"}
        and type(policy["schema_version"]) is int
        and policy["schema_version"] == 1
        and policy["kind"] == "pinned-go-runtime-sources"
        and isinstance(policy["sources"], list)
        and 0 < len(policy["sources"]) <= 32,
        "Invalid committed Go runtime source policy",
    )
    versions = set()
    for record in policy["sources"]:
        require(
            isinstance(record, dict)
            and set(record) == {"version", "commit", "file", "url", "sha256", "size"},
            "Invalid pinned Go runtime source",
        )
        selected_version = matches(
            record["version"], GO_VERSION, "Invalid pinned Go version"
        )
        commit = matches(record["commit"], COMMIT, "Invalid pinned Go source commit")
        require(
            selected_version not in versions
            and record["file"] == "go-" + commit + ".tar.gz"
            and record["url"]
            == f"https://codeload.github.com/golang/go/tar.gz/{commit}"
            and isinstance(record["sha256"], str)
            and type(record["size"]) is int
            and 0 < record["size"] <= LIMIT,
            "Invalid pinned Go archive identity",
        )
        matches(
            "sha256:" + record["sha256"], DIGEST, "Invalid pinned Go archive checksum"
        )
        versions.add(selected_version)
    records = [record for record in policy["sources"] if record["version"] == version]
    require(
        len(records) == 1, "Executable Go version has no pinned complete runtime source"
    )
    return records[0]


def verify_go_source(folder: Path, inventory: dict) -> dict[str, bytes]:
    """Bind the full Go source archive and original legal files to its inventory."""
    record = inventory.get("go_source")
    require(
        isinstance(record, dict)
        and set(record)
        == {"file", "sha256", "size", "version", "commit", "version_file_sha256"},
        "Full Go runtime source inventory is missing or invalid",
    )
    version = matches(
        record["version"], GO_VERSION, "Invalid Go runtime source version"
    )
    commit = matches(record["commit"], COMMIT, "Invalid Go runtime source revision")
    require(
        version == inventory.get("go_version")
        and commit == inventory.get("go_source_revision")
        and record["file"] == "go-" + commit + ".tar.gz"
        and type(record["size"]) is int
        and 0 < record["size"] <= LIMIT,
        "Go runtime source identity differs",
    )
    pinned = go_source_policy(version)
    require(
        {key: record[key] for key in ("file", "sha256", "size", "version", "commit")}
        == {
            key: pinned[key] for key in ("file", "sha256", "size", "version", "commit")
        },
        "Go runtime source differs from committed pinned policy",
    )
    entries = [
        entry for entry in inventory["sources"] if entry.get("file") == record["file"]
    ]
    require(
        len(entries) == 1
        and entries[0].get("sha256") == record["sha256"]
        and entries[0].get("url") == pinned["url"],
        "Go runtime archive differs from retained source binding",
    )
    matches("sha256:" + record["sha256"], DIGEST, "Invalid Go runtime archive checksum")
    archive = folder / record["file"]
    require(
        archive.is_file()
        and not archive.is_symlink()
        and archive.stat().st_size == record["size"]
        and file_hash(archive) == record["sha256"],
        "Go runtime source archive differs",
    )
    notices, version_digest = go_source_contents(archive, version, commit)
    require(
        version_digest == record["version_file_sha256"],
        "Go runtime VERSION binding differs",
    )
    for name in ("LICENSE", "PATENTS"):
        require(
            (folder / ("go-" + name)).is_file()
            and not (folder / ("go-" + name)).is_symlink()
            and (folder / ("go-" + name)).read_bytes()
            == notices["go-" + commit + "/" + name],
            "Go runtime legal files differ from original source tree",
        )
    require(
        file_hash(archive) == record["sha256"],
        "Go runtime source changed during replay",
    )
    return notices


def collect_go_runtime_source(
    output: Path, version: str
) -> tuple[dict, dict[str, bytes]]:
    """Retain the exact pinned full compiler/runtime tree and upstream legal files."""
    require(output.is_dir() and not output.is_symlink(), "Invalid Go source output")
    retained, notices = [], {}
    go_version = matches(version, GO_VERSION, "Unsupported Go toolchain version")
    pinned_go = go_source_policy(go_version)
    go_ref = json.loads(
        command("gh", "api", f"repos/golang/go/git/ref/tags/{go_version}")
    )
    go_object = go_ref.get("object", {})
    if go_object.get("type") == "tag":
        tag = json.loads(
            command(
                "gh",
                "api",
                "repos/golang/go/git/tags/"
                + matches(go_object.get("sha"), COMMIT, "Invalid Go toolchain tag"),
            )
        )
        go_object = tag.get("object", {})
    require(go_object.get("type") == "commit", "Unbound Go toolchain source")
    go_commit = matches(
        go_object.get("sha"), COMMIT, "Invalid Go toolchain source revision"
    )
    require(
        go_commit == pinned_go["commit"],
        "Resolved Go tag differs from pinned source policy",
    )
    go_root = json.loads(
        command("gh", "api", f"repos/golang/go/contents?ref={go_commit}")
    )
    require(isinstance(go_root, list), "Invalid Go toolchain source listing")
    go_files = {
        item.get("name"): item for item in go_root if item.get("type") == "file"
    }
    require({"LICENSE", "PATENTS"} <= go_files.keys(), "Go runtime legal files missing")
    for name in (
        "LICENSE",
        "PATENTS",
        "AUTHORS",
        "CONTRIBUTORS",
        "COPYRIGHT",
        "NOTICE",
    ):
        if name not in go_files:
            continue
        url = f"https://raw.githubusercontent.com/golang/go/{go_commit}/{name}"
        data = read_url(url, RECIPE_LIMIT)
        blob = hashlib.sha1(
            b"blob " + str(len(data)).encode() + b"\0" + data
        ).hexdigest()
        require(
            blob == go_files[name].get("sha"),
            "Go runtime legal file differs from source tree",
        )
        filename = "go-" + name
        (output / filename).write_bytes(data)
        retained.append(
            {"file": filename, "sha256": hashlib.sha256(data).hexdigest(), "url": url}
        )
        notices[filename] = data
    go_archive_name = "go-" + go_commit + ".tar.gz"
    go_archive_url = pinned_go["url"]
    go_archive = public_download(go_archive_url, pinned_go["size"])
    require(
        len(go_archive) == pinned_go["size"]
        and hashlib.sha256(go_archive).hexdigest() == pinned_go["sha256"],
        "Downloaded full Go runtime source differs from pinned policy",
    )
    (output / go_archive_name).write_bytes(go_archive)
    go_notices, version_digest = go_source_contents(
        output / go_archive_name, go_version, go_commit
    )
    go_source = {
        "file": go_archive_name,
        "sha256": hashlib.sha256(go_archive).hexdigest(),
        "size": len(go_archive),
        "version": go_version,
        "commit": go_commit,
        "version_file_sha256": version_digest,
    }
    retained.append(
        {"file": go_archive_name, "sha256": go_source["sha256"], "url": go_archive_url}
    )
    for name in ("LICENSE", "PATENTS"):
        require(
            go_notices["go-" + go_commit + "/" + name]
            == (output / ("go-" + name)).read_bytes(),
            "Downloaded Go runtime legal files differ from complete source archive",
        )
    notices.update(
        {go_archive_name + "::" + name: data for name, data in go_notices.items()}
    )
    inventory = {
        "go_version": go_version,
        "go_source_revision": go_commit,
        "go_source": go_source,
        "sources": retained,
        "notices": {
            name: hashlib.sha256(data).hexdigest() for name, data in notices.items()
        },
    }
    return inventory, notices


def binary_build_info(binary: bytes) -> dict:
    """Read metadata from retained executable bytes; never execute the program."""
    require(0 < len(binary) <= LIMIT, "Go executable exceeds metadata-reader bounds")
    with tempfile.TemporaryDirectory(prefix="psst-go-build-info-") as temporary:
        executable = Path(temporary) / "program"
        executable.write_bytes(binary)
        result = json.loads(command("go", "version", "-m", "-json", str(executable)))
    require(isinstance(result, dict), "Go executable build metadata is missing")
    return result


def verify_binary_go_source(build: dict, inventory: dict) -> None:
    version = matches(
        build.get("GoVersion"), GO_VERSION, "Unsupported executable GoVersion"
    )
    require(
        version
        == inventory.get("go_version")
        == inventory.get("go_source", {}).get("version"),
        "Executable GoVersion differs from retained runtime source",
    )


def go_notice_text(notices: dict[str, bytes]) -> bytes:
    return b"psst.zip Go runtime notices\n" + b"\n".join(
        name.encode() + b"\n" + data for name, data in sorted(notices.items())
    )


def collect(image: str, base: str, output: Path) -> dict:
    matches(base, OFFICIAL, "Caddy base must be digest-pinned")
    require(not output.exists(), "Caddy output directory already exists")
    output.mkdir(mode=0o700, parents=True)
    info = image_info(image)
    index = json.loads(
        command(
            "docker",
            "buildx",
            "imagetools",
            "inspect",
            base,
            "--format",
            "{{json .Manifest}}",
        )
    )
    recipe_commit, directory = recipe_identity(index, base, info["Architecture"])
    dockerfile = read_url(
        f"https://raw.githubusercontent.com/caddyserver/caddy-docker/{recipe_commit}/{directory}/Dockerfile",
        RECIPE_LIMIT,
    ).decode()
    version_match = re.search(
        r"^ENV CADDY_VERSION[ =](v[0-9]+\.[0-9]+\.[0-9]+)$", dockerfile, re.M
    )
    require(version_match is not None, "Unsupported Caddy recipe version declaration")
    version = matches(version_match.group(1), VERSION, "Invalid Caddy version")
    dist_match = re.search(
        r"caddyserver/dist/(?:archive|raw)/([a-f0-9]{40})(?:\.tar\.gz|/)", dockerfile
    )
    require(
        dist_match is not None, "Official distribution assets are not bound to a commit"
    )
    dist_commit = dist_match.group(1)
    release = json.loads(
        command("gh", "api", f"repos/caddyserver/caddy/releases/tags/{version}")
    )
    require(release.get("tag_name") == version, "Caddy release metadata differs")
    assets = {item["name"]: item for item in release.get("assets", [])}
    short = version[1:]
    archive_name = f"caddy_{short}_buildable-artifact.tar.gz"
    binary_name = f"caddy_{short}_linux_{info['Architecture']}.tar.gz"
    names = [
        archive_name,
        archive_name + ".sig",
        archive_name.removesuffix(".tar.gz") + ".pem",
        binary_name,
        f"caddy_{short}_checksums.txt",
        f"caddy_{short}_checksums.txt.sig",
        f"caddy_{short}_checksums.txt.pem",
    ]
    retained = []
    for name in names:
        require(name in assets, "Required official Caddy source asset missing")
        asset = assets[name]
        url = f"https://github.com/caddyserver/caddy/releases/download/{version}/{name}"
        require(
            asset.get("browser_download_url") == url, "Caddy source asset URL differs"
        )
        digest = matches(
            asset.get("digest"), DIGEST, "Caddy release asset checksum missing"
        )
        data = public_download(url)
        require(
            len(data) == asset.get("size")
            and "sha256:" + hashlib.sha256(data).hexdigest() == digest,
            "Caddy release source checksum differs",
        )
        (output / name).write_bytes(data)
        retained.append({"file": name, "sha256": digest[7:], "url": url})
    for repo, commit in (("caddy-docker", recipe_commit), ("dist", dist_commit)):
        name = repo + "-" + commit + ".tar.gz"
        url = f"https://github.com/caddyserver/{repo}/archive/{commit}.tar.gz"
        data = public_download(url)
        (output / name).write_bytes(data)
        retained.append(
            {"file": name, "sha256": hashlib.sha256(data).hexdigest(), "url": url}
        )
    binary_archive = output / binary_name
    # The recipe checksum binds the upstream executable archive to the image's
    # immutable source recipe, separately from mutable GitHub release metadata.
    expected = re.search(
        rf"binArch='{re.escape(info['Architecture'])}';\s*checksum='([a-f0-9]{{128}})'",
        dockerfile,
    )
    require(expected is not None, "Caddy recipe architecture checksum missing")
    with binary_archive.open("rb") as source:
        require(
            hashlib.file_digest(source, "sha512").hexdigest() == expected.group(1),
            "Official Caddy binary recipe checksum differs",
        )
    runtime_binary = command(
        "docker",
        "run",
        "--rm",
        "--network",
        "none",
        "--read-only",
        "--cap-drop",
        "ALL",
        "--entrypoint",
        "cat",
        info["Id"],
        "/usr/bin/caddy",
    )
    upstream_binary = archive_member(binary_archive, "caddy", LIMIT)
    require(
        runtime_binary == upstream_binary,
        "Runtime Caddy binary differs from bound release executable",
    )
    with tempfile.TemporaryDirectory(prefix="psst-caddy-build-info-") as folder:
        binary = Path(folder) / "caddy"
        binary.write_bytes(runtime_binary)
        build = json.loads(command("go", "version", "-m", "-json", str(binary)))
    require(
        build.get("Main", {}).get("Path") == "caddy"
        and build.get("Main", {}).get("Version") == "(devel)",
        "Unsupported official Caddy main module",
    )
    buildable = output / archive_name
    go_sum = archive_member(buildable, "go.sum", exact=True)
    modules = archive_member(buildable, "vendor/modules.txt", exact=True)
    verify_modules(build, go_sum, modules)
    settings = {item["Key"]: item["Value"] for item in build.get("Settings", [])}
    revision = matches(
        settings.get("vcs.revision"), COMMIT, "Caddy source revision missing"
    )
    require(settings.get("vcs.modified") == "false", "Modified Caddy source build")
    source_main = read_url(
        f"https://raw.githubusercontent.com/caddyserver/caddy/{revision}/cmd/caddy/main.go",
        RECIPE_LIMIT,
    )
    require(
        archive_member(buildable, "main.go", exact=True) == source_main,
        "Caddy wrapper source differs from binary revision",
    )
    (output / "build-info.json").write_bytes(json_bytes(build))
    notices = {}
    non_archives = []
    for item in retained:
        if item["file"].endswith(".tar.gz") and item["file"] != binary_name:
            for path, content in source_notices(
                output / item["file"], non_archives=non_archives
            ).items():
                notices[item["file"] + "::" + path] = content
    go_inventory, go_notices = collect_go_runtime_source(output, build.get("GoVersion"))
    go_version = go_inventory["go_version"]
    go_commit = go_inventory["go_source_revision"]
    go_source = go_inventory["go_source"]
    retained.extend(go_inventory["sources"])
    notices.update(go_notices)
    require(notices, "Caddy source has no notices")
    (output / "THIRD_PARTY_NOTICES.txt").write_bytes(
        b"psst.zip Caddy runtime notices\n"
        + b"\n".join(
            name.encode() + b"\n" + data for name, data in sorted(notices.items())
        )
    )
    result = {
        "schema_version": 1,
        "image_id": info["Id"],
        "base_index": base,
        "version": version,
        "source_revision": revision,
        "go_version": go_version,
        "go_source_revision": go_commit,
        "go_source": go_source,
        "docker_recipe_revision": recipe_commit,
        "dist_revision": dist_commit,
        "build_info_sha256": file_hash(output / "build-info.json"),
        "sources": retained,
        "notices": {
            name: hashlib.sha256(data).hexdigest() for name, data in notices.items()
        },
        "nested_non_archives_requiring_review": non_archives,
        "upstream_signatures_verified": False,
        "review_required": True,
    }
    (output / "caddy-source-inventory.json").write_bytes(json_bytes(result))
    return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", required=True)
    parser.add_argument("--base", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        result = collect(args.image, args.base, args.output.resolve())
        print(
            json.dumps(
                {
                    "version": result["version"],
                    "sources": len(result["sources"]),
                    "review_required": True,
                }
            )
        )
    except (InvalidRelease, OSError, ValueError, tarfile.TarError) as error:
        parser.exit(1, f"Caddy source collection rejected: {error}\n")


if __name__ == "__main__":
    main()
