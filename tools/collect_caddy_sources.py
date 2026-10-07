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
    LIMIT,
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
    go_version = matches(
        build.get("GoVersion"),
        re.compile(r"go[0-9]+\.[0-9]+\.[0-9]+\Z"),
        "Unsupported Caddy Go toolchain version",
    )
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
