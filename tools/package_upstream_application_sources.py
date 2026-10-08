#!/usr/bin/env python3
"""Retain pinned upstream inputs without claiming generated-output correspondence.

Policy comes exclusively from the selected application's committed catalog and
dependency locks. Archives are read in memory, never extracted or executed. The
original upstream bytes remain untouched inside a deterministic source offering.
"""

from __future__ import annotations

import argparse
import hashlib
import http.client
import io
from pathlib import Path, PurePosixPath
import re
import tarfile
import time
import zlib

from release_artifacts import (
    COMMIT,
    DIGEST,
    VERSION,
    create_output,
    fields,
    git,
    json_bytes,
    matches,
    read_json,
    repository_name,
    require,
)

ROOT = Path(__file__).resolve().parents[1]
CATALOG = "tools/upstream-application-sources.json"
LOCK = "web/package-lock.json"
GO_LOCKS = ("backend/go.mod", "backend/go.sum")
RECORD = "upstream-source-collection.json"
# The embedded jsQR source includes original test images (45,404,815 bytes).
# Retain its complete original archive rather than dropping source-tree inputs.
MAX_ARCHIVE = 64 * 1024**2
# The complete modernc SQLite project is 244,731,639 expanded bytes and includes
# nested generator modules omitted by the Go proxy ZIP. Retain them unchanged.
MAX_ASSET = 256 * 1024**2
MAX_EXPANDED = 512 * 1024**2
MAX_METADATA = 4 * 1024**2
MAX_MEMBERS = 20_000
UNAPPROVED = {
    "package_source_association_verified": False,
    "package_output_correspondence_verified": False,
    "package_reproduction_verified": False,
    "corresponding_source_completeness_verified": False,
    "publication_authorized": False,
}


def sha256(raw: bytes) -> str:
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def context(repository: str, version: str, commit: str) -> dict:
    return {
        "repository": repository_name(repository),
        "version": matches(version, VERSION, "Invalid stable upstream source version"),
        "commit": matches(
            commit, COMMIT, "Upstream inputs require a full source commit"
        ),
    }


def upstream_repository(value: object) -> str:
    """Preserve GitHub source-tree case without relaxing release identities."""
    require(isinstance(value, str), "Invalid upstream repository")
    repository_name(value.lower())
    return value


def regular(path: Path, maximum: int) -> bytes:
    require(
        path.is_file()
        and not any(item.is_symlink() for item in (path, *path.parents))
        and 0 < path.stat().st_size <= maximum,
        "Unsafe or oversized upstream input file",
    )
    with path.open("rb") as stream:
        raw = stream.read(maximum + 1)
    require(0 < len(raw) <= maximum, "Upstream input exceeds bounds")
    return raw


def safe_path(name: str, *, directory: bool = False) -> str:
    require(isinstance(name, str), "Invalid upstream archive path")
    normalized = name.rstrip("/") if directory else name
    path = PurePosixPath(normalized)
    require(
        isinstance(name, str)
        and normalized
        and not path.is_absolute()
        and all(part not in {".", ".."} for part in path.parts)
        and str(path) == normalized
        and "\\" not in name
        and not any(ord(item) < 32 or ord(item) == 127 for item in name),
        "Unsafe upstream archive path",
    )
    return normalized


def committed(root: Path, commit: str, name: str) -> bytes:
    item = git(root, "ls-tree", commit, "--", name).split(b"\t", 1)
    require(
        len(item) == 2
        and item[0].split()[:2] == [b"100644", b"blob"]
        and item[1].rstrip(b"\n").decode() == name,
        "Upstream policy must be a regular exact committed input",
    )
    raw = git(root, "show", commit + ":" + name)
    require(0 < len(raw) <= MAX_METADATA, "Committed upstream policy exceeds bounds")
    return raw


