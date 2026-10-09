"""Validate a committed, expiring acceptance of exact official Caddy findings.

This grants no signing or publication authority. Scan, compiler, upstream
signature and source correspondence must already have been verified by the caller.
"""

from datetime import datetime, timedelta, timezone
import hashlib
from pathlib import Path
import re

from release_artifacts import (
    COMMIT,
    DIGEST,
    PLATFORMS,
    fields,
    matches,
    repository_name,
    require,
    json_bytes,
)


def utc_now():
    return datetime.now(timezone.utc)


def date(value):
    require(
        isinstance(value, str)
        and re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", value),
        "Temporary acceptance requires a UTC timestamp",
    )
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def validate_policy(node, reviewers):
    fields(
        node,
        {
            "schema_version",
            "kind",
            "id",
            "repository",
            "authorized_by",
            "authorized_at",
            "expires_at",
            "reason",
            "caddy_version",
            "source_revision",
            "go_version",
            "base_image",
            "platforms",
            "findings",
        },
        "temporary Caddy acceptance policy",
    )
    require(
        type(node["schema_version"]) is int
        and node["schema_version"] == 1
        and node["kind"] == "temporary-caddy-vulnerability-acceptance"
        and node["authorized_by"] in reviewers
        and isinstance(node["reason"], str)
        and 20 <= len(node["reason"]) <= 2048,
        "Temporary Caddy acceptance lacks authorized committed scope",
    )
    matches(
        node["id"], re.compile(r"[a-z0-9][a-z0-9-]{0,63}\Z"), "Invalid acceptance ID"
    )
    repository_name(node["repository"])
    matches(node["source_revision"], COMMIT, "Missing accepted Caddy source revision")
    matches(
        node["caddy_version"],
        re.compile(r"v\d+\.\d+\.\d+\Z"),
        "Invalid accepted Caddy release",
    )
    matches(
        node["go_version"],
        re.compile(r"go\d+\.\d+\.\d+\Z"),
        "Invalid accepted Caddy compiler",
    )
    require(
        isinstance(node["base_image"], str)
        and node["base_image"].startswith("docker.io/library/caddy@sha256:"),
        "Acceptance requires an exact official Caddy base index",
    )
    matches(node["base_image"].split("@")[-1], DIGEST, "Invalid accepted base index")
    start, end = date(node["authorized_at"]), date(node["expires_at"])
    require(
        start < end <= start + timedelta(days=14),
        "Acceptance must expire within fourteen days",
    )
    fields(node["platforms"], set(PLATFORMS), "both accepted Caddy architectures")
    for values in node["platforms"].values():
        fields(
            values, {"base_digest", "binary_sha256"}, "accepted native Caddy identity"
        )
        for value in values.values():
            matches(value, DIGEST, "Invalid accepted native Caddy digest")
    rows = node["findings"]
    require(
        isinstance(rows, list) and 0 < len(rows) <= 32,
        "Missing bounded accepted findings",
    )
    seen = set()
    for row in rows:
        fields(
            row,
            {
                "go_id",
                "scanner_id",
                "module",
                "installed_version",
                "fixed_version",
                "advisory_sha256",
            },
            "accepted Caddy finding",
        )
        matches(
            row["go_id"], re.compile(r"GO-\d{4}-\d+\Z"), "Invalid accepted Go advisory"
        )
        matches(
            row["scanner_id"],
            re.compile(r"(?:GO-\d{4}-\d+|CVE-\d{4}-\d+)\Z"),
            "Invalid accepted scanner advisory",
        )
        require(
            row["module"] in {"stdlib", "golang.org/x/net"}
            and isinstance(row["installed_version"], str)
            and re.fullmatch(r"v\d+\.\d+\.\d+", row["installed_version"])
            and isinstance(row["fixed_version"], str)
            and 0 < len(row["fixed_version"]) <= 64,
            "Acceptance is limited to exact Caddy Go/runtime networking versions",
        )
        if row["module"] == "stdlib":
            require(
                row["installed_version"] == "v" + node["go_version"][2:],
                "Accepted stdlib differs from Caddy compiler",
            )
        matches(
            row["advisory_sha256"], DIGEST, "Accepted official advisory hash missing"
        )
        key = row["module"], row["go_id"]
        require(key not in seen, "Duplicate accepted Caddy advisory")
        seen.add(key)
    return node


