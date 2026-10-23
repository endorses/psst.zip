"""Validate tag push identity before invoking normal pinned workflow actions.

Signing belongs to actions/attest in release.yml; this module runs no downloaded
JavaScript, discovers no runner runtimes, and wraps no attestation action.
"""

from release_artifacts import COMMIT, require


def validate_tag_push_event(
    record: object, *, repository: str, version: str, commit: str
) -> None:
    """Bind tag pushes to their expanded commit, including annotated tags."""
    # GitHub's push schema specifies head_commit as the commit pointed to by
    # after, even when after identifies an annotated tag object.
    require(
        isinstance(record, dict)
        and isinstance(record.get("repository"), dict)
        and record["repository"].get("visibility") == "public"
        and record["repository"].get("full_name") == repository
        and record.get("ref") == "refs/tags/" + version
        and isinstance(record.get("after"), str)
        and COMMIT.fullmatch(record["after"]) is not None
        and isinstance(record.get("head_commit"), dict)
        and record["head_commit"].get("id") == commit,
        "Signing requires the exact public repository push event",
    )
