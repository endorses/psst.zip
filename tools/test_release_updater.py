"""Disposable fault injection for the privileged updater's transaction boundary.

These replace Docker/GitHub operations with a deterministic adapter. They prove
fail-closed control flow and protected input boundaries, not a live VPS upgrade.
"""

from __future__ import annotations

from contextlib import closing, contextmanager
import copy
from datetime import datetime, timezone
import hashlib
import importlib.util
import io
import json
import os
import shutil
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import release_artifacts
from test_release_artifacts import manifest

_spec = importlib.util.spec_from_file_location(
    "release_update", Path(__file__).resolve().parents[1] / "deploy/update.py"
)
update = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(update)


def config(root: Path) -> dict:
    return {
        "repository": "endorses/psst.zip",
        "signer_workflow": "endorses/psst.zip/.github/workflows/release.yml",
        "installation": str(root),
        "project": "psst-zip",
        "environment": ".env",
        "compose_files": ["compose.yml"],
        "operator_files": ["Caddyfile"],
        "release_overrides": [],
        "external_proxy": False,
        "domain": "transfer.example.com",
        "db_path": "/app/data/existing.db",
        "volume_names": {
            "backend:/app/data": "old_backend",
            "caddy:/data": "old_caddy_data",
            "caddy:/config": "old_caddy_config",
        },
        "disk_reserve_bytes": 1024**3,
        "image_reserve_bytes": 1024**3,
        "verification_hook": None,
        "checkpoint_hook": None,
        "github_token_file": None,
        "retention_count": 2,
    }


def compose() -> dict:
    return {
        "services": {
            "backend": {
                "image": "old-backend",
                "environment": {"DB_PATH": "/app/data/existing.db"},
                "volumes": [
                    {"type": "volume", "source": "backend", "target": "/app/data"}
                ],
            },
            "caddy": {
                "image": "old-web",
                "ports": [
                    {
                        "published": "443",
                        "target": 8443,
                        "protocol": "tcp",
                        "host_ip": "0.0.0.0",
                    }
                ],
                "volumes": [
                    {"type": "volume", "source": "data", "target": "/data"},
                    {"type": "volume", "source": "config", "target": "/config"},
                ],
            },
        },
        "volumes": {
            "backend": {"name": "old_backend"},
            "data": {"name": "old_caddy_data"},
            "config": {"name": "old_caddy_config"},
        },
    }


def evidence(transaction: dict) -> dict:
    identity = transaction.get(
        "verification_identity",
        {
            "version": transaction["version"],
            "source_commit": transaction["manifest"]["source"]["commit"],
        },
    )
    checks = update.FLOW_CHECKS | (
        update.RESTORE_CHECKS if transaction.get("restoring") else set()
    )
    return {
        "checks": {name: "Observed real fixture invariant: " + name for name in checks},
        "observed_at": datetime.now(timezone.utc).isoformat(),
        **identity,
    }


class FakeHost:
    def __init__(
        self,
        root: Path,
        fail: str | None = None,
        pause: bool = False,
        hook: bool = False,
    ):
        self.root, self.fail, self.pause, self.hook = root, fail, pause, hook
        self.calls = []
        self.original = compose()
        self.source_volume = root / "source-volume"
        self.source_volume.mkdir()
        (self.source_volume / "ciphertext").write_bytes(b"original encrypted bytes")
        self.original_hash = update.fingerprint(self.source_volume / "ciphertext")
        self.restored = None
        self.updater = None

    def step(self, name: str) -> None:
        self.calls.append(name)
        if self.fail == name:
            raise update.UpdateError("injected fault: " + name)

    def protected_inputs(self):
        self.step("configuration")
        return []

    def download(self, version, target):
        self.step("authenticate")
        result = manifest()
        result["version"] = version
        result["payload_profile"] = "deployment-ready"
        return result

    def current(self):
        self.step("inspect-current")
        return copy.deepcopy(self.original)

    def candidate(self, target, selected):
        self.step("candidate-config")
        result = self.current()
        result["services"]["backend"]["image"] = selected["images"]["backend"]["index"]
        result["services"]["caddy"]["image"] = selected["images"]["web"]["index"]
        return result

    def preflight(self, current, candidate, selected, checkpoint):
        self.step("preflight")
        return {
            "images": {"backend": "old-backend-id", "caddy": "old-web-id"},
            "platform": "linux/amd64",
            "volumes": {"old_backend": {}},
            "baseline_config": {"max_file_size": 100},
            "database": "fixture-only",
        }

    def pull(self, selected, platform):
        self.step("pull")

    def original_health(self, current, adopted):
        self.step("old-health")

    def ownership(self, selected, adopted):
        self.step("ownership")

    def validate_caddy(self, path):
        self.step("proxy-validation")

    def incident(self, config, action):
        self.step("incident-" + action)
        if action != "incident-status":
            self.pause = action == "pause"
        return self.pause

    def stopped(self, path, volumes):
        self.step("stop-writers")

    def free_ports(self, config):
        self.step("free-ports")

    def backup(self, current, adopted, checkpoint, transaction):
        self.step("backup")
        checkpoint.mkdir()
        (checkpoint / "ciphertext").write_bytes(
            (self.source_volume / "ciphertext").read_bytes()
        )
        (checkpoint / "checksum").write_text(
            update.fingerprint(checkpoint / "ciphertext")
        )
        self.step("verify-backup")

    def compose(self, path, *args):
        data = update.load_json(path)
        command = args[0]
        if command == "up" and path.name == "private.compose.json":
            # At the actual call boundary, not after it, migration risk must be durable.
            record = self.updater.read()
            if not record["mutation_started"] or record["phase"] not in {
                "migration-starting",
                "restore-starting",
            }:
                raise AssertionError(
                    "normal startup called before durable mutation boundary"
                )
            self.step("start-restored" if self.restored else "start-candidate")
            for service in data["services"].values():
                for port in service.get("ports", []):
                    if port["host_ip"] != "127.0.0.1":
                        raise AssertionError("candidate exposed public ingress")
            if not self.restored:
                (self.source_volume / "ciphertext").write_bytes(
                    b"candidate migration changed store"
                )
        elif command == "up" and path.name == "public.compose.json":
            if self.updater.read()["phase"] != "activating":
                raise AssertionError("routing changed without durable activation gate")
            self.step("open-public")
        elif command == "up":
            self.step("restart-original")
        elif command == "stop":
            self.step("stop-candidate")
        return b""

    def automatic_checks(self, config, transaction):
        self.step("health")
        if not self.pause:
            raise AssertionError("checks must precede resume")

    def verification_hook(self, path, transaction_path):
        self.step("flow-verification")
        return evidence(update.load_json(transaction_path)) if self.hook else None

    def verify_backup(self, checkpoint):
        self.step("verify-checkpoint")
        if (
            update.fingerprint(checkpoint / "ciphertext")
            != (checkpoint / "checksum").read_text()
        ):
            raise update.UpdateError("corrupt checkpoint")
        return {}

    def https(self, compose, path, **kwargs):
        return {"max_file_size": 100, "public_transfers_paused": self.pause}

    def restore(self, checkpoint, target, transaction):
        self.verify_backup(checkpoint)
        self.step("restore")
        self.restored = self.root / "new-isolated-volume"
        self.restored.mkdir()
        (self.restored / "ciphertext").write_bytes(
            (checkpoint / "ciphertext").read_bytes()
        )
        result = copy.deepcopy(self.original)
        result["volumes"]["backend"]["name"] = "new-isolated-volume"
        self.pause = True
        return result

    def run(self, *args):
        self.step("restart-policy")
        return b"fixture-container" if args[:3] == ("docker", "ps", "-q") else b""


