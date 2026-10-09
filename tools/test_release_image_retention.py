"""Fast updater image-retention faults; no daemon or deployment access."""

from contextlib import contextmanager
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from test_release_updater import Transactions, config, update


def image_id(number):
    return "sha256:" + f"{number:064x}"


def reference(number):
    return "ghcr.io/endorses/psst-zip-backend@" + image_id(number + 100)


class ImageHost(update.Host):
    def __init__(self, policy):
        super().__init__(policy)
        self.images = {
            image_id(number): {"tags": set(), "digests": {reference(number)}}
            for number in range(5)
        }
        self.images[image_id(0)]["digests"] = set()
        self.images[image_id(99)] = {"tags": {"unrelated:latest"}, "digests": set()}
        self.containers = {}
        self.removed = []
        self.fail_remove = False

    def verify_backup(self, checkpoint):
        return update.load_json(checkpoint / "checkpoint.json")

    def run(self, *args, **kwargs):
        if args == ("docker", "image", "ls", "-aq", "--no-trunc"):
            return "\n".join(self.images).encode()
        if args == ("docker", "ps", "-aq", "--no-trunc"):
            return "\n".join(self.containers).encode()
        if args[:3] == ("docker", "image", "inspect"):
            return json.dumps(
                [
                    {
                        "Id": identity,
                        "RepoTags": sorted(self.images[identity]["tags"]),
                        "RepoDigests": sorted(self.images[identity]["digests"]),
                    }
                    for identity in args[3:]
                ]
            ).encode()
        if args[:3] == ("docker", "container", "inspect"):
            return json.dumps(
                [{"Id": identity, **self.containers[identity]} for identity in args[3:]]
            ).encode()
        assert args[:4] == ("docker", "image", "rm", "--no-prune") and len(args) == 5
        if self.fail_remove:
            raise update.UpdateError("injected image removal failure")
        value = args[4]
        self.removed.append(value)
        if value in self.images:
            assert all(row["Image"] != value for row in self.containers.values())
            del self.images[value]
        else:
            owners = [row for row in self.images.values() if value in row["tags"]]
            assert len(owners) == 1
            owners[0]["tags"].remove(value)
        return b""


