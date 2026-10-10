"""Fast source-security boundary regressions; no lint, builds or network."""

import contextlib
import io
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import check_source_bidi as checker


class SourceBidiTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="psst-bidi-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.source = self.root / "app.kt"
        self.source.write_text("// psst.zip — Grüße und 日本語\nval title = 'A'\n")
        inventory = patch.object(checker, "git_inventory", return_value=b"app.kt\0")
        inventory.start()
        self.addCleanup(inventory.stop)

    def test_ordinary_unicode_and_safe_unicode_escapes_remain_allowed(self):
        self.source.write_text('// Grüße und 日本語\nval title = "\\u0041"\n')
        self.assertEqual(checker.check_sources(self.root, []), 1)

    def test_literal_controls_and_escaped_java_kotlin_swift_forms_are_rejected(self):
        cases = [
            "// hidden \u202e\n",
            "val hidden = '\\u202A'\n",
            "// hidden \\uuuu202e\n",
            'let hidden = "\\u{2066}"\n',
            "// hidden \\U2069\n",
            'val hidden = "\\\\u202e"\n',
        ]
        for source in cases:
            with self.subTest(source=ascii(source)):
                self.source.write_text("// ordinary\n" + source)
                with self.assertRaisesRegex(
                    checker.SourceCheckError, r"app\.kt.*:2 U\+"
                ) as found:
                    checker.check_sources(self.root, [])
                self.assertNotIn("hidden", str(found.exception))

    def test_tracked_inventory_ignores_untracked_source_and_covers_uppercase_suffix(
        self,
    ):
        (self.root / "untracked.kt").write_text("// \u202e")
        self.assertEqual(checker.check_sources(self.root, []), 1)
        upper = self.root / "Account.SWIFT"
        upper.write_text('let title = "\\u{202E}"')
        with (
            patch.object(
                checker, "git_inventory", return_value=b"app.kt\0Account.SWIFT\0"
            ),
            self.assertRaises(checker.SourceCheckError),
        ):
            checker.check_sources(self.root, [])

    def test_invalid_utf8_in_known_source_fails_without_printing_contents(self):
        self.source.write_bytes(b"private-value\xff")
        output = io.StringIO()
        with (
            patch.object(checker, "ROOT", self.root),
            patch("sys.argv", ["check_source_bidi.py"]),
            contextlib.redirect_stderr(output),
            self.assertRaises(SystemExit) as result,
        ):
            checker.main()
        self.assertEqual(result.exception.code, 1)
        self.assertIn("invalid UTF-8", output.getvalue())
        self.assertNotIn("private-value", output.getvalue())

    def test_missing_source_bad_inventory_and_inventory_size_fail_closed(self):
        self.source.unlink()
        with self.assertRaises(checker.SourceCheckError):
            checker.check_sources(self.root, [])
        for raw in (b"app.kt", b"../outside.kt\0", b"/absolute.kt\0", b"README.md\0"):
            with (
                patch.object(checker, "git_inventory", return_value=raw),
                self.assertRaises(checker.SourceCheckError),
            ):
                checker.check_sources(self.root, [])
        with (
            patch.object(checker, "MAX_FILES", 1),
            patch.object(
                checker, "git_inventory", return_value=b"app.kt\0other.java\0"
            ),
            self.assertRaises(checker.SourceCheckError),
        ):
            checker.check_sources(self.root, [])

    def test_symlink_source_and_parent_are_rejected(self):
        target = self.root / "regular.kt"
        target.write_text("// ordinary")
        self.source.unlink()
        self.source.symlink_to(target)
        with self.assertRaisesRegex(checker.SourceCheckError, "symlinks"):
            checker.check_sources(self.root, [])
        directory = self.root / "linked"
        directory.symlink_to(self.root, target_is_directory=True)
        with (
            patch.object(checker, "git_inventory", return_value=b"linked/regular.kt\0"),
            self.assertRaisesRegex(checker.SourceCheckError, "symlinks"),
        ):
            checker.check_sources(self.root, [])

    def test_explicit_generated_source_is_checked_but_broad_build_directories_are_refused(
        self,
    ):
        generated = self.root / "app/build/generated/ksp/release/kotlin"
        generated.mkdir(parents=True)
        (generated / "Dao.kt").write_text("// ordinary")
        self.assertEqual(checker.check_sources(self.root, [generated]), 2)
        (generated / "Dao.kt").write_text("// \u2067")
        with self.assertRaises(checker.SourceCheckError):
            checker.check_sources(self.root, [generated])
        for directory in (generated.parents[2], self.root, self.root.parent):
            with self.assertRaises(checker.SourceCheckError):
                checker.check_sources(self.root, [directory])
        (generated / "Dao.kt").write_text("// ordinary")
        (generated / "linked.kt").symlink_to(self.source)
        with self.assertRaisesRegex(checker.SourceCheckError, "symlinks"):
            checker.check_sources(self.root, [generated])

    def test_file_total_and_generated_entry_bounds_are_enforced(self):
        self.source.write_text("// ordinary\n" * 20)
        with (
            patch.object(checker, "MAX_FILE_BYTES", 10),
            self.assertRaisesRegex(checker.SourceCheckError, "file size"),
        ):
            checker.check_sources(self.root, [])
        with (
            patch.object(checker, "MAX_TOTAL_BYTES", 10),
            self.assertRaisesRegex(checker.SourceCheckError, "Combined source"),
        ):
            checker.check_sources(self.root, [])
        generated = self.root / "app/build/generated/ksp/release/kotlin"
        generated.mkdir(parents=True)
        (generated / "one.kt").write_text("// ordinary")
        (generated / "two.kt").write_text("// ordinary")
        with (
            patch.object(checker, "MAX_FILES", 1),
            self.assertRaisesRegex(
                checker.SourceCheckError, "Generated directory inventory"
            ),
        ):
            checker.check_sources(self.root, [generated])
        nested = generated / "nested"
        nested.mkdir()
        with (
            patch.object(checker, "MAX_DIRECTORY_DEPTH", 0),
            self.assertRaisesRegex(checker.SourceCheckError, "depth"),
        ):
            checker.check_sources(self.root, [generated])


if __name__ == "__main__":
    unittest.main()