class Transactions(unittest.TestCase):
    @contextmanager
    def fixture(self, **kwargs):
        with tempfile.TemporaryDirectory(prefix="psst-update-unit-") as directory:
            root = Path(directory)
            state, backups = root / "state", root / "checkpoints"
            (state / "transactions").mkdir(parents=True)
            backups.mkdir()
            host = FakeHost(root, **kwargs)
            runner = update.Updater(config(root), host, state, backups)
            host.updater = runner
            yield runner, host, root

    def test_authenticated_update_without_probe_hook_is_honestly_pending(self):
        with self.fixture() as (runner, host, root):
            record = runner.update("v1.2.3")
            self.assertEqual(record["phase"], "awaiting-verification")
            self.assertTrue(record["mutation_started"])
            self.assertTrue(host.pause)
            self.assertNotIn("open-public", host.calls)
            self.assertNotIn("incident-resume", host.calls)
            self.assertLess(host.calls.index("pull"), host.calls.index("stop-writers"))
            self.assertLess(
                host.calls.index("verify-backup"), host.calls.index("start-candidate")
            )
            self.assertEqual(
                (root / "checkpoints" / record["id"] / "ciphertext").read_bytes(),
                b"original encrypted bytes",
            )

    def test_protected_probe_success_activates_and_restores_prior_pause(self):
        for paused in (False, True):
            with self.subTest(paused=paused), self.fixture(pause=paused, hook=True) as (
                runner,
                host,
                _,
            ):
                record = runner.update("v1.2.3")
                self.assertEqual(record["phase"], "completed")
                self.assertEqual(host.pause, paused)
                self.assertIn("open-public", host.calls)
                self.assertTrue((runner.state / "active.json").is_file())
                self.assertNotIn("restart-original", host.calls)
                self.assertLess(
                    host.calls.index("flow-verification"),
                    host.calls.index("open-public"),
                )

    def test_all_before_stop_failures_keep_original_untouched(self):
        for fault in (
            "configuration",
            "authenticate",
            "inspect-current",
            "candidate-config",
            "preflight",
            "pull",
            "ownership",
            "proxy-validation",
        ):
            with self.subTest(fault=fault), self.fixture(fail=fault) as (
                runner,
                host,
                _,
            ):
                with self.assertRaises(update.UpdateError):
                    runner.update("v1.2.3")
                self.assertEqual(runner.read()["phase"], "failed-safe")
                self.assertFalse(runner.read()["mutation_started"])
                self.assertNotIn("stop-writers", host.calls)
                self.assertNotIn("start-candidate", host.calls)
                self.assertEqual(
                    update.fingerprint(host.source_volume / "ciphertext"),
                    host.original_hash,
                )

    def test_partial_stop_and_backup_failures_restart_only_original(self):
        for fault in ("stop-writers", "free-ports", "backup", "verify-backup"):
            with self.subTest(fault=fault), self.fixture(fail=fault) as (
                runner,
                host,
                _,
            ):
                with self.assertRaises(update.UpdateError):
                    runner.update("v1.2.3")
                self.assertIn("restart-original", host.calls)
                self.assertFalse(host.pause)
                self.assertNotIn("start-candidate", host.calls)
                self.assertEqual(runner.read()["phase"], "failed-safe")
                self.assertEqual(
                    update.fingerprint(host.source_volume / "ciphertext"),
                    host.original_hash,
                )

    def test_startup_health_and_authenticated_failure_never_restart_old_binary(self):
        for fault in (
            "start-candidate",
            "health",
            "flow-verification",
            "open-public",
            "restart-policy",
        ):
            with self.subTest(fault=fault), self.fixture(fail=fault, hook=True) as (
                runner,
                host,
                _,
            ):
                with self.assertRaises(update.UpdateError):
                    runner.update("v1.2.3")
                self.assertTrue(runner.read()["mutation_started"])
                self.assertEqual(runner.read()["phase"], "failed-closed")
                self.assertNotIn("restart-original", host.calls)
                self.assertIn("stop-candidate", host.calls)
                checkpoint = Path(runner.read()["checkpoint"])
                self.assertEqual(
                    (checkpoint / "ciphertext").read_bytes(),
                    b"original encrypted bytes",
                )

    def test_interrupted_transaction_refuses_a_new_version(self):
        with self.fixture() as (runner, host, _):
            runner.update("v1.2.3")
            count = len(host.calls)
            with self.assertRaisesRegex(update.UpdateError, "unresolved"):
                runner.update("v1.2.4")
            self.assertEqual(len(host.calls), count)
            with self.assertRaisesRegex(update.UpdateError, "forbidden"):
                runner.recover_safe()

    def test_boundary_is_recorded_even_when_process_dies_on_start(self):
        with self.fixture() as (runner, host, _):
            original = host.step

            def interrupt(name):
                original(name)
                if name == "start-candidate":
                    raise KeyboardInterrupt()

            host.step = interrupt
            with self.assertRaises(KeyboardInterrupt):
                runner.update("v1.2.3")
            self.assertTrue(runner.read()["mutation_started"])
            self.assertEqual(runner.read()["phase"], "failed-closed")
            self.assertNotIn("restart-original", host.calls)

    def test_isolated_restore_preserves_original_and_requires_security_gate(self):
        with self.fixture() as (runner, host, _):
            failed = runner.update("v1.2.3")
            migrated = update.fingerprint(host.source_volume / "ciphertext")
            restored = runner.restore()
            self.assertEqual(restored["phase"], "restored-awaiting-verification")
            self.assertEqual(
                update.fingerprint(host.source_volume / "ciphertext"), migrated
            )
            self.assertEqual(
                (host.restored / "ciphertext").read_bytes(), b"original encrypted bytes"
            )
            self.assertEqual(
                (Path(failed["checkpoint"]) / "ciphertext").read_bytes(),
                b"original encrypted bytes",
            )
            self.assertNotIn("open-public", host.calls)
            report = evidence(restored)
            del report["checks"]["independent_traffic_allowances_reconciled"]
            with self.assertRaises(update.UpdateError):
                runner.verify_evidence(restored, report)
            runner.verify_evidence(restored, evidence(restored))
            runner.activate(restored)
            self.assertEqual(runner.read()["phase"], "completed")

    def test_corrupt_checkpoint_refuses_restore_before_stopping_candidate(self):
        with self.fixture() as (runner, host, _):
            record = runner.update("v1.2.3")
            (Path(record["checkpoint"]) / "ciphertext").write_bytes(b"corruption")
            count = host.calls.count("stop-candidate")
            with self.assertRaisesRegex(update.UpdateError, "corrupt"):
                runner.restore()
            self.assertEqual(host.calls.count("stop-candidate"), count)
            self.assertIsNone(host.restored)

    def test_restore_failure_remains_closed_and_cannot_overwrite_partial_copy(self):
        with self.fixture() as (runner, host, _):
            runner.update("v1.2.3")
            host.fail = "start-restored"
            with self.assertRaises(update.UpdateError):
                runner.restore()
            self.assertEqual(runner.read()["phase"], "failed-closed")
            self.assertNotIn("open-public", host.calls)
            with self.assertRaisesRegex(update.UpdateError, "already attempted"):
                runner.restore()

    def test_rechecking_verified_local_candidate_failure_is_durably_closed(self):
        with self.fixture() as (runner, host, root):
            record = runner.update("v1.2.3")
            runner.verify_evidence(record, evidence(record))
            host.fail = "health"
            with self.assertRaises(update.UpdateError):
                runner.activate(record)
            self.assertEqual(runner.read()["phase"], "failed-closed")
            self.assertIn("stop-candidate", host.calls)
            self.assertNotIn("open-public", host.calls)

    def test_explicit_recovery_health_failure_stops_original_and_keeps_pause(self):
        with self.fixture() as (runner, host, root):
            transaction, target = runner.new("v1.2.3")
            update.atomic_json(target / "previous.compose.json", compose())
            transaction.update(prior_pause=False, adopted={"baseline_config": {}})
            runner.write(transaction, "backing-up")
            host.fail = "old-health"
            with self.assertRaises(update.UpdateError):
                runner.recover_safe()
            self.assertEqual(runner.read()["phase"], "failed-closed")
            self.assertTrue(host.pause)
            self.assertNotIn("incident-resume", host.calls)
            self.assertIn("stop-candidate", host.calls)

    def test_failed_attempt_does_not_become_the_previous_deployed_version(self):
        with self.fixture(hook=True) as (runner, host, root):
            runner.update("v1.2.3")
            host.fail = "pull"
            with self.assertRaises(update.UpdateError):
                runner.update("v1.2.4")
            host.fail = None
            host.hook = False
            record = runner.update("v1.2.5")
            self.assertEqual(record["previous_version"], "v1.2.3")

    def test_false_stale_or_wrong_candidate_reports_cannot_activate(self):
        with self.fixture() as (runner, _, _):
            record = runner.update("v1.2.3")
            for modify in (
                lambda value: value["checks"].update(existing_account_and_session=True),
                lambda value: value.update(observed_at="2020-01-01T00:00:00Z"),
                lambda value: value.update(source_commit="b" * 40),
                lambda value: value["checks"].pop("existing_encrypted_download"),
            ):
                report = evidence(record)
                modify(report)
                with self.assertRaises(update.UpdateError):
                    runner.verify_evidence(record, report)
            with self.assertRaises(update.UpdateError):
                runner.activate(record)
            self.assertEqual(runner.read()["phase"], "awaiting-verification")

    def test_explicit_before_mutation_recovery_and_preparing_abandon(self):
        with self.fixture() as (runner, host, _):
            record, target = runner.new("v1.2.3")
            runner.recover_safe()
            self.assertEqual(runner.read()["phase"], "abandoned")
            self.assertNotIn("restart-original", host.calls)
            record, target = runner.new("v1.2.4")
            update.atomic_json(target / "previous.compose.json", compose())
            record["prior_pause"] = False
            record["adopted"] = {"baseline_config": {}}
            runner.write(record, "backing-up")
            host.pause = True
            runner.recover_safe()
            self.assertFalse(host.pause)
            self.assertEqual(runner.read()["phase"], "failed-safe")


