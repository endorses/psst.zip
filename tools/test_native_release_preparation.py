"""Native artifact contracts with real tar/OCI bytes and disposable adapters.

Source/signature and application execution below are fixtures, not hosted release
approval. The actual source/smoke/OCI helpers have their separate live evidence.
"""

from __future__ import annotations

import copy
import hashlib
import gzip
import os
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import generate_release_gate_reports as reports
import prepare_native_release as native
from prepare_release_candidate import BASES
from release_artifacts import InvalidRelease, json_bytes, read_json
from test_release_oci import fixture as oci_fixture


def write_tar(path, files):
    with tarfile.open(path, "w:") as saved:
        for name, body in files.items():
            entry = tarfile.TarInfo(name)
            entry.size = len(body)
            saved.addfile(entry, io.BytesIO(body))


def docker_save(path, configs):
    files = {}
    manifest = []
    for component, raw in configs.items():
        name = hashlib.sha256(raw).hexdigest() + ".json"
        files[name] = raw
        manifest.append(
            {"Config": name, "RepoTags": [component + ":fixture"], "Layers": []}
        )
    files["manifest.json"] = json_bytes(manifest)
    write_tar(path, files)


class FixtureOperations(native.Operations):
    def __init__(self, test):
        self.test = test
        self.events = []
        self.fail = None
        self.cleaned = None
        self.changed_measurement = None
        self.changed_replay = None

    def event(self, name):
        self.events.append(name)
        if self.fail == name:
            raise InvalidRelease("fixture stage failed: " + name)

    def image(self, config):
        self.event("inspect")
        return self.test.images[config]

    def preflight(self, context):
        self.event("preflight")

    def collect_apk(self, image, helper, output):
        self.event("collect")
        output.mkdir()
        self.test.assertIn(image, self.test.originals.values())
        self.test.assertEqual(helper, self.test.helper)

    def collect_caddy(self, image, base, output):
        self.event("caddy")
        output.mkdir()
        self.test.assertEqual(image, self.test.originals["web"])
        self.test.assertEqual(base, self.test.candidate["base_images"]["caddy"])

    def package(self, collections, cosign, context, output):
        self.event("signed-package")
        self.test.assertEqual(cosign, self.test.cosign)
        output.mkdir()
        name = "psst.zip-v1.2.3-runtime-sources-amd64.tar.gz"
        source = b"unsigned runtime source fixture; never published"
        (output / name).write_bytes(source)
        record = {
            "schema_version": 1,
            "version": context.version,
            "revision": context.commit,
            "architecture": "amd64",
            "source_asset": {
                "file": name,
                "sha256": hashlib.sha256(source).hexdigest(),
                "size": len(source),
                "url": f"https://github.com/{context.repository}/releases/download/{context.version}/{name}",
            },
            "bindings": {
                component: {"original_image_id": config}
                for component, config in self.test.originals.items()
            },
            "overlays": {
                component: {
                    key: "1" * 64
                    for key in [
                        "THIRD_PARTY_NOTICES.txt",
                        "runtime-inventory.json",
                        "SOURCE.txt",
                    ]
                }
                for component in native.COMPONENTS
            },
        }
        (output / "runtime-pack.json").write_bytes(json_bytes(record))
        return record

    def overlay(self, image, alias, output_alias, folder, platform, iidfile):
        self.event("overlay")
        component = folder.name
        self.test.assertEqual(image, self.test.originals[component])
        self.test.assertEqual(platform, self.test.context.platform)
        config = self.test.configs[component]
        iidfile.write_text(config)
        return config

    def overlays(self, pack, configs):
        self.event("overlay-check")
        return {"fixture_verified_configs": configs}

    def save(self, aliases, output):
        self.event("save")
        docker_save(output, self.test.final_raw)

    def export(self, context, archive, alias, output, component, tested_config):
        self.event("export")
        self.test.assertEqual(tested_config, self.test.configs[component])
        write_tar(output, self.test.oci[component])

    def measure(self, context, pack, archives, configs):
        self.event("native-smoke")

        def execute(args, *, environment, timeout):
            smoke = {
                "schema_version": 1,
                "kind": "release-image-smoke",
                "version": context.version,
                "revision": context.commit,
                "platform": context.platform,
                "execution": "native",
                "tested_configs": configs,
                "runtime_pack_sha256": reports.runtime_inputs(context, pack)[1][
                    "runtime_pack_sha256"
                ],
                "checks": sorted(reports.CHECKS),
                "completed_at": "2026-10-07T16:00:00+00:00",
                "publication_authorized": False,
            }
            Path(args[args.index("--report") + 1]).write_bytes(json_bytes(smoke))
            return b""

        value = reports.collect_native_measurement(
            context,
            pack=pack,
            archives=archives,
            tested_configs=configs,
            execute=execute,
        )
        if self.changed_measurement:
            self.changed_measurement(value)
        self.measurement = value
        return value

    def replay(self, context, pack, archives, smoke, cosign, asset_digest):
        self.event("source-replay")
        self.test.assertEqual(read_json(smoke.read_bytes()), self.measurement["smoke"])
        value = {
            "runtime_source_inputs_verified": True,
            "distribution_authorized": False,
            "runtime_pack_sha256": reports.runtime_inputs(context, pack)[1][
                "runtime_pack_sha256"
            ],
            "images": copy.deepcopy(self.measurement["images"]),
        }
        if self.changed_replay:
            self.changed_replay(value, archives)
        return value

    def cleanup(self, aliases):
        self.cleaned = aliases
        self.events.append("cleanup")