def active(node):
    require(
        date(node["authorized_at"]) <= utc_now() < date(node["expires_at"]),
        "Temporary Caddy acceptance is not active or has expired",
    )


def committed_acceptance(binding, root=None):
    # Import at use time: the distribution loader calls our schema validator.
    from generate_distribution_review import committed_policy

    policy, fact = committed_policy(
        root or Path(__file__).resolve().parent.parent, binding
    )
    node = policy.get("temporary_caddy_exception")
    require(
        node is not None and node["repository"] == binding.repository,
        "No committed Caddy acceptance for this repository",
    )
    active(node)
    return node, fact


def accepted_row(node, row, official):
    finding = row["finding"]
    matches = [
        accepted
        for accepted in node["findings"]
        if accepted["go_id"] == official["id"]
        and accepted["scanner_id"] == finding.get("VulnerabilityID")
        and accepted["module"] == finding.get("PkgName")
        and accepted["installed_version"] == finding.get("InstalledVersion")
        and accepted["fixed_version"] == finding.get("FixedVersion")
        and accepted["advisory_sha256"] == official["sha256"]
    ]
    require(
        len(matches) == 1, "Affected Caddy finding is outside the temporary acceptance"
    )


def acceptance_fact(node, fact, platform):
    return {
        "policy": fact,
        "exception_id": node["id"],
        "authorized_by": node["authorized_by"],
        "expires_at": node["expires_at"],
        "caddy_version": node["caddy_version"],
        "source_revision": node["source_revision"],
        "base_image": node["base_image"],
        "go_version": node["go_version"],
        **node["platforms"][platform],
    }


def verify_finding(record, binding, target, node, fact):
    require(
        target in {"web-amd64", "web-arm64"},
        "Temporary acceptance is limited to Caddy web images",
    )
    active(node)
    platform = "linux/" + target.split("-")[-1]
    require(
        node["repository"] == binding.repository
        and fact.get("source_commit") == binding.commit
        and record.get("disposition") == "temporarily-accepted"
        and record.get("reason") == node["reason"]
        and record.get("class") == "lang-pkgs"
        and record.get("target") == "usr/bin/caddy"
        and record.get("finding", {}).get("DataSource", {}).get("ID") == "govulndb"
        and record.get("finding_sha256")
        == "sha256:" + hashlib.sha256(json_bytes(record.get("finding"))).hexdigest()
        and record.get("acceptance") == acceptance_fact(node, fact, platform)
        and record.get("binary_sha256") == node["platforms"][platform]["binary_sha256"]
        and record.get("source_inputs", {}).get("wrapper_revision")
        == node["source_revision"],
        "Temporary acceptance differs from exact committed Caddy scope",
    )
    matches(
        record.get("graph_sha256"),
        DIGEST,
        "Accepted finding lacks compiler graph binding",
    )
    official = record.get("official_advisory", {})
    require(
        official.get("origin")
        == "https://vuln.go.dev/ID/" + str(official.get("id")) + ".json"
        and isinstance(record.get("affected_packages"), list)
        and record["affected_packages"],
        "Accepted finding lacks official affected package evidence",
    )
    accepted_row(node, record, official)


def accept_finding(row, graph, affected, official, *, binding, target, node, fact):
    platform = "linux/" + target.split("-")[-1]
    require(
        target in {"web-amd64", "web-arm64"},
        "Temporary acceptance is limited to Caddy web images",
    )
    proof = graph.get("signature_verification", {})
    require(
        graph.get("component") == "web"
        and graph["binary"]["build_info"]["GoVersion"] == node["go_version"]
        and graph["binary"]["sha256"] == node["platforms"][platform]["binary_sha256"]
        and proof.get("version") == node["caddy_version"]
        and proof.get("source_revision") == node["source_revision"],
        "Current Caddy executable/upstream proof is outside temporary acceptance",
    )
    result = {
        **row,
        "disposition": "temporarily-accepted",
        "reason": node["reason"],
        "official_advisory": official,
        "affected_packages": sorted(affected),
        "graph_sha256": graph["source_graph"]["sha256"],
        "binary_sha256": graph["binary"]["sha256"],
        "source_inputs": graph["source_inputs"],
        "acceptance": acceptance_fact(node, fact, platform),
    }
    verify_finding(result, binding, target, node, fact)
    return result