class Boundaries(unittest.TestCase):
    def test_checkpoint_protection_defaults_strict_and_requires_explicit_enum(self):
        good = config(Path("/opt/psst.zip"))
        self.assertEqual(
            update.checkpoint_protection(update.validate_config(good)), "off-host"
        )
        for policy in ("off-host", "local-only"):
            self.assertEqual(
                update.validate_config({**good, "checkpoint_protection": policy})[
                    "checkpoint_protection"
                ],
                policy,
            )
        for policy in (None, True, False, 0, "local", "", {}):
            with self.subTest(policy=policy), self.assertRaises(update.UpdateError):
                update.validate_config({**good, "checkpoint_protection": policy})

    def test_configuration_requires_explicit_storage_and_trusted_repository(self):
        good = config(Path("/opt/psst.zip"))
        self.assertEqual(update.validate_config(good), good)
        for key, bad in (
            ("repository", "owner/repo;id"),
            ("signer_workflow", "other/release.yml"),
            ("installation", "/opt/../etc"),
            ("compose_files", ["../other.yml"]),
            ("environment", "/etc/shadow"),
            ("domain", "https://psst.zip"),
            ("db_path", "/app/data/../other.db"),
            ("db_path", "/app/data//etc/shadow"),
            ("db_path", "/app/data/db\nOTHER=value"),
            ("volume_names", {"backend:/app/data": "guess"}),
            ("retention_count", 1),
            ("disk_reserve_bytes", True),
            ("external_proxy", "false"),
        ):
            with self.subTest(key=key):
                bad_config = copy.deepcopy(good)
                bad_config[key] = bad
                with self.assertRaises(
                    (update.UpdateError, update._release.InvalidRelease)
                ):
                    update.validate_config(bad_config)

    def test_forced_ssh_accepts_only_version_and_status(self):
        self.assertEqual(update.ssh_command("update v1.2.3"), ["update", "v1.2.3"])
        self.assertEqual(update.ssh_command("status"), ["status"])
        for command in (
            "",
            "update v1.2.3;id",
            "update --manifest /tmp/file",
            "update v1.2.3\n",
            "verify",
            "restore",
            "update v01.2.3",
            "status x",
            "env A=1 update v1.2.3",
        ):
            with self.subTest(command=command), self.assertRaises(update.UpdateError):
                update.ssh_command(command)

    def test_public_github_commands_fix_host_without_inheriting_credentials(self):
        with tempfile.TemporaryDirectory(prefix="psst-public-release-") as directory:
            host = update.Host(config(Path(directory)))
            with patch.dict(
                update.os.environ,
                {"GH_HOST": "unexpected.example", "GH_TOKEN": "not-a-real-token"},
            ), patch.object(
                update.subprocess,
                "run",
                return_value=SimpleNamespace(returncode=0, stdout=b"public metadata"),
            ) as command:
                host.run("gh", "api", "public-release", gh=True)
                environment = command.call_args.kwargs["env"]
                self.assertEqual(environment["GH_HOST"], "github.com")
                self.assertNotIn("GH_TOKEN", environment)
            with patch.object(
                update.subprocess,
                "Popen",
                side_effect=OSError("fixture does not execute CLI"),
            ) as download:
                with self.assertRaises(OSError):
                    host.download_asset(1, Path(directory) / "manifest.json")
                environment = download.call_args.kwargs["env"]
                self.assertEqual(environment["GH_HOST"], "github.com")
                self.assertNotIn("GH_TOKEN", environment)

    def test_atomic_record_and_host_lock_prevent_partial_or_simultaneous_work(self):
        with tempfile.TemporaryDirectory(prefix="psst-update-unit-") as directory:
            root = Path(directory)
            update.atomic_json(root / "state.json", {"first": True})
            update.atomic_json(root / "state.json", {"second": True})
            self.assertEqual(update.load_json(root / "state.json"), {"second": True})
            self.assertEqual((root / "state.json").stat().st_mode & 0o777, 0o600)
            self.assertEqual(list(root.glob("*.new-*")), [])
            with patch.object(
                update.os,
                "fstat",
                return_value=SimpleNamespace(st_uid=0, st_mode=0o100600),
            ):
                with update.deployment_lock(root):
                    with self.assertRaisesRegex(
                        update.UpdateError, "another deployment"
                    ):
                        with update.deployment_lock(root):
                            self.fail("a second lock entered")
                with update.deployment_lock(root):
                    pass

    def test_isolation_removes_every_proxy_public_listener_and_restart(self):
        value = compose()
        value["services"]["external-proxy"] = copy.deepcopy(value["services"]["caddy"])
        private = update.isolated(value)
        for service in private["services"].values():
            self.assertEqual(service["restart"], "no")
            for port in service.get("ports", []):
                self.assertEqual(port["host_ip"], "127.0.0.1")
        self.assertEqual(value["services"]["caddy"]["ports"][0]["host_ip"], "0.0.0.0")
        value["services"]["backend"]["ports"] = [{"published": "8080", "target": 8080}]
        with self.assertRaises(update.UpdateError):
            update.isolated(value)

    def test_checkpoint_archive_reads_payload_and_rejects_links_traversal_and_truncation(
        self,
    ):
        with tempfile.TemporaryDirectory(prefix="psst-update-unit-") as directory:
            path = Path(directory) / "checkpoint.tar"
            for name, kind in (
                ("./payload", tarfile.REGTYPE),
                ("../escape", tarfile.REGTYPE),
                ("./link", tarfile.SYMTYPE),
            ):
                with tarfile.open(path, "w", format=tarfile.USTAR_FORMAT) as archive:
                    member = tarfile.TarInfo(name)
                    member.mode = 0o600
                    member.type = kind
                    member.size = 5 if kind == tarfile.REGTYPE else 0
                    archive.addfile(
                        member, io.BytesIO(b"bytes") if member.size else None
                    )
                if name == "./payload":
                    update.verify_archive(path)
                else:
                    with self.assertRaises(update.UpdateError):
                        update.verify_archive(path)
            with tarfile.open(path, "w") as archive:
                member = tarfile.TarInfo("database")
                member.size = 5000
                archive.addfile(member, io.BytesIO(b"x" * 5000))
            path.write_bytes(path.read_bytes()[:1000])
            with self.assertRaises((tarfile.ReadError, update.UpdateError)):
                update.verify_archive(path)

    def test_checkpoint_preserves_caddy_sticky_and_group_directories_but_rejects_privileged_files(
        self,
    ):
        with tempfile.TemporaryDirectory(prefix="psst-update-unit-") as directory:
            path = Path(directory) / "checkpoint.tar"
            for kind, mode, allowed in (
                (tarfile.DIRTYPE, 0o1777, True),
                (tarfile.DIRTYPE, 0o2770, True),
                (tarfile.DIRTYPE, 0o4770, False),
                (tarfile.REGTYPE, 0o4600, False),
                (tarfile.REGTYPE, 0o2600, False),
                (tarfile.REGTYPE, 0o1600, False),
            ):
                with self.subTest(kind=kind, mode=oct(mode)):
                    with tarfile.open(path, "w") as archive:
                        member = tarfile.TarInfo("state")
                        member.type, member.mode = kind, mode
                        archive.addfile(member)
                    if allowed:
                        update.verify_archive(path)
                    else:
                        with self.assertRaises(update.UpdateError):
                            update.verify_archive(path)

    def test_active_installation_reuses_declared_protected_operator_bind_only(self):
        with tempfile.TemporaryDirectory(prefix="psst-update-unit-") as directory:
            root = Path(directory)
            state = root / "state"
            state.mkdir()
            host = update.Host(config(root / "installation"))
            approved = host.root / "Caddyfile"
            saved = compose()
            mount = {
                "type": "bind",
                "source": str(approved),
                "target": "/etc/caddy/Caddyfile",
                "read_only": True,
            }
            saved["services"]["caddy"]["volumes"].append(mount)
            active = state / "active.json"
            active.write_text(json.dumps({"compose": saved}))
            with patch.object(update, "STATE_PATH", state), patch.object(
                update, "protected"
            ) as protect, patch.object(
                host, "protected_inputs", return_value=[approved]
            ):
                self.assertEqual(host.current(), saved)
                protect.assert_any_call(approved, private=False)
                self.assertEqual(host.bound_inputs, [])
                mount["source"] = str(host.root / "undeclared.caddy")
                active.write_text(json.dumps({"compose": saved}))
                with self.assertRaisesRegex(update.UpdateError, "escaped"):
                    host.current()

    def test_protocol_capability_is_not_operator_policy_and_cannot_hide_policy_drift(
        self,
    ):
        original = {"max_file_size": 100}
        announced = original | {"history_sync_version": 1}
        self.assertEqual(
            update.persisted_public_settings(original),
            update.persisted_public_settings(announced),
        )
        self.assertNotEqual(
            update.persisted_public_settings(original),
            update.persisted_public_settings(announced | {"max_file_size": 101}),
        )
        for invalid in (True, None, "1", -1):
            with self.subTest(invalid=invalid), self.assertRaises(update.UpdateError):
                update.persisted_public_settings(
                    original | {"history_sync_version": invalid}
                )

    def test_restore_gate_allows_only_positive_budget_reductions_and_no_unrelated_changes(
        self,
    ):
        baseline = {
            "max_file_size": 100,
            "traffic_policy": {
                "server_budget_bytes": 1000,
                "default_account_budget_bytes": 500,
                "enforcement_enabled": True,
            },
        }
        observed = copy.deepcopy(baseline) | {"public_transfers_paused": True}
        observed["traffic_policy"]["server_budget_bytes"] = 900
        observed["traffic_policy"]["default_account_budget_bytes"] = 450
        self.assertEqual(
            update.reconciled_restore_settings(baseline, observed),
            {
                key: value
                for key, value in observed.items()
                if key != "public_transfers_paused"
            },
        )
        for field, value in (
            ("server_budget_bytes", 1001),
            ("server_budget_bytes", 400),
            ("default_account_budget_bytes", 0),
            ("default_account_budget_bytes", True),
            ("enforcement_enabled", False),
        ):
            changed = copy.deepcopy(observed)
            changed["traffic_policy"][field] = value
            with self.subTest(field=field, value=value), self.assertRaises(
                update.UpdateError
            ):
                update.reconciled_restore_settings(baseline, changed)
        for field, value in (
            ("max_file_size", 101),
            ("public_transfers_paused", False),
        ):
            with self.subTest(field=field), self.assertRaises(update.UpdateError):
                update.reconciled_restore_settings(baseline, observed | {field: value})

    def test_attestation_policy_binds_exact_certificate_signer_commit_source_tag_and_runner(
        self,
    ):
        host = update.Host(config(Path("/opt/psst.zip")))
        calls = []
        host.run = lambda *args, **kwargs: calls.append((args, kwargs)) or b""
        value = manifest()
        subjects = [
            "/private/release-manifest.json",
            "/private/deployment-bundle.tar.gz",
            "oci://" + value["images"]["backend"]["index"],
            "oci://" + value["images"]["web"]["index"],
        ]
        for subject in subjects:
            host.verify_identity(subject, value)
            args, kwargs = calls[-1]
            self.assertEqual(args[:4], ("gh", "attestation", "verify", subject))
            self.assertIn("--deny-self-hosted-runners", args)
            expected_flags = {
                "--hostname": "github.com",
                "--repo": "endorses/psst.zip",
                "--cert-identity": "https://github.com/endorses/psst.zip/.github/workflows/release.yml@refs/tags/v1.2.3",
                "--signer-digest": "a" * 40,
                "--source-digest": "a" * 40,
                "--source-ref": "refs/tags/v1.2.3",
                "--cert-oidc-issuer": "https://token.actions.githubusercontent.com",
                "--predicate-type": "https://slsa.dev/provenance/v1",
            }
            for flag, expected in expected_flags.items():
                self.assertEqual(args.count(flag), 1)
                self.assertEqual(args[args.index(flag) + 1], expected)
            # An extra weak/mixed certificate selector or a test trust override
            # must fail this policy regression, not just duplicate its values.
            self.assertEqual(
                {arg for arg in args if arg.startswith("--")},
                set(expected_flags) | {"--deny-self-hosted-runners"},
            )
            self.assertEqual(kwargs, {"gh": True})

    def test_attestation_binding_rejects_weak_workflow_or_malformed_ref_before_cli(
        self,
    ):
        for workflow in (
            "other/psst.zip/.github/workflows/release.yml",
            "endorses/psst.zip/.github/workflows/other.yml",
            "endorses/psst.zip/.github/workflows/release.yml@refs/heads/main",
            "https://github.com/endorses/psst.zip/.github/workflows/release.yml",
        ):
            settings = config(Path("/opt/psst.zip")) | {"signer_workflow": workflow}
            with self.subTest(workflow=workflow), self.assertRaises(update.UpdateError):
                update.validate_config(settings)
            host = update.Host(settings)
            with patch.object(host, "run") as runner, self.assertRaises(
                update.UpdateError
            ):
                host.verify_identity("/private/manifest.json", manifest())
            runner.assert_not_called()
        for field, value in (
            ("version", "v1.2.3@refs/heads/main"),
            ("version", "v1.2.3\n"),
            ("commit", "a" * 39),
            ("commit", "refs/tags/v1.2.3"),
        ):
            selected = manifest()
            if field == "commit":
                selected["source"][field] = value
            else:
                selected[field] = value
            host = update.Host(config(Path("/opt/psst.zip")))
            with self.subTest(field=field, value=value), patch.object(
                host, "run"
            ) as runner, self.assertRaises(update._release.InvalidRelease):
                host.verify_identity("/private/manifest.json", selected)
            runner.assert_not_called()

    @unittest.skipUnless(
        shutil.which("gh"), "Install GitHub CLI for argument compatibility"
    )
    def test_actual_cli_accepts_exact_policy_before_offline_missing_trust_failure(self):
        # Exercise the production-generated arguments with the actual CLI parser.
        # Missing test-local trust fails before network/attestation access; these
        # added test flags are never accepted by the production helper.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            names = ("release-manifest.json", "deployment-bundle.tar.gz")
            for name in names:
                (root / name).write_bytes(b"disposable policy parser fixture")
            subjects = [str(root / name) for name in names] + [
                "oci://" + manifest()["images"]["backend"]["index"]
            ]
            for subject in subjects:
                host = update.Host(config(Path("/opt/psst.zip")))
                calls = []
                host.run = lambda *args, **kwargs: calls.append(args) or b""
                host.verify_identity(str(subject), manifest())
                args = [shutil.which("gh"), *calls[0][1:]]
                args += ["--bundle", str(root / "missing-attestation.json")]
                args += [
                    "--custom-trusted-root",
                    str(root / "missing-trusted-root.jsonl"),
                ]
                with self.subTest(subject=subject):
                    result = subprocess.run(
                        args,
                        capture_output=True,
                        timeout=5,
                        env={
                            "PATH": os.defpath,
                            "HOME": str(root),
                            "GH_CONFIG_DIR": str(root / "gh-config"),
                            "GH_PROMPT_DISABLED": "1",
                            "GH_TOKEN": "fixture-token",
                        },
                    )
                    self.assertNotEqual(result.returncode, 0)
                    diagnostic = result.stderr.decode()
                    self.assertIn("missing-trusted-root.jsonl", diagnostic)
                    self.assertNotIn("cannot be used together", diagnostic)
                    self.assertNotIn("mutually exclusive", diagnostic)
                    self.assertNotIn("unknown flag", diagnostic)

    def test_command_failure_withholds_secret_output(self):
        host = update.Host(config(Path("/opt/psst.zip")))
        failed = subprocess.CompletedProcess(
            ["tool"], 1, stdout=b"private fixture secret", stderr=b"operator password"
        )
        with patch.object(
            update.subprocess, "run", return_value=failed
        ), self.assertRaises(update.UpdateError) as caught:
            host.run("tool", "subcommand")
        self.assertNotIn("private fixture secret", str(caught.exception))
        self.assertNotIn("operator password", str(caught.exception))

    def test_private_paths_reject_symlink_and_world_writable_ancestors(self):
        with tempfile.TemporaryDirectory(prefix="psst-update-unit-") as directory:
            path = Path(directory) / "config"
            path.write_text("{}")
            os.chmod(path, 0o600)
            with self.assertRaises(update.UpdateError):
                update.protected(path)
            link = Path(directory) / "link"
            link.symlink_to(path)
            with self.assertRaises(update.UpdateError):
                update.protected(link)


