"""Real transport adapter exercised through in-memory HTTP/command boundaries."""

from __future__ import annotations

import base64
import copy
import io
import json
import os
import stat
import sys
import tarfile
import time
import unittest
from pathlib import Path
from unittest.mock import patch
from urllib.parse import parse_qs, urlsplit

import github_release_transport as transport
import publish_container_release as publication
import test_release_oci as oci_fixtures
import test_release_publication as publication_fixtures
from release_artifacts import InvalidRelease, json_bytes


class API:
    def __init__(self, fixture):
        self.fixture = fixture
        self.calls = []
        self.registry = {}
        self.releases = []
        self.assets = {}
        self.failures = {}
        self.visibility = "public"
        self.package_states = {}
        self.request_timeouts = []
        self.immutable_status = 200
        self.workflow = "concurrency:\n  group: container-release-publication\n  cancel-in-progress: false\njobs:\n  publish:\n    runs-on: ubuntu-24.04\n"
        self.redirect = "https://release-assets.githubusercontent.com/fixture/"
        self.next_asset = 100
        self.context = {
            "id": 77,
            "run_attempt": 1,
            "status": "in_progress",
            "event": "push",
            "head_sha": fixture.commit,
            "path": publication.WORKFLOW,
            "repository": {"full_name": "endorses/psst.zip"},
            "head_repository": {"full_name": "endorses/psst.zip"},
        }

    def response(self, value, status=200):
        return transport.Response(status, {}, json_bytes(value))

    def request(
        self,
        method,
        url,
        *,
        headers=None,
        body=None,
        limit=transport.MAX_JSON,
        destination=None,
        timeout=None,
    ):
        parsed = urlsplit(url)
        path = parsed.path
        query = parse_qs(parsed.query)
        self.calls.append((method, parsed.hostname, path, copy.deepcopy(headers or {})))
        if timeout is not None:
            assert 0 < timeout <= 600
            self.request_timeouts.append(timeout)
        failure = self.failures.get((method, parsed.hostname, path))
        if failure:
            if isinstance(failure, BaseException):
                raise failure
            return failure
        if parsed.hostname == "ghcr.io":
            if path == "/token":
                return self.response({"token": "fixture-registry-token"})
            key = path.removeprefix("/v2/").split("/manifests/")
            if method == "PUT":
                self.registry[tuple(key)] = body
                self.created_package(key[0])
                return transport.Response(
                    201, {"docker-content-digest": publication.sha256(body)}
                )
            raw = self.registry.get(tuple(key))
            if raw is None:
                return self.response({"errors": [{"code": "MANIFEST_UNKNOWN"}]}, 404)
            return transport.Response(
                200, {"docker-content-digest": publication.sha256(raw)}, raw
            )
        if parsed.hostname == "uploads.github.com":
            self.next_asset += 1
            raw = body.read_bytes()
            asset = {
                "id": self.next_asset,
                "name": query["name"][0],
                "size": len(raw),
                "digest": publication.sha256(raw),
                "state": "uploaded",
            }
            self.assets[asset["id"]] = (asset, raw)
            self.releases[0]["assets"].append(asset)
            return self.response(asset, 201)
        if parsed.hostname in {
            "release-assets.githubusercontent.com",
            "objects.githubusercontent.com",
        }:
            asset_id = int(path.rstrip("/").split("/")[-1])
            asset, raw = self.assets[asset_id]
            assert len(raw) <= limit
            destination.write_bytes(raw)
            return transport.Response(
                200, {}, digest=publication.sha256(raw), size=len(raw)
            )
        assert parsed.hostname == "api.github.com", url
        prefix = "/repos/endorses/psst.zip/"
        suffix = path.removeprefix(prefix)
        if suffix == "actions/runs/77/attempts/1":
            return self.response(self.context)
        if suffix == "contents/" + publication.WORKFLOW:
            assert query["ref"] == [self.fixture.commit]
            return self.response(
                {
                    "encoding": "base64",
                    "content": base64.b64encode(self.workflow.encode()).decode(),
                }
            )
        if suffix == "git/ref/tags/v1.2.3":
            return self.response(
                {
                    "ref": "refs/tags/v1.2.3",
                    "object": {"type": "commit", "sha": self.fixture.commit},
                }
            )
        if suffix == "immutable-releases":
            return self.response({"enabled": True}, self.immutable_status)
        if suffix == "releases":
            if method == "POST":
                data = json.loads(body)
                assert data["make_latest"] == "false"
                self.releases.append(
                    {
                        "id": 17,
                        "tag_name": data["tag_name"],
                        "draft": True,
                        "prerelease": False,
                        "immutable": False,
                        "assets": [],
                    }
                )
                return self.response(self.releases[-1], 201)
            page = int(query["page"][0])
            return self.response(self.releases[(page - 1) * 100 : page * 100])
        if suffix == "releases/17":
            value = self.releases[0]
            if method == "PATCH":
                data = json.loads(body)
                assert data == {
                    "draft": False,
                    "prerelease": False,
                    "make_latest": "false",
                }
                value.update(draft=False, immutable=True)
            return self.response(value)
        if suffix == "releases/17/assets":
            values = [value for value, _ in self.assets.values()]
            page = int(query["page"][0])
            return self.response(values[(page - 1) * 100 : page * 100])
        if suffix.startswith("releases/assets/"):
            asset_id = int(suffix.split("/")[-1])
            return transport.Response(302, {"location": self.redirect + str(asset_id)})
        if path == "/users/endorses":
            return self.response({"type": "User", "login": "endorses"})
        if path.startswith("/users/endorses/packages/container/"):
            name = path.split("/")[-1]
            component = name.removeprefix("psst-zip-")
            if (
                component in self.package_states
                and self.package_states[component] is None
            ):
                return self.response({"message": "Not Found"}, 404)
            value = {
                "name": name,
                "owner": {"login": "endorses"},
                "package_type": "container",
                "visibility": self.visibility,
                "repository": {"full_name": "endorses/psst.zip"},
            }
            value.update(self.package_states.get(component, {}))
            return self.response(value)
        raise AssertionError("Unexpected fixture request " + method + " " + url)

    def created_package(self, repository):
        component = repository.split("/")[-1].removeprefix("psst-zip-")
        if component in self.package_states and self.package_states[component] is None:
            self.package_states[component] = {"visibility": "private"}


