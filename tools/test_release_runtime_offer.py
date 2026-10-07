"""Runtime notices/source offers must match served final overlay bytes."""

import hashlib
import unittest
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace

from verify_release_images import check_runtime_offer, write_smoke_report


class RuntimeOfferChecks(unittest.TestCase):
    def setUp(self):
        self.url = "https://github.com/endorses/psst.zip/releases/download/v1.2.3/runtime.tar.gz"
        self.asset_hash = "a" * 64
        self.files = {
            "THIRD_PARTY_NOTICES.txt": b"Complete recorded runtime notice terms",
            "runtime-inventory.json": b'{"original_sources_recorded":true}',
            "SOURCE.txt": (self.url + "\n" + self.asset_hash + "\n").encode(),
            "licenses/MIT.txt": b"Original complete MIT terms",
        }
        self.pack = {
            "overlays": {
                "web": {
                    name: hashlib.sha256(data).hexdigest()
                    for name, data in self.files.items()
                }
            },
            "source_asset": {"url": self.url, "sha256": self.asset_hash},
        }

    def get(self, route):
        return self.files[route.removeprefix("/licenses/runtime/")]

    def test_all_served_legal_files_and_exact_source_offer_are_checked(self):
        check_runtime_offer(self.get, self.pack)
        self.files["licenses/MIT.txt"] += b" modified"
        with self.assertRaisesRegex(RuntimeError, "bytes differ"):
            check_runtime_offer(self.get, self.pack)

    def test_source_offer_cannot_point_at_another_archive(self):
        self.pack["source_asset"]["url"] = "https://example.invalid/wrong.tar.gz"
        with self.assertRaisesRegex(RuntimeError, "archive identity"):
            check_runtime_offer(self.get, self.pack)

    def test_missing_and_unsafe_paths_are_rejected(self):
        del self.pack["overlays"]["web"]["SOURCE.txt"]
        with self.assertRaisesRegex(RuntimeError, "files missing"):
            check_runtime_offer(self.get, self.pack)
        self.setUp()
        self.pack["overlays"]["web"]["../private"] = "b" * 64
        with self.assertRaisesRegex(RuntimeError, "Unsafe"):
            check_runtime_offer(self.get, self.pack)


class SmokeReportChecks(unittest.TestCase):
    def test_measurements_bind_exact_configs_and_pack_without_approval(self):
        args = SimpleNamespace(
            version="v1.2.3", revision="a" * 40, platform="linux/amd64"
        )
        configs = {"backend": "sha256:" + "b" * 64, "web": "sha256:" + "c" * 64}
        with tempfile.TemporaryDirectory(prefix="psst-smoke-report-") as directory:
            path = Path(directory) / "smoke.json"
            write_smoke_report(path, args, configs, "native", "sha256:" + "d" * 64)
            result = json.loads(path.read_bytes())
            self.assertEqual(result["tested_configs"], configs)
            self.assertIn("runtime-offer", result["checks"])
            self.assertFalse(result["publication_authorized"])
            self.assertNotIn("passed", result)
            with self.assertRaises(FileExistsError):
                write_smoke_report(path, args, configs, "native", None)
            path.unlink()
            write_smoke_report(path, args, configs, "emulated", None)
            result = json.loads(path.read_bytes())
            self.assertIsNone(result["runtime_pack_sha256"])
            self.assertNotIn("runtime-offer", result["checks"])

    def test_invalid_or_incomplete_measurements_cannot_create_report(self):
        from release_artifacts import InvalidRelease

        args = SimpleNamespace(
            version="v1.2.3", revision="a" * 40, platform="linux/amd64"
        )
        with tempfile.TemporaryDirectory(prefix="psst-smoke-report-") as directory:
            path = Path(directory) / "smoke.json"
            for configs, mode, pack in [
                ({"backend": "sha256:" + "b" * 64}, "native", None),
                (
                    {"backend": "sha256:" + "b" * 64, "web": "mutable-tag"},
                    "native",
                    None,
                ),
                (
                    {"backend": "sha256:" + "b" * 64, "web": "sha256:" + "c" * 64},
                    "unknown",
                    None,
                ),
                (
                    {"backend": "sha256:" + "b" * 64, "web": "sha256:" + "c" * 64},
                    "native",
                    "not-a-digest",
                ),
            ]:
                with self.subTest(configs=configs, mode=mode, pack=pack):
                    with self.assertRaises((RuntimeError, InvalidRelease)):
                        write_smoke_report(path, args, configs, mode, pack)
                    self.assertFalse(path.exists())


if __name__ == "__main__":
    unittest.main()