def go_requirements(raw: bytes) -> dict[str, str]:
    """Read ordinary committed require/replace directives without executing Go.

    Unsupported quoting, inline blocks and block comments fail closed rather
    than allowing a replacement to hide from this small policy reader.
    """
    try:
        text = raw.decode("utf-8")
    except UnicodeError:
        require(False, "Invalid committed backend module encoding")
    require(
        "/*" not in text and "*/" not in text, "Unsupported backend module comments"
    )
    requirements, replaced, block = {}, set(), None
    for original in text.splitlines():
        line = original.split("//", 1)[0].strip()
        if not line:
            continue
        if line == ")":
            require(block is not None, "Unexpected backend module block terminator")
            block = None
            continue
        tokens = line.split()
        directive = block or tokens.pop(0)
        if directive not in {"require", "replace"}:
            continue
        if block is None and tokens == ["("]:
            block = directive
            continue
        require(
            tokens
            and not any('"' in item or "(" in item or ")" in item for item in tokens),
            "Unsupported backend module directive",
        )
        if directive == "require":
            require(
                len(tokens) == 2 and tokens[0] not in requirements,
                "Invalid or duplicate backend module requirement",
            )
            requirements[tokens[0]] = tokens[1]
        else:
            require(
                tokens.count("=>") == 1
                and tokens.index("=>") in {1, 2}
                and tokens.index("=>") < len(tokens) - 1,
                "Invalid backend module replacement",
            )
            replaced.add(tokens[0])
    require(block is None, "Unclosed backend module block")
    for module in replaced:
        requirements.pop(module, None)
    return requirements


