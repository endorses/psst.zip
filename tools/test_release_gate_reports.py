"""Native smoke and private process checks with tiny direct fixtures.

The runtime description is an explicit injected boundary; actual OCI archives go
through the shared Go verifier. Source and source-CI policy live in Go tests.
"""

from __future__ import annotations

import copy
import io
import os
import sys
import tarfile
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import patch

import generate_release_gate_reports as producer
import publish_container_release as publication
import release_process
import test_release_oci as oci_fixtures
from release_artifacts import InvalidRelease, json_bytes


class NativeSmokeChecks(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="psst-smoke-inputs-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.binding = producer.NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", "a" * 40, "linux/amd64"
        )
        self.original_source = self.root / "runtime-source.tar.gz"
        self.original_source.write_bytes(b"disposable retained-source fixture")
        self.packs = {}
        self.measurements = {}
        self.smokes = {}
        self.archives = {}
        self.configs = {}
        self.smoke_temp_roots = []
        self.descriptions = {}
        for platform in producer.PLATFORMS:
            arch = platform.split("/")[1]
            pack = self.root / ("runtime-" + arch)
            pack.mkdir()
            source = self.original_source
            (pack / source.name).write_bytes(source.read_bytes())
            manifest = {
                "schema_version": 2,
                "kind": "native-runtime-source-description",
                "version": self.binding.version,
                "revision": self.binding.commit,
                "architecture": arch,
                "source_asset": {
                    "file": source.name,
                    "sha256": publication.source_digest(source)[7:],
                    "size": source.stat().st_size,
                    "url": f"https://github.com/{self.binding.repository}/releases/download/{self.binding.version}/{source.name}",
                },
                "overlays": {
                    component: {
                        name: "a" * 64
                        for name in (
                            "THIRD_PARTY_NOTICES.txt",
                            "runtime-inventory.json",
                            "SOURCE.txt",
                        )
                    }
                    for component in ("backend", "web")
                },
            }
            inventory = {
                "schema_version": 2,
                "kind": "container-source-inventory",
                "source": {
                    "repository": self.binding.repository,
                    "version": self.binding.version,
                    "commit": self.binding.commit,
                },
            }
            (pack / "source-inventory.json").write_bytes(json_bytes(inventory))
            manifest["source_inventory_sha256"] = publication.sha256(
                json_bytes(inventory)
            )
            self.descriptions[str(pack)] = manifest
            configs, archives = {}, {}
            for component in ("backend", "web"):
                files, configs[component] = oci_fixtures.fixture(component, arch)
                archive = self.root / (component + "-" + arch + ".tar")
                with tarfile.open(archive, "w:") as output:
                    for name, body in files.items():
                        member = tarfile.TarInfo(name)
                        member.size = len(body)
                        output.addfile(member, io.BytesIO(body))
                archives[component] = archive
            self.packs[platform] = pack
            self.configs[platform] = configs
            self.archives[platform] = archives
            self.smokes[platform] = {
                "schema_version": 1,
                "kind": "release-image-smoke",
                "version": self.binding.version,
                "revision": self.binding.commit,
                "platform": platform,
                "execution": "native",
                "tested_configs": configs,
                "runtime_pack_sha256": publication.sha256(
                    (pack / "source-inventory.json").read_bytes()
                ),
                "checks": sorted(producer.CHECKS),
                "completed_at": "2026-10-07T16:00:00+00:00",
                "publication_authorized": False,
            }

        def describe(context, mode, *args):
            self.assertEqual(mode, "describe")
            self.assertEqual(args[0], "--collection")
            pack = Path(args[1])
            record = self.descriptions[str(pack)]
            source = pack / record["source_asset"]["file"]
            if (
                publication.source_digest(source)[7:]
                != record["source_asset"]["sha256"]
            ):
                raise InvalidRelease("fixture source bytes changed")
            return json_bytes(record)

        self.description_patch = patch.object(
            producer, "source_command", side_effect=describe
        )
        self.description_patch.start()
        self.addCleanup(self.description_patch.stop)

    def execute_smoke(self, args, *, environment, timeout):
        self.assertEqual(args[1], str(producer.ROOT / "tools/verify_release_images.py"))
        self.assertNotIn("GH_TOKEN", environment)
        self.assertNotIn("GITHUB_TOKEN", environment)
        self.assertEqual(timeout, 1200)
        platform = args[args.index("--platform") + 1]
        for component in ("backend", "web"):
            self.assertEqual(
                args[args.index("--" + component + "-image") + 1],
                self.configs[platform][component],
            )
        self.assertIn("--runtime-pack", args)
        report = Path(args[args.index("--report") + 1])
        self.smoke_temp_roots.append(report.parent)
        report.write_bytes(json_bytes(self.smokes[platform]))
        return b"fixture process succeeded"

    def native(self, platform="linux/amd64", execute=None):
        return producer.collect_native_measurement(
            producer.NativeSourceContext(
                self.binding.repository,
                self.binding.version,
                self.binding.commit,
                platform,
            ),
            pack=self.packs[platform],
            archives=self.archives[platform],
            tested_configs=self.configs[platform],
            execute=execute or self.execute_smoke,
        )

    def test_native_driver_runs_harness_and_maps_actual_configs_to_final_children(self):
        result = self.native()
        self.assertFalse(result["publication_authorized"])
        self.assertNotIn("binding_digest", result)
        self.assertEqual(result["source"]["commit"], self.binding.commit)
        for component in ("backend", "web"):
            self.assertEqual(
                result["images"][component]["config_digest"],
                result["smoke"]["tested_configs"][component],
            )
        self.assertTrue(all(not path.exists() for path in self.smoke_temp_roots))

    def test_runtime_source_inventory_substitution_during_smoke_fails(self):
        def substitute(args, **kwargs):
            self.execute_smoke(args, **kwargs)
            path = self.packs["linux/amd64"] / "source-inventory.json"
            path.write_bytes(path.read_bytes() + b"\n")
            return b"fixture harness succeeded"

        with self.assertRaisesRegex(
            InvalidRelease, "Runtime .*context|Runtime inputs changed"
        ):
            self.native(execute=substitute)

    def test_failed_harness_missing_report_or_incomplete_smoke_does_not_yield_measurement(
        self,
    ):
        def failure(*args, **kwargs):
            raise InvalidRelease("actual harness failed")

        with self.assertRaisesRegex(InvalidRelease, "harness failed"):
            self.native(execute=failure)
        with self.assertRaises((InvalidRelease, OSError)):
            self.native(execute=lambda *args, **kwargs: b"no report")
        original = copy.deepcopy(self.smokes["linux/amd64"])
        for field, value in (
            ("runtime_pack_sha256", None),
            ("checks", []),
            ("revision", "b" * 40),
            ("execution", "emulated"),
            ("publication_authorized", True),
            ("tested_configs", self.configs["linux/arm64"]),
        ):
            self.smokes["linux/amd64"] = {**original, field: value}
            with self.subTest(field=field), self.assertRaises(InvalidRelease):
                self.native()

    def test_changed_source_or_notice_inventory_and_config_substitution_fail(self):
        source = self.packs["linux/amd64"] / self.original_source.name
        source.write_bytes(b"substituted corresponding sources")
        with self.assertRaisesRegex(InvalidRelease, "source bytes"):
            self.native()
        source.write_bytes(self.original_source.read_bytes())
        self.configs["linux/amd64"]["backend"] = "sha256:" + "c" * 64
        with self.assertRaises(InvalidRelease):
            self.native()


class PrivateProcessChecks(unittest.TestCase):
    def test_process_timeout_and_output_bound_remain_private(self):
        before = time.monotonic()
        with self.assertRaises(InvalidRelease):
            release_process.command(
                [sys.executable, "-c", "import time; time.sleep(60)"],
                environment={"PATH": os.environ["PATH"]},
                timeout=0.05,
            )
        self.assertLess(time.monotonic() - before, 3)
        with (
            patch.object(release_process, "MAX_JSON", 8),
            self.assertRaises(InvalidRelease),
        ):
            release_process.command(
                [sys.executable, "-c", "print('private disposable output')"],
                environment={"PATH": os.environ["PATH"]},
                timeout=1,
            )


if __name__ == "__main__":
    unittest.main()
