"""Bind authorized distribution review to committed policy and GitHub approval.

Reads GitHub only. A signed complete corresponding-source gate is a prerequisite;
this producer neither reviews source completeness nor signs or publishes reports.
The protected-environment approval comment identifies the exact attempt, complete
artifact binding and source report so an earlier approval cannot approve a rerun.
"""

from __future__ import annotations

from pathlib import Path
import re

from generate_release_gate_reports import checked_binding
from github_release_transport import API_VERSION, HTTPS
from publish_container_release import (
    Binding,
    EvidenceVerifier,
    SOURCE_REVIEW_POLICY,
    WORKFLOW,
    VerifiedEvidence,
    sha256,
    source_review_details,
)
from release_artifacts import (
    fields,
    git,
    json_bytes,
    read_bounded_file,
    read_json,
    require,
)


def committed_policy(root: Path, binding: Binding) -> tuple[dict, dict]:
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
    policy = fields(
        read_json(raw),
        {"schema_version", "kind", "environment", "reviewers"},
        "committed distribution authorization policy",
    )
    require(
        type(policy["schema_version"]) is int
        and policy["schema_version"] == 1
        and policy["kind"] == "container-distribution-review-policy"
        and isinstance(policy["environment"], str)
        and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]{0,63}", policy["environment"])
        and isinstance(policy["reviewers"], list)
        and 0 < len(policy["reviewers"]) <= 6
        and all(
            isinstance(user, str) and re.fullmatch(r"[a-z0-9][a-z0-9-]{0,38}", user)
            for user in policy["reviewers"]
        )
        and len(set(policy["reviewers"])) == len(policy["reviewers"]),
        "Invalid committed distribution authorization policy",
    )
    return policy, {
        "path": SOURCE_REVIEW_POLICY,
        "source_commit": binding.commit,
        "record_digest": sha256(raw),
        "git_blob": match[1].decode(),
    }


def approval_comment(
    binding: Binding, source_report_digest: str, run_id: int, attempt: int
) -> str:
    return (
        f"psst.zip distribution review: {binding.digest}; "
        f"run {run_id}; attempt {attempt}; source report {source_report_digest}"
    )