def _policy_inputs(
    root: Path, source: dict
) -> tuple[bytes, bytes, list[dict], dict[str, bytes]]:
    require(
        git(root, "rev-parse", "--verify", source["commit"] + "^{commit}")
        .decode()
        .strip()
        == source["commit"],
        "Application source commit is unavailable",
    )
    catalog_raw = committed(root, source["commit"], CATALOG)
    lock_raw = committed(root, source["commit"], LOCK)
    catalog = fields(
        read_json(catalog_raw),
        {"schema_version", "kind", "upstreams"},
        "upstream catalog",
    )
    require(
        type(catalog["schema_version"]) is int
        and catalog["schema_version"] == 1
        and catalog["kind"] == "pinned-upstream-application-source-inputs"
        and isinstance(catalog["upstreams"], list)
        and 0 < len(catalog["upstreams"]) <= 32,
        "Invalid committed upstream catalog",
    )
    lock = read_json(lock_raw)
    require(
        isinstance(lock, dict)
        and type(lock.get("lockfileVersion")) is int
        and lock["lockfileVersion"] == 3
        and isinstance(lock.get("packages"), dict),
        "Invalid exact web dependency lock",
    )
    identifiers, associations, records = set(), {}, []
    go_inputs, go_versions = {}, None
    for record in catalog["upstreams"]:
        require(isinstance(record, dict), "Invalid pinned upstream record")
        required = {
            "id",
            "repository",
            "commit",
            "archive",
            "inspect_paths",
        }
        require(
            ("packages" in record) != ("go_modules" in record),
            "Exactly one package or Go module association is required",
        )
        association_key = "packages" if "packages" in record else "go_modules"
        required.add(association_key)
        optional = {
            name
            for name in ("relationship", "source_fixture_links", "source_host")
            if name in record
        }
        fields(
            record,
            required | optional,
            "pinned upstream input",
        )
        identifier = matches(
            record["id"], re.compile(r"[a-z][a-z0-9-]{0,63}\Z"), "Invalid upstream ID"
        )
        require(identifier not in identifiers, "Duplicate upstream ID")
        identifiers.add(identifier)
        upstream_repository(record["repository"])
        matches(record["commit"], COMMIT, "Upstream reference must be a full commit")
        url(record)
        if record.get("source_host") == "gitlab":
            require(
                association_key == "go_modules"
                and set(record["go_modules"])
                == {"modernc.org/" + record["repository"].split("/")[1]},
                "Canonical modernc project must match its locked module",
            )
        archive = fields(
            record["archive"], {"file", "sha256", "size"}, "pinned source archive"
        )
        require(
            archive["file"]
            == record["repository"].split("/")[1] + "-" + record["commit"] + ".tar.gz",
            "Unexpected upstream archive filename",
        )
        matches(archive["sha256"], DIGEST, "Invalid pinned upstream checksum")
        require(
            type(archive["size"]) is int and 0 < archive["size"] <= MAX_ARCHIVE,
            "Pinned upstream archive exceeds bounds",
        )
        require(
            isinstance(record[association_key], dict) and record[association_key],
            "Missing locked package associations",
        )
        if association_key == "go_modules" and go_versions is None:
            go_inputs = {
                name: committed(root, source["commit"], name) for name in GO_LOCKS
            }
            go_versions = go_requirements(go_inputs[GO_LOCKS[0]])
        for package, version in record[association_key].items():
            if association_key == "go_modules":
                require(
                    package in {"modernc.org/sqlite", "modernc.org/libc"}
                    and isinstance(version, str)
                    and re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", version),
                    "Invalid locked Go module association",
                )
                require(
                    go_versions.get(package) == version,
                    "Pinned upstream Go module differs from exact unreplaced backend lock",
                )
                continue
            require(
                isinstance(package, str)
                and re.fullmatch(r"(?:@[a-z0-9_.-]+/)?[a-z0-9_.-]+", package)
                and isinstance(version, str)
                and re.fullmatch(
                    r"[0-9]+\.[0-9]+\.[0-9]+(?:[-+][a-zA-Z0-9.-]+)?", version
                ),
                "Invalid locked package association",
            )
            associations.setdefault(package, []).append(record)
            item = lock["packages"].get("node_modules/" + package)
            require(
                isinstance(item, dict)
                and item.get("version") == version
                and not item.get("link"),
                "Pinned upstream package version differs from exact web lock",
            )
        inspected = record["inspect_paths"]
        require(
            isinstance(inspected, list)
            and 0 < len(inspected) <= 64
            and len(set(inspected)) == len(inspected),
            "Invalid upstream inspected-input inventory",
        )
        for name in inspected:
            safe_path(name)
        fixture_links(record)
        records.append(record)
        if "relationship" in record:
            relationship = record["relationship"]
            require(isinstance(relationship, dict), "Invalid upstream relationship")
            build_configuration = relationship.get("kind") == "build-configuration"
            fields(
                relationship,
                {"kind", "name", "version", "package"}
                | (
                    {"integrity", "configuration_sha256"}
                    if build_configuration
                    else set()
                ),
                "upstream relationship",
            )
            require(
                relationship["kind"] in {"embedded-component", "build-configuration"}
                and isinstance(relationship["name"], str)
                and re.fullmatch(
                    r"(?:@[a-z0-9_.-]+/)?[a-z0-9_.-]+", relationship["name"]
                )
                and isinstance(relationship["version"], str)
                and re.fullmatch(
                    r"[0-9]+\.[0-9]+\.[0-9]+(?:[-+][a-zA-Z0-9.-]+)?",
                    relationship["version"],
                )
                and isinstance(relationship["package"], str)
                and association_key == "packages"
                and set(record["packages"]) == {relationship["package"]}
                and relationship["name"] != relationship["package"],
                "Invalid embedded component association",
            )
            if build_configuration:
                require(
                    identifier == "jsbt"
                    and record["repository"] == "paulmillr/jsbt"
                    and relationship["name"] == "@paulmillr/jsbt"
                    and relationship["version"] == "0.7.1"
                    and {"LICENSE", "package.json", "tsconfig.json"} <= set(inspected)
                    and isinstance(relationship["integrity"], str)
                    and re.fullmatch(
                        r"sha512-[A-Za-z0-9+/]{86}==", relationship["integrity"]
                    ),
                    "Unreviewed external build configuration",
                )
                matches(
                    relationship["configuration_sha256"],
                    DIGEST,
                    "External configuration digest missing",
                )
    for related in associations.values():
        require(
            sum("relationship" not in record for record in related) == 1,
            "Each locked package requires exactly one primary upstream association",
        )
    return (
        catalog_raw,
        lock_raw,
        sorted(records, key=lambda item: item["id"]),
        go_inputs,
    )


def policy(root: Path, source: dict) -> tuple[bytes, bytes, list[dict]]:
    catalog, lock, records, _ = _policy_inputs(root, source)
    return catalog, lock, records