class NativePreparation(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(
            prefix="psst-native-preparation-test-"
        )
        self.addCleanup(self.temporary.cleanup)
        self.folder = Path(self.temporary.name)
        self.context = reports.NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", "a" * 40, "linux/amd64"
        )
        self.helper = "sha256:" + "7" * 64
        self.images = {}
        self.oci, self.configs, self.final_raw, self.originals, original_raw = (
            {},
            {},
            {},
            {},
            {},
        )
        for component in native.COMPONENTS:
            files, config = oci_fixture(component)
            raw = files["blobs/sha256/" + config[7:]]
            self.oci[component], self.configs[component], self.final_raw[component] = (
                files,
                config,
                raw,
            )
            value = read_json(raw)
            # Original configs differ from final configs but carry identical
            # source identity. Tests use real bytes/config hashes for both.
            original = json_bytes(
                {**value, "history": [{"created_by": "fixture-original"}]}
            )
            original_raw[component] = original
            original_config = "sha256:" + hashlib.sha256(original).hexdigest()
            self.originals[component] = original_config
            for identity, parsed in [
                (config, value),
                (original_config, read_json(original)),
            ]:
                self.images[identity] = {
                    "Id": identity,
                    "Os": "linux",
                    "Architecture": "amd64",
                    "Config": parsed["config"],
                }
        self.images[self.helper] = {
            "Id": self.helper,
            "Os": "linux",
            "Architecture": "amd64",
            "Config": {"User": "65532:65532"},
        }
        self.archive = self.folder / "actual-original-pair.tar"
        docker_save(self.archive, original_raw)
        self.candidate = {
            "schema_version": 1,
            "kind": "release-candidate",
            "candidate_only": True,
            "version": self.context.version,
            "source_commit": self.context.commit,
            "platforms": native.PLATFORMS,
            "base_images": {
                key: value.rsplit(":", 1)[0] + "@sha256:" + "1" * 64
                for key, value in BASES.items()
            },
            "base_platform_digests": {
                key: {
                    "linux/amd64": "sha256:" + "2" * 64,
                    "linux/arm64": "sha256:" + "3" * 64,
                }
                for key in BASES
            },
        }
        self.record = {
            **self.candidate,
            "checked_platform": self.context.platform,
            "native_execution": True,
            "toolchain_output": {
                "go": "go version go1.26.8 linux/amd64",
                "node": "v22.22.0",
                "docker": "29.8.2",
                "compose": "5.6.0",
                "buildx": "v0.37.1",
                "builder": "actual fixture output",
            },
            "build_metadata": {
                component: {
                    "containerimage.config.digest": config,
                    "containerimage.digest": "sha256:" + "5" * 64,
                }
                for component, config in self.originals.items()
            },
            "image_archive": {
                "name": self.archive.name,
                "sha256": native.file_record(self.archive)["sha256"][7:],
                "size": self.archive.stat().st_size,
            },
            "limitations": ["Fixture, not actual build approval."],
        }
        self.record_path = self.folder / "build.json"
        self.write_record()
        self.cosign = self.folder / "cosign"
        self.cosign.write_bytes(b"pinned executable fixture")
        self.ops = FixtureOperations(self)
        self.output = self.folder / "prepared"

    def write_record(self):
        self.record_path.write_bytes(json_bytes(self.record))

    def prepare(self, **overrides):
        return native.prepare(
            **(
                {
                    "context": self.context,
                    "build_record": self.record_path,
                    "original_archive": self.archive,
                    "helper_config": self.helper,
                    "cosign": self.cosign,
                    "output": self.output,
                    "operations": self.ops,
                }
                | overrides
            )
        )

    def test_private_pair_layout_binds_original_and_final_real_archive_bytes(self):
        result = self.prepare()
        self.assertEqual(
            result, read_json((self.output / "native-artifacts.json").read_bytes())
        )
        self.assertFalse(result["publication_authorized"])
        self.assertTrue(result["measurement_authentication_required"])
        self.assertEqual(result["original_tested_configs"], self.originals)
        self.assertEqual(result["tested_configs"], self.configs)
        self.assertEqual(
            (self.output / "build-record.json").read_bytes(),
            self.record_path.read_bytes(),
        )
        original = result["artifacts"]["original_archive"]
        self.assertEqual(original["file"], "original/" + self.archive.name)
        self.assertEqual(original["sha256"][7:], self.record["image_archive"]["sha256"])
        for value in result["artifacts"].values():
            self.assertEqual(
                value, native.file_record(self.output / value["file"], self.output)
            )
        self.assertLess(
            self.ops.events.index("signed-package"), self.ops.events.index("overlay")
        )
        self.assertLess(
            self.ops.events.index("native-smoke"),
            self.ops.events.index("source-replay"),
        )
        self.assertEqual(len(self.ops.cleaned), 4)
        self.assertTrue(
            all(value.startswith("psst-native-") for value in self.ops.cleaned)
        )
        self.assertFalse(set(self.ops.cleaned) & set(self.originals.values()))

    def test_wrong_source_platform_toolchain_base_and_weak_record_fail_before_docker(
        self,
    ):
        cases = [
            lambda value: value.update(source_commit="b" * 40),
            lambda value: value.update(checked_platform="linux/arm64"),
            lambda value: value.update(native_execution=False),
            lambda value: value["toolchain_output"].update(
                go="go version go1.26.8 linux/arm64"
            ),
            lambda value: value["base_images"].update(caddy="caddy:latest"),
            lambda value: value["build_metadata"]["web"].pop(
                "containerimage.config.digest"
            ),
            lambda value: value.update(invented_approval=True),
        ]
        baseline = copy.deepcopy(self.record)
        for mutation in cases:
            with self.subTest(mutation=mutation):
                self.record = copy.deepcopy(baseline)
                mutation(self.record)
                self.write_record()
                with self.assertRaises(InvalidRelease):
                    self.prepare()
                self.assertEqual(self.ops.events, [])
                self.assertFalse(self.output.exists())

    def test_substituted_original_bytes_and_rehashed_wrong_pair_are_rejected(self):
        self.archive.write_bytes(b"substitution")
        with self.assertRaisesRegex(InvalidRelease, "bytes differ"):
            self.prepare()
        docker_save(self.archive, self.final_raw)
        self.record["image_archive"].update(
            sha256=native.file_record(self.archive)["sha256"][7:],
            size=self.archive.stat().st_size,
        )
        self.write_record()
        with self.assertRaisesRegex(InvalidRelease, "another image pair"):
            self.prepare()
        self.assertEqual(self.ops.events, [])

    def test_wrong_preloaded_platform_source_and_root_helper_are_preflight_failures(
        self,
    ):
        for config, field, value in [
            (self.originals["backend"], "Architecture", "arm64"),
            (self.originals["web"], "Id", "sha256:" + "0" * 64),
            (self.helper, "Config", {"User": "0:0"}),
            (self.originals["backend"], "Config", {"Labels": {}}),
        ]:
            original = self.images[config][field]
            self.images[config][field] = value
            with self.subTest(config=config, field=field), self.assertRaises(
                InvalidRelease
            ):
                self.prepare()
            self.images[config][field] = original
            self.assertFalse(self.output.exists())
            self.assertNotIn("collect", self.ops.events)

    def test_stage_failures_never_emit_completion_and_cleanup_only_owned_aliases(self):
        for stage in [
            "collect",
            "caddy",
            "signed-package",
            "overlay",
            "overlay-check",
            "save",
            "export",
            "native-smoke",
            "source-replay",
        ]:
            with self.subTest(stage=stage):
                self.output = self.folder / stage
                self.ops = FixtureOperations(self)
                self.ops.fail = stage
                with self.assertRaises(InvalidRelease):
                    self.prepare()
                self.assertFalse((self.output / "native-artifacts.json").exists())
                self.assertEqual(self.ops.events[-1], "cleanup")
                self.assertEqual(len(self.ops.cleaned), 4)

    def test_mixed_measurement_source_configs_or_emulated_smoke_cannot_complete(self):
        mutations = [
            lambda value: value["source"].update(commit="b" * 40),
            lambda value: value["smoke"].update(execution="emulated"),
            lambda value: value["smoke"]["tested_configs"].update(
                web=self.originals["web"]
            ),
            lambda value: value.update(publication_authorized=True),
            lambda value: value["smoke"]["checks"].remove("runtime-offer"),
        ]
        for number, mutation in enumerate(mutations):
            with self.subTest(number=number):
                self.output = self.folder / ("mixed" + str(number))
                self.ops = FixtureOperations(self)
                self.ops.changed_measurement = mutation
                with self.assertRaises(InvalidRelease):
                    self.prepare()
                self.assertFalse((self.output / "native-artifacts.json").exists())

    def test_replay_failure_and_changed_oci_or_original_inputs_cannot_complete(self):
        def change_oci(value, archives):
            archives["backend"].write_bytes(b"substitution after native smoke")

        def change_original(value, archives):
            self.record_path.write_bytes(b"changed original build record")

        mutations = [
            lambda value, archives: value.update(runtime_source_inputs_verified=False),
            lambda value, archives: value.update(distribution_authorized=True),
            change_oci,
            change_original,
        ]
        baseline = self.record_path.read_bytes()
        for number, mutation in enumerate(mutations):
            self.output = self.folder / ("replay" + str(number))
            self.record_path.write_bytes(baseline)
            self.ops = FixtureOperations(self)
            self.ops.changed_replay = mutation
            with self.subTest(number=number), self.assertRaises(InvalidRelease):
                self.prepare()
            self.assertFalse((self.output / "native-artifacts.json").exists())