def distribution_review_report(
    binding: Binding,
    *,
    root: Path,
    source_report: Path,
    verifier: EvidenceVerifier,
    run_id: int,
    attempt: int,
    token: str,
    http=None,
) -> tuple[dict, dict]:
    """Produce a report and retained API evidence, never accept approval JSON.

    Only an authorized user's exact approval comment for the selected run can
    approve these subjects. The workflow must present the artifacts/review scope
    before waiting on this environment and attest both returned records afterward.
    """
    policy, policy_fact = committed_policy(root, binding)
    raw = read_bounded_file(source_report)
    source = verifier.verify("corresponding-source", source_report, binding)
    require(
        isinstance(source, VerifiedEvidence)
        and source.gate == "corresponding-source"
        and source.passed is True
        and source.binding_digest == binding.digest
        and source.report_digest == sha256(raw)
        and read_bounded_file(source_report) == raw,
        "Distribution requires the authenticated unchanged corresponding-source report",
    )
    source_review_details(source.details, binding, distribution=False)
    require(
        source.details["policy"] == policy_fact,
        "Source review policy differs from exact Git",
    )
    require(
        type(run_id) is int and run_id > 0 and type(attempt) is int and attempt > 0,
        "Invalid selected distribution review attempt",
    )
    require(
        isinstance(token, str) and token and not any(c in token for c in "\r\n"),
        "Explicit Actions read credential required",
    )
    http = http or HTTPS()
    api = f"https://api.github.com/repos/{binding.repository}/"

    def get(path):
        response = http.request(
            "GET",
            api + path,
            headers={
                "Authorization": "Bearer " + token,
                "Accept": "application/vnd.github+json",
                "X-GitHub-Api-Version": API_VERSION,
                "User-Agent": "psst.zip-distribution-review",
            },
        )
        require(
            response.status == 200 and not response.headers.get("link"),
            "Distribution review API failed or returned incomplete evidence",
        )
        return read_json(response.body)

    paths = {
        "run": f"actions/runs/{run_id}/attempts/{attempt}",
        "environment": "environments/" + policy["environment"],
        "reviews": f"actions/runs/{run_id}/approvals",
    }
    evidence = {key: get(path) for key, path in paths.items()}
    run = evidence["run"]
    require(
        isinstance(run, dict)
        and run.get("id") == run_id
        and run.get("run_attempt") == attempt
        and run.get("head_sha") == binding.commit
        and run.get("head_branch") == binding.version
        and run.get("event") == "push"
        and run.get("path") == WORKFLOW
        and run.get("status") == "in_progress"
        and run.get("repository", {}).get("full_name") == binding.repository
        and run.get("head_repository", {}).get("full_name") == binding.repository,
        "Review belongs to another source, workflow, repository or attempt",
    )
    environment = evidence["environment"]
    require(
        isinstance(environment, dict)
        and environment.get("name") == policy["environment"]
        and type(environment.get("id")) is int
        and environment["id"] > 0
        and isinstance(environment.get("protection_rules"), list),
        "Protected review environment missing",
    )
    require(
        len(environment["protection_rules"]) <= 32
        and all(isinstance(rule, dict) for rule in environment["protection_rules"]),
        "Malformed environment protection rules",
    )
    rules = [
        r
        for r in environment["protection_rules"]
        if r.get("type") == "required_reviewers"
    ]
    require(
        len(rules) == 1, "Distribution review requires configured required reviewers"
    )
    configured = rules[0].get("reviewers")
    require(
        isinstance(configured, list)
        and len(configured) == len(policy["reviewers"])
        and all(
            isinstance(item, dict)
            and isinstance(item.get("reviewer"), dict)
            and item.get("type") == "User"
            and item["reviewer"].get("type") == "User"
            and type(item["reviewer"].get("id")) is int
            and item["reviewer"]["id"] > 0
            and isinstance(item["reviewer"].get("login"), str)
            for item in configured
        )
        and {item["reviewer"]["login"] for item in configured}
        == set(policy["reviewers"]),
        "Environment reviewers differ from committed authorization policy",
    )
    identities = {
        item["reviewer"]["login"]: item["reviewer"]["id"] for item in configured
    }
    reviews = evidence["reviews"]
    require(
        isinstance(reviews, list)
        and len(reviews) <= 100
        and all(
            isinstance(review, dict)
            and isinstance(review.get("environments"), list)
            and len(review["environments"]) <= 64
            and all(isinstance(entry, dict) for entry in review["environments"])
            and isinstance(review.get("user"), dict)
            for review in reviews
        ),
        "Malformed or oversized review history",
    )
    comment = approval_comment(binding, source.report_digest, run_id, attempt)
    selected = [
        review
        for review in reviews
        if review.get("comment") == comment
        and any(
            entry.get("id") == environment["id"]
            and entry.get("name") == policy["environment"]
            for entry in review.get("environments", [])
        )
    ]
    require(
        len(selected) == 1,
        "Missing or ambiguous approval for exact release subjects and attempt",
    )
    review = selected[0]
    user = review.get("user", {})
    require(
        review.get("state") == "approved"
        and user.get("type") == "User"
        and user.get("login") in identities
        and user.get("id") == identities[user["login"]],
        "Distribution review is rejected or not by an authorized user",
    )
    require(
        evidence == {key: get(path) for key, path in paths.items()}
        and read_bounded_file(source_report) == raw
        and committed_policy(root, binding) == (policy, policy_fact),
        "Distribution review evidence changed during verification",
    )
    retained = {
        "schema_version": 1,
        "kind": "authorized-container-distribution-review",
        "binding_digest": binding.digest,
        "source_gate_report_digest": source.report_digest,
        "policy": policy_fact,
        "run_id": run_id,
        "run_attempt": attempt,
        "github_evidence": evidence,
    }
    details = {
        key: source.details[key]
        for key in ("schema_version", "source_subjects", "images", "policy")
    }
    details.update(
        coverage_digest=sha256(json_bytes(source.details["coverage"])),
        review={
            "decision": "approved",
            "reviewer": user["login"],
            "record_digest": sha256(json_bytes(retained)),
            "source_gate_report_digest": source.report_digest,
            "reviewed_subjects": dict(binding.subjects),
        },
    )
    source_review_details(details, binding, distribution=True)
    return {
        "schema_version": 1,
        "gate": "distribution-review",
        "binding_digest": binding.digest,
        "passed": True,
        "details": details,
    }, retained