def url(record: dict) -> str:
    if record.get("source_host") == "gitlab":
        require(
            record["repository"] in {"cznic/sqlite", "cznic/libc"},
            "Only the exact canonical modernc project routes are supported",
        )
        matches(record["commit"], COMMIT, "Upstream reference must be a full commit")
        name = record["repository"].split("/")[1]
        return (
            "https://gitlab.com/"
            + record["repository"]
            + "/-/archive/"
            + record["commit"]
            + "/"
            + name
            + "-"
            + record["commit"]
            + ".tar.gz"
        )
    if "source_host" in record:
        require(
            record["source_host"] == "musl"
            and record["repository"] == "musl-libc/musl",
            "Only the exact canonical musl source host is supported",
        )
        matches(record["commit"], COMMIT, "Upstream reference must be a full commit")
        return (
            "https://git.musl-libc.org/cgit/musl/snapshot/musl-"
            + record["commit"]
            + ".tar.gz"
        )
    return (
        "https://codeload.github.com/"
        + record["repository"]
        + "/tar.gz/"
        + record["commit"]
    )


def official_fetch(url: str) -> bytes:
    """Fixed-host HTTPS; no authentication, redirects, environment proxies or tags."""
    if re.fullmatch(
        r"https://codeload\.github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/tar\.gz/[0-9a-f]{40}",
        url,
    ):
        host = "codeload.github.com"
    elif re.fullmatch(
        r"https://gitlab\.com/cznic/(sqlite|libc)/-/archive/([0-9a-f]{40})/\1-\2\.tar\.gz",
        url,
    ):
        host = "gitlab.com"
    else:
        require(
            re.fullmatch(
                r"https://git\.musl-libc\.org/cgit/musl/snapshot/musl-[0-9a-f]{40}\.tar\.gz",
                url,
            ),
            "Only official full-commit upstream URLs are accepted",
        )
        host = "git.musl-libc.org"
    connection = http.client.HTTPSConnection(host, timeout=30)
    started, raw = time.monotonic(), bytearray()
    try:
        connection.request(
            "GET",
            url.removeprefix("https://" + host),
            headers={"User-Agent": "psst.zip-upstream-source-inputs"},
        )
        response = connection.getresponse()
        require(
            response.status == 200,
            "Official upstream request failed; redirects are refused",
        )
        length = response.getheader("Content-Length")
        require(
            length is None or length.isdecimal() and 0 < int(length) <= MAX_ARCHIVE,
            "Official source archive exceeds bounds",
        )
        while chunk := response.read1(min(1024**2, MAX_ARCHIVE - len(raw) + 1)):
            raw.extend(chunk)
            require(
                len(raw) <= MAX_ARCHIVE and time.monotonic() - started < 180,
                "Official source acquisition exceeds bounds",
            )
        require(
            raw and time.monotonic() - started < 180, "Empty or expired source response"
        )
        return bytes(raw)
    finally:
        connection.close()


def expanded_gzip(raw: bytes, maximum: int = MAX_EXPANDED) -> bytes:
    decoder = zlib.decompressobj(31)
    expanded = decoder.decompress(raw, maximum + 1)
    require(
        len(expanded) <= maximum
        and decoder.eof
        and not decoder.unconsumed_tail
        and not decoder.unused_data,
        "Incomplete, concatenated or oversized upstream gzip stream",
    )
    return expanded


class SourceTarInfo(tarfile.TarInfo):
    """Permit GitHub's bounded global commit comment, never path/link rewrites."""

    def _proc_member(self, archive):
        if self.type == tarfile.XGLTYPE:
            require(
                not archive.pax_headers and 0 < self.size <= 128,
                "Unsupported upstream global metadata",
            )
            position = archive.fileobj.tell()
            raw = archive.fileobj.read(self.size)
            archive.fileobj.seek(position)
            match = re.fullmatch(rb"([0-9]{1,3}) comment=([0-9a-f]{40})\n", raw)
            require(
                match is not None and int(match[1]) == len(raw),
                "Only bounded global source commit comments are accepted",
            )
        else:
            require(
                self.type
                in {
                    tarfile.REGTYPE,
                    tarfile.AREGTYPE,
                    tarfile.DIRTYPE,
                    tarfile.SYMTYPE,
                },
                "Unsupported upstream source archive header or entry type",
            )
        return super()._proc_member(archive)