class ConcreteHostBoundaries(unittest.TestCase):
    @contextmanager
    def fixture(self):
        with tempfile.TemporaryDirectory(prefix="psst-update-host-unit-") as directory:
            root = Path(directory)
            policy = config(root)
            policy["checkpoint_hook"] = "/usr/local/libexec/protected-backup"
            host = update.Host(policy)
            value = compose()
            for name, service in value["services"].items():
                service.update(
                    read_only=True,
                    cap_drop=["ALL"],
                    security_opt=["no-new-privileges:true"],
                    pids_limit=256 if name == "backend" else 128,
                )
                service["environment"] = (
                    {
                        "DB_PATH": "/app/data/existing.db",
                        "STORAGE_PATH": "/app/data/files",
                        "ADMIN_USERNAME": "",
                        "ADMIN_PASSWORD": "",
                        "AUTH_ALLOW_INSECURE_HTTP": "false",
                        "PUBLIC_URL": "https://transfer.example.com",
                    }
                    if name == "backend"
                    else {"PSST_DOMAIN": "transfer.example.com"}
                )
            objects, volumes = {}, {}
            for name, service in value["services"].items():
                mounts = []
                for target, physical in host.bindings(value, name).items():
                    mountpoint = root / physical
                    mountpoint.mkdir()
                    volumes[physical] = {
                        "Name": physical,
                        "Driver": "local",
                        "Options": None,
                        "Labels": {"com.docker.compose.project": "psst-zip"},
                        "Mountpoint": str(mountpoint),
                    }
                    mounts.append(
                        {
                            "Type": "volume",
                            "Name": physical,
                            "Destination": target,
                            "RW": True,
                        }
                    )
                objects[name] = {
                    "Id": name,
                    "Image": service["image"],
                    "State": {"Running": True, "Restarting": False},
                    "Config": {
                        "Labels": {"com.docker.compose.service": name},
                        "Env": [
                            f"{key}={data}"
                            for key, data in service["environment"].items()
                        ],
                    },
                    "Mounts": mounts,
                    "HostConfig": {
                        "PortBindings": (
                            {"8443/tcp": [{"HostIp": "0.0.0.0", "HostPort": "443"}]}
                            if name == "caddy"
                            else {}
                        )
                    },
                }
            database = root / "old_backend/existing.db"
            database.write_bytes(b"existing database fixture")
            os.chmod(database, 0o600)
            facts = {
                "docker": "29.8.1",
                "compose": "5.5.1",
                "architecture": "x86_64",
                "setup_required": False,
                "disk_free": 100 * 1024**3,
            }
            calls = []

            def run(*args, **kwargs):
                calls.append(args)
                if args[:2] == ("docker", "version"):
                    return facts["docker"].encode()
                if args[:3] == ("docker", "compose", "version"):
                    return facts["compose"].encode()
                if args[:2] == ("docker", "info"):
                    return json.dumps(
                        {
                            "OSType": "linux",
                            "Architecture": facts["architecture"],
                            "DockerRootDir": str(root),
                        }
                    ).encode()
                if args[:2] == ("docker", "ps"):
                    names = (
                        ["backend", "caddy"] if "--filter" in args else list(objects)
                    )
                    return "\n".join(names).encode()
                if args[:3] == ("docker", "volume", "inspect"):
                    return json.dumps([volumes[args[3]]]).encode()
                if args[:3] == ("docker", "image", "inspect"):
                    return b'[{"Size":12345}]'
                if args[:2] == ("docker", "inspect"):
                    return json.dumps(
                        [objects[identity] for identity in args[2:]]
                    ).encode()
                if args[:2] == ("docker", "run") and args[-1] == "incident-status":
                    return b'{"public_transfers_paused":false}'
                raise AssertionError("unexpected preflight command " + repr(args))

            host.run = run
            host.protected_inputs = lambda: []
            host.https = lambda comp, path, **kwargs: (
                {"setup_required": facts["setup_required"]}
                if path.endswith("auth/status")
                else {"max_file_size": 100, "public_transfers_paused": False}
            )
            candidate = copy.deepcopy(value)
            selected = manifest()
            for name in candidate["services"]:
                candidate["services"][name]["image"] = selected["images"][
                    "backend" if name == "backend" else "web"
                ]["index"]
            with patch.object(
                update.shutil,
                "disk_usage",
                side_effect=lambda path: SimpleNamespace(free=facts["disk_free"]),
            ):
                yield host, root, value, candidate, selected, facts, objects, volumes, calls

    def test_preflight_checks_real_storage_account_ports_and_capacity_without_mutation(
        self,
    ):
        with self.fixture() as (
            host,
            root,
            current,
            candidate,
            selected,
            facts,
            objects,
            volumes,
            calls,
        ):
            adoption = host.preflight(current, candidate, selected, root / "checkpoint")
            self.assertEqual(
                set(adoption["volumes"]),
                {"old_backend", "old_caddy_data", "old_caddy_config"},
            )
            self.assertEqual(adoption["baseline_config"], {"max_file_size": 100})
            self.assertEqual(adoption["platform"], "linux/amd64")
            self.assertFalse(
                any(command[-1] in {"pause", "resume"} for command in calls)
            )
            self.assertFalse(
                any(command[:3] == ("docker", "volume", "create") for command in calls)
            )

    def test_only_explicit_local_policy_permits_preflight_without_export_hook(self):
        with self.fixture() as data:
            host, root, current, candidate, selected, *_ = data
            host.config["checkpoint_hook"] = None
            with self.assertRaisesRegex(update.UpdateError, "off-host"):
                host.preflight(current, candidate, selected, root / "checkpoint")
            host.config["checkpoint_protection"] = "local-only"
            adoption = host.preflight(current, candidate, selected, root / "checkpoint")
            self.assertEqual(len(adoption["volumes"]), 3)

    def test_source_build_adoption_uses_the_running_binary_without_rendered_image_field(
        self,
    ):
        with self.fixture() as (
            host,
            root,
            current,
            candidate,
            selected,
            facts,
            objects,
            volumes,
            calls,
        ):
            for service in current["services"].values():
                service.pop("image")
                service["build"] = {"context": "protected-source"}
            adopted = host.preflight(current, candidate, selected, root / "checkpoint")
            self.assertEqual(adopted["images"]["backend"], "old-backend")
            status_calls = [
                command for command in calls if command[-1] == "incident-status"
            ]
            self.assertEqual(status_calls[0][-2], "old-backend")

    def test_mapping_ownership_config_port_account_and_disk_drift_fail_actual_preflight(
        self,
    ):
        mutations = {
            "user override": lambda h, r, c, n, m, f, o, v, calls: n["services"][
                "backend"
            ].update(user="12345:12345"),
            "wrong image": lambda h, r, c, n, m, f, o, v, calls: n["services"][
                "backend"
            ].update(image="local:untrusted"),
            "host privileges": lambda h, r, c, n, m, f, o, v, calls: n["services"][
                "backend"
            ].update(privileged=True),
            "network change": lambda h, r, c, n, m, f, o, v, calls: n.update(
                networks={"other": {}}
            ),
            "missing volume": lambda h, r, c, n, m, f, o, v, calls: o["backend"][
                "Mounts"
            ][0].update(Name="wrong-volume"),
            "wrong owner": lambda h, r, c, n, m, f, o, v, calls: v["old_backend"][
                "Labels"
            ].update({"com.docker.compose.project": "other-project"}),
            "extra writer": lambda h, r, c, n, m, f, o, v, calls: o.update(
                other={"Id": "other", "Mounts": [{"Name": "old_backend", "RW": True}]}
            ),
            "changed environment": lambda h, r, c, n, m, f, o, v, calls: n["services"][
                "backend"
            ]["environment"].update(PUBLIC_URL="https://different.example.com"),
            "bootstrap password": lambda h, r, c, n, m, f, o, v, calls: c["services"][
                "backend"
            ]["environment"].update(ADMIN_PASSWORD="must be removed"),
            "missing resource limit": lambda h, r, c, n, m, f, o, v, calls: n[
                "services"
            ]["backend"].pop("pids_limit"),
            "published backend": lambda h, r, c, n, m, f, o, v, calls: o["backend"][
                "HostConfig"
            ].update(
                PortBindings={"8080/tcp": [{"HostIp": "0.0.0.0", "HostPort": "8080"}]}
            ),
            "different public port": lambda h, r, c, n, m, f, o, v, calls: o["caddy"][
                "HostConfig"
            ]["PortBindings"]["8443/tcp"][0].update(HostPort="444"),
            "unsupported daemon": lambda h, r, c, n, m, f, o, v, calls: f.update(
                architecture="riscv64"
            ),
            "old Compose": lambda h, r, c, n, m, f, o, v, calls: f.update(
                compose="2.23.0"
            ),
            "insufficient disk": lambda h, r, c, n, m, f, o, v, calls: f.update(
                disk_free=1024
            ),
            "lost initialization": lambda h, r, c, n, m, f, o, v, calls: f.update(
                setup_required=True
            ),
            "missing offhost": lambda h, r, c, n, m, f, o, v, calls: h.config.update(
                checkpoint_hook=None
            ),
        }
        for label, change in mutations.items():
            with self.subTest(label=label), self.fixture() as data:
                change(*data)
                host, root, current, candidate, selected, *_ = data
                with self.assertRaises(update.UpdateError):
                    host.preflight(current, candidate, selected, root / "checkpoint")

    def test_runtime_checks_use_actual_hardening_and_loopback_bindings(self):
        with self.fixture() as (
            host,
            root,
            current,
            candidate,
            selected,
            facts,
            objects,
            volumes,
            calls,
        ):
            private = update.isolated(candidate)
            private["networks"] = {"edge": {"name": "psst-zip_edge"}}
            private["services"]["caddy"]["networks"] = {"edge": None}
            for name, service in private["services"].items():
                container = objects[name]
                container["Config"]["Image"] = service["image"]
                container["HostConfig"].update(
                    ReadonlyRootfs=True,
                    CapDrop=["ALL"],
                    CapAdd=[],
                    Privileged=False,
                    SecurityOpt=["no-new-privileges"],
                )
                if name == "caddy":
                    container["HostConfig"]["PortBindings"]["8443/tcp"][0][
                        "HostIp"
                    ] = "127.0.0.1"
            host.runtime_checks(private)
            objects["caddy"]["HostConfig"]["PortBindings"]["8443/tcp"][0][
                "HostIp"
            ] = "0.0.0.0"
            with self.assertRaisesRegex(update.UpdateError, "listener"):
                host.runtime_checks(private)
            objects["caddy"]["HostConfig"]["PortBindings"]["8443/tcp"][0][
                "HostIp"
            ] = "127.0.0.1"
            objects["backend"]["HostConfig"]["Privileged"] = True
            with self.assertRaisesRegex(update.UpdateError, "hardening"):
                host.runtime_checks(private)

    def test_sqlite_integrity_is_readonly_and_rejects_foreign_key_corruption(self):
        import sqlite3

        with tempfile.TemporaryDirectory(
            prefix="psst-update-sqlite-unit-"
        ) as directory:
            path = Path(directory) / "database.db"
            with closing(sqlite3.connect(path)) as db:
                db.execute("CREATE TABLE parent(id INTEGER PRIMARY KEY)")
                db.execute("CREATE TABLE child(parent INTEGER REFERENCES parent(id))")
                db.commit()
            before = update.fingerprint(path)
            update.Host.sqlite_integrity(path)
            self.assertEqual(update.fingerprint(path), before)
            with closing(sqlite3.connect(path)) as db:
                db.execute("INSERT INTO child VALUES (1)")
                db.commit()
            with self.assertRaisesRegex(update.UpdateError, "foreign-key"):
                update.Host.sqlite_integrity(path)

    def test_actual_auth_scan_gate_rejects_uninitialized_pending_or_error_states(self):
        host = update.Host(config(Path("/opt/psst.zip")))
        baseline = {
            "state": "checked",
            "scan_pending": False,
            "last_scan_completed_at": datetime.now(timezone.utc).isoformat(),
        }
        facts = {"role": "admin", "status": baseline}
        host.https = lambda comp, path, **kwargs: (
            {"user": {"role": facts["role"]}}
            if path.endswith("auth/me")
            else facts["status"]
        )
        host.authenticated_checks({}, "psst_session=fixture-cookie")
        for change in (
            {"state": "pending"},
            {"scan_pending": True},
            {"last_scan_completed_at": None},
            {"scan_error_code": "read-failed"},
            {"failed_count": 1},
            {"saturated": True},
            {"pending_candidates": 1},
        ):
            facts["status"] = {**baseline, **change}
            with self.subTest(change=change), self.assertRaises(update.UpdateError):
                host.authenticated_checks({}, "psst_session=fixture-cookie")
        facts.update(role="user", status=baseline)
        with self.assertRaises(update.UpdateError):
            host.authenticated_checks({}, "psst_session=fixture-cookie")

    def test_export_receipt_must_bind_encryption_and_restore_evidence_to_checkpoint(
        self,
    ):
        with tempfile.TemporaryDirectory(
            prefix="psst-update-receipt-unit-"
        ) as directory:
            root = Path(directory)
            update.atomic_json(root / "checkpoint.json", {"snapshot": "fixture"})
            host = update.Host(config(Path("/opt/psst.zip")))
            host.config["checkpoint_hook"] = "/protected/export-hook"
            report = {
                "checkpoint_sha256": update.fingerprint(root / "checkpoint.json"),
                "encrypted_off_host_receipt": "fixture export receipt hash checked",
                "restore_exercise": "fixture isolated decrypted copy verified",
                "verified_at": datetime.now(timezone.utc).isoformat(),
            }
            host.run = lambda *args, **kwargs: json.dumps(report).encode()
            with patch.object(update, "protected"):
                host.export_checkpoint(root)
                self.assertTrue((root / "off-host-receipt.json").is_file())
                for field, wrong in (
                    ("checkpoint_sha256", "a" * 64),
                    ("encrypted_off_host_receipt", True),
                    ("restore_exercise", ""),
                    ("verified_at", "2020-01-01T00:00:00Z"),
                ):
                    original = report[field]
                    report[field] = wrong
                    with self.subTest(field=field), self.assertRaises(
                        update.UpdateError
                    ):
                        host.export_checkpoint(root)
                    report[field] = original

    def test_source_bundle_is_not_extracted_until_all_required_provenance_passes(self):
        with tempfile.TemporaryDirectory(
            prefix="psst-update-provenance-unit-"
        ) as directory:
            target = Path(directory)
            host = update.Host(config(Path("/opt/psst.zip")))
            selected = manifest()
            selected["payload_profile"] = "deployment-ready"
            bundle = io.BytesIO()
            with tarfile.open(
                fileobj=bundle, mode="w:gz", format=tarfile.USTAR_FORMAT
            ) as archive:
                inputs = {
                    name: b"fixture content"
                    for name in release_artifacts.REQUIRED_FILES
                }
                inputs["deploy/update.py"] = (
                    b"raise RuntimeError('must never execute bundle updater')"
                )
                inputs[release_artifacts.METADATA] = release_artifacts.json_bytes(
                    {
                        "schema_version": 1,
                        "version": "v1.2.3",
                        "source_commit": "a" * 40,
                        "payload_profile": "deployment-ready",
                    }
                )
                for name, content in inputs.items():
                    member = tarfile.TarInfo(name)
                    member.mode = 0o644
                    member.size = len(content)
                    archive.addfile(member, io.BytesIO(content))
            data = bundle.getvalue()
            selected["bundle"]["sha256"] = hashlib.sha256(data).hexdigest()
            assets = [
                {"id": 1, "name": "release-manifest.json", "size": 1000},
                {"id": 2, "name": selected["bundle"]["name"], "size": len(data)},
            ]
            host.download_asset = lambda identity, path: path.write_bytes(
                release_artifacts.json_bytes(selected) if identity == 1 else data
            )
            calls = []

            def run(*args, **kwargs):
                calls.append(args)
                if args[:2] == ("gh", "api"):
                    return json.dumps(
                        {
                            "draft": False,
                            "prerelease": False,
                            "tag_name": "v1.2.3",
                            "assets": assets,
                        }
                    ).encode()
                if args[:3] == ("gh", "attestation", "verify"):
                    raise update.UpdateError("invalid cryptographic provenance")
                raise AssertionError("unexpected unauthenticated command")

            host.run = run
            with self.assertRaisesRegex(update.UpdateError, "provenance"):
                host.download("v1.2.3", target)
            self.assertFalse((target / "bundle").exists())
            self.assertFalse(any(command[:2] == ("docker", "run") for command in calls))
            assets[0]["size"] = release_artifacts.MAX_BUNDLE_BYTES + 1
            calls.clear()
            with self.assertRaisesRegex(update.UpdateError, "oversized"):
                host.download("v1.2.3", target)
            self.assertFalse(
                any(command[:3] == ("gh", "attestation", "verify") for command in calls)
            )


