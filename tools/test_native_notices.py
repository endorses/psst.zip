"""Boundary tests for exact native licensing inputs, without Gradle/network."""

import io
import json
from pathlib import Path
import tempfile
import unittest
import zipfile

import generate_native_notices as native


class NativeNoticeTests(unittest.TestCase):
    def archive(self, files):
        output = io.BytesIO()
        with zipfile.ZipFile(output, "w") as archive:
            for name, value in files.items():
                archive.writestr(name, value)
        return output.getvalue()

    def test_nested_aar_preserves_actual_notice_and_ignores_classes(self):
        inner = self.archive(
            {
                "META-INF/LICENSE.txt": "Copyright Alice\r\nLicense text  \r\n",
                "Notice.class": b"\xca\xfe\xba\xbe",
            }
        )
        legal = native.legal_files(
            self.archive({"classes.jar": inner, "NOTICE": "Upstream notice"})
        )
        self.assertEqual(set(legal), {"classes.jar!/META-INF/LICENSE.txt", "NOTICE"})
        self.assertEqual(
            native.normalize(legal["classes.jar!/META-INF/LICENSE.txt"]),
            b"Copyright Alice\nLicense text\n",
        )

    def test_oversized_notice_is_rejected_before_reading(self):
        with self.assertRaisesRegex(ValueError, "Oversized native legal input"):
            native.legal_files(
                self.archive({"LICENSE.txt": b"x" * (native.MAX_LICENSE + 1)})
            )

    def test_mit_requires_exact_reviewed_copyright_source(self):
        mit = [{"name": "MIT License", "url": "https://example.org/MIT"}]
        self.assertEqual(
            native.effective_license("org.slf4j:slf4j-api:2.0.16", mit), "MIT"
        )
        with self.assertRaisesRegex(ValueError, "Unreviewed native dependency"):
            native.effective_license("org.slf4j:slf4j-api:2.0.17", mit)
        with self.assertRaisesRegex(ValueError, "Review multiple"):
            native.effective_license(
                "example:unknown:1", mit + [{"name": "GPL", "url": ""}]
            )

    def test_parent_pom_license_and_actual_copyright_are_preserved(self):
        with tempfile.TemporaryDirectory(
            prefix="psst-native-license-test-"
        ) as directory:
            child = Path(directory) / "child.pom"
            parent = Path(directory) / "parent.pom"
            child.write_text(
                '<project xmlns="urn:maven"><scm><url>https://example.org/exact-source</url></scm></project>'
            )
            parent.write_text(
                '<!-- Copyright 2026 Alice --><project xmlns="urn:maven"><licenses><license><name>Apache-2.0</name><url>https://www.apache.org/licenses/LICENSE-2.0.txt</url></license></licenses></project>'
            )
            licenses, source, inputs, copyrights = native.pom_metadata(
                [str(child), str(parent)]
            )
            self.assertEqual(licenses[0]["name"], "Apache-2.0")
            self.assertEqual(source, "https://example.org/exact-source")
            self.assertEqual(len(inputs), 2)
            self.assertEqual(copyrights, ["Copyright 2026 Alice"])
            with self.assertRaisesRegex(ValueError, "no effective declared license"):
                native.pom_metadata([str(child)])

    def test_snapshot_byte_drift_and_version_changes_fail_closed(self):
        with tempfile.TemporaryDirectory(
            prefix="psst-native-license-test-"
        ) as directory:
            root = Path(directory)
            (root / "shared/gradle").mkdir(parents=True)
            (root / "shared/licenses/upstream").mkdir(parents=True)
            (root / "shared/gradle/libs.versions.toml").write_text(
                '[versions]\nkotlin="2.3.21"\nskie="0.10.12"\n'
            )
            text = root / "shared/licenses/upstream/LICENSE.txt"
            text.write_bytes(b"Copyright Alice\n")
            inventory = {
                "kotlin_version": "2.3.21",
                "skie_version": "0.10.12",
                "sources": [
                    {
                        "file": "LICENSE.txt",
                        "sha256": native.sha(text.read_bytes()),
                        "source": "https://example.org/exact/LICENSE.txt",
                    }
                ],
            }
            (root / "shared/licenses/source-inventory.json").write_text(
                json.dumps(inventory)
            )
            native.load_sources(root)
            text.write_bytes(b"License without copyright\n")
            with self.assertRaisesRegex(ValueError, "notice drift"):
                native.load_sources(root)
            inventory["kotlin_version"] = "2.3.20"
            (root / "shared/licenses/source-inventory.json").write_text(
                json.dumps(inventory)
            )
            with self.assertRaisesRegex(ValueError, "explicit version refresh"):
                native.load_sources(root)


if __name__ == "__main__":
    unittest.main()