def tar_members(raw: bytes, *, upstream: bool) -> list[tuple[tarfile.TarInfo, bytes]]:
    expanded = expanded_gzip(raw)
    entries, seen, total = [], set(), 0
    with tarfile.open(
        fileobj=io.BytesIO(expanded), mode="r:", tarinfo=SourceTarInfo
    ) as archive:
        for member in archive:
            name = safe_path(member.name, directory=member.isdir())
            require(
                name not in seen
                and len(seen) < MAX_MEMBERS
                and (member.isfile() or upstream and (member.isdir() or member.issym()))
                and not member.sparse
                and (
                    not member.pax_headers
                    or upstream
                    and set(member.pax_headers) == {"comment"}
                )
                and (
                    member.issym()
                    and upstream
                    and member.size == 0
                    or not member.linkname
                )
                and not member.mode & 0o7000,
                "Linked, duplicate or unsupported upstream archive member",
            )
            seen.add(name)
            total += member.size
            require(
                0 <= member.size <= MAX_EXPANDED and total <= MAX_EXPANDED,
                "Expanded upstream archive exceeds bounds",
            )
            content = archive.extractfile(member).read() if member.isfile() else b""
            require(len(content) == member.size, "Upstream member size differs")
            entries.append((member, content))
        end = archive.offset
    require(
        entries
        and len(expanded) % 512 == 0
        and len(expanded) - end >= 1024
        and not any(expanded[end:]),
        "Hidden or incomplete upstream tar payload",
    )
    return entries


def fixture_links(record: dict) -> dict[str, str]:
    """Review only package-self test links as inert, pinned archive metadata."""
    links = record.get("source_fixture_links", {})
    require(
        isinstance(links, dict) and len(links) <= 64, "Invalid source fixture links"
    )
    for path, target in links.items():
        safe_path(path)
        require(
            isinstance(path, str)
            and re.fullmatch(
                r"packages/[A-Za-z0-9_.-]+/test/node_modules/current-package", path
            )
            and target == "../..",
            "Only exact reviewed package-self test links may be retained",
        )
    return links


def inspect(raw: bytes, record: dict) -> dict:
    require(
        len(raw) == record["archive"]["size"]
        and sha256(raw) == record["archive"]["sha256"],
        "Official upstream archive differs from committed pin",
    )
    prefix = record["repository"].split("/")[1] + "-" + record["commit"]
    inventory, observed, found = [], {}, set()
    reviewed_links, retained_links = fixture_links(record), set()
    entries = tar_members(raw, upstream=True)
    members = {
        safe_path(member.name, directory=member.isdir()): member
        for member, _ in entries
    }
    for member, content in entries:
        require(
            not member.pax_headers
            or member.pax_headers == {"comment": record["commit"]},
            "Global archive source commit differs from pinned upstream",
        )
        name = safe_path(member.name, directory=member.isdir())
        require(
            name == prefix or name.startswith(prefix + "/"),
            "Upstream archive root differs from full source commit",
        )
        relative = name.removeprefix(prefix + "/") if name != prefix else ""
        for parent in PurePosixPath(name).parents:
            ancestor = members.get(str(parent))
            require(
                ancestor is None or ancestor.isdir(),
                "Upstream member has a non-directory ancestor",
            )
        link = {}
        if member.issym():
            target = source_link_target(name, member.linkname, prefix)
            package_self = relative in reviewed_links
            if package_self:
                require(
                    member.linkname == reviewed_links[relative]
                    and target == prefix + "/" + relative.split("/test/")[0]
                    and target in members
                    and members[target].isdir(),
                    "Reviewed package-self test link differs from original source",
                )
                retained_links.add(relative)
            require(
                package_self
                or target in members
                and (members[target].isfile() or members[target].isdir())
                and not name.startswith(target + "/")
                and target != name,
                "Upstream symbolic link target is missing, linked or recursive",
            )
            link = {"link_target": member.linkname, "resolved_target": target}
            if package_self:
                link["source_fixture_metadata_only"] = True
        inventory.append(
            {
                "path": name,
                "kind": (
                    "file"
                    if member.isfile()
                    else "symlink" if member.issym() else "directory"
                ),
                "mode": member.mode,
                "size": member.size,
                **({"sha256": sha256(content)} if member.isfile() else {}),
                **link,
            }
        )
        if member.isfile() and relative in record["inspect_paths"]:
            found.add(relative)
            observed[relative] = {"size": len(content), "sha256": sha256(content)}
            if relative.endswith("package.json"):
                metadata = read_json(content)
                require(isinstance(metadata, dict), "Invalid upstream package manifest")
                observed[relative]["package_identity"] = {
                    key: metadata.get(key) for key in ("name", "version")
                }
    require(
        found == set(record["inspect_paths"]),
        "Pinned upstream inspected inputs are missing",
    )
    require(
        retained_links == set(reviewed_links), "Reviewed source fixture link is missing"
    )
    relationship = record.get("relationship", {})
    if relationship.get("kind") == "build-configuration":
        require(
            observed["package.json"].get("package_identity")
            == {"name": relationship["name"], "version": relationship["version"]}
            and observed["tsconfig.json"]["sha256"]
            == relationship["configuration_sha256"],
            "External configuration identity or original bytes differ from pin",
        )
    return {
        "schema_version": 1,
        "upstream": {"repository": record["repository"], "commit": record["commit"]},
        "members": sorted(inventory, key=lambda item: item["path"]),
        "observed_inputs": observed,
        **UNAPPROVED,
    }


