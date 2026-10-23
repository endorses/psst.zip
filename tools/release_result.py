"""Read one canonical container result while Python consumers migrate to Go.

The Go publication-result command is the sole producer. Semantic source, scan and
recovery validation remains with its existing consumer until that code migrates;
this module creates no envelopes, signs nothing and grants no trust itself.
"""

from pathlib import Path

from publish_container_release import GATES, Binding
from release_artifacts import (
    COMMIT,
    DIGEST,
    VERSION,
    fields,
    matches,
    read_bounded_file,
    read_json,
    repository_name,
    require,
)

BASE_CHECKS = GATES - {"upgrade-recovery"}


def checked_result(value: object, binding: Binding, invocation=None) -> dict:
    result = fields(
        value,
        {
            "schema_version",
            "kind",
            "repository",
            "version",
            "commit",
            "run_id",
            "attempt",
            "binding_digest",
            "subjects",
            "checks",
            "inputs",
            "platforms",
        },
        "canonical release result",
    )
    require(
        type(result["schema_version"]) is int
        and result["schema_version"] == 1
        and result["kind"] == "container-release-result"
        and result["repository"] == binding.repository
        and result["version"] == binding.version
        and result["commit"] == binding.commit
        and result["binding_digest"] == binding.digest
        and result["subjects"] == dict(binding.subjects),
        "Release result source or subjects differ",
    )
    require(
        type(result["run_id"]) is int
        and result["run_id"] > 0
        and type(result["attempt"]) is int
        and result["attempt"] > 0
        and (invocation is None or (result["run_id"], result["attempt"]) == invocation),
        "Release result belongs to another invocation",
    )
    checks = result["checks"]
    require(
        isinstance(checks, dict)
        and set(checks) in (BASE_CHECKS, GATES)
        and all(isinstance(details, dict) for details in checks.values()),
        "Release result check coverage is incomplete",
    )
    inputs = result["inputs"]
    require(
        isinstance(inputs, dict) and 0 < len(inputs) <= 64,
        "Missing release result input inventory",
    )
    for name, item in inputs.items():
        require(isinstance(name, str) and name, "Invalid result input name")
        fields(item, {"sha256"}, "raw result input reference")
        matches(item["sha256"], DIGEST, "Invalid result input digest")
    require(
        "corresponding-source-replay" in inputs
        and ("upgrade-recovery" not in checks or "source-result" in inputs),
        "Result raw input inventory is incomplete",
    )
    require(
        type(checks["source-ci"].get("run_id")) is int
        and type(checks["source-ci"].get("run_attempt")) is int
        and checks["source-ci"].get("run_id") == result["run_id"]
        and checks["source-ci"].get("run_attempt") == result["attempt"],
        "Source CI belongs to another result invocation",
    )
    for check in ("final-image-smoke",) + (
        ("upgrade-recovery",) if "upgrade-recovery" in checks else ()
    ):
        require(
            checks[check].get("execution")
            == {"linux/amd64": "native", "linux/arm64": "native"},
            "Result lacks native source measurements",
        )
    platforms = fields(
        result["platforms"], {"linux/amd64", "linux/arm64"}, "native platform results"
    )
    expected = ["final-image-smoke"] + (
        ["upgrade-recovery"] if "upgrade-recovery" in checks else []
    )
    for item in platforms.values():
        fields(item, {"execution", "checks"}, "native platform result")
        require(
            item["execution"] == "native" and item["checks"] == expected,
            "Release result lacks native platform validation",
        )
    return result


def binding_from_result(result: dict) -> Binding:
    repository_name(result.get("repository"))
    matches(result.get("version"), VERSION, "Invalid result version")
    matches(result.get("commit"), COMMIT, "Invalid result source")
    subjects = result.get("subjects")
    require(
        isinstance(subjects, dict)
        and all(
            isinstance(name, str) and isinstance(value, str)
            for name, value in subjects.items()
        ),
        "Invalid result subjects",
    )
    return Binding(
        result["repository"],
        result["version"],
        result["commit"],
        tuple(sorted(subjects.items())),
    )


def main() -> None:
    import argparse
    import os

    from github_release_evidence import GhEvidenceVerifier
    from publish_container_release import verify_gates

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify", type=Path, required=True)
    args = parser.parse_args()
    raw = read_bounded_file(args.verify)
    result = read_json(raw)
    binding = binding_from_result(result)
    invocation = (
        int(os.environ["GITHUB_RUN_ID"]),
        int(os.environ["GITHUB_RUN_ATTEMPT"]),
    )
    require(
        binding.repository == os.environ["GITHUB_REPOSITORY"]
        and binding.version == os.environ["GITHUB_REF"].removeprefix("refs/tags/")
        and binding.commit == os.environ["GITHUB_SHA"],
        "Result differs from workflow source",
    )
    checked_result(result, binding, invocation)
    verifier = GhEvidenceVerifier(
        token=os.environ.get("GH_TOKEN"),
        run_id=invocation[0],
        run_attempt=invocation[1],
    )
    verify_gates(
        {gate: args.verify for gate in result["checks"]},
        frozenset(result["checks"]),
        binding,
        verifier,
    )
    print("Authenticated one exact release result and its substantive checks.")


if __name__ == "__main__":
    main()
