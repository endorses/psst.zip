"""SSH credential, command and completion boundaries for manual deployment."""

from __future__ import annotations

import base64
import contextlib
import io
import json
import os
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import request_production_deployment as request


def known(host="vps.example.com"):
    kind = b"ssh-ed25519"
    binary = struct.pack("!I", len(kind)) + kind + struct.pack("!I", 32) + bytes(32)
    return host + " ssh-ed25519 " + base64.b64encode(binary).decode()


def status(version="v1.2.3", phase="completed"):
    return json.dumps(
        {"version": version, "active_version": version, "phase": phase}
    ).encode()


class DeploymentRequestTests(unittest.TestCase):
    def test_host_and_key_binding_reject_shell_alias_and_unverified_hosts(self):
        for value in [
            "-oProxyCommand=evil",
            "example.com:22",
            "example.com\n",
            "u@host",
            "",
            "127.0.0.1/32",
            "fe80::1%eth0",
        ]:
            with self.subTest(value=value), self.assertRaises(request.RequestError):
                request.host_name(value)
        for value in [
            known("other.example.com"),
            known() + " comment",
            "* ssh-ed25519 abc",
            "@cert-authority " + known(),
            "|1|hashed ssh-ed25519 abc",
            known().replace("ssh-ed25519", "ssh-rsa", 1),
        ]:
            with self.subTest(value=value), self.assertRaises(request.RequestError):
                request.pinned_hosts(value, "vps.example.com")
        for host in ["2.28.142.186", "vps.example.com", "2001:db8::1"]:
            self.assertEqual(request.host_name(host), host)
            self.assertEqual(
                request.pinned_hosts(known(host), host), known(host) + "\n"
            )

    def test_no_ssh_request_for_invalid_version_or_missing_credentials(self):
        for version in [
            "v01.2.3",
            "v1.2.3; id",
            "v1.2.3\n",
            "main",
            "v1.2.3-rc.1",
            "v" + "1" * 100 + ".2.3",
        ]:
            with self.subTest(version=version), patch.object(
                request, "bounded_ssh"
            ) as ssh:
                with self.assertRaises(request.RequestError):
                    request.request({"PSST_RELEASE_VERSION": version})
                ssh.assert_not_called()

    def test_bounded_transport_suppresses_stderr_and_reaps_failed_process(self):
        code, output = request.bounded_ssh(
            [
                sys.executable,
                "-c",
                "import sys; print('private error', file=sys.stderr); print('status'); sys.exit(20)",
            ]
        )
        self.assertEqual(code, 20)
        self.assertEqual(output, b"status\n")
        with patch.object(request, "MAX_OUTPUT", 16):
            with self.assertRaisesRegex(request.RequestError, "exceeded"):
                request.bounded_ssh(
                    [
                        sys.executable,
                        "-c",
                        "print('x'*100, flush=True); import time; time.sleep(30)",
                    ]
                )
        with patch.object(request, "TIMEOUT", 0.05):
            with self.assertRaisesRegex(request.RequestError, "timed out"):
                request.bounded_ssh(
                    [sys.executable, "-c", "import time; time.sleep(30)"]
                )

    def test_only_exact_completed_pair_is_success(self):
        request.completed_status(status(), "v1.2.3")
        for output in [
            status("v1.2.2"),
            status(phase="awaiting-verification"),
            b"not JSON",
            b"[]",
            b'{"phase":"completed","version":"v1.2.3"}',
        ]:
            with self.subTest(output=output), self.assertRaises(request.RequestError):
                request.completed_status(output, "v1.2.3")

    def test_real_key_parsing_restricted_command_private_files_and_cleanup(self):
        with tempfile.TemporaryDirectory(prefix="psst-request-fixture-") as folder:
            key = Path(folder) / "fixture-key"
            subprocess.run(
                [
                    "/usr/bin/ssh-keygen",
                    "-q",
                    "-t",
                    "ed25519",
                    "-N",
                    "",
                    "-f",
                    str(key),
                ],
                check=True,
            )
            environment = {
                "PSST_RELEASE_VERSION": "v1.2.3",
                "PSST_VPS_HOST": "vps.example.com",
                "PSST_KNOWN_HOSTS": known(),
                "PSST_DEPLOY_KEY": key.read_text(),
            }
            captured = []

            def ssh(args):
                private = Path(args[args.index("-i") + 1])
                hosts = Path(
                    next(
                        arg.split("=", 1)[1]
                        for arg in args
                        if arg.startswith("UserKnownHostsFile=")
                    )
                )
                captured.append(private.parent)
                self.assertEqual(private.stat().st_mode & 0o777, 0o600)
                self.assertEqual(hosts.stat().st_mode & 0o777, 0o600)
                self.assertEqual(private.parent.stat().st_mode & 0o777, 0o700)
                self.assertEqual(
                    args[-2:], ["psst-deploy@vps.example.com", "update v1.2.3"]
                )
                self.assertIn("StrictHostKeyChecking=yes", args)
                self.assertIn("IdentityAgent=none", args)
                self.assertEqual(args[1:3], ["-F", "/dev/null"])
                self.assertNotIn(environment["PSST_DEPLOY_KEY"], args)
                return 0, status()

            with patch.object(
                request, "bounded_ssh", side_effect=ssh
            ), contextlib.redirect_stdout(io.StringIO()) as log:
                request.request(environment)
            self.assertEqual(log.getvalue(), "Verified updater completed v1.2.3\n")
            self.assertTrue(all(not directory.exists() for directory in captured))
            with patch.object(
                request,
                "bounded_ssh",
                return_value=(20, status(phase="awaiting-verification")),
            ):
                with self.assertRaisesRegex(request.RequestError, "awaits local"):
                    request.request(environment)
            # A forged success code is insufficient: inspect the protected helper response.
            with patch.object(
                request, "bounded_ssh", return_value=(0, status("v9.9.9"))
            ):
                with self.assertRaises(request.RequestError):
                    request.request(environment)


if __name__ == "__main__":
    unittest.main()
