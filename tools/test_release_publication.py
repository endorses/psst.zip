"""Publication preparation and failure boundaries; no live transport or credentials."""

from __future__ import annotations

import copy
import hashlib
import json
import os
import subprocess
import sys
import tempfile
import unittest
from contextlib import contextmanager
from dataclasses import replace
from pathlib import Path
from unittest.mock import patch

import publish_container_release as publication
import release_artifacts as release
from test_release_artifacts import digest, manifest


def raw_index(children: dict[str, str]) -> bytes:
    return release.json_bytes(
        {
            "schemaVersion": 2,
            "mediaType": "application/vnd.oci.image.index.v1+json",
            "manifests": [
                {
                    "mediaType": "application/vnd.oci.image.manifest.v1+json",
                    "size": 321,
                    "digest": value,
                    "platform": {"os": "linux", "architecture": platform.split("/")[1]},
                }
                for platform, value in children.items()
            ],
        }
    )


class FixtureVerifier:
    """Tests' trusted adapter; production has no verifier implementation yet."""

    def __init__(self):
        self.mutate = lambda receipt: receipt
        self.details = {}
        self.calls = []

    def verify(self, gate, path, binding):
        self.calls.append((gate, binding.digest))
        details = {}
        if gate == "source-ci":
            details["jobs"] = {
                name: "success"
                for name in ("security", "backend", "web", "android", "ios")
            }
        elif gate in {"source-scanners", "final-image-scanners"}:
            subjects = dict(binding.subjects)
            targets = (
                ["backend-source", "web-source"]
                if gate == "source-scanners"
                else [key for key in subjects if key.endswith(("-amd64", "-arm64"))]
            )
            details["scans"] = [
                {
                    "target": target,
                    "subject": (
                        "git:" + binding.repository + "@" + binding.commit
                        if gate == "source-scanners"
                        else subjects[target]
                    ),
                    "scanner": "fixture-scanner",
                    "version": "1.0.0",
                    "database": "sha256:fixture-database",
                    "scanned_at": "2026-10-07T00:00:00Z",
                    "status": "complete",
                    "exit_code": 0,
                    "findings": [],
                }
                for target in targets
            ]
        elif gate == "final-image-smoke":
            details["execution"] = {name: "native" for name in release.PLATFORMS}
        elif gate == "provenance":
            details["subjects"] = dict(binding.subjects)
        details = self.details.get(gate, details)
        return self.mutate(
            publication.VerifiedEvidence(
                gate,
                binding.digest,
                publication.sha256(path.read_bytes()),
                True,
                details,
            )
        )


class FixtureReservation:
    def __init__(self, plan):
        self.plan = plan
        self.held = False
        self.created = []
        self.state = {
            "releases": [],
            "version_tags": {"backend": None, "web": None},
            "tag_commit": plan.binding.commit,
            "immutable_releases": True,
        }
        self.draft = {
            "id": 17,
            "tag_name": plan.binding.version,
            "draft": True,
            "prerelease": False,
            "assets": [],
        }

    @contextmanager
    def serialized(self, repository):
        if self.held:
            raise release.InvalidRelease("Another publication holds the lease")
        assert repository == self.plan.binding.repository
        self.held = True
        try:
            yield
        finally:
            self.held = False

    def snapshot(self, plan):
        assert self.held
        return self.state

    def create_draft(self, parameters):
        assert self.held
        self.created.append(parameters)
        return self.draft