class RealAdapterPolicy(unittest.TestCase):
    def setUp(self):
        self.ops = native.Operations()
        self.context = reports.NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", "a" * 40, "linux/amd64"
        )

    def test_native_docker_driver_required_without_converter_or_emulation(self):
        with patch.object(
            self.ops, "run", side_effect=[b"x86_64", b"Name: default\nDriver: docker\n"]
        ) as run:
            self.ops.preflight(self.context)
            self.assertEqual(run.call_count, 2)
        with patch.object(self.ops, "run", return_value=b"aarch64") as run:
            with self.assertRaisesRegex(InvalidRelease, "forbids emulation"):
                self.ops.preflight(self.context)
            self.assertEqual(run.call_count, 1)
        with patch.object(
            self.ops, "run", side_effect=[b"x86_64", b"Driver: docker-container"]
        ):
            with self.assertRaisesRegex(InvalidRelease, "Docker driver"):
                self.ops.preflight(self.context)
        arm = reports.NativeSourceContext(
            self.context.repository,
            self.context.version,
            self.context.commit,
            "linux/arm64",
        )
        with patch.object(
            self.ops, "run", side_effect=[b"aarch64", b"Driver: docker\n"]
        ):
            self.ops.preflight(arm)

    def test_concrete_helpers_receive_exact_context_and_do_not_publish(self):
        with patch.object(
            native.package_runtime_sources, "package", return_value={}
        ) as package:
            self.ops.package(
                Path("collection"), Path("cosign"), self.context, Path("pack")
            )
        self.assertEqual(
            package.call_args.args[3:7],
            (
                Path("cosign"),
                self.context.version,
                self.context.commit,
                "https://github.com/endorses/psst.zip/releases/download/v1.2.3",
            ),
        )
        with patch.object(
            native.verify_runtime_source_pack, "verify", return_value={}
        ) as replay:
            self.ops.replay(
                self.context,
                Path("pack"),
                {},
                Path("actual-smoke.json"),
                Path("cosign"),
                "sha256:" + "5" * 64,
            )
        self.assertEqual(replay.call_args.kwargs["repository"], self.context.repository)
        self.assertEqual(replay.call_args.kwargs["revision"], self.context.commit)
        with patch.object(
            native, "collect_native_measurement", return_value={}
        ) as measure:
            self.ops.measure(
                self.context,
                Path("pack"),
                {"backend": Path("b"), "web": Path("w")},
                {"backend": "b", "web": "w"},
            )
        self.assertEqual(measure.call_args.args, (self.context,))
        self.assertNotIn("binding", measure.call_args.kwargs)