class ImageRetention(unittest.TestCase):
    @contextmanager
    def fixture(self):
        with tempfile.TemporaryDirectory(prefix="psst-image-retention-") as temporary:
            root = Path(temporary)
            state, backups = root / "state", root / "backups"
            (state / "transactions").mkdir(parents=True)
            backups.mkdir()
            host = ImageHost(config(root))
            updater = update.Updater(config(root), host, state, backups)
            identities = []
            for number in range(4):
                identity = f"20261009T00000{number}Z-{number:012x}"
                identities.append(identity)
                target = state / "transactions" / identity
                target.mkdir()
                checkpoint = backups / identity
                checkpoint.mkdir()
                adopted = {"backend": image_id(number)}
                record = {
                    "schema_version": 1,
                    "id": identity,
                    "phase": "completed",
                    "checkpoint": str(checkpoint),
                    "adopted": {"images": adopted},
                    "manifest": {
                        "images": {"backend": {"index": reference(number + 1)}}
                    },
                }
                update.atomic_json(target / "transaction.json", record)
                update.atomic_json(
                    checkpoint / "checkpoint.json",
                    {"transaction": identity, "images": adopted},
                )
                host.images[image_id(number)]["tags"].add(
                    "psst-checkpoint-" + identity.lower() + ":0"
                )
            update.atomic_json(updater.record, record)
            update.atomic_json(
                state / "active.json",
                {
                    "transaction": identities[-1],
                    "compose": {"services": {"backend": {"image": reference(4)}}},
                },
            )
            # File protection is separately covered by the updater boundary tests.
            with patch.object(update, "protected"):
                yield updater, host, identities

    def test_superseded_tracked_images_and_tags_removed_keep_two_checkpoints(self):
        with self.fixture() as (updater, host, ids):
            updater.retention()
            self.assertEqual(
                set(host.images), {image_id(2), image_id(3), image_id(4), image_id(99)}
            )
            self.assertEqual(
                {path.name for path in updater.backups.iterdir()}, set(ids[2:])
            )
            self.assertEqual(
                set(host.removed),
                {
                    image_id(0),
                    image_id(1),
                    *(
                        "psst-checkpoint-" + identity.lower() + ":0"
                        for identity in ids[:2]
                    ),
                },
            )
            self.assertEqual(updater.read()["cleanup"]["status"], "completed")
            updater.retention()
            self.assertEqual(len(host.removed), 4)

    def test_running_and_stopped_foreign_containers_protect_images(self):
        with self.fixture() as (updater, host, _):
            host.containers = {
                f"{90:064x}": {"Image": image_id(0), "State": {"Running": True}},
                f"{91:064x}": {"Image": image_id(1), "State": {"Running": False}},
            }
            updater.retention()
            self.assertEqual(host.removed, [])
            self.assertTrue({image_id(0), image_id(1)} <= host.images.keys())

    def test_foreign_tags_and_digests_protect_shared_images(self):
        with self.fixture() as (updater, host, _):
            host.images[image_id(0)]["tags"].add("operator-image:keep")
            host.images[image_id(1)]["digests"].add(
                "example.org/foreign@" + image_id(88)
            )
            updater.retention()
            self.assertEqual(host.removed, [])

    def test_failed_partial_and_restoring_transactions_protect_images(self):
        for phase, restoring in (
            ("failed-safe", False),
            ("failed-closed", False),
            ("preparing", False),
            ("completed", True),
        ):
            with self.subTest(phase=phase, restoring=restoring), self.fixture() as (
                updater,
                host,
                ids,
            ):
                path = updater.state / "transactions" / ids[0] / "transaction.json"
                record = update.load_json(path)
                record.update(phase=phase, restoring=restoring)
                update.atomic_json(path, record)
                updater.retention()
                self.assertEqual(host.removed, [])
                self.assertTrue((updater.backups / ids[0]).is_dir())

    def test_ambiguous_state_fails_before_any_deletion(self):
        with self.fixture() as (updater, host, _):
            (updater.backups / "unknown-checkpoint").mkdir()
            with self.assertRaises(update.UpdateError):
                updater.retention()
            self.assertEqual(host.removed, [])
            self.assertEqual(len(list(updater.backups.iterdir())), 5)
            self.assertEqual(updater.read()["phase"], "completed")
            self.assertEqual(updater.read()["cleanup"]["status"], "incomplete")

    def test_missing_retained_checkpoint_preserves_all_images(self):
        with self.fixture() as (updater, host, ids):
            update.shutil.rmtree(updater.backups / ids[-2])
            with self.assertRaises(update.UpdateError):
                updater.retention()
            self.assertEqual(host.removed, [])
            self.assertTrue((updater.backups / ids[0]).exists())

    def test_expired_checkpoint_mismatch_fails_before_removal(self):
        with self.fixture() as (updater, host, ids):
            checkpoint = updater.backups / ids[0] / "checkpoint.json"
            record = update.load_json(checkpoint)
            record["images"] = {"backend": image_id(99)}
            update.atomic_json(checkpoint, record)
            with self.assertRaises(update.UpdateError):
                updater.retention()
            self.assertEqual(host.removed, [])
            self.assertTrue(checkpoint.is_file())

    def test_image_removal_failure_preserves_activation_and_manual_retry(self):
        with self.fixture() as (updater, host, _):
            active = (updater.state / "active.json").read_bytes()
            host.fail_remove = True
            with self.assertRaises(update.UpdateError):
                updater.retention()
            self.assertEqual(updater.read()["phase"], "completed")
            self.assertEqual(updater.read()["cleanup"]["status"], "incomplete")
            self.assertEqual((updater.state / "active.json").read_bytes(), active)
            host.fail_remove = False
            updater.retention()
            self.assertNotIn(image_id(0), host.images)
            self.assertNotIn(image_id(1), host.images)
            self.assertEqual(updater.read()["cleanup"]["status"], "completed")

    def test_partial_checkpoint_cleanup_can_retry_without_adopting_corrupt_backup(self):
        with self.fixture() as (updater, host, ids):

            def interrupted(path):
                (path / "checkpoint.json").unlink()
                raise OSError("injected deletion interruption")

            with patch.object(update.shutil, "rmtree", interrupted):
                with self.assertRaises(OSError):
                    updater.retention()
            record = update.load_json(
                updater.state / "transactions" / ids[0] / "transaction.json"
            )
            self.assertIs(record["checkpoint_prune_started"], True)
            updater.retention()
            self.assertFalse((updater.backups / ids[0]).exists())
            self.assertNotIn(image_id(0), host.images)

    def test_unsuccessful_updates_do_not_cleanup(self):
        for fault in (None, "health"):
            with Transactions().fixture(fail=fault) as (updater, host, _), patch.object(
                updater, "retention"
            ) as cleanup:
                if fault:
                    with self.assertRaises(update.UpdateError):
                        updater.update("v1.2.3")
                else:
                    self.assertEqual(
                        updater.update("v1.2.3")["phase"], "awaiting-verification"
                    )
                cleanup.assert_not_called()

    def test_automatic_cleanup_failure_does_not_stop_verified_active_deployment(self):
        with Transactions().fixture(hook=True) as (updater, host, _), patch.object(
            updater,
            "prune_tracked_images",
            side_effect=update.UpdateError("cleanup failed"),
        ):
            record = updater.update("v1.2.3")
            self.assertEqual(record["phase"], "completed")
            self.assertEqual(record["cleanup"]["status"], "incomplete")
            self.assertNotIn("stop-candidate", host.calls)
            self.assertTrue((updater.state / "active.json").is_file())

    def test_automatic_cleanup_starts_only_after_completed_activation(self):
        with Transactions().fixture(hook=True) as (updater, host, _):

            def cleanup(current):
                self.assertEqual(current["phase"], "completed")
                self.assertEqual(current["cleanup"]["status"], "incomplete")
                self.assertEqual(
                    update.load_json(updater.state / "active.json")["transaction"],
                    current["id"],
                )

            with patch.object(updater, "prune_tracked_images", cleanup):
                record = updater.update("v1.2.3")
            self.assertEqual(record["cleanup"]["status"], "completed")
            output = io.StringIO()
            with patch("sys.stdout", output):
                self.assertEqual(update.display(record), 0)
            self.assertEqual(
                json.loads(output.getvalue())["cleanup"]["status"], "completed"
            )

    def test_interrupted_automatic_cleanup_does_not_revoke_activation(self):
        with Transactions().fixture(hook=True) as (updater, host, _), patch.object(
            updater, "prune_tracked_images", side_effect=KeyboardInterrupt
        ):
            with self.assertRaises(KeyboardInterrupt):
                updater.update("v1.2.3")
            self.assertEqual(updater.read()["phase"], "completed")
            self.assertEqual(updater.read()["cleanup"]["status"], "incomplete")
            self.assertNotIn("stop-candidate", host.calls)


if __name__ == "__main__":
    unittest.main()
