"""Bind authorized distribution review to committed policy and GitHub approval.

Reads GitHub only. A signed complete corresponding-source gate is a prerequisite;
this producer neither reviews source completeness nor signs or publishes reports.
The protected-environment approval comment identifies the exact attempt, complete
artifact binding and source report so an earlier approval cannot approve a rerun.
"""

from __future__ import annotations

import argparse
import os
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
    InvalidRelease,
    create_output,
    fields,
    git,
    json_bytes,
    read_bounded_file,
    read_json,
    require,
)
from temporary_caddy_acceptance import validate_policy as validate_caddy_acceptance

PRESENTATION_NAMES = {"review-presentation.json", "review.md"}
REVIEW_NAMES = {"distribution-review.json", "distribution-review-evidence.json"}
MAX_COMMAND_OUTPUT = 4 * 1024 * 1024


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
    value = read_json(raw)
    expected = {"schema_version", "kind", "environment", "reviewers"}
    if isinstance(value, dict) and "temporary_caddy_exception" in value:
        expected.add("temporary_caddy_exception")
    policy = fields(
        value,
        expected,
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
    if "temporary_caddy_exception" in policy:
        validate_caddy_acceptance(
            policy["temporary_caddy_exception"], policy["reviewers"]
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

    Only an authorized user's approval comment matching the selected run after
    removing surrounding ASCII whitespace can approve these subjects. The
    workflow must present the artifacts/review scope
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
        if isinstance(review.get("comment"), str)
        and review["comment"].strip(" \t\n\r\v\f") == comment
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


def authenticated_source(binding: Binding, path: Path, verifier) -> tuple[dict, bytes]:
    raw = read_bounded_file(path)
    receipt = verifier.verify("corresponding-source", path, binding)
    report = fields(
        read_json(raw),
        {"schema_version", "gate", "binding_digest", "passed", "details"},
        "authenticated corresponding-source report",
    )
    require(
        isinstance(receipt, VerifiedEvidence)
        and receipt.gate == "corresponding-source"
        and receipt.passed is True
        and receipt.binding_digest == binding.digest
        and receipt.report_digest == sha256(raw)
        and type(report["schema_version"]) is int
        and report["schema_version"] == 1
        and report["gate"] == receipt.gate
        and report["binding_digest"] == receipt.binding_digest
        and report["passed"] is True
        and report["details"] == receipt.details
        and json_bytes(report) == raw
        and read_bounded_file(path) == raw,
        "Distribution source report differs from authenticated canonical bytes",
    )
    source_review_details(report["details"], binding, distribution=False)
    return report, raw


def review_presentation(binding, source, source_raw, policy, policy_fact, args):
    """Present exact authenticated facts; this document cannot approve a release."""
    artifact_suffix = f"{args.run_id}-{args.run_attempt}"
    artifacts = {
        "url": f"https://github.com/{binding.repository}/actions/runs/{args.run_id}/attempts/{args.run_attempt}#artifacts",
        "prepared": f"candidate-prepared-inputs-{artifact_suffix}",
        "source_review": f"candidate-source-review-{artifact_suffix}",
        "native": {
            arch: f"candidate-native-inputs-{arch}-{artifact_suffix}"
            for arch in ("amd64", "arm64")
        },
        "recovery": f"candidate-upgrade-recovery-gate-{artifact_suffix}",
    }
    presentation = {
        "schema_version": 1,
        "kind": "container-distribution-review-presentation",
        "repository": binding.repository,
        "version": binding.version,
        "source_commit": binding.commit,
        "binding_digest": binding.digest,
        "run_id": args.run_id,
        "run_attempt": args.run_attempt,
        "source_gate_report_digest": sha256(source_raw),
        "source_report": args.source_report.name,
        "workflow_artifacts": artifacts,
        "final_subjects": dict(binding.subjects),
        "policy": policy,
        "policy_fact": policy_fact,
        "source_details": source["details"],
        "approval_comment": approval_comment(
            binding, sha256(source_raw), args.run_id, args.run_attempt
        ),
        "distribution_authorized": False,
        "publication_authorized": False,
    }
    lines = [
        "# psst.zip distribution review",
        "",
        "This presentation is not approval. Distribution and publication remain unauthorized.",
        "",
        f"Repository: `{binding.repository}`; version: `{binding.version}`; commit: `{binding.commit}`.",
        f"Run: https://github.com/{binding.repository}/actions/runs/{args.run_id}/attempts/{args.run_attempt}",
        f"Current-run artifacts: {artifacts['url']}",
        f"Binding: `{binding.digest}`.",
        f"Authenticated source report: `{args.source_report.name}` (`{sha256(source_raw)}`).",
        f"Committed policy: `{policy_fact['path']}`; blob: `{policy_fact['git_blob']}`; digest: `{policy_fact['record_digest']}`.",
        f"Environment: `{policy['environment']}`; authorized reviewers: "
        + ", ".join(f"`{user}`" for user in policy["reviewers"])
        + ".",
        "",
        "The authorized reviewer must use this exact approval comment:",
        "",
        "```text",
        presentation["approval_comment"],
        "```",
        "",
        "## Exact final subjects and source artifact references",
        "",
        f"- `{artifacts['prepared']}` contains the named offered source artifacts, release manifest and deployment bundle listed below.",
        f"- `{artifacts['source_review']}` contains `corresponding-source.json` and `corresponding-source-evidence.json` with the retained raw source evidence.",
        f"- `{artifacts['native']['amd64']}` and `{artifacts['native']['arm64']}` contain the exact final OCI exports and native evidence for each architecture.",
        f"- `{artifacts['recovery']}` contains the authenticated upgrade and recovery gate.",
        "",
    ]
    lines.extend(f"- `{name}`: `{subject}`" for name, subject in binding.subjects)
    if "temporary_caddy_exception" in policy:
        exception = policy["temporary_caddy_exception"]
        lines.extend(
            [
                "## Temporary acceptance of known Caddy vulnerabilities",
                "",
                f"Operator `{exception['authorized_by']}` accepts the listed findings for official Caddy `{exception['caddy_version']}` until `{exception['expires_at']}`.",
                "Affected code remains present. This acceptance does not establish that these vulnerabilities are fixed or inapplicable.",
                exception["reason"],
                f"Exact base: `{exception['base_image']}`. Acceptance: `{exception['id']}`.",
                "",
            ]
        )
        lines.extend(
            f"- `{row['scanner_id']}` / `{row['go_id']}`: `{row['module']} {row['installed_version']}`; official advisory `{row['advisory_sha256']}`."
            for row in exception["findings"]
        )
        lines.append("")
    lines.extend(["", "## Final image notices and complete source coverage", ""])
    for image, notice in source["details"]["images"].items():
        lines.append(
            f"- `{image}` notice inventory: `{notice['notice_inventory_digest']}`."
        )
        for category, fact in source["details"]["coverage"][image].items():
            lines.append(
                f"- `{image}` / `{category}`: `{fact['status']}`; evidence `{fact['evidence_digest']}`."
            )
    lines.extend(
        [
            "",
            "Coverage digest: `"
            + sha256(json_bytes(source["details"]["coverage"]))
            + "`.",
            "",
            "Review all referenced final subjects, notices and source evidence before approving the protected environment.",
            "",
        ]
    )
    return presentation, "\n".join(lines).encode("utf-8")


def verify_command_output(
    args, binding, source, source_raw, policy, policy_fact, verifier
):
    """Authenticate retained producer outputs without querying review history."""
    from measure_browser_source_inventory import root_directory

    root_directory(args.output)
    require(
        {p.name for p in args.output.iterdir()} == REVIEW_NAMES
        and all(p.is_file() and not p.is_symlink() for p in args.output.iterdir()),
        "Distribution verification requires exactly two regular output files",
    )
    report_path = args.output / "distribution-review.json"
    evidence_path = args.output / "distribution-review-evidence.json"
    report_raw, evidence_raw = read_bounded_file(report_path), read_bounded_file(
        evidence_path
    )
    require(
        0 < len(report_raw) + len(evidence_raw) <= MAX_COMMAND_OUTPUT,
        "Distribution output exceeds bounds",
    )
    receipt = verifier.verify("distribution-review", report_path, binding)
    verifier.authenticate(evidence_raw, binding)
    report = fields(
        read_json(report_raw),
        {"schema_version", "gate", "binding_digest", "passed", "details"},
        "authenticated distribution report",
    )
    require(
        isinstance(receipt, VerifiedEvidence)
        and receipt.gate == "distribution-review"
        and receipt.binding_digest == binding.digest
        and receipt.report_digest == sha256(report_raw)
        and receipt.passed is True
        and type(report["schema_version"]) is int
        and report["schema_version"] == 1
        and report["gate"] == receipt.gate
        and report["binding_digest"] == receipt.binding_digest
        and report["passed"] is True
        and report["details"] == receipt.details,
        "Distribution report differs from authenticated receipt",
    )
    source_review_details(report["details"], binding, distribution=True)
    details = report["details"]
    evidence = fields(
        read_json(evidence_raw),
        {
            "schema_version",
            "kind",
            "binding_digest",
            "source_gate_report_digest",
            "policy",
            "run_id",
            "run_attempt",
            "github_evidence",
        },
        "authenticated distribution review evidence",
    )
    require(
        type(evidence["schema_version"]) is int
        and evidence["schema_version"] == 1
        and evidence["kind"] == "authorized-container-distribution-review"
        and evidence["binding_digest"] == binding.digest
        and type(evidence["run_id"]) is int
        and evidence["run_id"] == args.run_id
        and type(evidence["run_attempt"]) is int
        and evidence["run_attempt"] == args.run_attempt
        and evidence["policy"] == policy_fact
        and evidence["source_gate_report_digest"] == sha256(source_raw)
        and details["review"]["source_gate_report_digest"] == sha256(source_raw)
        and details["review"]["record_digest"] == sha256(evidence_raw)
        and details["review"]["reviewer"] in policy["reviewers"]
        and details["coverage_digest"]
        == sha256(json_bytes(source["details"]["coverage"]))
        and all(
            details[key] == source["details"][key]
            for key in ("schema_version", "source_subjects", "images", "policy")
        )
        and json_bytes(report) == report_raw
        and json_bytes(evidence) == evidence_raw,
        "Distribution evidence differs from exact source, policy, attempt or producer bytes",
    )
    fields(
        evidence["github_evidence"],
        {"run", "environment", "reviews"},
        "retained GitHub review evidence",
    )
    require(
        read_bounded_file(report_path) == report_raw
        and read_bounded_file(evidence_path) == evidence_raw
        and {p.name for p in args.output.iterdir()} == REVIEW_NAMES
        and all(p.is_file() and not p.is_symlink() for p in args.output.iterdir()),
        "Distribution outputs changed during authentication",
    )
    return report, evidence


def run_command(args) -> tuple[dict, dict | bytes]:
    # Late imports preserve the existing source -> distribution -> recovery graph.
    from aggregate_release_recovery import authenticated_command_inputs
    from github_release_evidence import GhEvidenceVerifier, checked_invocation
    from measure_browser_source_inventory import root_directory
    from publish_container_release import source_digest

    require(
        args.mode in {"present", "produce", "verify"},
        "Invalid distribution command mode",
    )
    require(
        checked_invocation(args.run_id, args.run_attempt) is not None,
        "Distribution review requires an exact workflow run and attempt",
    )
    root_directory(args.output.parent)
    require(
        not args.output.resolve().is_relative_to(args.prepared.resolve()),
        "Distribution outputs must be outside prepared binding inputs",
    )
    if args.mode != "verify":
        require(
            not args.output.exists() and not args.output.is_symlink(),
            "Distribution output must be a new directory",
        )
    verifier = GhEvidenceVerifier(
        token=os.environ.get("GH_TOKEN"),
        run_id=args.run_id,
        run_attempt=args.run_attempt,
    )
    binding, record_raw, names, snapshots = authenticated_command_inputs(args, verifier)
    source, source_raw = authenticated_source(binding, args.source_report, verifier)
    policy, policy_fact = committed_policy(args.root, binding)
    require(
        source["details"]["policy"] == policy_fact,
        "Source review policy differs from exact Git",
    )
    output_snapshots = {}
    if args.mode == "present":
        result = review_presentation(
            binding, source, source_raw, policy, policy_fact, args
        )
        outputs = {
            "review-presentation.json": json_bytes(result[0]),
            "review.md": result[1],
        }
    elif args.mode == "produce":
        result = distribution_review_report(
            binding,
            root=args.root,
            source_report=args.source_report,
            verifier=verifier,
            run_id=args.run_id,
            attempt=args.run_attempt,
            token=os.environ.get("GH_TOKEN"),
        )
        outputs = {
            "distribution-review.json": json_bytes(result[0]),
            "distribution-review-evidence.json": json_bytes(result[1]),
        }
    else:
        output_snapshots = {
            args.output / name: read_bounded_file(args.output / name)
            for name in REVIEW_NAMES
        }
        result = verify_command_output(
            args, binding, source, source_raw, policy, policy_fact, verifier
        )
        outputs = {}
    require(
        read_bounded_file(args.prepared / "release-inputs.json") == record_raw
        and {p.name for p in args.prepared.iterdir()} == names
        and all(p.is_file() and not p.is_symlink() for p in args.prepared.iterdir())
        and all(source_digest(path) == digest for path, digest in snapshots.items())
        and read_bounded_file(args.source_report) == source_raw
        and committed_policy(args.root, binding) == (policy, policy_fact),
        "Distribution command inputs changed during authentication",
    )
    if output_snapshots:
        require(
            all(
                read_bounded_file(path) == raw for path, raw in output_snapshots.items()
            )
            and {p.name for p in args.output.iterdir()} == REVIEW_NAMES
            and all(p.is_file() and not p.is_symlink() for p in args.output.iterdir()),
            "Distribution outputs changed during final input recheck",
        )
    if outputs:
        require(
            0 < sum(map(len, outputs.values())) <= MAX_COMMAND_OUTPUT,
            "Distribution output exceeds bounds",
        )
        root_directory(args.output.parent)
        args.output.mkdir(mode=0o700)
        for name, raw in outputs.items():
            create_output(args.output / name, raw)
    return result


def main(argv=None) -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--mode", choices=("present", "produce", "verify"), required=True
    )
    for name in ("root", "prepared", "source-report", "output"):
        parser.add_argument("--" + name, type=Path, required=True)
    for name in ("repository", "version", "commit"):
        parser.add_argument("--" + name, required=True)
    for name in ("run-id", "run-attempt"):
        parser.add_argument("--" + name, type=int, required=True)
    args = parser.parse_args(argv)
    try:
        run_command(args)
    except (InvalidRelease, OSError, ValueError, RecursionError):
        parser.exit(
            1,
            "Distribution review command failed; no new publication authorization was issued.\n",
        )


if __name__ == "__main__":
    main()
