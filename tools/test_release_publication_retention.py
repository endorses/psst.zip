"""Small offline S3 boundary fixtures; no credentials, network or payload builds."""

import base64
import hashlib
import os
from pathlib import Path
import tempfile
import unittest

from github_release_transport import WorkflowContext
from publication_retention import S3PublicationRetention, PREFIX
from publish_container_release import Binding, GATES, WORKFLOW
from release_artifacts import InvalidRelease, json_bytes


class FakeAWS:
    def __init__(self):
        self.calls, self.objects = [], {}
        self.fail, self.mismatch, self.public = None, False, False
        self.private = None
        self.version = b"aws-cli/2.31.0 Python/3 Linux/x86_64\n"

    def __call__(self, args, *, environment, timeout):
        self.calls.append(args)
        self.private = Path(environment["AWS_CONFIG_FILE"]).parent
        assert "HOME" not in environment
        assert "AWS_ACCESS_KEY_ID" not in environment
        assert "HTTP_PROXY" not in environment and "AWS_PROFILE" not in environment
        assert environment["AWS_MAX_ATTEMPTS"] == "1"
        assert environment["AWS_EC2_METADATA_DISABLED"] == "true"
        assert self.private.stat().st_mode & 0o077 == 0
        assert (self.private / "credentials").stat().st_mode & 0o077 == 0
        assert "fixture-secret" not in str(args)
        if args[1] == "--version":
            return self.version
        operation = args[2]
        if self.fail == operation:
            raise InvalidRelease("fixture-secret signed-url failure")
        if operation == "get-public-access-block":
            return json_bytes(
                {
                    "PublicAccessBlockConfiguration": {
                        "BlockPublicAcls": True,
                        "IgnorePublicAcls": True,
                        "BlockPublicPolicy": True,
                        "RestrictPublicBuckets": True,
                    }
                }
            )
        if operation == "get-bucket-policy-status":
            return json_bytes({"PolicyStatus": {"IsPublic": self.public}})
        key = args[args.index("--key") + 1]
        if operation == "put-object":
            assert args[args.index("--if-none-match") + 1] == "*"
            assert args[args.index("--server-side-encryption") + 1] == "AES256"
            if key in self.objects:
                raise InvalidRelease("PreconditionFailed")
            raw = Path(args[args.index("--body") + 1]).read_bytes()
            checksum = base64.b64encode(hashlib.sha256(raw).digest()).decode()
            assert args[args.index("--checksum-sha256") + 1] == checksum
            self.objects[key] = raw
            return b"{}"
        assert operation == "head-object"
        raw = self.objects[key]
        return json_bytes(
            {
                "ContentLength": len(raw),
                "ServerSideEncryption": "AES256",
                "ChecksumSHA256": (
                    "wrong"
                    if self.mismatch
                    else base64.b64encode(hashlib.sha256(raw).digest()).decode()
                ),
            }
        )