def source_link_target(name: str, link: str, prefix: str) -> str:
    """Measure an original link without extraction or following it on disk."""
    require(
        isinstance(link, str)
        and 0 < len(link) <= 4096
        and not link.startswith("/")
        and "\\" not in link
        and not any(ord(char) < 32 or ord(char) == 127 for char in link),
        "Unsafe upstream symbolic link target",
    )
    parts = list(PurePosixPath(name).parent.parts)
    for part in link.split("/"):
        require(bool(part), "Noncanonical upstream symbolic link target")
        if part == ".":
            continue
        if part == "..":
            require(len(parts) > 1, "Upstream symbolic link escapes source root")
            parts.pop()
        else:
            parts.append(part)
    target = safe_path("/".join(parts))
    require(
        target == prefix or target.startswith(prefix + "/"),
        "Upstream symbolic link escapes source root",
    )
    return target


def tar_gzip(files: dict[str, bytes]) -> bytes:
    stream = io.BytesIO()
    with tarfile.open(
        fileobj=stream, mode="w:", format=tarfile.USTAR_FORMAT
    ) as archive:
        for name, raw in sorted(files.items()):
            member = tarfile.TarInfo(name)
            member.size, member.mode = len(raw), 0o644
            member.uid = member.gid = member.mtime = 0
            archive.addfile(member, io.BytesIO(raw))
    encoder = zlib.compressobj(9, zlib.DEFLATED, 31)
    return encoder.compress(stream.getvalue()) + encoder.flush()


