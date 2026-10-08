"""Small joins of independently tested source producers; no builds or downloads."""

from __future__ import annotations

from contextlib import ExitStack
import copy
from dataclasses import replace
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import generate_corresponding_source_review as source
from publish_container_release import (
    Binding,
    SOURCE_COVERAGE,
    sha256,
    source_review_details,
)
from release_artifacts import InvalidRelease, json_bytes


class CorrespondingSource(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="psst-source-join-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.assets = {}
        self.packs, self.native, self.runtime_paths, self.runtime_inputs = (
            {},
            {},
            {},
            {},
        )
        self.native_paths, self.collections, self.captures, self.archives = (
            {},
            {},
            {},
            {},
        )
        subjects = {
            key: "fixture:" + key
            for key in ("manifest", "bundle", "backend-index", "web-index")
        }
        for target in SOURCE_COVERAGE:
            subjects[target] = (
                "oci://ghcr.io/fixture/psst-zip-"
                + target.split("-")[0]
                + "@"
                + sha256(target.encode())
            )

        def asset(name):
            path = self.root / name
            path.write_bytes(name.encode())
            self.assets[name] = path
            subjects["source:" + name] = (
                "file:" + name + "@" + sha256(path.read_bytes())
            )
            return path

        application = asset("psst.zip-source-v0.1.0.tar.gz")
        upstream = asset("psst.zip-upstream-inputs-v0.1.0.tar.gz")
        self.upstream = {
            "asset": {"name": upstream.name, "digest": sha256(upstream.read_bytes())},
            "collection_sha256": sha256(b"upstream record"),
        }
        self.upstream_collection = self.root / "offering"
        self.upstream_collection.mkdir()
        (self.upstream_collection / source.upstream_inputs.RECORD).write_bytes(
            b"upstream record"
        )
        for platform in source.PLATFORMS:
            arch = platform.split("/")[1]
            runtime_asset = asset(f"psst.zip-runtime-sources-v0.1.0-{arch}.tar.gz")
            asset(f"psst.zip-dependency-inputs-v0.1.0-{arch}.tar.gz")
            pack = self.root / ("pack-" + arch)
            pack.mkdir()
            (pack / "runtime-pack.json").write_bytes(json_bytes({"architecture": arch}))
            self.packs[platform] = pack
            runtime = {
                "runtime_pack_sha256": sha256(
                    (pack / "runtime-pack.json").read_bytes()
                ),
                "source_asset": {
                    "name": runtime_asset.name,
                    "digest": sha256(runtime_asset.read_bytes()),
                    "size": runtime_asset.stat().st_size,
                },
                "notice_files": {},
                "distribution_review_required": True,
            }
            self.runtime_inputs[platform] = runtime
            self.archives[platform] = {}
            images = {}
            for component in ("backend", "web"):
                archive = self.root / (component + "-" + arch + ".tar")
                archive.write_bytes((component + arch).encode())
                self.archives[platform][component] = archive
                images[component] = {
                    "archive_digest": sha256(archive.read_bytes()),
                    "platform": platform,
                }
            smoke = {"platform": platform, "execution": "native"}
            native_path = self.root / ("native-" + arch + ".json")
            native_path.write_bytes(json_bytes({"smoke": smoke, "images": images}))
            self.native_paths[platform] = native_path
            self.native[platform] = {
                "measurement_digest": sha256(native_path.read_bytes()),
                "runtime": runtime,
                "images": images,
                "smoke": smoke,
            }
            record = {
                "schema_version": 1,
                "kind": "runtime-source-completeness",
                "verified_at": "2026-10-08T12:00:00Z",
                "repository": "fixture/psst.zip",
                "version": "v0.1.0",
                "revision": "a" * 40,
                "platform": platform,
                "runtime_source_asset_sha256": runtime["source_asset"]["digest"],
                "runtime_pack_sha256": runtime["runtime_pack_sha256"],
                "native_smoke_report_sha256": sha256(json_bytes(smoke)),
                "images": images,
                "coverage": {
                    "backend_origins": 1,
                    "web_origins": 1,
                    "retained_package_versions": 2,
                    "go_runtime": {
                        "version": "go1.26.8",
                        "commit": "b" * 40,
                        "archive_sha256": sha256(b"Go original"),
                        "executables": {"backend": "go1.26.8", "web": "go1.26.8"},
                    },
                },
                "caddy_signature_verification": {
                    "legacy_sigstore_signatures_verified": True
                },
                "runtime_source_inputs_verified": True,
                "application_source_verified": False,
                "apk_binary_signatures_verified": False,
                "source_publication_verified": False,
                "distribution_authorized": False,
            }
            path = self.root / ("runtime-" + arch + ".json")
            path.write_bytes(json_bytes(record))
            self.runtime_paths[platform] = path
            collection = self.root / ("dependencies-" + arch)
            collection.mkdir()
            (collection / "dependency-collection.json").write_bytes(
                b"dependency record"
            )
            self.collections[platform] = collection
            capture = self.root / ("browser-" + arch + ".tar")
            capture.write_bytes(b"tiny captured inputs")
            self.captures[platform] = capture
        self.binding = Binding(
            "fixture/psst.zip", "v0.1.0", "a" * 40, tuple(sorted(subjects.items()))
        )
        self.policy = {
            "path": "tools/container-distribution-policy.json",
            "source_commit": self.binding.commit,
            "record_digest": sha256(b"policy"),
            "git_blob": "c" * 40,
        }
        self.application = {
            "asset": {"name": application.name},
            "git_archive_sha256": sha256(b"Git source"),
            "publication_authorized": False,
        }
        self.backend = {
            "binding_digest": self.binding.digest,
            "upstream_inputs": self.upstream,
            "images": {},
        }
        self.browser = {
            "binding_digest": self.binding.digest,
            "upstream_inputs": self.upstream,
            "images": {},
        }
        notice_names = (
            "AGPL-3.0-only.txt",
            "THIRD_PARTY_NOTICES.txt",
            "dependency-inventory.json",
            "release.json",
            "backend/AGPL-3.0-only.txt",
            "runtime/THIRD_PARTY_NOTICES.txt",
            "runtime/SOURCE.txt",
            "runtime/runtime-inventory.json",
        )
        self.files = {
            name: {"sha256": sha256(name.encode()), "size": len(name)}
            for name in notice_names
        }
        for platform in source.PLATFORMS:
            arch = platform.split("/")[1]
            for component, partial in (
                ("backend", self.backend),
                ("web", self.browser),
            ):
                target = component + "-" + arch
                row = {
                    "subject": subjects[target],
                    "image": self.native[platform]["images"][component],
                }
                if component == "backend":
                    row.update(
                        dependency_asset={
                            "name": f"psst.zip-dependency-inputs-v0.1.0-{arch}.tar.gz"
                        },
                        dependency_replay={
                            "collection_sha256": sha256(b"dependency record"),
                            "backend_notice_sources": {
                                "inventory_sha256": self.files[
                                    "dependency-inventory.json"
                                ]["sha256"]
                            },
                        },
                    )
                else:
                    row.update(
                        browser_measurement_sha256=sha256(b"measurement"),
                        browser_inputs_sha256=sha256(b"tiny captured inputs"),
                        preferred_sources={},
                        source_associations={},
                        compiler_sources={},
                        generator_sources={},
                        notice_files=copy.deepcopy(self.files),
                    )
                partial["images"][target] = row
        stack = ExitStack()
        self.addCleanup(stack.close)
        self.calls = {}
        for name, result in (
            (
                "aggregate_native_reports",
                {
                    "final-image-smoke": {
                        "details": {"native_measurements": self.native}
                    }
                },
            ),
            ("committed_policy", ({}, self.policy)),
            ("verify_application_source_archive", self.application),
            ("verify_backend_source_inputs", self.backend),
            ("verify_browser_source_inputs", self.browser),
        ):
            self.calls[name] = stack.enter_context(
                patch.object(source, name, return_value=result)
            )
        stack.enter_context(
            patch.object(
                source,
                "runtime_inputs",
                side_effect=lambda context, pack: (
                    {},
                    self.runtime_inputs[context.platform],
                ),
            )
        )
        self.notices = stack.enter_context(
            patch.object(
                source.final_notices,
                "verify_backend_notices",
                return_value={
                    "files": self.files,
                    "notice_inventory_digest": sha256(json_bytes(self.files)),
                },
            )
        )
        self.trusted = {path.read_bytes() for path in self.runtime_paths.values()}

        class Authenticator:
            def authenticate(inner, content, binding):
                if content not in self.trusted:
                    raise InvalidRelease("unsigned runtime-source record")

        self.auth = Authenticator()

    def produce(self, **overrides):
        return source.corresponding_source_report(
            self.binding,
            **{
                "root": self.root,
                "source_assets": self.assets,
                "native_measurements": self.native_paths,
                "runtime_packs": self.packs,
                "runtime_source_records": self.runtime_paths,
                "dependency_collections": self.collections,
                "source_scans": {},
                "compiler_graphs": {},
                "upstream_collection": self.upstream_collection,
                "browser_measurements": {},
                "captures": self.captures,
                "archives": self.archives,
                "authenticator": self.auth,
                **overrides,
            },
        )

    def test_complete_join_retains_exact_category_evidence_without_publication(self):
        report, evidence = self.produce()
        source_review_details(report["details"], self.binding, distribution=False)
        self.assertTrue(report["passed"])
        self.assertFalse(evidence["publication_authorized"])
        self.assertFalse(evidence["byte_reproduction_verified"])
        retained = {
            sha256(json_bytes(value))
            for value in evidence["coverage_evidence"].values()
        }
        for target, categories in SOURCE_COVERAGE.items():
            self.assertEqual(
                set(report["details"]["coverage"][target]), set(categories)
            )
            for value in report["details"]["coverage"][target].values():
                self.assertIn(value["evidence_digest"], retained)
        self.assertEqual(self.notices.call_count, 2)
        for name in (
            "verify_application_source_archive",
            "verify_backend_source_inputs",
            "verify_browser_source_inputs",
        ):
            self.calls[name].assert_called_once()
        before = report["details"]["images"]["web-arm64"]["notice_inventory_digest"]
        self.browser["images"]["web-arm64"]["notice_files"]["release.json"][
            "sha256"
        ] = sha256(b"other release")
        self.assertNotEqual(
            self.produce()[0]["details"]["images"]["web-arm64"][
                "notice_inventory_digest"
            ],
            before,
        )

    def test_runtime_substitution_missing_coverage_and_late_mutation_fail_closed(self):
        path = self.runtime_paths["linux/arm64"]
        original = path.read_bytes()
        for key, value in (
            ("revision", "f" * 40),
            ("platform", "linux/amd64"),
            ("runtime_source_asset_sha256", sha256(b"wrong original")),
            ("native_smoke_report_sha256", sha256(b"different smoke")),
            ("images", self.native["linux/amd64"]["images"]),
            ("runtime_source_inputs_verified", False),
        ):
            record = source.read_json(original)
            record[key] = value
            path.write_bytes(json_bytes(record))
            self.trusted.add(path.read_bytes())
            with self.subTest(changed=key), self.assertRaises(InvalidRelease):
                self.produce()
        path.write_bytes(original + b" ")
        with self.assertRaisesRegex(InvalidRelease, "unsigned"):
            self.produce()
        path.write_bytes(original)
        with self.assertRaisesRegex(
            InvalidRelease, "both authenticated runtime-source"
        ):
            self.produce(
                runtime_source_records={
                    "linux/amd64": self.runtime_paths["linux/amd64"]
                }
            )
        row = self.backend["images"].pop("backend-arm64")
        with self.assertRaisesRegex(InvalidRelease, "both backend source replays"):
            self.produce()
        self.backend["images"]["backend-arm64"] = row
        file = self.browser["images"]["web-arm64"]["notice_files"].pop("release.json")
        with self.assertRaisesRegex(InvalidRelease, "notice inventory is incomplete"):
            self.produce()
        self.browser["images"]["web-arm64"]["notice_files"]["release.json"] = file
        original_subjects = self.binding.subjects
        extra = self.root / "unused-source.tar.gz"
        extra.write_bytes(b"source with no checked category")
        self.assets[extra.name] = extra
        self.binding = replace(
            self.binding,
            subjects=tuple(
                sorted(
                    (
                        *original_subjects,
                        (
                            "source:" + extra.name,
                            "file:" + extra.name + "@" + sha256(extra.read_bytes()),
                        ),
                    )
                )
            ),
        )
        for partial in (self.backend, self.browser):
            partial["binding_digest"] = self.binding.digest
        with self.assertRaisesRegex(
            InvalidRelease, "lacks substantive source coverage"
        ):
            self.produce()
        del self.assets[extra.name]
        self.binding = replace(self.binding, subjects=original_subjects)
        for partial in (self.backend, self.browser):
            partial["binding_digest"] = self.binding.digest

        def mutate(*args, **kwargs):
            (self.upstream_collection / source.upstream_inputs.RECORD).write_bytes(
                b"late substitution"
            )
            return self.notices.return_value

        self.notices.side_effect = mutate
        with self.assertRaisesRegex(InvalidRelease, "record changed"):
            self.produce()

    def test_distinct_go_runtime_sources_need_exact_coverage_for_each_executable(self):
        for path in self.runtime_paths.values():
            record = source.read_json(path.read_bytes())
            record["coverage"]["go_runtime"] = {
                "sources": {
                    "backend": {
                        "version": "go1.27.1",
                        "commit": "c" * 40,
                        "archive_sha256": sha256(b"Backend Go original"),
                    },
                    "web": {
                        "version": "go1.26.8",
                        "commit": "b" * 40,
                        "archive_sha256": sha256(b"Caddy Go original"),
                    },
                },
                "executables": {"backend": "go1.27.1", "web": "go1.26.8"},
            }
            path.write_bytes(json_bytes(record))
            self.trusted.add(path.read_bytes())
        self.produce()
        path = self.runtime_paths["linux/arm64"]
        original = path.read_bytes()
        for alteration in ("missing", "swapped"):
            record = source.read_json(original)
            sources = record["coverage"]["go_runtime"]["sources"]
            if alteration == "missing":
                del sources["backend"]
            else:
                sources["backend"], sources["web"] = sources["web"], sources["backend"]
            path.write_bytes(json_bytes(record))
            self.trusted.add(path.read_bytes())
            with self.subTest(alteration=alteration), self.assertRaises(InvalidRelease):
                self.produce()


if __name__ == "__main__":
    unittest.main()
