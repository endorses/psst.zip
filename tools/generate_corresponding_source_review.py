"""Replay corresponding-source inputs; partial facts never grant release approval.

The application archive check establishes the exact retained Git source tree and
release packaging recipe. It does not establish dependency, runtime, generator,
or preferred-form source completeness, and cannot produce a passed source gate.
"""

from __future__ import annotations

import gzip
import io
from pathlib import Path

from publish_container_release import Binding, sha256, source_digest
from release_artifacts import (
    COMMIT,
    VERSION,
    git,
    matches,
    repository_name,
    require,
)

# Same bound as the native source scanner's Git snapshot. No extraction is needed.
MAX_APPLICATION_SOURCE_BYTES = 256 * 1024**2


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