class RetentionChecks(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="psst-retention-fixture-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.binding = Binding(
            "endorses/psst.zip",
            "v0.1.0",
            "a" * 40,
            (("manifest", "file:release-manifest.json@sha256:" + "b" * 64),),
        )
        self.context = WorkflowContext(77, 1)
        self.environment = {
            PREFIX + name: value
            for name, value in {
                "ENDPOINT": "https://s3.example.test",
                "BUCKET": "private-publication",
                "REGION": "eu-central-1",
                "ACCESS_KEY_ID": "fixture-key",
                "SECRET_ACCESS_KEY": "fixture-secret",
            }.items()
        }
        self.environment.update(
            {"AWS_PROFILE": "unsafe", "HTTP_PROXY": "http://unsafe"}
        )
        self.aws = FakeAWS()
        self.packet = self.root / "packet"
        self.packet.mkdir(mode=0o700)
        (self.packet / "assets").mkdir(mode=0o700)
        self.write(self.packet / "assets" / "source.tar.gz", b"tiny-original-payload")
        self.write(
            self.packet / "snapshot-binding.json",
            json_bytes(
                {
                    "schema_version": 1,
                    "kind": "publication-preparation",
                    "binding": self.binding.digest,
                    "repository": self.binding.repository,
                    "version": self.binding.version,
                    "source_commit": self.binding.commit,
                    "signer_workflow": WORKFLOW,
                    "publication_authorized": False,
                    "updater_subjects": dict(self.binding.subjects),
                    "source_assets": {},
                    "evidence": {gate: "sha256:" + "c" * 64 for gate in GATES},
                }
            ),
        )
        self.inventory = {
            path.relative_to(self.packet).as_posix(): {
                "sha256": "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest(),
                "size": path.stat().st_size,
            }
            for path in self.packet.rglob("*")
            if path.is_file()
        }

    @staticmethod
    def write(path, raw):
        path.write_bytes(raw)
        path.chmod(0o600)

    def adapter(self):
        return S3PublicationRetention(
            self.binding, self.context, environment=self.environment, execute=self.aws
        )

    def test_ordered_inputs_cumulative_checkpoints_and_cleanup(self):
        with self.adapter() as adapter:
            private = self.aws.private
            self.assertNotEqual(private, self.packet)
            adapter.persist_inputs(self.packet, self.inventory)
            journal = self.root / "v0.1.0.jsonl"
            raw = b""
            for sequence in (1, 2):
                raw += (
                    json_bytes(
                        {
                            "sequence": sequence,
                            "binding": self.binding.digest,
                            "phase": "begin" if sequence == 1 else "intent",
                            "operation": "transaction",
                            "details": {},
                        }
                    ).replace(b"\n", b" ")
                    + b"\n"
                )
                self.write(journal, raw)
                with journal.open("rb") as stream:
                    os.fsync(stream.fileno())
                adapter.checkpoint(journal, sequence)
            receipt = {
                "schema_version": 1,
                "kind": "container-publication-receipt",
                "immutable": True,
                "binding_digest": self.binding.digest,
                "repository": self.binding.repository,
                "version": self.binding.version,
                "commit": self.binding.commit,
                "journal_sha256": "sha256:" + hashlib.sha256(raw).hexdigest(),
            }
            with self.assertRaises(InvalidRelease):
                adapter.finish({**receipt, "journal_sha256": "sha256:" + "0" * 64})
            adapter.finish(receipt)
            keys = list(self.aws.objects)
            self.assertTrue(keys[0].endswith("inputs/snapshot-binding.json"))
            self.assertTrue(keys[2].endswith("snapshot-inventory.json"))
            self.assertTrue(keys[-1].endswith("publication-receipt.json"))
            self.assertEqual(self.aws.objects[keys[-2]], raw)
            self.assertTrue(all("run-77/attempt-1/" in key for key in keys))
            self.assertNotIn(b"fixture-secret", b"".join(self.aws.objects.values()))
            with self.assertRaises(InvalidRelease):
                adapter.checkpoint(journal, 2)
        self.assertFalse(private.exists())
        self.assertEqual(len(list(self.packet.rglob("*"))), 3)

    def test_interruption_readback_conflicts_and_untrusted_inputs_fail_closed(self):
        for scenario in (
            "failure",
            "checksum",
            "existing",
            "symlink",
            "omitted",
            "binding",
            "inventory-hash",
        ):
            with self.subTest(scenario=scenario):
                aws = FakeAWS()
                self.aws = aws
                with self.adapter() as adapter:
                    if scenario == "failure":
                        aws.fail = "put-object"
                    elif scenario == "checksum":
                        aws.mismatch = True
                    elif scenario == "existing":
                        aws.objects[adapter.prefix + "inputs/snapshot-binding.json"] = (
                            b"existing"
                        )
                    elif scenario == "symlink":
                        (self.packet / "assets" / "link").symlink_to("source.tar.gz")
                    elif scenario == "binding":
                        adapter.binding = Binding(
                            "endorses/psst.zip",
                            "v0.1.0",
                            "d" * 40,
                            self.binding.subjects,
                        )
                    inventory = dict(self.inventory)
                    if scenario == "omitted":
                        inventory.pop("assets/source.tar.gz")
                    elif scenario == "inventory-hash":
                        inventory["assets/source.tar.gz"] = {
                            **inventory["assets/source.tar.gz"],
                            "sha256": "sha256:" + "0" * 64,
                        }
                    with self.assertRaises(InvalidRelease) as caught:
                        adapter.persist_inputs(self.packet, inventory)
                    self.assertNotIn("fixture-secret", str(caught.exception))
                    if scenario in {"symlink", "omitted", "binding"}:
                        self.assertFalse(
                            any(args[2:3] == ["put-object"] for args in aws.calls)
                        )
                    if scenario in {
                        "failure",
                        "checksum",
                        "existing",
                        "inventory-hash",
                    }:
                        with self.assertRaises(InvalidRelease):
                            adapter.persist_inputs(self.packet, inventory)
                    if scenario == "symlink":
                        (self.packet / "assets" / "link").unlink()
                self.assertFalse(aws.private.exists())

    def test_missing_configuration_and_unknown_public_posture_prevent_writes(self):
        for endpoint in (
            None,
            "http://insecure.test",
            "https://host.test?credential=secret",
        ):
            with self.subTest(endpoint=endpoint):
                environment = dict(self.environment)
                environment[PREFIX + "ENDPOINT"] = endpoint
                with self.assertRaises(InvalidRelease):
                    S3PublicationRetention(
                        self.binding,
                        self.context,
                        environment=environment,
                        execute=self.aws,
                    )
        for scenario in ("public", "unsupported", "v1"):
            unsupported = scenario == "unsupported"
            self.aws = FakeAWS()
            self.aws.public = scenario == "public"
            if scenario == "v1":
                self.aws.version = b"aws-cli/1.31.0 Python/3 Linux/x86_64\n"
            if unsupported:
                self.aws.fail = "get-public-access-block"
            with self.assertRaises(InvalidRelease):
                with self.adapter():
                    self.fail("Unsafe posture entered")
            self.assertFalse(self.aws.private.exists())
            self.assertFalse(
                any(args[2:3] == ["put-object"] for args in self.aws.calls)
            )


if __name__ == "__main__":
    unittest.main()