class CheckpointProtection(unittest.TestCase):
    @contextmanager
    def fixture(self, mode="local-only"):
        import sqlite3

        with tempfile.TemporaryDirectory(prefix="psst-local-checkpoint-") as directory:
            root = Path(directory)
            policy = config(root)
            if mode is not None:
                policy["checkpoint_protection"] = mode
            if mode != "local-only":
                policy["checkpoint_hook"] = "/protected/checkpoint-export"
            host = update.Host(policy)
            database = root / "existing.db"
            with closing(sqlite3.connect(database)) as connection:
                connection.execute("CREATE TABLE fixture(id INTEGER PRIMARY KEY)")
                connection.commit()
            inputs = [root / ".env", root / "compose.yml"]
            for path in inputs:
                path.write_text("protected fixture configuration")
            host.protected_inputs = lambda: inputs
            calls = []

            def run(*args, output=None, **kwargs):
                calls.append(args)
                if args[0] == "/protected/checkpoint-export":
                    return json.dumps(
                        {
                            "checkpoint_sha256": update.fingerprint(
                                Path(args[-1]) / "checkpoint.json"
                            ),
                            "encrypted_off_host_receipt": "verified encrypted fixture",
                            "restore_exercise": "verified isolated fixture",
                            "verified_at": datetime.now(timezone.utc).isoformat(),
                        }
                    ).encode()
                if output:
                    payloads = {"fixture": b"saved image or state"}
                    if (
                        args[:2] == ("docker", "run")
                        and "type=volume,src=old_backend,dst=/snapshot,readonly" in args
                    ):
                        payloads = {"existing.db": database.read_bytes()}
                    with tarfile.open(
                        output, "w", format=tarfile.USTAR_FORMAT
                    ) as archive:
                        for name, contents in payloads.items():
                            item = tarfile.TarInfo(name)
                            item.size = len(contents)
                            archive.addfile(item, io.BytesIO(contents))
                elif args[:3] == ("docker", "volume", "create"):
                    (root / args[-1]).mkdir()
                elif args[:3] == ("docker", "volume", "inspect"):
                    return json.dumps([{"Mountpoint": str(root / args[-1])}]).encode()
                elif args[0] == "tar":
                    mount = Path(args[args.index("-C") + 1])
                    with tarfile.open(args[-1], "r:") as archive:
                        for item in archive:
                            (mount / item.name).write_bytes(
                                archive.extractfile(item).read()
                            )
                return b""

            host.run = run
            host.incident = lambda *args: calls.append(("incident", "pause"))
            images = {"backend": "sha256:" + "a" * 64, "caddy": "sha256:" + "b" * 64}
            adopted = {
                "images": images,
                "volumes": {
                    name: {"image": images["backend"]}
                    for name in policy["volume_names"].values()
                },
                "database": str(database),
            }
            current = compose()
            for name, identity in images.items():
                current["services"][name]["image"] = identity
            identity = "20261009T000000Z-000000000000"
            backups = root / "backups"
            backups.mkdir()
            checkpoint = backups / identity
            transaction = {"id": identity, "prior_pause": False}
            with patch.object(update, "protected"):
                host.backup(current, adopted, checkpoint, transaction)
                yield host, root, checkpoint, transaction, adopted, current, calls

    def test_local_backup_is_complete_verified_and_records_actual_policy_without_receipt(
        self,
    ):
        with self.fixture() as (
            host,
            root,
            checkpoint,
            transaction,
            adopted,
            current,
            calls,
        ):
            backup = host.verify_backup(checkpoint)
            self.assertEqual(backup["checkpoint_protection"], "local-only")
            self.assertEqual(transaction["checkpoint_protection"], "local-only")
            self.assertEqual(backup["images"], adopted["images"])
            self.assertEqual(
                sum(row["kind"] == "volume" for row in backup["records"].values()), 3
            )
            self.assertEqual(
                sum(row["kind"] == "image" for row in backup["records"].values()), 2
            )
            self.assertEqual(
                sum(
                    row["kind"] == "configuration" for row in backup["records"].values()
                ),
                3,
            )
            self.assertFalse((checkpoint / "off-host-receipt.json").exists())
            # The real checksum verifier still rejects corruption in local-only mode.
            (checkpoint / "image-0.tar").write_bytes(b"corrupt")
            with self.assertRaisesRegex(update.UpdateError, "checksum"):
                host.verify_backup(checkpoint)

    def test_local_checkpoint_restores_under_later_offhost_host_policy(self):
        with self.fixture() as (host, root, checkpoint, transaction, *_):
            host.config["checkpoint_protection"] = "off-host"
            restored = host.restore(checkpoint, root, transaction)
            self.assertEqual(len(transaction["restored_volumes"]), 3)
            self.assertTrue(
                all(
                    value.startswith("psst-restore-")
                    for value in transaction["restored_volumes"].values()
                )
            )
            self.assertEqual(
                restored["services"]["backend"]["image"], "sha256:" + "a" * 64
            )
            self.assertEqual(
                update.load_json(checkpoint / "checkpoint.json")[
                    "checkpoint_protection"
                ],
                "local-only",
            )

    def test_default_backup_still_exports_and_requires_real_bound_receipt(self):
        with self.fixture(mode=None) as (host, _, checkpoint, transaction, *_):
            self.assertEqual(transaction["checkpoint_protection"], "off-host")
            self.assertEqual(
                host.verify_backup(checkpoint)["checkpoint_protection"], "off-host"
            )
            self.assertTrue((checkpoint / "off-host-receipt.json").is_file())
            (checkpoint / "off-host-receipt.json").unlink()
            with self.assertRaises(
                (update.UpdateError, update._release.InvalidRelease)
            ):
                host.verify_backup(checkpoint)

    def test_local_checkpoint_retention_preserves_creation_policy(self):
        with self.fixture() as (
            host,
            root,
            checkpoint,
            transaction,
            adopted,
            current,
            _,
        ):
            state = root / "state"
            (state / "transactions").mkdir(parents=True)
            updater = update.Updater(host.config, host, state, checkpoint.parent)
            for number in range(4):
                identity = f"20261009T00000{number}Z-{number:012x}"
                path = checkpoint.parent / identity
                if path != checkpoint:
                    shutil.copytree(checkpoint, path)
                    backup = update.load_json(path / "checkpoint.json")
                    backup["transaction"] = identity
                    update.atomic_json(path / "checkpoint.json", backup)
                (state / "transactions" / identity).mkdir()
                record = {
                    "schema_version": 1,
                    "id": identity,
                    "phase": "completed",
                    "checkpoint": str(path),
                    "checkpoint_protection": "local-only",
                    "adopted": adopted,
                }
                update.atomic_json(
                    state / "transactions" / identity / "transaction.json", record
                )
            update.atomic_json(updater.record, record)
            update.atomic_json(
                state / "active.json", {"transaction": identity, "compose": current}
            )
            inventory = {
                value: {"tags": set(), "digests": set()}
                for value in adopted["images"].values()
            }
            host.retention_inventory = lambda: (inventory, set())
            host.config["checkpoint_protection"] = "off-host"
            updater.retention()
            self.assertEqual(len(list(checkpoint.parent.iterdir())), 2)
            self.assertEqual(updater.read()["cleanup"]["status"], "completed")
            self.assertEqual(updater.read()["checkpoint_protection"], "local-only")

    def test_legacy_default_and_explicit_strict_verification_never_downgrade(self):
        with self.fixture() as (host, _, checkpoint, *_):
            with self.assertRaises(
                (update.UpdateError, update._release.InvalidRelease)
            ):
                host.verify_backup(checkpoint, require_off_host=True)
            path = checkpoint / "checkpoint.json"
            backup = update.load_json(path)
            for policy in ("legacy", "off-host"):
                if policy == "legacy":
                    backup.pop("checkpoint_protection", None)
                else:
                    backup["checkpoint_protection"] = policy
                update.atomic_json(path, backup)
                with self.subTest(policy=policy), self.assertRaises(
                    (update.UpdateError, update._release.InvalidRelease)
                ):
                    host.verify_backup(checkpoint)
            receipt = {
                "checkpoint_sha256": update.fingerprint(path),
                "encrypted_off_host_receipt": "verified encrypted fixture",
                "restore_exercise": "verified isolated fixture",
                "verified_at": "2020-01-01T00:00:00Z",
            }
            receipt_path = checkpoint / "off-host-receipt.json"
            update.atomic_json(receipt_path, receipt)
            host.verify_backup(checkpoint)
            for field, wrong in (
                ("checkpoint_sha256", "0" * 64),
                ("encrypted_off_host_receipt", True),
                ("restore_exercise", ""),
                ("verified_at", "invalid"),
            ):
                update.atomic_json(receipt_path, {**receipt, field: wrong})
                with self.subTest(field=field), self.assertRaises(update.UpdateError):
                    host.verify_backup(checkpoint)


if __name__ == "__main__":
    unittest.main()