class PublicationChecks(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="psst-publication-test-")
        self.addCleanup(self.temp.cleanup)
        self.folder = Path(self.temp.name)
        self.root = self.folder / "repo"
        self.root.mkdir()
        self.git("init", "-q")
        self.git("config", "user.name", "Publication fixture")
        self.git("config", "user.email", "publication@example.invalid")
        self.git("config", "core.hooksPath", os.devnull)
        for name in release.REQUIRED_FILES | {"deploy/update.py"}:
            file = self.root / name
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_text("fixture\n")
        self.git("add", ".")
        self.git("commit", "-qm", "Reviewed publication fixture")
        self.git("branch", "-M", "main")
        self.commit = self.git("rev-parse", "HEAD").decode().strip()
        self.git("update-ref", "refs/remotes/origin/main", self.commit)
        self.git("remote", "add", "origin", "https://github.com/endorses/psst.zip.git")
        self.git("tag", "v1.2.3")
        self.bundle = release.build_bundle(
            self.root, self.folder, "v1.2.3", "deployment-ready"
        )
        self.manifest = manifest(self.commit)
        self.manifest["payload_profile"] = "deployment-ready"
        self.manifest["bundle"]["sha256"] = hashlib.sha256(
            self.bundle.read_bytes()
        ).hexdigest()
        self.indexes = {}
        for component, record in self.manifest["images"].items():
            raw = raw_index(record["platform_digests"])
            record["index"] = (
                record["index"].split("@")[0] + "@" + publication.sha256(raw)
            )
            self.indexes[component] = self.folder / (component + ".json")
            self.indexes[component].write_bytes(raw)
        self.manifest_path = self.folder / "release-manifest.json"
        self.write_manifest()
        self.source = self.folder / "runtime-source.tar.gz"
        self.source.write_bytes(b"fixture source with retained recipe and notices")
        self.reports = {}
        for gate in publication.GATES | publication.READBACK_GATES:
            self.reports[gate] = self.folder / (gate + ".json")
            self.reports[gate].write_text('{"fixture":true}\n')
        self.verifier = FixtureVerifier()

    def git(self, *args):
        return subprocess.run(
            ["git", "-C", str(self.root), *args],
            capture_output=True,
            check=True,
            env={
                **os.environ,
                "GIT_CONFIG_GLOBAL": os.devnull,
                "GIT_CONFIG_NOSYSTEM": "1",
            },
        ).stdout

    def write_manifest(self):
        self.manifest_path.write_bytes(release.json_bytes(self.manifest))

    def prepare(self, **overrides):
        args = {
            "root": self.root,
            "repository": "endorses/psst.zip",
            "ref": "refs/tags/v1.2.3",
            "event_sha": self.commit,
            "reviewed_commit": self.commit,
            "manifest_path": self.manifest_path,
            "bundle": self.bundle,
            "indexes": self.indexes,
            "source_assets": {self.source.name: self.source},
            "reports": {key: self.reports[key] for key in publication.GATES},
            "verifier": self.verifier,
        }
        args.update(overrides)
        return publication.prepare_publication(**args)

    def ready_snapshot(self, plan):
        return {
            "release": {
                "id": 17,
                "tag_name": plan.binding.version,
                "draft": True,
                "prerelease": False,
                "assets": [
                    {"name": name, "digest": value, "size": 123, "state": "uploaded"}
                    for name, value in plan.assets
                ],
            },
            "version_tags": {
                name: value["index"].split("@")[1]
                for name, value in self.manifest["images"].items()
            },
            "tag_commit": self.commit,
            "immutable_releases": True,
        }

    def ready(self, plan, snapshot=None, reports=None):
        return publication.ready_release_request(
            plan,
            {"release_id": 17, "binding": plan.binding.digest, "ready": False},
            snapshot or self.ready_snapshot(plan),
            reports or {key: self.reports[key] for key in publication.READBACK_GATES},
            self.verifier,
        )

    def test_preparation_enumerates_exact_eight_updater_subjects_and_bound_source(self):
        plan = self.prepare()
        self.assertEqual(len(plan.updater_subjects), 8)
        self.assertEqual(
            set(dict(plan.updater_subjects)),
            {
                "manifest",
                "bundle",
                "backend-index",
                "web-index",
                "backend-amd64",
                "backend-arm64",
                "web-amd64",
                "web-arm64",
            },
        )
        self.assertEqual(len(plan.binding.subjects), 9)
        self.assertFalse(plan.record()["publication_authorized"])
        self.assertEqual(len(self.verifier.calls), len(publication.GATES))

    def test_repository_review_tag_checkout_manifest_and_bundle_are_bound(self):
        for args in (
            {"reviewed_commit": "f" * 40},
            {"ref": "refs/heads/main"},
            {"repository": "attacker/psst.zip"},
            {"event_sha": "--help"},
            {"source_assets": {}},
        ):
            with self.subTest(args=args), self.assertRaises(release.InvalidRelease):
                self.prepare(**args)
        self.git(
            "remote", "set-url", "origin", "https://github.com/attacker/psst.zip.git"
        )
        with self.assertRaisesRegex(release.InvalidRelease, "origin differs"):
            self.prepare()
        self.git(
            "remote", "set-url", "origin", "https://github.com/endorses/psst.zip.git"
        )
        self.manifest["payload_profile"] = "artifact-foundation"
        self.write_manifest()
        with self.assertRaisesRegex(release.InvalidRelease, "deployment-ready"):
            self.prepare()

    def test_tracked_changes_and_source_asset_empty_content_fail(self):
        tracked = self.root / "LICENSE"
        tracked.write_text("unreviewed change\n")
        with self.assertRaisesRegex(release.InvalidRelease, "Tracked checkout differs"):
            self.prepare()
        self.git("checkout", "--", "LICENSE")
        self.source.write_bytes(b"")
        with self.assertRaisesRegex(release.InvalidRelease, "source asset"):
            self.prepare()

    def test_changed_plan_is_rejected_before_any_reservation_or_readiness(self):
        plan = self.prepare()
        plan.manifest["images"]["backend"]["index"] = (
            "ghcr.io/endorses/psst-zip-backend@" + digest("f")
        )
        adapter = FixtureReservation(plan)
        with (
            self.assertRaisesRegex(
                release.InvalidRelease, "changed after verification"
            ),
            publication.reserve_draft(plan, adapter),
        ):
            self.fail("Mutated plan reached reservation")
        self.assertFalse(adapter.created)
        with self.assertRaises(release.InvalidRelease):
            self.ready(plan)

    def test_missing_and_self_asserted_reports_never_approve(self):
        reports = {key: self.reports[key] for key in publication.GATES}
        for gate in publication.GATES:
            with self.subTest(gate=gate), self.assertRaises(release.InvalidRelease):
                self.prepare(
                    reports={key: path for key, path in reports.items() if key != gate}
                )
        for change in (
            {"passed": False},
            {"binding_digest": digest("f")},
            {"report_digest": digest("f")},
            {"gate": "unexpected"},
        ):
            self.verifier.mutate = lambda receipt, change=change: replace(
                receipt, **change
            )
            with self.subTest(change=change), self.assertRaises(release.InvalidRelease):
                self.prepare()
        self.verifier.mutate = lambda receipt: {"passed": True}
        with self.assertRaisesRegex(release.InvalidRelease, "no receipt"):
            self.prepare()

    def test_scanner_errors_missing_targets_unresolved_findings_and_wrong_subject_fail(
        self,
    ):
        plan = self.prepare()
        for gate in ("source-scanners", "final-image-scanners"):
            valid = self.verifier.verify(gate, self.reports[gate], plan.binding).details
            for field, replacement in (
                ("exit_code", 1),
                ("exit_code", True),
                ("status", "failed"),
                ("database", ""),
                ("subject", "wrong"),
                ("findings", None),
                ("findings", [{"disposition": "accepted-risk", "reason": "later"}]),
            ):
                details = copy.deepcopy(valid)
                details["scans"][0][field] = replacement
                self.verifier.details = {gate: details}
                with (
                    self.subTest(gate=gate, field=field),
                    self.assertRaises(release.InvalidRelease),
                ):
                    self.prepare()
            details = copy.deepcopy(valid)
            details["scans"].pop()
            self.verifier.details = {gate: details}
            with self.assertRaises(release.InvalidRelease):
                self.prepare()
            self.verifier.details = {}

    def test_smoke_requires_both_architectures_and_ci_requires_ios(self):
        for gate, details in (
            ("final-image-smoke", {"execution": {"linux/amd64": "native"}}),
            (
                "final-image-smoke",
                {"execution": {"linux/amd64": "native", "linux/arm64": "emulated"}},
            ),
            (
                "final-image-smoke",
                {"execution": {platform: "not-run" for platform in release.PLATFORMS}},
            ),
            ("source-ci", {"jobs": {"backend": "success"}}),
        ):
            self.verifier.details = {gate: details}
            with self.subTest(gate=gate), self.assertRaises(release.InvalidRelease):
                self.prepare()

    def test_actual_index_bytes_and_pair_platforms_are_checked(self):
        record = self.manifest["images"]["backend"]
        raw = self.indexes["backend"].read_bytes()
        with self.assertRaisesRegex(release.InvalidRelease, "wrong digest"):
            publication.validate_registry_index(raw + b" ", record)
        value = json.loads(raw)
        variations = []
        for mutate in (
            lambda item: item["manifests"].pop(),
            lambda item: item["manifests"].append(copy.deepcopy(item["manifests"][0])),
            lambda item: item["manifests"][1]["platform"].update(variant="v9"),
            lambda item: item["manifests"][1]["platform"].update(
                architecture="riscv64"
            ),
            lambda item: item.update(mediaType=[]),
            lambda item: item["manifests"][0].update(size=True),
        ):
            changed = copy.deepcopy(value)
            mutate(changed)
            variations.append(changed)
        for changed in variations:
            content = release.json_bytes(changed)
            matching_record = {
                **record,
                "index": record["index"].split("@")[0]
                + "@"
                + publication.sha256(content),
            }
            with self.assertRaises(release.InvalidRelease):
                publication.validate_registry_index(content, matching_record)

    def test_attestation_descriptors_are_bound_and_cross_component_aliases_fail(self):
        record = self.manifest["images"]["backend"]
        value = json.loads(self.indexes["backend"].read_bytes())
        value["manifests"].append(
            {
                "mediaType": "application/vnd.oci.image.manifest.v1+json",
                "size": 12,
                "digest": digest("8"),
                "platform": {"os": "unknown", "architecture": "unknown"},
                "annotations": {
                    "vnd.docker.reference.type": "attestation-manifest",
                    "vnd.docker.reference.digest": record["platform_digests"][
                        "linux/amd64"
                    ],
                },
            }
        )

        def check():
            raw = release.json_bytes(value)
            publication.validate_registry_index(
                raw,
                {
                    **record,
                    "index": record["index"].split("@")[0]
                    + "@"
                    + publication.sha256(raw),
                },
            )

        check()
        value["manifests"][-1]["annotations"]["vnd.docker.reference.digest"] = digest(
            "f"
        )
        with self.assertRaisesRegex(release.InvalidRelease, "another image"):
            check()
        self.manifest["images"]["web"] = copy.deepcopy(record)
        with self.assertRaises(release.InvalidRelease):
            publication.image_subjects(self.manifest)

    def test_source_asset_substitution_changes_binding_and_unsafe_names_fail(self):
        first = self.prepare()
        self.source.write_bytes(b"changed")
        self.assertNotEqual(first.binding.digest, self.prepare().binding.digest)
        for name in ("../source.tar.gz", "release-manifest.json", "different.tar.gz"):
            with self.subTest(name=name), self.assertRaises(release.InvalidRelease):
                self.prepare(source_assets={name: self.source})
        link = self.folder / "link.tar.gz"
        link.symlink_to(self.source)
        with self.assertRaises(release.InvalidRelease):
            self.prepare(source_assets={link.name: link})

    def test_exclusive_reservation_holds_lease_and_never_resumes_existing_draft(self):
        plan = self.prepare()
        adapter = FixtureReservation(plan)
        with publication.reserve_draft(plan, adapter) as receipt:
            self.assertTrue(adapter.held)
            self.assertFalse(receipt["ready"])
            with (
                self.assertRaisesRegex(release.InvalidRelease, "lease"),
                publication.reserve_draft(plan, adapter),
            ):
                self.fail("Second reservation entered")
        self.assertFalse(adapter.held)
        self.assertEqual(adapter.created[0]["make_latest"], "false")
        adapter.state["releases"] = [adapter.draft]
        with (
            self.assertRaisesRegex(release.InvalidRelease, "recovery required"),
            publication.reserve_draft(plan, adapter),
        ):
            self.fail("Existing draft adopted")
        self.assertEqual(len(adapter.created), 1)

    def test_partial_push_moved_tag_disabled_immutability_and_api_failure_do_not_reserve(
        self,
    ):
        plan = self.prepare()
        for key, value in (
            ("version_tags", {"backend": digest("f"), "web": None}),
            ("tag_commit", "f" * 40),
            ("immutable_releases", False),
            ("releases", None),
        ):
            adapter = FixtureReservation(plan)
            adapter.state[key] = value
            with (
                self.subTest(key=key),
                self.assertRaises(release.InvalidRelease),
                publication.reserve_draft(plan, adapter),
            ):
                self.fail("Unsafe reservation")
            self.assertFalse(adapter.created)

        adapter = FixtureReservation(plan)
        with (
            patch.object(adapter, "snapshot", side_effect=OSError("API lookup failed")),
            self.assertRaises(OSError),
            publication.reserve_draft(plan, adapter),
        ):
            self.fail("API failure treated as absent")
        self.assertFalse(adapter.created)
        self.assertFalse(adapter.held)

    def test_failed_draft_creation_and_caller_failure_release_lease_without_cleanup(
        self,
    ):
        plan = self.prepare()
        adapter = FixtureReservation(plan)
        adapter.draft["assets"] = [{"name": "unexpected"}]
        with (
            self.assertRaisesRegex(release.InvalidRelease, "fresh empty"),
            publication.reserve_draft(plan, adapter),
        ):
            self.fail("Dirty draft accepted")
        self.assertFalse(adapter.held)
        adapter = FixtureReservation(plan)
        with self.assertRaises(RuntimeError), publication.reserve_draft(plan, adapter):
            raise RuntimeError("Interrupted before image pushes")
        self.assertFalse(adapter.held)
        self.assertEqual(len(adapter.created), 1)

    def test_readiness_requires_exact_complete_uploads_tags_and_authenticated_readbacks(
        self,
    ):
        plan = self.prepare()
        ready = self.ready(plan)
        self.assertEqual(
            ready,
            {
                "release_id": 17,
                "draft": False,
                "prerelease": False,
                "make_latest": "false",
            },
        )
        for mutate in (
            lambda state: state["release"]["assets"].pop(),
            lambda state: state["release"]["assets"][0].update(state="starter"),
            lambda state: state["release"]["assets"][0].update(digest=digest("f")),
            lambda state: state["release"].update(id=18),
            lambda state: state["release"].update(draft=False),
            lambda state: state["version_tags"].update(web=None),
            lambda state: state.update(tag_commit="f" * 40),
            lambda state: state.update(immutable_releases=False),
        ):
            state = self.ready_snapshot(plan)
            mutate(state)
            with self.assertRaises(release.InvalidRelease):
                self.ready(plan, snapshot=state)
        for gate in publication.READBACK_GATES:
            with self.assertRaises(release.InvalidRelease):
                self.ready(
                    plan,
                    reports={
                        key: self.reports[key]
                        for key in publication.READBACK_GATES
                        if key != gate
                    },
                )
        self.verifier.details["provenance"] = {"subjects": dict(plan.updater_subjects)}
        with self.assertRaisesRegex(release.InvalidRelease, "every updater and source"):
            self.ready(plan)

    def test_recovery_preserves_partial_work_without_ready_or_convenience_tag_actions(
        self,
    ):
        plan = self.prepare()
        adapter = FixtureReservation(plan)
        adapter.state["version_tags"]["backend"] = digest("f")
        adapter.state["releases"] = [
            {**adapter.draft, "assets": [{"name": "partial", "state": "starter"}]}
        ]
        report = publication.recovery_report(plan, adapter.state)
        self.assertFalse(report["ready"])
        self.assertFalse(report["automatic_resume_allowed"])
        self.assertFalse(report["automatic_cleanup_allowed"])
        self.assertEqual(report["snapshot"], adapter.state)
        order = publication.publication_order(plan)
        self.assertLess(
            order.index("verify-downloaded-assets-and-all-required-provenance"),
            order.index("publish-complete-immutable-draft-with-make-latest-false"),
        )
        self.assertEqual(
            order[-1], "advance-convenience-tags-only-after-public-readback"
        )

    def test_cli_can_only_inspect_and_exclusive_output_cannot_overwrite(self):
        output = self.folder / "inspection.json"
        command = [
            sys.executable,
            str(Path(publication.__file__)),
            "--manifest",
            str(self.manifest_path),
            "--backend-index",
            str(self.indexes["backend"]),
            "--web-index",
            str(self.indexes["web"]),
            "--output",
            str(output),
        ]
        first = subprocess.run(command, capture_output=True, check=False)
        self.assertEqual(first.returncode, 0, first.stderr)
        value = json.loads(output.read_bytes())
        self.assertFalse(value["publication_authorized"])
        self.assertEqual(len(value["updater_subjects"]), 8)
        second = subprocess.run(command, capture_output=True, check=False)
        self.assertNotEqual(second.returncode, 0)
        self.assertEqual(value, json.loads(output.read_bytes()))


if __name__ == "__main__":
    unittest.main()
