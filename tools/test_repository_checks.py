#!/usr/bin/env python3
"""Disposable integration checks; generated fake credentials never enter this repo."""

from __future__ import annotations

import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
CHECKER = ROOT / "tools/check_repository.py"
REAL_SCANNER = shutil.which("gitleaks")


class RepositoryChecks(unittest.TestCase):
    def setUp(self) -> None:
        self.directory = tempfile.TemporaryDirectory(prefix="psst-hook-test-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.repo = self.root / "repository with spaces"
        self.repo.mkdir()
        self.env = os.environ.copy()
        self.env["GIT_CONFIG_GLOBAL"] = os.devnull
        self.env["GIT_CONFIG_NOSYSTEM"] = "1"
        self.git("init", "-q")
        self.git("config", "user.name", "Hook test")
        self.git("config", "user.email", "test@example.invalid")
        self.write(".gitleaks.toml", "[extend]\nuseDefault = true\n")
        self.git("add", ".gitleaks.toml")
        self.git("commit", "-qm", "Initial policy")
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.fake_scanner("raise SystemExit(0)\n")
        self.env["PATH"] = str(self.bin) + os.pathsep + self.env["PATH"]

    def git(self, *args: str) -> bytes:
        result = subprocess.run(
            ["git", *args], cwd=self.repo, env=self.env, capture_output=True, check=True
        )
        return result.stdout

    def write(self, path: str, contents: str | bytes) -> Path:
        target = self.repo / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(contents.encode() if isinstance(contents, str) else contents)
        return target

    def fake_scanner(self, body: str) -> None:
        script = self.bin / "gitleaks"
        script.write_text(
            f"#!{sys.executable}\nimport sys, os, json\nfrom pathlib import Path\n{body}"
        )
        script.chmod(0o755)

    def check(self, mode: str = "staged") -> subprocess.CompletedProcess:
        return subprocess.run(
            [sys.executable, str(CHECKER), mode],
            cwd=self.repo,
            env=self.env,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_missing_scanner_fails_closed(self) -> None:
        (self.bin / "gitleaks").unlink()
        (self.bin / "git").symlink_to(shutil.which("git"))
        self.env["PATH"] = str(self.bin)
        result = self.check()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Gitleaks is required", result.stderr)

    def test_scanner_flags_policy_and_ignore_are_from_index(self) -> None:
        self.write(".gitleaksignore", "reviewed:historical:fingerprint\n")
        self.git("add", ".gitleaksignore")
        self.write(".gitleaksignore", "unreviewed-worktree-ignore\n")
        self.write(".gitleaks.toml", "unreviewed-worktree-policy\n")
        self.env["GITLEAKS_CONFIG_TOML"] = "unreviewed-ambient-policy"
        self.env["GITLEAKS_CONFIG"] = "/nonexistent/policy"
        self.fake_scanner(
            "args = sys.argv[1:]\n"
            "assert '--pre-commit' in args and '--staged' in args\n"
            "assert '--redact=100' in args and '--ignore-gitleaks-allow' in args\n"
            "assert not any(k.startswith('GITLEAKS_') for k in os.environ)\n"
            "assert Path(args[args.index('--config') + 1]).read_text() == '[extend]\\nuseDefault = true\\n'\n"
            "assert Path(args[args.index('--gitleaks-ignore-path') + 1]).read_text() == 'reviewed:historical:fingerprint\\n'\n"
        )
        self.assertEqual(self.check().returncode, 0)

    def test_scanner_output_and_findings_are_not_echoed(self) -> None:
        generated_value = secrets.token_urlsafe(32)
        self.fake_scanner(
            f"print({generated_value!r})\n"
            "args = sys.argv[1:]\n"
            "Path(args[args.index('--report-path') + 1]).write_text(json.dumps([{'Secret': 'hidden'}]))\n"
            "raise SystemExit(1)\n"
        )
        result = self.check()
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(generated_value, result.stdout + result.stderr)
        self.assertIn("1 secret finding", result.stderr)

    def test_sensitive_filenames_with_spaces_and_renames(self) -> None:
        self.write("private folder/production store.sqlite-wal", "disposable data\n")
        self.git("add", "private folder/production store.sqlite-wal")
        result = self.check("files")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("production store.sqlite-wal", result.stderr)
        self.git("mv", "private folder/production store.sqlite-wal", "public.txt")
        self.assertEqual(self.check("files").returncode, 0)

    def test_env_payload_and_mobile_artifacts_are_rejected(self) -> None:
        for path in (
            ".env.production",
            "deploy/release.env",
            "deploy/release.env.backup",
            "uploads/example.txt",
            "app.ipa",
            "server.key",
        ):
            with self.subTest(path=path):
                self.write(path, "generated\n")
                self.git("add", path)
                self.assertNotEqual(self.check("files").returncode, 0)
                self.git("rm", "--cached", path)

    def test_public_fixtures_and_env_example_are_allowed(self) -> None:
        self.write(".env.example", "PUBLIC_URL=https://example.invalid\n")
        self.write("deploy/release.env.example", "PUBLIC_URL=https://example.invalid\n")
        self.write("tests/fixtures/public.json", '{"example": true}\n')
        self.write("tests/fixtures/public.crt", "public certificate fixture\n")
        self.git("add", ".env.example", "deploy/release.env.example", "tests")
        self.assertEqual(self.check().returncode, 0)

    def test_force_added_retcon_artifacts_remain_blocked_after_deletion(self) -> None:
        self.write(".gitignore", ".retcon-private/\n")
        self.git("add", ".gitignore")
        self.git("commit", "-qm", "Ignore private rewrite artifacts")
        for path in (
            ".retcon-private/before-retcon.bundle",
            "nested/.retcon-private/commit-map.txt",
        ):
            with self.subTest(path=path):
                self.write(path, "disposable private rewrite artifact\n")
                self.git("add", "-f", path)
                result = self.check("files")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(repr(path), result.stderr)
                self.git("commit", "-qm", "Disposable historical rewrite artifact")
                self.git("rm", path)
                self.git("commit", "-qm", "Remove disposable rewrite artifact")
                self.assertEqual(self.check("files").returncode, 0)
                result = self.check("history")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(repr(path), result.stderr)

    def test_size_uses_staged_blob(self) -> None:
        target = self.write("large.txt", b"x" * (5 * 1024 * 1024 + 1))
        self.git("add", "large.txt")
        target.write_text("small unstaged contents\n")
        result = self.check("files")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("5 MiB", result.stderr)

    def test_deleted_sensitive_files_and_large_blobs_remain_blocked(self) -> None:
        fixtures = {
            ".env": b"PUBLIC_URL=https://example.invalid\n",
            "private folder/store.sqlite": b"disposable database\n",
            "payloads/file with\na newline.txt": b"disposable payload\n",
            "large deleted.txt": b"x" * (5 * 1024 * 1024 + 1),
        }
        for path, contents in fixtures.items():
            with self.subTest(path=path):
                self.write(path, contents)
                self.git("add", path)
                self.git("commit", "-qm", "Disposable historical artifact")
                self.git("rm", path)
                self.git("commit", "-qm", "Removed historical artifact")
                self.assertEqual(self.check("files").returncode, 0)
                result = self.check("history")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(repr(path), result.stderr)

    def test_conflict_marker_is_rejected_without_echoing_contents(self) -> None:
        value = secrets.token_urlsafe(32)
        self.write(
            "conflict.txt", f"<<<<<<< {value}\nold\n=======\nnew\n>>>>>>> branch\n"
        )
        self.git("add", "conflict.txt")
        result = self.check()
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(value, result.stdout + result.stderr)

    @unittest.skipUnless(
        shutil.which("gofmt"), "Go is needed for Go formatting integration"
    )
    def test_go_format_checks_staged_contents_not_worktree(self) -> None:
        target = self.write("sample.go", "package sample\n\nvar X = 1\n")
        self.git("add", "sample.go")
        target.write_text("package sample\nvar X=1\n")
        self.assertEqual(self.check().returncode, 0)
        self.git("add", "sample.go")
        target.write_text("package sample\n\nvar X = 1\n")
        self.assertNotEqual(self.check().returncode, 0)

    def test_web_format_checks_staged_contents_not_worktree(self) -> None:
        prettier = self.write(
            "web/node_modules/.bin/prettier",
            f"#!{sys.executable}\nimport sys\nprint(sys.stdin.read().strip())\n",
        )
        prettier.chmod(0o755)
        target = self.write("web/src/example.ts", "export const value = 1;\n")
        self.git("add", "web/src/example.ts")
        target.write_text("  export const value = 1;\n")
        self.assertEqual(self.check().returncode, 0)
        self.git("add", "web/src/example.ts")
        target.write_text("export const value = 1;\n")
        self.assertNotEqual(self.check().returncode, 0)

    @unittest.skipUnless(
        REAL_SCANNER, "Gitleaks is needed for actual secret-scanning integration"
    )
    def test_real_scanner_rejects_staged_secret_removed_in_worktree(self) -> None:
        (self.bin / "gitleaks").unlink()
        (self.bin / "gitleaks").symlink_to(REAL_SCANNER)
        token = "gh" + "p_" + secrets.token_hex(18)
        target = self.write("secret.txt", f"access_token={token}\n")
        self.git("add", "secret.txt")
        target.write_text("removed only in worktree\n")
        result = self.check()
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(token, result.stdout + result.stderr)

    @unittest.skipUnless(
        REAL_SCANNER, "Gitleaks is needed for actual history integration"
    )
    def test_real_scanner_rejects_secret_only_in_history(self) -> None:
        (self.bin / "gitleaks").unlink()
        (self.bin / "gitleaks").symlink_to(REAL_SCANNER)
        token = "gh" + "p_" + secrets.token_hex(18)
        self.write("removed.txt", f"access_token={token}\n")
        self.git("add", "removed.txt")
        self.git("commit", "-qm", "Disposable finding")
        self.git("rm", "removed.txt")
        self.git("commit", "-qm", "Removed finding")
        result = self.check("history")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(token, result.stdout + result.stderr)

    def prepare_installer(self) -> None:
        shutil.copy2(ROOT / "install-hooks.sh", self.repo / "install-hooks.sh")
        (self.repo / "hooks").mkdir()
        for name in ("pre-commit", "pre-push"):
            shutil.copy2(ROOT / "hooks" / name, self.repo / "hooks" / name)

    def install(self) -> subprocess.CompletedProcess:
        return subprocess.run(
            ["sh", str(self.repo / "install-hooks.sh")],
            cwd=self.repo,
            env=self.env,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_installer_is_repeatable_and_sets_absolute_path(self) -> None:
        self.prepare_installer()
        self.assertEqual(self.install().returncode, 0)
        self.assertEqual(self.install().returncode, 0)
        self.assertEqual(
            self.git("config", "--get", "core.hooksPath").decode().strip(),
            str(self.repo / "hooks"),
        )

    def test_installer_refuses_existing_hooks_configuration(self) -> None:
        self.prepare_installer()
        self.git("config", "core.hooksPath", "existing-hooks")
        self.assertNotEqual(self.install().returncode, 0)
        self.assertEqual(
            self.git("config", "--get", "core.hooksPath").strip(), b"existing-hooks"
        )

    def test_installer_refuses_existing_default_hook(self) -> None:
        self.prepare_installer()
        hook = self.repo / ".git/hooks/pre-commit"
        hook.write_text("#!/bin/sh\nexit 0\n")
        self.assertNotEqual(self.install().returncode, 0)
        self.assertEqual(hook.read_text(), "#!/bin/sh\nexit 0\n")


if __name__ == "__main__":
    unittest.main()