class Commands:
    def __init__(self, api):
        self.api = api
        self.inspect_override = None
        self.calls = []
        self.local_images = {}
        self.fail_copy = False

    def __call__(self, args, *, environment, timeout=1200):
        self.calls.append(args)
        assert args[0] == "skopeo"
        assert "GH_TOKEN" not in environment and "GITHUB_TOKEN" not in environment
        if args[1] == "inspect":
            source = args[-1]
            directory = Path(source.removeprefix("oci:"))
            if directory.joinpath("index.json").exists():
                if self.inspect_override and directory.name == "web-arm64":
                    return self.inspect_override
                return self.read_layout(directory)
            return self.local_images[str(directory)]
        assert args[1] == "copy"
        assert (
            "--preserve-digests" in args
            and args[args.index("--retry-times") + 1] == "0"
        )
        source, destination = args[-2:]
        if destination.startswith("docker://ghcr.io/"):
            auth = Path(args[args.index("--dest-authfile") + 1])
            assert stat.S_IMODE(auth.stat().st_mode) == 0o600
            assert "fixture-workflow-token" not in " ".join(args)
            raw = self.read_layout(Path(source.removeprefix("oci:")))
            repository, digest = destination.removeprefix("docker://ghcr.io/").split(
                "@"
            )
            assert publication.sha256(raw) == digest
            self.api.registry[(repository, digest)] = raw
            self.api.created_package(repository)
            if self.fail_copy:
                raise transport.TransportError("Fixture interrupted registry response")
        else:
            assert "--src-no-creds" in args
            auth = Path(args[args.index("--authfile") + 1])
            assert json.loads(auth.read_bytes()) == {"auths": {}}
            repository, digest = source.removeprefix("docker://ghcr.io/").split("@")
            self.local_images[destination.removeprefix("oci:")] = self.api.registry[
                (repository, digest)
            ]
        return b""

    def read_layout(self, directory):
        index = json.loads((directory / "index.json").read_bytes())
        assert len(index["manifests"]) == 1
        descriptor = index["manifests"][0]
        raw = (directory / "blobs/sha256" / descriptor["digest"][7:]).read_bytes()
        assert len(raw) == descriptor["size"]
        assert publication.sha256(raw) == descriptor["digest"]
        image = json.loads(raw)
        for descriptor in [image["config"], *image["layers"]]:
            payload = (
                directory / "blobs/sha256" / descriptor["digest"][7:]
            ).read_bytes()
            assert len(payload) == descriptor["size"]
            assert publication.sha256(payload) == descriptor["digest"]
        return raw


