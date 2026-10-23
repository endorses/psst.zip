"""Read the exact committed publication and vulnerability-exception policy.

GitHub environment rules own reviewer authorization; the source policy retains
only the environment identity and owners authorized for narrow scan exceptions.
"""

import re
from pathlib import Path

from publish_container_release import SOURCE_REVIEW_POLICY, Binding, sha256
from release_artifacts import fields, git, read_json, require
from temporary_caddy_acceptance import validate_policy as validate_caddy_acceptance


def committed_policy(root: Path, binding: Binding) -> tuple[dict, dict]:
    from generate_release_gate_reports import checked_binding

    checked_binding(binding)
    listing = git(root, "ls-tree", binding.commit, "--", SOURCE_REVIEW_POLICY)
    match = re.fullmatch(
        rb"100644 blob ([a-f0-9]{40})\t"
        + re.escape(SOURCE_REVIEW_POLICY.encode())
        + rb"\n",
        listing,
    )
    require(match is not None, "Review policy must be a regular exact committed file")
    raw = git(root, "cat-file", "blob", binding.commit + ":" + SOURCE_REVIEW_POLICY)
    require(len(raw) <= 16384, "Distribution policy exceeds bounds")
    value = read_json(raw)
    expected = {"schema_version", "kind", "environment", "authorized_exception_owners"}
    if isinstance(value, dict) and "temporary_caddy_exception" in value:
        expected.add("temporary_caddy_exception")
    policy = fields(
        value,
        expected,
        "committed container publication policy",
    )
    require(
        type(policy["schema_version"]) is int
        and policy["schema_version"] == 1
        and policy["kind"] == "container-publication-policy"
        and isinstance(policy["environment"], str)
        and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]{0,63}", policy["environment"])
        and isinstance(policy["authorized_exception_owners"], list)
        and 0 < len(policy["authorized_exception_owners"]) <= 6
        and all(
            isinstance(user, str) and re.fullmatch(r"[a-z0-9][a-z0-9-]{0,38}", user)
            for user in policy["authorized_exception_owners"]
        )
        and len(set(policy["authorized_exception_owners"]))
        == len(policy["authorized_exception_owners"]),
        "Invalid committed container publication policy",
    )
    if "temporary_caddy_exception" in policy:
        validate_caddy_acceptance(
            policy["temporary_caddy_exception"], policy["authorized_exception_owners"]
        )
    return policy, {
        "path": SOURCE_REVIEW_POLICY,
        "source_commit": binding.commit,
        "record_digest": sha256(raw),
        "git_blob": match[1].decode(),
    }