def offering(
    source: dict,
    catalog: bytes,
    lock: bytes,
    records: list[dict],
    fetch,
    *,
    go_inputs: dict[str, bytes] | None = None,
) -> tuple[dict, bytes]:
    go_inputs = go_inputs or {}
    require(
        set(go_inputs)
        == (set(GO_LOCKS) if any("go_modules" in item for item in records) else set()),
        "Backend locks are required exactly for Go-associated source inputs",
    )
    files = {"inputs/" + CATALOG: catalog, "inputs/" + LOCK: lock}
    files.update({"inputs/" + name: raw for name, raw in go_inputs.items()})
    go_hashes = (
        {"backend_lock_sha256": {name: sha256(raw) for name, raw in go_inputs.items()}}
        if go_inputs
        else {}
    )
    proofs = []
    for record in records:
        raw = fetch(url(record))
        require(isinstance(raw, bytes), "Upstream acquisition returned non-byte input")
        inventory = inspect(raw, record)
        archive_name = "archives/" + record["id"] + "/" + record["archive"]["file"]
        inventory_name = "inventory/" + record["id"] + ".json"
        files[archive_name] = raw
        files[inventory_name] = json_bytes(inventory)
        proofs.append(
            {
                "id": record["id"],
                "repository": record["repository"],
                "commit": record["commit"],
                "url": url(record),
                **{
                    key: record[key]
                    for key in ("packages", "go_modules", "source_host")
                    if key in record
                },
                **(
                    {"relationship": record["relationship"]}
                    if "relationship" in record
                    else {}
                ),
                "archive": {
                    "file": archive_name,
                    "sha256": sha256(raw),
                    "size": len(raw),
                },
                "inventory": {
                    "file": inventory_name,
                    "sha256": sha256(files[inventory_name]),
                    "members": len(inventory["members"]),
                },
                "observed_inputs": inventory["observed_inputs"],
            }
        )
    metadata = {
        "schema_version": 1,
        "kind": "upstream-application-source-inputs",
        "source": source,
        "catalog_sha256": sha256(catalog),
        "web_lock_sha256": sha256(lock),
        **go_hashes,
        "upstreams": proofs,
        **UNAPPROVED,
    }
    files["upstream-inputs.json"] = json_bytes(metadata)
    asset = tar_gzip(files)
    require(len(asset) <= MAX_ASSET, "Upstream source offering exceeds bounds")
    # Recheck packaging: no concatenated/hidden payload or altered original bytes.
    require(
        {member.name: raw for member, raw in tar_members(asset, upstream=False)}
        == files,
        "Deterministic upstream offering changed inputs",
    )
    return metadata, asset


def collect(
    root: Path,
    repository: str,
    version: str,
    commit: str,
    output: Path,
    fetch=official_fetch,
) -> dict:
    source = context(repository, version, commit)
    catalog, lock, records, go_inputs = _policy_inputs(root, source)
    require(
        not output.exists()
        and not output.is_symlink()
        and output.parent.is_dir()
        and not any(
            path.is_symlink() for path in (output.parent, *output.parent.parents)
        ),
        "Upstream collection output must be a new real directory",
    )
    metadata, asset = offering(
        source, catalog, lock, records, fetch, go_inputs=go_inputs
    )
    name = "psst.zip-upstream-inputs-" + version + ".tar.gz"
    collection = {
        "schema_version": 1,
        "kind": "upstream-application-source-collection",
        "source": source,
        "catalog_sha256": sha256(catalog),
        "web_lock_sha256": sha256(lock),
        **(
            {"backend_lock_sha256": metadata["backend_lock_sha256"]}
            if go_inputs
            else {}
        ),
        "asset": {"name": name, "digest": sha256(asset), "size": len(asset)},
        "upstreams": metadata["upstreams"],
        **UNAPPROVED,
    }
    output.mkdir(mode=0o700)
    create_output(output / name, asset)
    create_output(output / RECORD, json_bytes(collection))
    return collection