def saved_oci_fixture(path, arch="amd64", *, compressed=False):
    files, configs, manifest = {}, {}, []
    for component in ("backend", "web"):
        oci, config = oci_fixture(component, arch)
        raw = oci["blobs/sha256/" + config[7:]]
        image_index = read_json(oci["index.json"])
        image = read_json(
            oci["blobs/sha256/" + image_index["manifests"][0]["digest"][7:]]
        )
        encoded = oci["blobs/sha256/" + image["layers"][0]["digest"][7:]]
        configs[component] = config
        config_name = config[7:] + ".json"
        layer_name = component + "/layer.tar"
        files[config_name] = raw
        files[layer_name] = encoded if compressed else gzip.decompress(encoded)
        manifest.append(
            {
                "Config": config_name,
                "RepoTags": [component + ":fixture"],
                "Layers": [layer_name],
            }
        )
    files["manifest.json"] = json_bytes(manifest)
    write_tar(path, files)
    return configs, files


class SavedOciExport(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="psst-saved-oci-export-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.archive = self.root / "pair.tar"
        self.context = reports.NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", "a" * 40, "linux/amd64"
        )
        self.configs, self.files = saved_oci_fixture(self.archive)

    def export(self, *, output=None, **overrides):
        return native.export_saved_image(
            **(
                {
                    "context": self.context,
                    "archive": self.archive,
                    "alias": "backend:fixture",
                    "output": output or self.root / "backend.oci.tar",
                    "component": "backend",
                    "tested_config": self.configs["backend"],
                }
                | overrides
            )
        )

    def test_export_preserves_noncanonical_config_and_layers_deterministically(self):
        first = self.root / "first.tar"
        second = self.root / "second.tar"
        with patch.object(
            native.Operations,
            "run",
            side_effect=AssertionError("export must execute no tool"),
        ):
            one = self.export(output=first)
            two = self.export(output=second)
        self.assertEqual(one, two)
        self.assertEqual(first.read_bytes(), second.read_bytes())
        with tarfile.open(first) as archive:
            preserved = archive.extractfile(
                "blobs/sha256/" + self.configs["backend"][7:]
            ).read()
        self.assertEqual(preserved, self.files[self.configs["backend"][7:] + ".json"])
        self.assertEqual(one["config_digest"], self.configs["backend"])

    def test_both_platform_graphs_and_gzip_layers_supported_without_emulation(self):
        for number, (arch, compressed) in enumerate(
            [("amd64", True), ("arm64", False), ("arm64", True)]
        ):
            configs, unused = saved_oci_fixture(
                self.archive, arch, compressed=compressed
            )
            context = reports.NativeSourceContext(
                self.context.repository,
                self.context.version,
                self.context.commit,
                "linux/" + arch,
            )
            result = self.export(
                output=self.root / (str(number) + ".tar"),
                context=context,
                tested_config=configs["backend"],
            )
            self.assertEqual(result["platform"], context.platform)
            self.assertEqual(result["config_digest"], configs["backend"])

    def test_wrong_alias_config_layer_and_unsafe_duplicate_members_fail(self):
        with self.assertRaisesRegex(InvalidRelease, "missing/ambiguous"):
            self.export(alias="wrong:fixture")
        with self.assertRaisesRegex(InvalidRelease, "tested configuration"):
            self.export(tested_config=self.configs["web"])
        wrong = {**self.files, "backend/layer.tar": b"different layer bytes"}
        write_tar(self.archive, wrong)
        with self.assertRaisesRegex(InvalidRelease, "layer differs"):
            self.export(output=self.root / "wrong-layer.tar")
        write_tar(self.archive, {**self.files, "../escape": b"escape"})
        with self.assertRaises(InvalidRelease):
            self.export(output=self.root / "unsafe.tar")
        write_tar(self.archive, self.files)
        with tarfile.open(self.archive, "a:") as archive:
            member = tarfile.TarInfo("manifest.json")
            member.size = 2
            archive.addfile(member, io.BytesIO(b"[]"))
        with self.assertRaisesRegex(InvalidRelease, "Duplicate"):
            self.export(output=self.root / "duplicate.tar")


