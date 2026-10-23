"""Small filesystem regressions for build-cache invalidation; no builds or network."""

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "lan_assets", Path(__file__).with_name("prepare-lan-assets.py")
)
assets = importlib.util.module_from_spec(spec)
spec.loader.exec_module(assets)


class CacheInvalidationTests(unittest.TestCase):
    def test_source_archive_retains_embedded_vendor_notices(self):
        release_spec = importlib.util.spec_from_file_location(
            "release_inputs", Path(__file__).with_name("package-release.py")
        )
        release = importlib.util.module_from_spec(release_spec)
        release_spec.loader.exec_module(release)
        with tempfile.TemporaryDirectory(prefix="psst-lan-assets-test-") as directory:
            root = Path(directory)
            embedded = (
                root
                / "client/internal/lanassets/site/licenses/backend/go/src/vendor/example/LICENSE"
            )
            excluded = root / "client/vendor/example/file.go"
            for path in (embedded, excluded):
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("fixture")
            with (
                patch.object(release, "ROOT", root),
                patch.object(release, "CLIENT", root / "client"),
                patch.object(
                    release, "run", return_value=b"client/vendor/example/file.go\0"
                ),
            ):
                files = release.source_files(False)
                self.assertIn(embedded, files)
                self.assertNotIn(excluded, files)

    def test_lock_changes_refresh_installed_platform_subset(self):
        with tempfile.TemporaryDirectory(prefix="psst-lan-assets-test-") as directory:
            web = Path(directory)
            (web / "node_modules").mkdir()
            packages = {
                "": {"name": "fixture"},
                "node_modules/vite": {"version": "1.0.0", "integrity": "fixture"},
                "node_modules/darwin-only": {
                    "version": "1.0.0",
                    "optional": True,
                    "os": ["darwin"],
                },
            }
            (web / "node_modules/.package-lock.json").write_text(
                json.dumps(
                    {"packages": {"node_modules/vite": packages["node_modules/vite"]}}
                )
            )
            tracked = web / "package-lock.json"
            tracked.write_text(json.dumps({"packages": packages}))
            with (
                patch.object(assets, "WEB", web),
                patch.object(assets.platform, "system", return_value="Linux"),
            ):
                self.assertTrue(assets.npm_dependencies_current())
                packages["node_modules/vite"]["version"] = "2.0.0"
                tracked.write_text(json.dumps({"packages": packages}))
                self.assertFalse(assets.npm_dependencies_current())
                packages["node_modules/vite"]["version"] = "1.0.0"
                packages["node_modules/new-dependency"] = {"version": "1.0.0"}
                tracked.write_text(json.dumps({"packages": packages}))
                self.assertFalse(assets.npm_dependencies_current())

    def test_toolchain_and_legal_text_changes_invalidate_notices(self):
        with tempfile.TemporaryDirectory(prefix="psst-lan-assets-test-") as directory:
            root = Path(directory)
            (root / "client/scripts").mkdir(parents=True)
            (root / "backend").mkdir()
            (root / "client/scripts/generate-notices.py").write_text("fixture")
            toolchain = root / "toolchain"
            toolchain.mkdir()
            (toolchain / "LICENSE").write_text("first legal text")
            info = {"GOVERSION": "go1.27.3", "GOROOT": str(toolchain)}
            with (
                patch.object(assets, "ROOT", root),
                patch.object(
                    assets.subprocess,
                    "check_output",
                    side_effect=lambda *a, **kw: json.dumps(info).encode(),
                ),
            ):
                first = assets.notice_inputs()
                info["GOVERSION"] = "go1.27.4"
                second = assets.notice_inputs()
                self.assertNotEqual(first, second)
                (toolchain / "LICENSE").write_text("changed legal text")
                self.assertNotEqual(second, assets.notice_inputs())


if __name__ == "__main__":
    unittest.main()
