"""Reject skipped or unrelated Android CI and premature publication readiness."""

import copy
import os
import unittest
from unittest.mock import patch

import android_release_ci as release


class AndroidEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.repository = "owner/project"
        self.revision = "a" * 40
        self.run = {
            "head_sha": self.revision,
            "head_branch": "main",
            "event": "push",
            "path": ".github/workflows/ci.yml",
            "repository": {"full_name": self.repository},
            "head_repository": {"full_name": self.repository},
            "status": "completed",
            "run_attempt": 2,
            "id": 4,
        }
        self.job = {
            "name": "Android and shared module",
            "id": 8,
            "status": "completed",
            "conclusion": "success",
            "steps": [
                {
                    "name": name,
                    "conclusion": "success",
                    "started_at": "2026-10-09T00:00:00Z",
                    "completed_at": "2026-10-09T00:00:01Z",
                }
                for name in release.REQUIRED["Android and shared module"]
            ],
        }

    def evidence(self, runs, jobs=None):
        responses = [{"workflow_runs": runs}]
        if jobs is not None:
            responses.append({"jobs": jobs})
        with (
            patch.object(release.subprocess, "check_output", return_value=b"trusted"),
            patch.object(release, "gh_json", side_effect=responses),
        ):
            return release.ci_evidence(self.repository, self.revision)

    def test_exact_commit_executed_checks_can_be_reused(self):
        result = self.evidence([self.run], [self.job])
        self.assertEqual(result["android"]["job_id"], 8)
        self.assertEqual(result["android"]["attempt"], 2)
        self.assertIsNone(result["security"])

    def test_green_job_with_skipped_or_missing_steps_is_not_evidence(self):
        for change in ("skipped", "missing"):
            with self.subTest(change=change):
                job = copy.deepcopy(self.job)
                if change == "skipped":
                    job["steps"][0]["conclusion"] = "skipped"
                else:
                    job["steps"].pop()
                self.assertIsNone(self.evidence([self.run], [job])["android"])

    def test_fork_pr_other_commit_and_wrong_workflow_are_not_reused(self):
        changes = [
            {"head_repository": {"full_name": "other/fork"}},
            {"event": "pull_request"},
            {"head_sha": "b" * 40},
            {"path": ".github/workflows/other.yml"},
        ]
        for change in changes:
            with self.subTest(change=change):
                self.assertIsNone(self.evidence([{**self.run, **change}])["android"])

    def test_completed_android_check_is_reused_while_other_jobs_run(self):
        result = self.evidence([{**self.run, "status": "in_progress"}], [self.job])
        self.assertEqual(result["android"]["job_id"], 8)

    def test_pending_android_job_is_not_success_evidence(self):
        for state in ("queued", "in_progress"):
            with self.subTest(state=state):
                job = {**self.job, "status": state}
                self.assertIsNone(
                    self.evidence([{**self.run, "status": "in_progress"}], [job])[
                        "android"
                    ]
                )

    def test_changed_workflow_requires_fresh_checks_without_api_reuse(self):
        with (
            patch.object(
                release.subprocess, "check_output", side_effect=[b"new", b"old"]
            ),
            patch.object(release, "gh_json") as api,
        ):
            result = release.ci_evidence(self.repository, self.revision)
        self.assertIsNone(result["android"])
        api.assert_not_called()

    def test_missing_reviewed_device_readiness_blocks_before_api(self):
        with (
            patch.dict(os.environ, {}, clear=True),
            patch.object(release, "gh_json") as api,
            self.assertRaisesRegex(ValueError, "readiness"),
        ):
            release.protected_publication(self.repository, "android-v0.1.0")
        api.assert_not_called()

    def test_no_reviewer_or_mutable_tag_blocks_publication(self):
        ready = {
            "ANDROID_RELEASE_PUBLICATION_READY": "true",
            "ANDROID_RELEASE_DEVICE_EVIDENCE": "private operator record 2026-10-09",
            "ANDROID_RELEASE_SIGNER_SHA256": "a" * 64,
        }
        protected = {
            "protection_rules": [
                {"type": "required_reviewers", "reviewers": [{"id": 1}]}
            ],
            "deployment_branch_policy": {
                "protected_branches": False,
                "custom_branch_policies": True,
            },
        }
        for responses, error in [
            ([{"protection_rules": []}], "reviewer"),
            (
                [
                    protected,
                    {
                        "total_count": 1,
                        "branch_policies": [{"name": "main", "type": "branch"}],
                    },
                    [],
                ],
                "immutable",
            ),
        ]:
            with (
                self.subTest(error=error),
                patch.dict(os.environ, ready, clear=True),
                patch.object(
                    release,
                    "gh_json",
                    side_effect=[{"name": "main", "protected": True}, {"enabled": True}]
                    + responses,
                ),
                self.assertRaisesRegex(ValueError, error),
            ):
                release.protected_publication(self.repository, "android-v0.1.0")

    def test_unprotected_main_is_rejected_before_source_or_key_use(self):
        with (
            patch.object(
                release, "gh_json", return_value={"name": "main", "protected": False}
            ),
            self.assertRaisesRegex(ValueError, "protected main"),
        ):
            release.protected_main(self.repository)

    def test_missing_or_nonempty_bypass_policy_is_not_assumed_empty(self):
        ready = {
            "ANDROID_RELEASE_PUBLICATION_READY": "true",
            "ANDROID_RELEASE_DEVICE_EVIDENCE": "private reviewed device record",
            "ANDROID_RELEASE_SIGNER_SHA256": "a" * 64,
        }
        ruleset = {
            "conditions": {
                "ref_name": {"include": ["refs/tags/android-v*"], "exclude": []}
            },
            "rules": [{"type": "update"}, {"type": "deletion"}],
        }
        for bypass in ({}, {"bypass_actors": [{"actor_id": 1}]}, {"bypass_actors": []}):
            with (
                self.subTest(bypass=bypass),
                patch.dict(os.environ, ready, clear=True),
                patch.object(
                    release,
                    "gh_json",
                    side_effect=[
                        {"name": "main", "protected": True},
                        {"enabled": True},
                        {
                            "protection_rules": [
                                {"type": "required_reviewers", "reviewers": [{"id": 1}]}
                            ],
                            "deployment_branch_policy": {
                                "protected_branches": False,
                                "custom_branch_policies": True,
                            },
                        },
                        {
                            "total_count": 1,
                            "branch_policies": [{"name": "main", "type": "branch"}],
                        },
                        [{"enforcement": "active", "target": "tag", "id": 4}],
                        {**ruleset, **bypass},
                    ],
                ),
            ):
                if bypass.get("bypass_actors") == []:
                    release.protected_publication(self.repository, "android-v0.1.0")
                else:
                    with self.assertRaisesRegex(ValueError, "hidden or permits bypass"):
                        release.protected_publication(self.repository, "android-v0.1.0")

    def test_disabled_or_missing_repository_immutability_is_rejected(self):
        ready = {
            "ANDROID_RELEASE_PUBLICATION_READY": "true",
            "ANDROID_RELEASE_DEVICE_EVIDENCE": "private reviewed device record",
            "ANDROID_RELEASE_SIGNER_SHA256": "a" * 64,
        }
        for immutable in ({}, {"enabled": False}):
            with (
                self.subTest(immutable=immutable),
                patch.dict(os.environ, ready, clear=True),
                patch.object(
                    release,
                    "gh_json",
                    side_effect=[
                        {"name": "main", "protected": True},
                        immutable,
                    ],
                ),
                self.assertRaisesRegex(ValueError, "immutable releases"),
            ):
                release.protected_publication(self.repository, "android-v0.1.0")

    def test_reviewer_does_not_authorize_unrestricted_branches_or_main_tag(self):
        ready = {
            "ANDROID_RELEASE_PUBLICATION_READY": "true",
            "ANDROID_RELEASE_DEVICE_EVIDENCE": "private reviewed device record",
            "ANDROID_RELEASE_SIGNER_SHA256": "a" * 64,
        }
        environment = {
            "protection_rules": [
                {"type": "required_reviewers", "reviewers": [{"id": 1}]}
            ],
            "deployment_branch_policy": {
                "protected_branches": False,
                "custom_branch_policies": True,
            },
        }
        cases = [
            ({**environment, "deployment_branch_policy": None}, None),
            (
                environment,
                {
                    "total_count": 1,
                    "branch_policies": [{"name": "main", "type": "tag"}],
                },
            ),
            (
                environment,
                {
                    "total_count": 1,
                    "branch_policies": [{"name": "*", "type": "branch"}],
                },
            ),
            (
                environment,
                {
                    "total_count": 2,
                    "branch_policies": [
                        {"name": "main", "type": "branch"},
                        {"name": "unreviewed", "type": "branch"},
                    ],
                },
            ),
            (environment, {"total_count": 1, "branch_policies": [{"name": "main"}]}),
        ]
        for configured, branches in cases:
            responses = [
                {"name": "main", "protected": True},
                {"enabled": True},
                configured,
            ]
            if branches is not None:
                responses.append(branches)
            with (
                self.subTest(configured=configured, branches=branches),
                patch.dict(os.environ, ready, clear=True),
                patch.object(release, "gh_json", side_effect=responses),
                self.assertRaisesRegex(ValueError, "main"),
            ):
                release.protected_publication(self.repository, "android-v0.1.0")


if __name__ == "__main__":
    unittest.main()
