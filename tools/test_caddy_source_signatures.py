#!/usr/bin/env python3
"""Fast fail-closed checks; real Sigstore verification is a separate online gate."""

import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from release_artifacts import InvalidRelease
import verify_caddy_source_signatures as verifier


class SignatureVerifierTests(unittest.TestCase):
    def fixture(self, root: Path) -> dict:
        names = [
            "caddy_2.11.7_buildable-artifact.tar.gz",
            "caddy_2.11.7_buildable-artifact.tar.gz.sig",
            "caddy_2.11.7_buildable-artifact.pem",
            "caddy_2.11.7_checksums.txt",
            "caddy_2.11.7_checksums.txt.sig",
            "caddy_2.11.7_checksums.txt.pem",
            "caddy_2.11.7_linux_amd64.tar.gz",
        ]
        for name in names:
            (root / name).write_bytes(name.encode())
        checksum_text = "".join(
            hashlib.sha512((root / name).read_bytes()).hexdigest() + "  " + name + "\n"
            for name in (names[0], names[-1])
        )
        (root / names[3]).write_text(checksum_text)
        inventory = {
            "schema_version": 1,
            "review_required": True,
            "version": "v2.11.7",
            "source_revision": "a" * 40,
            "image_id": "sha256:" + "b" * 64,
            "sources": [
                {
                    "file": name,
                    "sha256": verifier.file_hash(root / name),
                    "url": "https://github.com/caddyserver/caddy/releases/download/v2.11.7/"
                    + name,
                }
                for name in names
            ],
        }
        self.write_inventory(root, inventory)
        return inventory

    def write_inventory(self, root: Path, inventory: dict) -> None:
        (root / "caddy-source-inventory.json").write_text(json.dumps(inventory))

    def test_exact_signer_constraints_and_transparency_checks_are_mandatory(self):
        args = verifier.verification_arguments(
            Path("/tool"),
            "v2.11.7",
            "a" * 40,
            Path("archive"),
            Path("cert"),
            Path("sig"),
        )
        for flag, value in {
            "--certificate-identity": verifier.WORKFLOW + "@refs/tags/v2.11.7",
            "--certificate-oidc-issuer": "https://token.actions.githubusercontent.com",
            "--certificate-github-workflow-sha": "a" * 40,
            "--certificate-github-workflow-ref": "refs/tags/v2.11.7",
            "--certificate-github-workflow-repository": "caddyserver/caddy",
            "--certificate-github-workflow-trigger": "push",
            "--certificate-github-workflow-name": "Release",
            "--rekor-url": "https://rekor.sigstore.dev",
        }.items():
            self.assertEqual(args[args.index(flag) + 1], value)
        for flag in (
            "--insecure-ignore-tlog=false",
            "--insecure-ignore-sct=false",
            "--private-infrastructure=false",
            "--offline=false",
        ):
            self.assertIn(flag, args)
        self.assertFalse(any("regexp" in arg for arg in args))

    def test_wrong_or_unpinned_verifier_is_rejected_before_execution(self):
        with tempfile.TemporaryDirectory(prefix="psst-signature-test-") as temp:
            tool = Path(temp) / "cosign"
            tool.write_bytes(b"untrusted replacement")
            with patch.object(verifier, "run_verifier") as run:
                with self.assertRaisesRegex(InvalidRelease, "pinned official release"):
                    verifier.verify_tool(tool)
                run.assert_not_called()

    def test_signature_failure_never_emits_success_evidence(self):
        with tempfile.TemporaryDirectory(prefix="psst-signature-test-") as temp:
            root = Path(temp)
            self.fixture(root)
            with patch.object(verifier, "verify_tool", return_value={}), patch.object(
                verifier,
                "run_verifier",
                return_value=subprocess.CompletedProcess(
                    [], 1, b"", b"identity or signature mismatch"
                ),
            ):
                with self.assertRaisesRegex(
                    InvalidRelease, "signature verification failed"
                ):
                    verifier.verify(root, Path("/cosign"))

    def test_verified_signature_cannot_bypass_wrong_signed_archive_checksum(self):
        with tempfile.TemporaryDirectory(prefix="psst-signature-test-") as temp:
            root = Path(temp)
            inventory = self.fixture(root)
            archive = root / "caddy_2.11.7_buildable-artifact.tar.gz"
            archive.write_bytes(b"different source bytes")
            inventory["sources"][0]["sha256"] = verifier.file_hash(archive)
            self.write_inventory(root, inventory)
            with patch.object(verifier, "verify_tool", return_value={}), patch.object(
                verifier,
                "run_verifier",
                return_value=subprocess.CompletedProcess([], 0, b"", b"Verified OK"),
            ):
                with self.assertRaisesRegex(InvalidRelease, "signed Caddy SHA512"):
                    verifier.verify(root, Path("/cosign"))

    def test_checksum_ambiguity_and_unsafe_names_are_rejected(self):
        line = ("a" * 128 + "  caddy.tar.gz\n").encode()
        with self.assertRaisesRegex(InvalidRelease, "Duplicate"):
            verifier.checksum_entries(line + line)
        with self.assertRaisesRegex(InvalidRelease, "Unsupported"):
            verifier.checksum_entries(("a" * 128 + "  ../escape\n").encode())

    def test_duplicate_collection_assets_are_rejected(self):
        with tempfile.TemporaryDirectory(prefix="psst-signature-test-") as temp:
            root = Path(temp)
            inventory = self.fixture(root)
            inventory["sources"].append(inventory["sources"][0])
            self.write_inventory(root, inventory)
            with self.assertRaisesRegex(InvalidRelease, "Duplicate retained"):
                verifier.verify(root, Path("/cosign"))

    def test_caddy_evidence_never_approves_other_upstreams_or_distribution(self):
        with tempfile.TemporaryDirectory(prefix="psst-signature-test-") as temp:
            root = Path(temp)
            self.fixture(root)
            with patch.object(verifier, "verify_tool", return_value={}), patch.object(
                verifier,
                "run_verifier",
                return_value=subprocess.CompletedProcess([], 0, b"", b"Verified OK"),
            ):
                result = verifier.verify(root, Path("/cosign"))
            self.assertEqual(len(result["verifications"]), 2)
            self.assertEqual(len(result["signed_sha512_bindings"]), 2)
            self.assertTrue(result["review_required"])
            self.assertFalse(result["apk_upstream_signatures_verified"])
            self.assertFalse(result["other_source_archive_signatures_verified"])


if __name__ == "__main__":
    unittest.main()
