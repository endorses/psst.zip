"""Completed-check report boundaries; fixture authentication is not approval."""

from __future__ import annotations

import copy
from dataclasses import replace
import gzip
import io
import json
import unittest
from pathlib import Path
from unittest.mock import Mock, patch

import generate_release_gate_reports as producer
import generate_corresponding_source_review as corresponding
import generate_distribution_review as distribution
import github_release_transport as transport
import publish_container_release as publication
import test_release_transport as transport_fixtures
import test_native_browser_inputs as browser_fixtures
from test_release_publication import source_review_fixture
from release_artifacts import InvalidRelease, json_bytes


class JobAPI:
    def __init__(self, binding):
        self.binding = binding
        self.run = {
            "id": 77,
            "run_attempt": 2,
            "head_sha": binding.commit,
            "head_branch": binding.version,
            "event": "push",
            "path": publication.WORKFLOW,
            "status": "in_progress",
            "repository": {"full_name": binding.repository},
            "head_repository": {"full_name": binding.repository},
        }
        self.jobs = [
            {
                "id": number,
                "run_id": 77,
                "head_sha": binding.commit,
                "name": producer.CI_PREFIX + name,
                "status": "completed",
                "conclusion": "success",
                "completed_at": "2026-10-07T16:00:00Z",
            }
            for number, name in enumerate(producer.CI_NAMES.values(), 1)
        ]
        self.calls = []
        self.status = 200
        self.truncate = False

    def request(self, method, url, *, headers):
        self.calls.append(url)
        assert method == "GET" and headers["Authorization"] == "Bearer fixture-token"
        if "/jobs?" in url:
            page = int(url.split("page=")[-1])
            jobs = self.jobs[(page - 1) * 100 : page * 100]
            value = {"total_count": len(self.jobs) + self.truncate, "jobs": jobs}
        else:
            value = self.run
        return transport.Response(self.status, {}, json_bytes(value))