def _verify(
    root: Path,
    repository: str,
    version: str,
    commit: str,
    collection: Path,
    *,
    with_source_files: bool = False,
):
    source = context(repository, version, commit)
    catalog, lock, records, go_inputs = _policy_inputs(root, source)
    require(
        collection.is_dir()
        and not any(path.is_symlink() for path in (collection, *collection.parents)),
        "Unsafe upstream collection directory",
    )
    record_raw = regular(collection / RECORD, MAX_METADATA)
    record = read_json(record_raw)
    name = "psst.zip-upstream-inputs-" + version + ".tar.gz"
    require(
        set(item.name for item in collection.iterdir()) == {RECORD, name},
        "Hidden or missing upstream collection assets",
    )
    asset = regular(collection / name, MAX_ASSET)
    outer = {
        member.name: content for member, content in tar_members(asset, upstream=False)
    }
    expected_names = {"inputs/" + CATALOG, "inputs/" + LOCK, "upstream-inputs.json"}
    expected_names.update("inputs/" + name for name in go_inputs)
    expected_names.update(
        "archives/" + item["id"] + "/" + item["archive"]["file"] for item in records
    )
    expected_names.update("inventory/" + item["id"] + ".json" for item in records)
    require(
        set(outer) == expected_names,
        "Hidden or missing upstream source offering members",
    )
    require(
        outer["inputs/" + CATALOG] == catalog and outer["inputs/" + LOCK] == lock,
        "Offering policy differs from exact committed inputs",
    )
    require(
        all(outer["inputs/" + name] == raw for name, raw in go_inputs.items()),
        "Offering backend locks differ from exact committed inputs",
    )
    by_url = {
        url(item): outer["archives/" + item["id"] + "/" + item["archive"]["file"]]
        for item in records
    }
    metadata, rebuilt = offering(
        source, catalog, lock, records, by_url.__getitem__, go_inputs=go_inputs
    )
    expected = {
        "schema_version": 1,
        "kind": "upstream-application-source-collection",
        "source": source,
        "catalog_sha256": sha256(catalog),
        "web_lock_sha256": sha256(lock),
        **(
            {"backend_lock_sha256": metadata["backend_lock_sha256"]}
            if go_inputs
            else {}
        ),
        "asset": {"name": name, "digest": sha256(rebuilt), "size": len(rebuilt)},
        "upstreams": metadata["upstreams"],
        **UNAPPROVED,
    }
    require(
        record == expected and asset == rebuilt,
        "Upstream collection record or retained payload differs from independent replay",
    )
    replay = {
        "schema_version": 1,
        "kind": "upstream-application-source-input-replay",
        "source": source,
        "asset": expected["asset"],
        "catalog_sha256": sha256(catalog),
        "web_lock_sha256": sha256(lock),
        **(
            {"backend_lock_sha256": metadata["backend_lock_sha256"]}
            if go_inputs
            else {}
        ),
        "collection_sha256": sha256(record_raw),
        "package_inputs_replayed": True,
        **UNAPPROVED,
    }

    if not with_source_files:
        return replay
    # The independent offering replay above has already checked the complete
    # original archives against committed pins. Read browser originals only;
    # do not unpack the much larger backend projects a second time.
    sources = {}
    for item in records:
        if "packages" not in item:
            continue
        prefix = item["repository"].split("/")[1] + "-" + item["commit"] + "/"
        original = by_url[url(item)]
        sources[item["id"]] = {
            "record": item,
            "files": {
                member.name.removeprefix(prefix): content
                for member, content in tar_members(original, upstream=True)
                if member.isfile()
            },
        }
    require(
        regular(collection / RECORD, MAX_METADATA) == record_raw
        and sha256(regular(collection / name, MAX_ASSET)) == replay["asset"]["digest"],
        "Upstream inputs changed during preferred source replay",
    )
    return replay, sources


def verify(
    root: Path, repository: str, version: str, commit: str, collection: Path
) -> dict:
    return _verify(root, repository, version, commit, collection)


def verify_source_files(
    root: Path, repository: str, version: str, commit: str, collection: Path
) -> tuple[dict, dict]:
    """Replay one offering and expose validated browser originals to its consumer.

    No extraction, upstream execution, authentication or approval is performed.
    The consumer must bind the returned asset digest to its publication subjects.
    """
    return _verify(
        root, repository, version, commit, collection, with_source_files=True
    )


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("collect", "verify"), default="collect")
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--collection", type=Path)
    args = parser.parse_args()
    if args.mode == "collect":
        require(args.collection is None, "Collection path is only accepted for replay")
        collect(args.root, args.repository, args.version, args.commit, args.output)
    else:
        require(
            args.collection is not None,
            "Replay requires the retained collection directory",
        )
        result = verify(
            args.root, args.repository, args.version, args.commit, args.collection
        )
        require(
            not args.output.exists()
            and not args.output.is_symlink()
            and args.output.parent.is_dir()
            and not any(
                path.is_symlink()
                for path in (args.output.parent, *args.output.parent.parents)
            ),
            "Replay output must be a new file in a real directory",
        )
        create_output(args.output, json_bytes(result))


if __name__ == "__main__":
    try:
        main()
    except (Exception, KeyboardInterrupt):
        raise SystemExit(
            "Upstream source input collection/replay failed; no completeness or publication approval was created."
        ) from None
