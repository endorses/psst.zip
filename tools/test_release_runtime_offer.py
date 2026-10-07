"""Runtime notices/source offers must match served final overlay bytes."""

import hashlib
import unittest

from verify_release_images import check_runtime_offer


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


if __name__ == "__main__":
    unittest.main()
