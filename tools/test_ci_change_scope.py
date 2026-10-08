#!/usr/bin/env python3
"""Small offline selector regressions using disposable Git repositories."""

from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import select_ci_checks as selector

SELECTOR = Path(__file__).with_name("select_ci_checks.py")


class ChangeScopeTests(unittest.TestCase):
    def setUp(self) -> None:
        directory = tempfile.TemporaryDirectory(prefix="psst-ci-scope-")
        self.addCleanup(directory.cleanup)
        self.temp = Path(directory.name)
        self.repo = self.temp / "repo with spaces"
        self.repo.mkdir()
        self.env = os.environ.copy()
        for key in tuple(self.env):
            if key.startswith("GIT_") or key.startswith("GITHUB_"):
                del self.env[key]
        self.env.update(GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1")
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.name", "CI selector test")
        self.git("config", "user.email", "ci@example.invalid")
        for path in ("README.md", "backend/old.go", "web/deleted.ts", "ios/old.swift"):
            self.write(path, f"initial {path}\n")
        self.base = self.commit()
        self.event = self.temp / "event.json"

    def git(self, *args: str) -> str:
        return subprocess.run(
            ["git", *args],
            cwd=self.repo,
            env=self.env,
            capture_output=True,
            text=True,
            check=True,
            timeout=10,
        ).stdout.strip()

    def write(self, path: str, content: str = "changed\n") -> None:
        target = self.repo / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")

    def commit(self) -> str:
        self.git("add", "-A")
        self.git("commit", "-qm", "Disposable change")
        return self.git("rev-parse", "HEAD")

    def push_event(self, before: str | None = None) -> None:
        self.event.write_text(json.dumps({"before": before or self.base}))
        self.env.update(
            GITHUB_EVENT_NAME="push",
            GITHUB_REF="refs/heads/main",
            GITHUB_SHA=self.git("rev-parse", "HEAD"),
            GITHUB_EVENT_PATH=str(self.event),
        )

    def cli(self, job: str, force_full: bool = False) -> dict[str, str]:
        output = self.temp / "github-output"
        output.write_text("")
        result = subprocess.run(
            [
                sys.executable,
                str(SELECTOR),
                "--job",
                job,
                "--force-full",
                str(force_full).lower(),
                "--github-output",
                str(output),
                "--root",
                str(self.repo),
            ],
            env=self.env,
            capture_output=True,
            text=True,
            timeout=15,
            check=True,
        )
        values = dict(line.split("=", 1) for line in output.read_text().splitlines())
        self.assertEqual(set(values), {"run", "reason", "scope"})
        self.assertIn(values["run"], {"true", "false"})
        self.assertNotIn(str(self.repo), result.stdout + result.stderr)
        return values

    def test_docs_skip_but_explicit_release_validation_runs_all(self) -> None:
        self.write("docs/plans/ci.md")
        self.write(".github/workflows/release.yml")
        self.commit()
        self.push_event()
        for job in sorted(selector.JOBS):
            with self.subTest(job=job):
                self.assertEqual(self.cli(job)["run"], "false")
                full = self.cli(job, force_full=True)
                self.assertEqual(full["run"], "true")
                self.assertEqual(full["scope"], "android,backend,ios,web")

    def test_reviewed_dependencies_and_unknown_paths(self) -> None:
        cases = {
            "backend/go.sum": {"backend", "web"},
            "web/tests/check.ts": {"web"},
            "tools/measure_browser_source_inventory.py": {"web"},
            "shared/build.gradle.kts": {"android", "ios"},
            "android/gradle/wrapper/gradle-wrapper.properties": {"android", "ios"},
            "android/app/build.gradle.kts": {"android", "ios"},
            "android/app/src/main/java/Example.kt": {"android"},
            "android/app/src/main/assets/licenses/notice.txt": {"android", "ios"},
            "ios/Shared/LegalResources/notice.txt": {"android", "ios"},
            "ios/PsstShare/ShareViewController.swift": {"ios"},
            "tools/generate_native_notices.py": {"android", "ios"},
            "assets/brand/logo.svg": selector.JOBS,
            "LICENSE": selector.JOBS,
            "docs/protocol/chunked-files.md": selector.JOBS,
            "docs/testing/fixtures/vector.json": selector.JOBS,
            "docs/security/fixtures/vector.json": selector.JOBS,
            ".github/workflows/ci.yml": selector.JOBS,
            ".github/workflows/release.yml": set(),
            ".github/workflows/release.yaml": selector.JOBS,
            ".github/workflows/new-workflow.yml": selector.JOBS,
            "tools/select_ci_checks.py": selector.JOBS,
            "tools/test_ci_change_scope.py": selector.JOBS,
            "README.md": set(),
            "AGENTS.md": set(),
            "docs/security/guide.md": set(),
            "docs/research/study.md": set(),
            "deploy/updater/Dockerfile": set(),
            "tools/publish_verified_release.py": set(),
            "tools/test_release_transport.py": set(),
            "tools/runtime-legal/verify.py": set(),
            "tools/fixtures/release-updater/controller.py": set(),
            "tools/new_tool.py": selector.JOBS,
            "tools/test_storage_pressure.py": selector.JOBS,
            "tools/test_release_new.json": selector.JOBS,
            "tools/test_release_nested/tool.py": selector.JOBS,
            "docs/security/new-input.json": selector.JOBS,
            "new-component/code.py": selector.JOBS,
        }
        for path, expected in cases.items():
            with self.subTest(path=path):
                self.assertEqual(selector.classify_path(path), expected)

    def test_rename_deletion_and_complete_multi_commit_push_range(self) -> None:
        # Renaming backend code into docs must still classify the removed code.
        (self.repo / "docs/plans").mkdir(parents=True)
        self.git("mv", "backend/old.go", "docs/plans/renamed\nnote.md")
        (self.repo / "ios/old.swift").unlink()
        self.commit()
        # The final commit alone only affects web; the full range needs all three.
        (self.repo / "web/deleted.ts").unlink()
        self.commit()
        self.push_event()
        result = self.cli("backend")
        self.assertEqual(result["run"], "true")
        self.assertEqual(result["scope"], "backend,ios,web")

    def test_pull_request_uses_merge_base_instead_of_base_tip(self) -> None:
        self.git("checkout", "-qb", "feature")
        self.write("ios/feature.swift")
        feature = self.commit()
        self.git("checkout", "-q", "main")
        self.write("web/base-only.ts")
        base_tip = self.commit()
        self.git("checkout", "-q", "feature")
        self.event.write_text(json.dumps({"pull_request": {"base": {"sha": base_tip}}}))
        self.env.update(
            GITHUB_EVENT_NAME="pull_request",
            GITHUB_REF="refs/pull/1/merge",
            GITHUB_SHA=feature,
            GITHUB_EVENT_PATH=str(self.event),
        )
        result = self.cli("web")
        self.assertEqual(result["run"], "false")
        self.assertEqual(result["scope"], "ios")

    def test_uncertain_events_history_and_unknown_changes_run_all(self) -> None:
        self.write("docs/plans/ci.md")
        self.write(".github/workflows/release.yml")
        self.commit()
        self.push_event()
        original_env = self.env.copy()
        cases = (
            ({"GITHUB_EVENT_NAME": "workflow_dispatch"}, None),
            ({"GITHUB_EVENT_NAME": "future_event"}, None),
            ({"GITHUB_REF": "refs/tags/v1"}, None),
            ({"GITHUB_REF": "refs/heads/other"}, None),
            ({"GITHUB_SHA": self.base}, None),
            ({"GITHUB_SHA": "invalid"}, None),
            ({"GITHUB_EVENT_PATH": str(self.temp / "missing")}, None),
            ({}, '{"before": "' + "0" * 40 + '"}'),
            ({}, '{"before": "' + "1" * 40 + '"}'),
            ({}, '{"before": "HEAD"}'),
            ({}, "{}"),
            ({}, "[]"),
            ({}, "malformed secret event data"),
            ({}, " " * (selector.MAX_EVENT_BYTES + 1)),
        )
        for overrides, contents in cases:
            with self.subTest(overrides=overrides, event_size=len(contents or "")):
                self.env = original_env | overrides
                self.event.write_text(contents or json.dumps({"before": self.base}))
                result = self.cli("android")
                self.assertEqual(result["run"], "true")
                self.assertEqual(result["scope"], "android,backend,ios,web")
        self.env = original_env
        self.event.write_text(json.dumps({"before": self.base}))
        for error in (OSError(), subprocess.TimeoutExpired("git", 10)):
            with self.subTest(git_error=type(error).__name__):
                with patch.object(selector.subprocess, "run", side_effect=error):
                    selection = selector.select(self.repo, self.env, False)
                self.assertEqual(selection.jobs, selector.JOBS)
                self.assertEqual(selection.reason, "change-range-unavailable")
        self.write("tools/unknown_release_helper.py")
        self.commit()
        self.push_event()
        self.assertEqual(self.cli("android")["scope"], "android,backend,ios,web")


if __name__ == "__main__":
    unittest.main()