class GateReports(unittest.TestCase):
    def setUp(self):
        self.fixture = transport_fixtures.TransportChecks()
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.binding = self.fixture.plan.binding
        self.root = self.fixture.fixture.folder
        self.jobs = JobAPI(self.binding)
        self.packs = {}
        self.measurements = {}
        self.smokes = {}
        self.archives = {}
        self.configs = {}
        self.smoke_temp_roots = []
        for platform in producer.PLATFORMS:
            arch = platform.split("/")[1]
            pack = self.root / ("runtime-" + arch)
            pack.mkdir()
            source = self.fixture.fixture.source
            (pack / source.name).write_bytes(source.read_bytes())
            manifest = {
                "schema_version": 1,
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
            (pack / "runtime-pack.json").write_bytes(json_bytes(manifest))
            configs = {
                component: self.fixture.tested_configs[component + "-" + arch]
                for component in ("backend", "web")
            }
            self.packs[platform] = pack
            self.configs[platform] = configs
            self.archives[platform] = {
                component: self.fixture.archives[component + "-" + arch]
                for component in ("backend", "web")
            }
            self.smokes[platform] = {
                "schema_version": 1,
                "kind": "release-image-smoke",
                "version": self.binding.version,
                "revision": self.binding.commit,
                "platform": platform,
                "execution": "native",
                "tested_configs": configs,
                "runtime_pack_sha256": publication.sha256(
                    (pack / "runtime-pack.json").read_bytes()
                ),
                "checks": sorted(producer.CHECKS),
                "completed_at": "2026-10-07T16:00:00+00:00",
                "publication_authorized": False,
            }

    def ci(self):
        return producer.source_ci_report(
            self.binding, run_id=77, attempt=2, token="fixture-token", http=self.jobs
        )

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

    def collect_both(self):
        result = {}
        for platform in producer.PLATFORMS:
            path = self.root / (platform.split("/")[1] + "-measurement.json")
            path.write_bytes(json_bytes(self.native(platform)))
            result[platform] = path
        return result

    def test_shared_input_builder_matches_plan_without_approving_gates(self):
        fixture = self.fixture.fixture
        value = publication.prepare_inputs(
            root=fixture.root,
            repository=self.binding.repository,
            ref="refs/tags/" + self.binding.version,
            event_sha=self.binding.commit,
            reviewed_commit=self.binding.commit,
            manifest_path=fixture.manifest_path,
            bundle=fixture.bundle,
            indexes=fixture.indexes,
            source_assets={fixture.source.name: fixture.source},
        )
        self.assertEqual(value.binding, self.binding)
        self.assertEqual(value.assets, self.fixture.plan.assets)
        self.assertFalse(hasattr(value, "evidence"))
        fixture.indexes["web"].write_bytes(b"substituted index")
        with self.assertRaises((InvalidRelease, ValueError)):
            publication.prepare_inputs(
                root=fixture.root,
                repository=self.binding.repository,
                ref="refs/tags/" + self.binding.version,
                event_sha=self.binding.commit,
                reviewed_commit=self.binding.commit,
                manifest_path=fixture.manifest_path,
                bundle=fixture.bundle,
                indexes=fixture.indexes,
                source_assets={fixture.source.name: fixture.source},
            )

    def test_source_ci_uses_exact_attempt_and_server_outcomes_without_mobile(self):
        for number, name in enumerate(sorted(producer.MOBILE_CI_NAMES), 201):
            self.jobs.jobs.append(
                {
                    "id": number,
                    "name": producer.CI_PREFIX + name,
                    "status": "completed",
                    "conclusion": "skipped",
                }
            )
        self.jobs.jobs.extend(
            {"id": number, "name": f"Other release job {number}"}
            for number in range(6, 107)
        )
        result = self.ci()
        self.assertEqual(
            result["details"]["jobs"], {key: "success" for key in producer.CI_NAMES}
        )
        self.assertEqual(set(result["details"]["job_evidence"]), set(producer.CI_NAMES))
        self.assertTrue(any("page=2" in url for url in self.jobs.calls))
        self.assertNotIn("fixture-token", json.dumps(result))

    def test_main_run_wrong_source_failed_skipped_pending_and_missing_ci_fail(self):
        original_run, original_jobs = (
            copy.deepcopy(self.jobs.run),
            copy.deepcopy(self.jobs.jobs),
        )
        for field, value in (
            ("head_branch", "main"),
            ("head_sha", "b" * 40),
            ("run_attempt", 1),
            ("path", ".github/workflows/ci.yml"),
            ("event", "pull_request"),
        ):
            self.jobs.run = {**original_run, field: value}
            with self.subTest(field=field), self.assertRaises(InvalidRelease):
                self.ci()
        self.jobs.run = original_run
        for index, required_job in enumerate(original_jobs):
            for field, value in (
                ("status", "in_progress"),
                ("conclusion", "failure"),
                ("conclusion", "skipped"),
                ("head_sha", "b" * 40),
            ):
                self.jobs.jobs = copy.deepcopy(original_jobs)
                self.jobs.jobs[index][field] = value
                with (
                    self.subTest(job=required_job["name"], field=field, value=value),
                    self.assertRaises(InvalidRelease),
                ):
                    self.ci()
            self.jobs.jobs = original_jobs[:index] + original_jobs[index + 1 :]
            with self.subTest(missing=required_job["name"]), self.assertRaisesRegex(
                InvalidRelease, "security/backend/web"
            ):
                self.ci()

    def test_api_failure_partial_pagination_and_duplicate_job_are_not_success(self):
        for status in (401, 403, 404, 429, 500):
            self.jobs.status = status
            with (
                self.subTest(status=status),
                self.assertRaisesRegex(InvalidRelease, "API failed"),
            ):
                self.ci()
        self.jobs.status = 200
        self.jobs.truncate = True
        with self.assertRaisesRegex(InvalidRelease, "Incomplete"):
            self.ci()
        self.jobs.truncate = False
        self.jobs.jobs.append(self.jobs.jobs[0])
        with self.assertRaisesRegex(InvalidRelease, "repeated"):
            self.ci()

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

    def test_unexpected_ci_prefix_job_and_changed_run_on_recheck_fail(self):
        self.jobs.jobs.append(
            {"id": 1000, "name": producer.CI_PREFIX + "Unreviewed job"}
        )
        with self.assertRaisesRegex(InvalidRelease, "Unexpected reusable"):
            self.ci()
        self.jobs.jobs.pop()
        original = self.jobs.request
        run_reads = 0

        def changed_run(method, url, **kwargs):
            nonlocal run_reads
            if "/jobs?" not in url:
                run_reads += 1
                if run_reads == 2:
                    self.jobs.run["run_attempt"] = 3
            return original(method, url, **kwargs)

        with (
            patch.object(self.jobs, "request", side_effect=changed_run),
            self.assertRaisesRegex(InvalidRelease, "selected version-tag"),
        ):
            self.ci()

    def test_changed_pagination_count_is_not_complete_job_coverage(self):
        self.jobs.jobs.extend(
            {"id": number, "name": f"Other release job {number}"}
            for number in range(6, 107)
        )
        original = self.jobs.request

        def changed_page(method, url, **kwargs):
            response = original(method, url, **kwargs)
            if url.endswith("page=2"):
                value = json.loads(response.body)
                value["total_count"] += 1
                return transport.Response(200, {}, json_bytes(value))
            return response

        with (
            patch.object(self.jobs, "request", side_effect=changed_page),
            self.assertRaisesRegex(InvalidRelease, "pagination changed"),
        ):
            self.ci()

    def test_runtime_pack_manifest_substitution_during_smoke_fails(self):
        def substitute(args, **kwargs):
            self.execute_smoke(args, **kwargs)
            path = self.packs["linux/amd64"] / "runtime-pack.json"
            path.write_bytes(path.read_bytes() + b"\n")
            return b"fixture harness succeeded"

        with self.assertRaisesRegex(InvalidRelease, "Runtime inputs changed"):
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
        source = self.packs["linux/amd64"] / self.fixture.fixture.source.name
        source.write_bytes(b"substituted corresponding sources")
        with self.assertRaisesRegex(InvalidRelease, "source bytes"):
            self.native()
        source.write_bytes(self.fixture.fixture.source.read_bytes())
        self.configs["linux/amd64"]["backend"] = "sha256:" + "c" * 64
        with self.assertRaisesRegex(InvalidRelease, "configuration"):
            self.native()

    def test_both_authenticated_measurements_required_before_gate_reports(self):
        paths = self.collect_both()
        trusted = {path.read_bytes() for path in paths.values()}

        class Authenticator:
            def authenticate(inner, content, binding):
                self.assertEqual(binding, self.binding)
                if content not in trusted:
                    raise InvalidRelease("unsigned native measurement")

        reports = producer.aggregate_native_reports(
            self.binding, paths, Authenticator()
        )
        self.assertEqual(set(reports), {"final-image-smoke", "runtime-notices"})
        self.assertEqual(
            reports["final-image-smoke"]["details"]["execution"],
            {platform: "native" for platform in producer.PLATFORMS},
        )
        self.assertEqual(
            set(reports["final-image-smoke"]["details"]["tested_configs"]),
            set(self.fixture.tested_configs),
        )
        self.assertTrue(
            reports["runtime-notices"]["details"]["distribution_review_required"]
        )
        reports["source-ci"] = self.ci()
        gate_paths = {}
        for gate, value in reports.items():
            gate_paths[gate] = self.root / (gate + ".json")
            gate_paths[gate].write_bytes(json_bytes(value))

        class VerifiedFixture:
            def verify(inner, gate, path, binding):
                raw = path.read_bytes()
                record = json.loads(raw)
                return publication.VerifiedEvidence(
                    gate,
                    binding.digest,
                    publication.sha256(raw),
                    record["passed"],
                    record["details"],
                )

        self.assertEqual(
            set(
                publication.verify_gates(
                    gate_paths, frozenset(gate_paths), self.binding, VerifiedFixture()
                )
            ),
            set(reports),
        )
        paths["linux/arm64"].write_bytes(b"unsigned substitution")
        with self.assertRaisesRegex(InvalidRelease, "unsigned"):
            producer.aggregate_native_reports(self.binding, paths, Authenticator())
        with self.assertRaises(InvalidRelease):
            producer.aggregate_native_reports(
                self.binding, {"linux/amd64": paths["linux/amd64"]}, Authenticator()
            )

    def test_wrong_release_or_image_mapping_even_signed_is_rejected(self):
        paths = self.collect_both()
        record = json.loads(paths["linux/arm64"].read_bytes())
        record["images"]["backend"]["manifest_digest"] = "sha256:" + "f" * 64
        paths["linux/arm64"].write_bytes(json_bytes(record))
        authenticator = type(
            "AuthenticatedFixture", (), {"authenticate": lambda *args: None}
        )()
        with self.assertRaisesRegex(InvalidRelease, "correspondence"):
            producer.aggregate_native_reports(self.binding, paths, authenticator)

    def test_matrix_measurement_precedes_other_arch_bundle_and_full_binding(self):
        # The matrix producer has only its own checked source context and files.
        # A final Binding is neither constructed nor inspected during collection.
        with patch.object(
            producer,
            "checked_binding",
            side_effect=AssertionError("full binding requested in native job"),
        ):
            native = self.native()
        self.assertEqual(
            set(native["source"]), {"repository", "version", "commit", "platform"}
        )
        self.assertNotIn("binding_digest", native)
        self.assertNotIn("bundle", json.dumps(native))
        paths = self.collect_both()
        authenticator = type(
            "AuthenticatedFixture", (), {"authenticate": lambda *args: None}
        )()
        incomplete = publication.Binding(
            self.binding.repository,
            self.binding.version,
            self.binding.commit,
            tuple(
                (key, value) for key, value in self.binding.subjects if key != "bundle"
            ),
        )
        with self.assertRaisesRegex(InvalidRelease, "complete"):
            producer.aggregate_native_reports(incomplete, paths, authenticator)
        for field, wrong in (
            ("repository", "wrong/repository"),
            ("commit", "b" * 40),
            ("platform", "linux/amd64"),
        ):
            value = json.loads(paths["linux/arm64"].read_bytes())
            original = copy.deepcopy(value)
            value["source"][field] = wrong
            paths["linux/arm64"].write_bytes(json_bytes(value))
            with (
                self.subTest(field=field),
                self.assertRaisesRegex(InvalidRelease, "Stale"),
            ):
                producer.aggregate_native_reports(self.binding, paths, authenticator)
            paths["linux/arm64"].write_bytes(json_bytes(original))
        changed_sources = publication.Binding(
            self.binding.repository,
            self.binding.version,
            self.binding.commit,
            tuple(
                (key, value + "0" if key.startswith("source:") else value)
                for key, value in self.binding.subjects
            ),
        )
        with self.assertRaisesRegex(InvalidRelease, "source asset differs"):
            producer.aggregate_native_reports(changed_sources, paths, authenticator)

    def test_source_hash_measurements_do_not_invent_source_completeness_or_approval(
        self,
    ):
        source = self.fixture.fixture.source
        result = producer.source_asset_measurements(self.binding, {source.name: source})
        self.assertFalse(result["corresponding_source_completeness_verified"])
        self.assertFalse(result["publication_authorized"])
        self.assertNotIn("passed", result)
        self.assertNotIn("gate", result)
        source.write_bytes(b"changed sources")
        with self.assertRaisesRegex(InvalidRelease, "differs"):
            producer.source_asset_measurements(self.binding, {source.name: source})

    def test_application_source_replay_binds_git_commit_and_retained_archive(self):
        fixture = self.fixture.fixture
        name = f"psst.zip-source-{self.binding.version}.tar.gz"
        source = self.root / name
        tar = fixture.git(
            "archive",
            "--format=tar",
            f"--prefix=psst.zip-{self.binding.version}/",
            self.binding.commit,
        )
        buffer = io.BytesIO()
        timestamp = int(fixture.git("show", "-s", "--format=%ct", self.binding.commit))
        with gzip.GzipFile(
            fileobj=buffer, mode="wb", filename="", mtime=timestamp
        ) as stream:
            stream.write(tar)
        raw = buffer.getvalue()
        source.write_bytes(raw)
        subjects = {
            **dict(self.binding.subjects),
            "source:" + name: "file:" + name + "@" + publication.sha256(raw),
        }
        binding = replace(self.binding, subjects=tuple(sorted(subjects.items())))
        # Working files and untracked secrets cannot influence the selected tree.
        (fixture.root / "LICENSE").write_bytes(b"uncommitted replacement")
        (fixture.root / "operator-secret.env").write_bytes(b"untracked operator data")
        result = corresponding.verify_application_source_archive(
            binding, root=fixture.root, source=source
        )
        self.assertEqual(result["git_archive_sha256"], publication.sha256(tar))
        self.assertTrue(result["source_commit_archive_verified"])
        self.assertFalse(result["corresponding_source_completeness_verified"])
        self.assertFalse(result["publication_authorized"])
        self.assertNotIn("passed", result)
        self.assertNotIn("gate", result)
        source.write_bytes(raw + b"substitution")
        with self.assertRaisesRegex(InvalidRelease, "publication binding"):
            corresponding.verify_application_source_archive(
                binding, root=fixture.root, source=source
            )
        # Updating only the caller's digest does not legitimize another archive.
        changed = replace(
            binding,
            subjects=tuple(
                sorted(
                    {
                        **subjects,
                        "source:"
                        + name: "file:"
                        + name
                        + "@"
                        + publication.sha256(source.read_bytes()),
                    }.items()
                )
            ),
        )
        with self.assertRaisesRegex(InvalidRelease, "selected committed Git"):
            corresponding.verify_application_source_archive(
                changed, root=fixture.root, source=source
            )
        source.write_bytes(raw)
        fixture.git("add", "LICENSE")
        fixture.git("commit", "-qm", "Another source tree")
        other_commit = fixture.git("rev-parse", "HEAD").decode().strip()
        with self.assertRaisesRegex(InvalidRelease, "selected committed Git"):
            corresponding.verify_application_source_archive(
                replace(binding, commit=other_commit), root=fixture.root, source=source
            )

    def test_browser_replay_requires_both_authenticated_images_and_bound_source_bytes(
        self,
    ):
        # Lower-level Git/npm/OCI and preferred mappings have separate byte tests.
        # Exercise the new join boundary without rebuilding or duplicating them.
        native = self.collect_both()
        capture = browser_fixtures.NativeBrowser()
        capture.setUp()
        self.addCleanup(capture.doCleanups)
        measurements, observations = {}, {}
        trusted = {path.read_bytes() for path in native.values()}
        for platform, path in native.items():
            image = json.loads(path.read_bytes())["images"]["web"]
            observations[platform] = {
                "kind": "native-browser-input-measurement",
                "source": producer.NativeSourceContext(
                    self.binding.repository,
                    self.binding.version,
                    self.binding.commit,
                    platform,
                ).checked(),
                "image": image,
                "tested_web_config": self.configs[platform]["web"],
                "browser_inputs_sha256": publication.source_digest(capture.pack),
                "npm_archives": {},
                "source_associations": {"unresolved_javascript": []},
                "git_inputs": {},
                "final_static_files": {},
            }
            measurement = self.root / ("browser-" + platform.split("/")[1] + ".json")
            measurement.write_bytes(
                json_bytes(
                    {**observations[platform], "builder_config": "sha256:" + "c" * 64}
                )
            )
            measurements[platform] = measurement
            trusted.add(measurement.read_bytes())
        asset_name = "psst.zip-upstream-inputs-" + self.binding.version + ".tar.gz"
        collection = self.root / "upstream"
        collection.mkdir()
        (collection / asset_name).write_bytes(
            b"independently replayed original fixture"
        )
        (collection / corresponding.upstream_inputs.RECORD).write_bytes(
            b"collection fixture"
        )
        upstream = {
            "asset": {
                "name": asset_name,
                "digest": publication.source_digest(collection / asset_name),
            },
            "collection_sha256": publication.source_digest(
                collection / corresponding.upstream_inputs.RECORD
            ),
        }
        subjects = dict(self.binding.subjects)
        subjects["source:" + asset_name] = (
            "file:" + asset_name + "@" + upstream["asset"]["digest"]
        )
        binding = replace(self.binding, subjects=tuple(sorted(subjects.items())))

        class Authenticator:
            def authenticate(inner, content, supplied):
                self.assertEqual(supplied, binding)
                if content not in trusted:
                    raise InvalidRelease("unsigned browser/native measurement")

        def replay(context, pack, archive, config, runtime, *, source_root):
            self.assertEqual(pack, capture.pack)
            self.assertEqual(archive, self.archives[context.platform]["web"])
            self.assertEqual(config, self.configs[context.platform]["web"])
            self.assertEqual(source_root, self.root)
            return observations[context.platform]

        def verify(**overrides):
            return corresponding.verify_browser_source_inputs(
                binding,
                **{
                    "root": self.root,
                    "native_measurements": native,
                    "runtime_packs": self.packs,
                    "browser_measurements": measurements,
                    "captures": {p: capture.pack for p in producer.PLATFORMS},
                    "web_archives": {
                        p: self.archives[p]["web"] for p in producer.PLATFORMS
                    },
                    "upstream_collection": collection,
                    "authenticator": Authenticator(),
                    **overrides,
                },
            )

        with patch.object(
            corresponding.browser_inputs, "replay", side_effect=replay
        ), patch.object(
            corresponding.upstream_inputs,
            "verify_source_files",
            return_value=(upstream, {}),
        ) as offering, patch.object(
            corresponding.preferred,
            "verify_preferred_relationships",
            return_value={"fixture": "mapped modules"},
        ), patch.object(
            corresponding.preferred,
            "verify_captured_compiler_inputs",
            return_value={"fixture": "mapped compiler"},
        ) as compiler, patch.object(
            corresponding.preferred,
            "verify_generator_relationships",
            return_value={"fixture": "mapped generators"},
        ):
            result = verify()
            offering.assert_called_once_with(
                self.root,
                binding.repository,
                binding.version,
                binding.commit,
                collection,
            )
            self.assertEqual(set(result["images"]), {"web-amd64", "web-arm64"})
            self.assertFalse(result["corresponding_source_completeness_verified"])
            self.assertFalse(result["publication_authorized"])
            self.assertNotIn("gate", result)
            with self.assertRaises(InvalidRelease):
                verify(
                    browser_measurements={"linux/amd64": measurements["linux/amd64"]}
                )
            arm = measurements["linux/arm64"]
            original = arm.read_bytes()
            arm.write_bytes(b"unsigned substitution")
            with self.assertRaisesRegex(InvalidRelease, "unsigned"):
                verify()
            arm.write_bytes(original)
            changed = {
                **json.loads(original),
                "tested_web_config": "sha256:" + "f" * 64,
            }
            arm.write_bytes(json_bytes(changed))
            trusted.add(arm.read_bytes())
            with self.assertRaisesRegex(InvalidRelease, "authenticated final image"):
                verify()
            arm.write_bytes(original)
            observations["linux/arm64"]["source_associations"] = {"substituted": True}
            with self.assertRaisesRegex(
                InvalidRelease, "independent Git/npm/OCI replay"
            ):
                verify()
            observations["linux/arm64"]["source_associations"] = {
                "unresolved_javascript": [{"file": "unknown-emitted-worker.js"}]
            }
            unresolved = {
                **json.loads(original),
                "source_associations": observations["linux/arm64"][
                    "source_associations"
                ],
            }
            arm.write_bytes(json_bytes(unresolved))
            trusted.add(arm.read_bytes())
            with self.assertRaisesRegex(
                InvalidRelease, "Unattributed final JavaScript"
            ):
                verify()
            arm.write_bytes(original)
            observations["linux/arm64"]["source_associations"] = {
                "unresolved_javascript": []
            }
            upstream["asset"]["digest"] = "sha256:" + "f" * 64
            with self.assertRaisesRegex(InvalidRelease, "publication binding"):
                verify()
            upstream["asset"]["digest"] = publication.source_digest(
                collection / asset_name
            )
            mappings = 0

            def substitute_after_mapping(*args):
                nonlocal mappings
                mappings += 1
                if mappings == 2:
                    capture.pack.write_bytes(
                        capture.pack.read_bytes() + b"changed after replay"
                    )
                return {"fixture": "mapped compiler"}

            compiler.side_effect = substitute_after_mapping
            with self.assertRaisesRegex(InvalidRelease, "changed during replay"):
                verify()

    def distribution_fixture(self):
        fixture = self.fixture.fixture
        path = fixture.root / publication.SOURCE_REVIEW_POLICY
        path.parent.mkdir(exist_ok=True)
        path.write_bytes(
            json_bytes(
                {
                    "schema_version": 1,
                    "kind": "container-distribution-review-policy",
                    "environment": "container-release",
                    "reviewers": ["fixture-owner"],
                }
            )
        )
        fixture.git("add", publication.SOURCE_REVIEW_POLICY)
        fixture.git("commit", "-qm", "Disposable authorization policy")
        binding = replace(
            self.binding, commit=fixture.git("rev-parse", "HEAD").decode().strip()
        )
        policy, fact = distribution.committed_policy(fixture.root, binding)
        source = source_review_fixture(binding)
        source["policy"] = fact
        source_path = self.root / "signed-source-review.json"
        source_path.write_bytes(
            json_bytes({"fixture": "authenticated-complete-source"})
        )
        receipt = publication.VerifiedEvidence(
            "corresponding-source",
            binding.digest,
            publication.sha256(source_path.read_bytes()),
            True,
            source,
        )
        verifier = Mock(verify=Mock(return_value=receipt))
        run = {**self.jobs.run, "head_sha": binding.commit}
        user = {"login": "fixture-owner", "id": 92, "type": "User"}
        environment = {
            "id": 31,
            "name": policy["environment"],
            "protection_rules": [
                {
                    "type": "required_reviewers",
                    "reviewers": [{"type": "User", "reviewer": user}],
                }
            ],
        }
        comment = distribution.approval_comment(binding, receipt.report_digest, 77, 2)
        approval = {
            "state": "approved",
            "comment": comment,
            "user": copy.deepcopy(user),
            "environments": [{"id": 31, "name": policy["environment"]}],
        }
        responses = {
            "attempts/2": run,
            "environments/container-release": environment,
            "approvals": [approval],
        }
        api = Mock()

        def request(method, url, *, headers):
            self.assertEqual(method, "GET")
            self.assertEqual(headers["Authorization"], "Bearer fixture-token")
            value = next(
                value for suffix, value in responses.items() if url.endswith(suffix)
            )
            return transport.Response(200, {}, json_bytes(value))

        api.request.side_effect = request
        args = {
            "root": fixture.root,
            "source_report": source_path,
            "verifier": verifier,
            "run_id": 77,
            "attempt": 2,
            "token": "fixture-token",
            "http": api,
        }
        return binding, args, responses, receipt, path

    def test_distribution_review_uses_committed_policy_and_exact_github_approval(self):
        binding, args, responses, receipt, path = self.distribution_fixture()
        # Uncommitted caller policy cannot grant or revoke release authorization.
        path.write_text('{"reviewers":["caller"]}\n')
        report, retained = distribution.distribution_review_report(binding, **args)
        publication.source_review_details(report["details"], binding, distribution=True)
        self.assertEqual(report["details"]["review"]["reviewer"], "fixture-owner")
        self.assertEqual(
            report["details"]["review"]["record_digest"],
            publication.sha256(json_bytes(retained)),
        )
        self.assertEqual(retained["github_evidence"]["reviews"], responses["approvals"])
        self.assertEqual(retained["source_gate_report_digest"], receipt.report_digest)
        self.assertEqual(args["http"].request.call_count, 6)

    def test_distribution_rejects_wrong_attempt_subjects_reviewer_policy_and_api_failure(
        self,
    ):
        binding, args, responses, receipt, _ = self.distribution_fixture()
        baseline = copy.deepcopy(responses)
        changes = [
            lambda r: r["attempts/2"].update(run_attempt=1),
            lambda r: r["attempts/2"].update(head_sha="0" * 40),
            lambda r: r["attempts/2"].update(event="pull_request"),
            lambda r: r["attempts/2"].update(path=".github/workflows/other.yml"),
            lambda r: r["environments/container-release"].update(protection_rules=[]),
            lambda r: r["environments/container-release"]["protection_rules"][0][
                "reviewers"
            ][0]["reviewer"].update(login="other"),
            lambda r: r["approvals"][0].update(state="rejected"),
            lambda r: r["approvals"][0].update(comment="earlier approval"),
            lambda r: r["approvals"][0]["user"].update(id=93),
            lambda r: r["approvals"][0]["environments"][0].update(id=32),
            lambda r: r["approvals"].append(copy.deepcopy(r["approvals"][0])),
        ]
        for change in changes:
            responses.clear()
            responses.update(copy.deepcopy(baseline))
            change(responses)
            with self.subTest(change=changes.index(change)), self.assertRaises(
                InvalidRelease
            ):
                distribution.distribution_review_report(binding, **args)
        responses.clear()
        responses.update(copy.deepcopy(baseline))
        args["verifier"].verify.return_value = replace(
            receipt, report_digest="sha256:" + "0" * 64
        )
        with self.assertRaises(InvalidRelease):
            distribution.distribution_review_report(binding, **args)
        args["verifier"].verify.return_value = receipt
        args["http"].request.side_effect = lambda *a, **k: transport.Response(
            403, {}, b"{}"
        )
        with self.assertRaises(InvalidRelease):
            distribution.distribution_review_report(binding, **args)


if __name__ == "__main__":
    unittest.main()