@unittest.skipUnless(
    os.environ.get("PSST_NATIVE_EXPORT_INTEGRATION") == "1",
    "Opt in to disposable native Docker builds/save/export",
)
class NativeDockerExport(unittest.TestCase):
    def test_actual_native_docker_build_save_export_keeps_tested_ids(self):
        operations = native.Operations()
        architecture = (
            operations.run("docker", "info", "--format", "{{.Architecture}}")
            .decode()
            .strip()
        )
        architecture = {"x86_64": "amd64", "aarch64": "arm64"}.get(
            architecture, architecture
        )
        context = reports.NativeSourceContext(
            "endorses/psst.zip", "v1.2.3", "a" * 40, "linux/" + architecture
        )
        context.checked()
        operations.preflight(context)
        with tempfile.TemporaryDirectory(
            prefix="psst-native-docker-export-"
        ) as temporary:
            root = Path(temporary)
            aliases = {
                component: "psst-native-" + root.name + "-" + component + ":fixture"
                for component in native.COMPONENTS
            }
            configs = {}
            try:
                for component in sorted(native.COMPONENTS):
                    folder = root / component
                    folder.mkdir()
                    (folder / "fixture.txt").write_text(
                        component + " disposable archive fixture"
                    )
                    files, config = oci_fixture(component, architecture)
                    labels = read_json(files["blobs/sha256/" + config[7:]])["config"][
                        "Labels"
                    ]
                    dockerfile = (
                        "FROM scratch\nCOPY fixture.txt /fixture.txt\nUSER 10001\n"
                        + "".join(
                            "LABEL " + key + "=" + json.dumps(value) + "\n"
                            for key, value in labels.items()
                        )
                    )
                    (folder / "Dockerfile").write_text(dockerfile)
                    iidfile = root / (component + ".id")
                    operations.run(
                        "docker",
                        "buildx",
                        "build",
                        "--builder",
                        "default",
                        "--network",
                        "none",
                        "--pull=false",
                        "--platform",
                        context.platform,
                        "--load",
                        "--provenance=false",
                        "--sbom=false",
                        "--tag",
                        aliases[component],
                        "--iidfile",
                        str(iidfile),
                        str(folder),
                    )
                    configs[component] = iidfile.read_text().strip()
                saved = root / "genuine-docker-save.tar"
                operations.save(aliases, saved)
                native.saved_pair(saved, configs)
                for component in sorted(native.COMPONENTS):
                    exported = root / (component + ".oci.tar")
                    result = operations.export(
                        context,
                        saved,
                        aliases[component],
                        exported,
                        component,
                        configs[component],
                    )
                    self.assertEqual(result["config_digest"], configs[component])
                    self.assertEqual(result["platform"], context.platform)
                    self.assertGreater(result["blob_count"], 2)
            finally:
                operations.cleanup(list(aliases.values()))


if __name__ == "__main__":
    unittest.main()