class TransportChecks(unittest.TestCase):
    def setUp(self):
        self.fixture = publication_fixtures.PublicationChecks()
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.children = {}
        self.archives = {}
        self.tested_configs = {}
        for component, record in self.fixture.manifest["images"].items():
            for architecture in ("amd64", "arm64"):
                key = component + "-" + architecture
                files, config_digest = oci_fixtures.fixture(component, architecture)
                child_digest = json.loads(files["index.json"])["manifests"][0]["digest"]
                child = json.loads(files.pop("blobs/sha256/" + child_digest[7:]))
                config = json.loads(files.pop("blobs/sha256/" + config_digest[7:]))
                labels = config["config"]["Labels"]
                labels["org.opencontainers.image.revision"] = self.fixture.commit
                labels["zip.psst.source.archive"] = (
                    "https://github.com/endorses/psst.zip/archive/"
                    + self.fixture.commit
                    + ".tar.gz"
                )
                config_raw = json_bytes(config)
                config_digest = publication.sha256(config_raw)
                self.tested_configs[key] = config_digest
                child["config"].update(digest=config_digest, size=len(config_raw))
                raw = json_bytes(child)
                child_digest = publication.sha256(raw)
                index = json.loads(files["index.json"])
                index["manifests"][0].update(digest=child_digest, size=len(raw))
                files["index.json"] = json_bytes(index)
                files["blobs/sha256/" + config_digest[7:]] = config_raw
                files["blobs/sha256/" + child_digest[7:]] = raw
                self.children[key] = raw
                record["platform_digests"]["linux/" + architecture] = (
                    publication.sha256(raw)
                )
                archive = self.fixture.folder / (key + ".tar")
                with tarfile.open(archive, "w:") as output:
                    for name, body in files.items():
                        member = tarfile.TarInfo(name)
                        member.size = len(body)
                        output.addfile(member, io.BytesIO(body))
                self.archives[key] = archive
            raw = publication_fixtures.raw_index(record["platform_digests"])
            record["index"] = (
                record["index"].split("@")[0] + "@" + publication.sha256(raw)
            )
            self.fixture.indexes[component].write_bytes(raw)
        self.fixture.write_manifest()
        self.plan = self.fixture.prepare()
        self.api = API(self.fixture)
        self.commands = Commands(self.api)
        self.state = self.fixture.folder / "state"
        self.adapter = self.new_adapter()
        self.files = {
            "release-manifest.json": self.fixture.manifest_path,
            self.fixture.bundle.name: self.fixture.bundle,
            self.fixture.source.name: self.fixture.source,
        }
        self.reports = {
            key: self.fixture.reports[key] for key in publication.READBACK_GATES
        }

    def new_adapter(self, state=None, **kwargs):
        return transport.GitHubReleaseTransport(
            self.plan,
            state or self.state,
            transport.WorkflowContext(77, 1),
            github_token="fixture-workflow-token",
            inspection_token="fixture-admin-read-token",
            actor="endorses",
            http=self.api,
            execute=self.commands,
            **kwargs,
        )

    def events(self):
        return [
            json.loads(line)
            for line in (self.state / "v1.2.3.jsonl").read_text().splitlines()
        ]

    def stage(self):
        with self.adapter.prepare_images(
            self.archives,
            self.fixture.indexes,
            tested_configs=self.tested_configs,
        ) as prepared:
            self.adapter.push_images(prepared)

    def stage_tags(self, reservation):
        self.stage()
        self.adapter.upload_assets(reservation, self.files)
        self.adapter.create_version_tags(self.fixture.indexes)

    def test_real_adapter_full_pipeline_requires_proof_and_public_readback(self):
        self.adapter = self.new_adapter(
            sleeper=lambda _: self.fail("existing public packages must not wait")
        )
        self.assertFalse(self.adapter.package_preflight()["initialization_required"])
        with publication.reserve_draft(self.plan, self.adapter) as reservation:
            self.assertTrue(self.adapter.held)
            self.stage()
            self.assertTrue(
                all(
                    value["visibility"] == "public"
                    for value in self.adapter.wait_for_public_packages().values()
                )
            )
            observation = self.adapter.anonymous_pull()
            self.assertEqual(observation["complete_child_pulls"], sorted(self.archives))
            self.adapter.upload_assets(reservation, self.files)
            self.adapter.inspect_assets(reservation)
            self.adapter.create_version_tags(self.fixture.indexes)
            result = self.adapter.publish(
                reservation, self.reports, self.fixture.verifier
            )
            self.assertTrue(result["immutable"])
            public = self.adapter.verify_public(reservation)
            self.assertTrue(public["pull"]["anonymous"])
            self.assertFalse(public["pull"]["credentials_used"])
            self.assertTrue(self.adapter.public_verified)
        self.assertFalse(self.adapter.held)
        self.assertFalse(list(self.state.glob("transport-*")))
        events = self.events()
        intents = [event["operation"] for event in events if event["phase"] == "intent"]
        self.assertEqual(intents[0], "reserve-draft")
        self.assertEqual(intents[-1], "publish-release")
        self.assertEqual(events[-1]["operation"], "public-readback")
        self.assertNotIn("fixture-workflow-token", json.dumps(events))
        self.assertNotIn("fixture-admin-read-token", json.dumps(events))
        self.assertFalse(
            any(
                reference in {"latest", "v1", "v1.2"}
                for _, reference in self.api.registry
            )
        )
        immutable_calls = [
            call for call in self.api.calls if call[2].endswith("/immutable-releases")
        ]
        self.assertTrue(
            all(
                call[3]["Authorization"] == "Bearer fixture-admin-read-token"
                for call in immutable_calls
            )
        )
        redirected_calls = [
            call
            for call in self.api.calls
            if call[1] == "release-assets.githubusercontent.com"
        ]
        self.assertTrue(
            all("Authorization" not in call[3] for call in redirected_calls)
        )

    def test_context_rejects_non_hosted_or_wrong_source_ref_job(self):
        env = {
            "GITHUB_ACTIONS": "true",
            "RUNNER_ENVIRONMENT": "github-hosted",
            "GITHUB_REPOSITORY": "endorses/psst.zip",
            "GITHUB_JOB": "publish",
            "GITHUB_REF": "refs/tags/v1.2.3",
            "GITHUB_SHA": self.fixture.commit,
            "GITHUB_EVENT_NAME": "push",
            "GITHUB_RUN_ID": "77",
            "GITHUB_RUN_ATTEMPT": "1",
            "GITHUB_WORKFLOW_REF": "endorses/psst.zip/"
            + publication.WORKFLOW
            + "@refs/tags/v1.2.3",
            "GITHUB_WORKFLOW_SHA": self.fixture.commit,
        }
        self.assertEqual(
            transport.WorkflowContext.from_environment(self.plan, env),
            transport.WorkflowContext(77, 1),
        )
        for key, value in (
            ("RUNNER_ENVIRONMENT", "self-hosted"),
            ("GITHUB_JOB", "other"),
            ("GITHUB_REF", "refs/heads/main"),
            ("GITHUB_SHA", "f" * 40),
            ("GITHUB_RUN_ID", "77;evil"),
            ("GITHUB_EVENT_NAME", "workflow_dispatch"),
        ):
            with self.subTest(key=key), self.assertRaises(InvalidRelease):
                transport.WorkflowContext.from_environment(
                    self.plan, {**env, key: value}
                )

    def test_wrong_remote_run_and_missing_serialization_fail_before_reservation(self):
        for key, value in (
            ("head_sha", "f" * 40),
            ("path", ".github/workflows/evil.yml"),
            ("status", "completed"),
            ("event", "pull_request"),
        ):
            original = self.api.context[key]
            self.api.context[key] = value
            with (
                self.subTest(key=key),
                self.assertRaises(InvalidRelease),
                publication.reserve_draft(self.plan, self.adapter),
            ):
                self.fail("Untrusted workflow reserved draft")
            self.api.context[key] = original
        self.api.workflow = self.api.workflow.replace(
            "cancel-in-progress: false", "cancel-in-progress: true"
        )
        with (
            self.assertRaises(InvalidRelease),
            publication.reserve_draft(self.plan, self.adapter),
        ):
            self.fail("Cancelable workflow reserved draft")
        self.assertEqual(self.api.releases, [])
        self.assertFalse((self.state / "v1.2.3.jsonl").exists())

    def test_unreadable_immutable_policy_is_not_success_or_absence(self):
        self.api.immutable_status = 403
        with (
            self.assertRaisesRegex(InvalidRelease, "HTTP 403"),
            publication.reserve_draft(self.plan, self.adapter),
        ):
            self.fail("Unknown immutable policy admitted")
        self.assertEqual(self.api.releases, [])
        self.assertEqual(self.events()[-1]["phase"], "stopped")

    def test_pagination_inspects_all_authenticated_drafts(self):
        self.api.releases = [
            {"id": number, "tag_name": "other"} for number in range(1, 101)
        ] + [{"id": 999, "tag_name": "v1.2.3", "draft": True, "assets": []}]
        with (
            self.assertRaisesRegex(InvalidRelease, "already has a release"),
            publication.reserve_draft(self.plan, self.adapter),
        ):
            self.fail("Page-two draft overlooked")
        self.assertEqual(len(self.api.releases), 101)

    def test_registry_unauthorized_throttled_failed_or_html404_never_means_absent(self):
        target = "/v2/endorses/psst-zip-backend/manifests/v1.2.3"
        for status, body in ((401, b"{}"), (429, b"{}"), (500, b"{}"), (404, b"{}")):
            self.api.failures[("GET", "ghcr.io", target)] = transport.Response(
                status, {}, body
            )
            with self.subTest(status=status), self.assertRaises(InvalidRelease):
                self.adapter.absent_or_digest("backend", "v1.2.3")
        self.api.failures.clear()
        self.assertIsNone(self.adapter.absent_or_digest("backend", "v1.2.3"))

    def test_mutation_requires_held_lease_and_old_journal_cannot_resume(self):
        with self.assertRaises(InvalidRelease):
            self.stage()
        with publication.reserve_draft(self.plan, self.adapter):
            pass
        adapter = self.new_adapter()
        with (
            self.assertRaises(FileExistsError),
            publication.reserve_draft(self.plan, adapter),
        ):
            self.fail("Old local journal replayed")
        self.assertEqual(len(self.api.releases), 1)
        adapter = self.new_adapter(self.fixture.folder / "other-runner-state")
        with (
            self.assertRaisesRegex(InvalidRelease, "already has a release"),
            publication.reserve_draft(self.plan, adapter),
        ):
            self.fail("Old remote draft adopted")

    def test_all_staged_sources_validated_before_first_image_push(self):
        self.commands.inspect_override = b"wrong reviewed digest"
        with (
            self.assertRaisesRegex(InvalidRelease, "reviewed final child"),
            self.adapter.prepare_images(
                self.archives, self.fixture.indexes, tested_configs=self.tested_configs
            ),
        ):
            self.fail("Wrong local child admitted")
        self.assertEqual(self.api.registry, {})
        self.assertEqual(self.api.releases, [])
        self.assertFalse((self.state / "v1.2.3.jsonl").exists())
        self.assertFalse(any(args[1] == "copy" for args in self.commands.calls))
        for args in self.commands.calls:
            self.assertFalse(Path(args[-1].removeprefix("oci:")).exists())

    def test_root_owned_nested_exports_become_exact_runner_owned_native_sources(self):
        originals = {}
        nested_keys = {"backend-amd64", "web-arm64"}
        for key in nested_keys:
            archive = self.archives[key]
            with tarfile.open(archive, "r:") as source:
                files = {
                    member.name: source.extractfile(member).read() for member in source
                }
            index = json.loads(files["index.json"])
            wrapper = files["index.json"]
            wrapper_digest = publication.sha256(wrapper)
            files["blobs/sha256/" + wrapper_digest[7:]] = wrapper
            index["manifests"] = [
                {
                    "mediaType": index["mediaType"],
                    "digest": wrapper_digest,
                    "size": len(wrapper),
                }
            ]
            files["index.json"] = json_bytes(index)
            with tarfile.open(archive, "w:") as target:
                # Buildx may omit explicit parent directory entries entirely.
                for name, raw in files.items():
                    member = tarfile.TarInfo(name)
                    member.size = len(raw)
                    member.uid = member.gid = 0
                    member.mode = 0o4777
                    target.addfile(member, io.BytesIO(raw))
        originals = {
            key: publication.source_digest(path) for key, path in self.archives.items()
        }
        with self.adapter.prepare_images(
            self.archives, self.fixture.indexes, tested_configs=self.tested_configs
        ) as prepared:
            root = prepared.images[0].directory.parent
            self.assertFalse(self.adapter.held)
            self.assertFalse(self.api.calls)
            for image in prepared.images:
                with self.subTest(key=image.key):
                    self.assertEqual(
                        self.commands.read_layout(image.directory),
                        self.children[image.key],
                    )
                    index = json.loads((image.directory / "index.json").read_bytes())
                    self.assertEqual(
                        index["manifests"][0]["platform"]["architecture"],
                        image.key.split("-")[1],
                    )
                    self.assertEqual(len(image.inventory), 5)
                    for path in image.directory.rglob("*"):
                        self.assertEqual(path.stat().st_uid, os.getuid())
                        self.assertEqual(
                            stat.S_IMODE(path.stat().st_mode),
                            0o700 if path.is_dir() else 0o600,
                        )
            with publication.reserve_draft(self.plan, self.adapter):
                self.adapter.push_images(prepared)
        self.assertFalse(root.exists())
        for key, path in self.archives.items():
            self.assertEqual(publication.source_digest(path), originals[key])
        copies = [args for args in self.commands.calls if args[1] == "copy"]
        self.assertEqual(len(copies), 4)
        self.assertTrue(all(args[-2].startswith("oci:") for args in copies))
        self.assertEqual(len(self.api.registry), 6)

    def test_prepared_sources_reject_original_and_derived_tampering_before_any_push(
        self,
    ):
        with self.adapter.prepare_images(
            self.archives, self.fixture.indexes, tested_configs=self.tested_configs
        ) as prepared:
            image = prepared.images[-1]
            paths = [image.archive, self.fixture.indexes["web"]]
            paths.extend(image.directory / name for name, _, _ in image.inventory)
            with publication.reserve_draft(self.plan, self.adapter):
                for path in paths:
                    original = path.read_bytes()
                    with self.subTest(name=path.name):
                        try:
                            path.write_bytes(original + b"substitution")
                            with self.assertRaisesRegex(InvalidRelease, "changed"):
                                self.adapter.push_images(prepared)
                            self.assertFalse(self.api.registry)
                            self.assertFalse(
                                any(args[1] == "copy" for args in self.commands.calls)
                            )
                            self.assertFalse(
                                any(
                                    event["operation"].startswith("push-")
                                    for event in self.events()
                                )
                            )
                        finally:
                            path.write_bytes(original)

    def test_prepared_sources_expire_with_context_and_reject_symlinks(self):
        with publication.reserve_draft(self.plan, self.adapter):
            with self.adapter.prepare_images(
                self.archives, self.fixture.indexes, tested_configs=self.tested_configs
            ) as prepared:
                image = prepared.images[-1]
                path = image.directory / "index.json"
                raw = path.read_bytes()
                path.unlink()
                path.symlink_to(self.fixture.indexes["web"])
                with self.assertRaisesRegex(InvalidRelease, "Unsafe staged OCI"):
                    self.adapter.push_images(prepared)
                path.unlink()
                path.write_bytes(raw)
                path.chmod(0o600)
            self.assertIsNone(self.adapter.prepared_images)
            self.assertFalse(image.directory.exists())
            with self.assertRaisesRegex(InvalidRelease, "active prepared"):
                self.adapter.push_images(prepared)

    def test_interrupted_child_push_is_durable_uncertain_and_read_only_reconciled(self):
        self.commands.fail_copy = True
        with (
            self.assertRaises(transport.TransportError),
            publication.reserve_draft(self.plan, self.adapter),
        ):
            self.stage()
        uncertain = [event for event in self.events() if event["phase"] == "uncertain"]
        self.assertEqual(len(uncertain), 1)
        self.assertEqual(uncertain[0]["operation"], "push-backend-amd64")
        self.assertEqual(len(self.api.registry), 1)
        before = len(self.api.calls)
        report = self.adapter.reconcile()
        self.assertEqual(
            report["local_journal"]["unresolved_intents"], ["push-backend-amd64"]
        )
        self.assertFalse(report["automatic_resume_allowed"])
        self.assertFalse(report["automatic_cleanup_allowed"])
        self.assertFalse(report["ready_advertisement_allowed"])
        self.assertTrue(all(method == "GET" for method, *_ in self.api.calls[before:]))

    def test_recovery_journal_substitution_or_truncation_is_not_adopted(self):
        self.commands.fail_copy = True
        with (
            self.assertRaises(transport.TransportError),
            publication.reserve_draft(self.plan, self.adapter),
        ):
            self.stage()
        path = self.state / "v1.2.3.jsonl"
        records = self.events()
        records[0]["binding"] = publication.sha256(b"another release")
        path.write_bytes(
            b"\n".join(json.dumps(value).encode() for value in records) + b"\n"
        )
        with self.assertRaisesRegex(InvalidRelease, "differs from reviewed"):
            self.adapter.reconcile()
        path.write_bytes(b'{"truncated"')
        with self.assertRaisesRegex(InvalidRelease, "Incomplete"):
            self.adapter.reconcile()

    def test_smoke_tested_configuration_substitution_blocks_every_push(self):
        self.tested_configs["web-arm64"] = publication.sha256(b"another configuration")
        with (
            self.assertRaisesRegex(InvalidRelease, "configuration"),
            publication.reserve_draft(self.plan, self.adapter),
        ):
            self.stage()
        self.assertEqual(self.api.registry, {})
        self.assertFalse(any(args[1] == "copy" for args in self.commands.calls))

    def test_upload_failure_preserves_draft_and_blocks_version_tags(self):
        target = "/repos/endorses/psst.zip/releases/17/assets"
        self.api.failures[("POST", "uploads.github.com", target)] = transport.Response(
            502, {}, b"{}"
        )
        with (
            self.assertRaisesRegex(InvalidRelease, "Asset upload failed"),
            publication.reserve_draft(self.plan, self.adapter) as reservation,
        ):
            self.stage()
            self.adapter.upload_assets(reservation, self.files)
        self.assertTrue(self.api.releases[0]["draft"])
        self.assertFalse(
            any(reference == "v1.2.3" for _, reference in self.api.registry)
        )
        self.assertTrue(
            any(
                event["phase"] == "uncertain"
                and event["operation"].startswith("upload-")
                for event in self.events()
            )
        )

    def test_private_package_and_existing_version_tag_block_tagging(self):
        with publication.reserve_draft(self.plan, self.adapter) as reservation:
            self.stage()
            self.adapter.upload_assets(reservation, self.files)
            self.api.visibility = "private"
            with self.assertRaisesRegex(InvalidRelease, "not public"):
                self.adapter.create_version_tags(self.fixture.indexes)
            self.api.visibility = "public"
            raw = self.fixture.indexes["backend"].read_bytes()
            self.api.registry[("endorses/psst-zip-backend", "v1.2.3")] = raw
            with self.assertRaisesRegex(InvalidRelease, "never overwrite"):
                self.adapter.create_version_tags(self.fixture.indexes)
            self.assertEqual(
                self.api.registry[("endorses/psst-zip-backend", "v1.2.3")], raw
            )
            self.assertTrue(self.api.releases[0]["draft"])

    def test_first_packages_require_explicit_opt_in_and_both_absent_namespaces(self):
        self.api.package_states = {"backend": None, "web": None}
        with self.assertRaisesRegex(InvalidRelease, "HTTP 404"):
            self.adapter.package_preflight()
        self.adapter = self.new_adapter(initialize_packages=True)
        result = self.adapter.package_preflight()
        self.assertTrue(result["initialization_required"])
        self.assertEqual(result["packages"], {"backend": None, "web": None})
        self.assertEqual(
            result["setup_urls"]["backend"],
            "https://github.com/users/endorses/packages/container/psst-zip-backend/settings",
        )
        key = (
            "GET",
            "api.github.com",
            "/users/endorses/packages/container/psst-zip-backend",
        )
        for status in (401, 403, 429, 500):
            self.api.failures[key] = self.api.response({}, status)
            with (
                self.subTest(status=status),
                self.assertRaisesRegex(InvalidRelease, "HTTP " + str(status)),
            ):
                self.new_adapter(initialize_packages=True).package_preflight()
        del self.api.failures[key]
        for states, error in (
            ({"backend": None}, "Mixed"),
            ({"backend": {"visibility": "private"}, "web": None}, "not public"),
            ({"backend": {"repository": {"full_name": "other/project"}}}, "not public"),
        ):
            with self.subTest(states=states):
                self.api.package_states = states
                with self.assertRaisesRegex(InvalidRelease, error):
                    self.new_adapter(initialize_packages=True).package_preflight()
        self.api.package_states = {}
        for enabled in (False, True):
            result = self.new_adapter(initialize_packages=enabled).package_preflight()
            self.assertFalse(result["initialization_required"])
        self.assertFalse(self.api.registry)
        self.assertFalse(self.api.releases)

    def test_initialized_pair_waits_only_until_operator_public_readback(self):
        now, waits = [0], []

        def sleep(seconds):
            waits.append(seconds)
            now[0] += seconds
            self.api.package_states["backend"]["visibility"] = "public"
            if len(waits) == 2:
                self.api.package_states["web"]["visibility"] = "public"

        self.adapter = self.new_adapter(
            initialize_packages=True, clock=lambda: now[0], sleeper=sleep
        )
        self.api.package_states = {"backend": None, "web": None}
        self.adapter.package_preflight()
        with self.assertRaisesRegex(InvalidRelease, "held trusted"):
            self.adapter.wait_for_public_packages()
        with publication.reserve_draft(self.plan, self.adapter) as reservation:
            with self.assertRaisesRegex(InvalidRelease, "complete pushed"):
                self.adapter.wait_for_public_packages()
            self.stage()
            self.assertTrue(self.api.releases[0]["draft"])
            self.assertTrue(
                all(
                    reference.startswith("sha256:")
                    for _, reference in self.api.registry
                )
            )
            with patch("sys.stdout", new_callable=io.StringIO) as output:
                result = self.adapter.wait_for_public_packages()
            self.assertEqual(waits, [5, 5])
            self.assertEqual(self.api.request_timeouts[-1], 590)
            self.assertTrue(
                all(value["visibility"] == "public" for value in result.values())
            )
            self.assertIn("/psst-zip-web/settings", output.getvalue())
            self.assertNotIn("fixture-workflow-token", output.getvalue())
            self.adapter.upload_assets(reservation, self.files)
            self.adapter.create_version_tags(self.fixture.indexes)
        self.assertTrue(self.api.releases[0]["draft"])
        self.assertEqual(
            len([args for args in self.commands.calls if args[1] == "copy"]), 4
        )

    def test_package_wait_fails_on_timeout_bad_identity_and_api_errors_without_tagging(
        self,
    ):
        now = [0]

        def sleep(seconds):
            now[0] += seconds

        self.adapter = self.new_adapter(
            initialize_packages=True, clock=lambda: now[0], sleeper=sleep
        )
        self.api.package_states = {"backend": None, "web": None}
        self.adapter.package_preflight()
        with publication.reserve_draft(self.plan, self.adapter):
            self.stage()
            for field, value in (
                ("repository", {"full_name": "other/project"}),
                ("owner", {"login": "other"}),
                ("name", "wrong-package"),
                ("package_type", "npm"),
            ):
                self.api.package_states["backend"] = {
                    "visibility": "private",
                    field: value,
                }
                with (
                    self.subTest(field=field),
                    self.assertRaisesRegex(InvalidRelease, "not public"),
                ):
                    self.adapter.wait_for_public_packages()
                self.assertEqual(now[0], 0)
            self.api.package_states["backend"] = {"visibility": "private"}
            key = (
                "GET",
                "api.github.com",
                "/users/endorses/packages/container/psst-zip-backend",
            )
            for status in (401, 403, 404, 429, 500):
                self.api.failures[key] = self.api.response({}, status)
                with (
                    self.subTest(status=status),
                    self.assertRaisesRegex(InvalidRelease, "HTTP " + str(status)),
                ):
                    self.adapter.wait_for_public_packages()
                self.assertEqual(now[0], 0)
            del self.api.failures[key]
            with (
                patch("sys.stdout", new_callable=io.StringIO),
                self.assertRaisesRegex(InvalidRelease, "timed out"),
            ):
                self.adapter.wait_for_public_packages()
            self.assertEqual(now[0], 600)
            self.assertFalse(self.adapter.tags_created)
            self.assertFalse(
                any(reference == "v1.2.3" for _, reference in self.api.registry)
            )
            self.assertTrue(self.api.releases[0]["draft"])

    def test_unverified_readback_report_cannot_publish_even_complete_draft(self):
        with publication.reserve_draft(self.plan, self.adapter) as reservation:
            self.stage_tags(reservation)
            self.fixture.verifier.mutate = lambda receipt: {"passed": True}
            with self.assertRaisesRegex(InvalidRelease, "no receipt"):
                self.adapter.publish(reservation, self.reports, self.fixture.verifier)
            self.assertTrue(self.api.releases[0]["draft"])
            self.assertFalse(any(method == "PATCH" for method, *_ in self.api.calls))

    def test_asset_redirect_is_constrained_and_never_forwards_workflow_token(self):
        with publication.reserve_draft(self.plan, self.adapter) as reservation:
            self.stage()
            self.adapter.upload_assets(reservation, self.files)
            self.api.redirect = "https://attacker.example/"
            with self.assertRaisesRegex(InvalidRelease, "Untrusted asset redirect"):
                self.adapter.inspect_assets(reservation)
            self.assertFalse(
                any(host == "attacker.example" for _, host, *_ in self.api.calls)
            )

    def test_journal_intent_is_synced_before_mutation_and_repeated_operation_rejected(
        self,
    ):
        private = self.fixture.folder / "journal"
        private.mkdir(mode=0o700)
        journal = transport.Journal(
            private / "receipt.jsonl", self.plan, transport.WorkflowContext(77, 1)
        )
        self.addCleanup(journal.close)

        def action():
            events = [
                json.loads(line) for line in journal.path.read_text().splitlines()
            ]
            self.assertEqual(events[-1]["phase"], "intent")
            return {"remote_id": 17}

        journal.mutate("fixture-mutation", {"safe": True}, action)
        with self.assertRaisesRegex(InvalidRelease, "already attempted"):
            journal.mutate("fixture-mutation", {}, action)

    def test_fixed_https_hosts_body_limits_and_command_timeout(self):
        with self.assertRaisesRegex(InvalidRelease, "Untrusted transport URL"):
            transport.HTTPS().request("GET", "https://attacker.example/path")
        response = unittest.mock.Mock()
        response.status = 200
        response.getheaders.return_value = [("Content-Length", "100")]
        response.read1.side_effect = io.BytesIO(b"too much output").read1
        connection = unittest.mock.Mock()
        connection.getresponse.return_value = response
        with (
            patch.object(
                transport.http.client, "HTTPSConnection", return_value=connection
            ),
            self.assertRaisesRegex(InvalidRelease, "exceeds size"),
        ):
            transport.HTTPS().request("GET", "https://api.github.com/fixture", limit=8)
        before = time.monotonic()
        with self.assertRaisesRegex(InvalidRelease, "timed out"):
            transport.command(
                [sys.executable, "-c", "import time; time.sleep(60)"],
                environment={"PATH": os.environ["PATH"]},
                timeout=0.1,
            )
        self.assertLess(time.monotonic() - before, 3)
        with (
            patch.object(transport, "MAX_JSON", 8),
            self.assertRaisesRegex(InvalidRelease, "output exceeds"),
        ):
            transport.command(
                [sys.executable, "-c", "print('fixture large command output')"],
                environment={"PATH": os.environ["PATH"]},
                timeout=1,
            )


if __name__ == "__main__":
    unittest.main()
