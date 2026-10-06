#!/usr/bin/env python3
"""Daemon-free regression checks for release Compose configuration.

Requires Docker Compose >=2.24.4. No images are pulled, containers started, or
operator environment files read. Missing tools fail rather than skip this gate.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[1]
BASE = "deploy/compose.release.yml"
PROXY = "deploy/external-proxy.release.compose.yml"
BACKEND_IMAGE = "ghcr.io/example/psst-zip-backend@sha256:" + "1" * 64
WEB_IMAGE = "ghcr.io/example/psst-zip-web@sha256:" + "2" * 64
ENVIRONMENT = {
    "PATH": os.environ.get("PATH", os.defpath),
    "HOME": os.environ.get("HOME", str(ROOT)),
    "PSST_DOMAIN": "transfer.example.test",
    "PUBLIC_URL": "https://transfer.example.test",
    "BACKEND_IMAGE": BACKEND_IMAGE,
    "WEB_IMAGE": WEB_IMAGE,
}
RETAINED = {
    "COMPOSE_PROJECT_NAME": "retained-project",
    "BACKEND_DATA_VOLUME": "existing-backend",
    "DB_PATH": "/app/data/existing.sqlite",
    "CADDY_DATA_VOLUME": "existing-inner-data",
    "CADDY_CONFIG_VOLUME": "existing-inner-config",
    "EXTERNAL_PROXY_DATA_VOLUME": "existing-gateway-data",
    "EXTERNAL_PROXY_CONFIG_VOLUME": "existing-gateway-config",
    "PSST_PRIVATE_SUBNET": "172.30.195.0/29",
    "PSST_BACKEND_IP": "172.30.195.2",
    "PSST_PROXY_IP": "172.30.195.3",
    "PSST_EXTERNAL_PROXY_SUBNET": "172.30.196.0/29",
    "PSST_EXTERNAL_PROXY_IP": "172.30.196.2",
    "PSST_WEB_PROXY_IP": "172.30.196.3",
    "HTTP_PORT": "18080",
    "HTTPS_PORT": "18443",
}


def render(files: tuple[str, ...], overrides: dict[str, str] | None = None) -> dict:
    command = ["docker", "compose", "--env-file", os.devnull]
    for path in files:
        command.extend(["-f", path])
    result = subprocess.run(
        command + ["config", "--format", "json"],
        cwd=ROOT,
        env=ENVIRONMENT | (overrides or {}),
        capture_output=True,
        text=True,
        timeout=15,
        check=True,
    )
    return json.loads(result.stdout)


class ReleaseComposeChecks(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        if shutil.which("docker", path=ENVIRONMENT["PATH"]) is None:
            raise RuntimeError("Docker CLI and Compose >=2.24.4 are required")
        version = subprocess.run(
            ["docker", "compose", "version", "--short"],
            env=ENVIRONMENT,
            capture_output=True,
            text=True,
            timeout=15,
            check=True,
        ).stdout.strip()
        match = re.match(r"v?(\d+)\.(\d+)\.(\d+)", version)
        if not match or tuple(map(int, match.groups())) < (2, 24, 4):
            raise RuntimeError("Docker Compose >=2.24.4 is required")
        cls.source = render(("docker-compose.yml",))
        cls.base = render((BASE,))
        cls.proxy = render((BASE, PROXY))
        cls.retained = render((BASE,), RETAINED)
        cls.retained_proxy = render((BASE, PROXY), RETAINED)
        cls.project_only = render(
            (BASE, PROXY), {"COMPOSE_PROJECT_NAME": "other-project"}
        )

    def test_source_hardening_and_backend_network_policy_are_preserved(self) -> None:
        self.assertEqual(set(self.base["services"]), {"backend", "caddy"})
        for name in ("backend", "caddy"):
            with self.subTest(service=name):
                source = self.source["services"][name]
                release = self.base["services"][name]
                for key in (
                    "restart",
                    "read_only",
                    "cap_drop",
                    "security_opt",
                    "pids_limit",
                    "mem_limit",
                    "cpus",
                    "tmpfs",
                    "logging",
                    "environment",
                ):
                    self.assertEqual(release[key], source[key], key)
                self.assertNotIn("build", release)
        backend = self.base["services"]["backend"]
        self.assertFalse(backend.get("ports"))
        self.assertEqual(set(backend["networks"]), {"private"})
        self.assertEqual(backend["networks"]["private"]["ipv4_address"], "172.30.193.2")
        self.assertEqual(backend["environment"]["TRUSTED_PROXIES"], "172.30.193.3/32")
        self.assertTrue(self.base["networks"]["private"]["internal"])
        self.assertEqual(
            set(self.base["services"]["caddy"]["networks"]), {"private", "edge"}
        )
        self.assertEqual(
            self.base["networks"]["private"]["ipam"]["config"],
            self.source["networks"]["private"]["ipam"]["config"],
        )
        self.assertEqual(
            self.base["services"]["caddy"]["ports"],
            self.source["services"]["caddy"]["ports"],
        )

    def test_base_uses_bundled_caddy_configuration_and_stable_volume_names(
        self,
    ) -> None:
        self.assertEqual(self.base["name"], "psst-zip")
        for name in ("psst-data", "caddy-data", "caddy-config"):
            self.assertEqual(self.base["volumes"][name]["name"], f"psst-zip_{name}")
        expected = {
            "backend": [("psst-data", "/app/data")],
            "caddy": [("caddy-data", "/data"), ("caddy-config", "/config")],
        }
        for name, mounts in expected.items():
            volumes = self.base["services"][name]["volumes"]
            self.assertTrue(all(volume["type"] == "volume" for volume in volumes))
            self.assertEqual(
                [(volume["source"], volume["target"]) for volume in volumes], mounts
            )

    def test_custom_project_name_defines_default_physical_volume_names(self) -> None:
        self.assertEqual(self.project_only["name"], "other-project")
        for logical, value in self.project_only["volumes"].items():
            self.assertEqual(value["name"], f"other-project_{logical}")

    def test_images_are_required_and_no_service_builds_from_source(self) -> None:
        self.assertEqual(self.base["services"]["backend"]["image"], BACKEND_IMAGE)
        self.assertEqual(self.base["services"]["caddy"]["image"], WEB_IMAGE)
        for config in (self.base, self.proxy):
            for service in config["services"].values():
                self.assertNotIn("build", service)
        for missing in ("BACKEND_IMAGE", "WEB_IMAGE"):
            with self.subTest(missing=missing):
                environment = ENVIRONMENT.copy()
                del environment[missing]
                result = subprocess.run(
                    [
                        "docker",
                        "compose",
                        "--env-file",
                        os.devnull,
                        "-f",
                        BASE,
                        "config",
                        "--quiet",
                    ],
                    cwd=ROOT,
                    env=environment,
                    capture_output=True,
                    text=True,
                    timeout=15,
                )
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(missing, result.stderr)

    def test_storage_database_and_project_overrides_preserve_existing_state(
        self,
    ) -> None:
        expected = {
            "psst-data": "existing-backend",
            "caddy-data": "existing-inner-data",
            "caddy-config": "existing-inner-config",
        }
        for config in (self.retained, self.retained_proxy):
            self.assertEqual(config["name"], "retained-project")
            for logical, physical in expected.items():
                self.assertEqual(config["volumes"][logical]["name"], physical)
            self.assertEqual(
                config["services"]["backend"]["environment"]["DB_PATH"],
                "/app/data/existing.sqlite",
            )
        for logical, physical in (
            ("external-proxy-data", "existing-gateway-data"),
            ("external-proxy-config", "existing-gateway-config"),
        ):
            self.assertEqual(self.retained_proxy["volumes"][logical]["name"], physical)

    def test_external_gateway_owns_ports_and_uses_matching_image_and_config(
        self,
    ) -> None:
        services = self.proxy["services"]
        self.assertEqual(set(services), {"backend", "caddy", "external-proxy"})
        self.assertFalse(services["caddy"].get("ports"))
        self.assertFalse(services["backend"].get("ports"))
        self.assertEqual(
            services["external-proxy"]["ports"], self.base["services"]["caddy"]["ports"]
        )
        for name in ("caddy", "external-proxy"):
            self.assertEqual(services[name]["image"], WEB_IMAGE)
            for key in (
                "read_only",
                "cap_drop",
                "security_opt",
                "pids_limit",
                "mem_limit",
                "cpus",
                "tmpfs",
                "logging",
            ):
                self.assertEqual(
                    services[name][key], self.base["services"]["caddy"][key]
                )
        expected_mounts = {
            "caddy": {
                "/etc/caddy/proxy-trust/external.caddy": "trusted-proxy.caddy",
            },
            "external-proxy": {"/etc/caddy/Caddyfile": "Caddyfile"},
        }
        for name, mounts in expected_mounts.items():
            bound = {
                mount["target"]: mount
                for mount in services[name]["volumes"]
                if mount["type"] == "bind"
            }
            self.assertEqual(set(bound), set(mounts))
            for target, filename in mounts.items():
                self.assertTrue(bound[target]["read_only"])
                self.assertEqual(
                    Path(bound[target]["source"]),
                    ROOT / "deploy" / "external-proxy" / filename,
                )
                self.assertTrue(Path(bound[target]["source"]).is_file())
        gateway_volumes = [
            volume["source"]
            for volume in services["external-proxy"]["volumes"]
            if volume["type"] == "volume"
        ]
        self.assertEqual(
            gateway_volumes, ["external-proxy-data", "external-proxy-config"]
        )
        for name in gateway_volumes:
            self.assertEqual(self.proxy["volumes"][name]["name"], f"psst-zip_{name}")

    def test_proxy_trust_stays_exact_on_separate_private_networks(self) -> None:
        for config, backend_ip, gateway_ip, inner_ip in (
            (self.proxy, "172.30.193.3", "172.30.194.2", "172.30.194.3"),
            (self.retained_proxy, "172.30.195.3", "172.30.196.2", "172.30.196.3"),
        ):
            services = config["services"]
            self.assertEqual(set(services["backend"]["networks"]), {"private"})
            self.assertEqual(set(services["caddy"]["networks"]), {"private", "proxy"})
            self.assertEqual(
                set(services["external-proxy"]["networks"]), {"proxy", "edge"}
            )
            self.assertEqual(
                services["backend"]["environment"]["TRUSTED_PROXIES"],
                backend_ip + "/32",
            )
            self.assertEqual(
                services["caddy"]["environment"]["PSST_EXTERNAL_PROXY_CIDRS"],
                gateway_ip + "/32",
            )
            self.assertEqual(
                services["caddy"]["environment"]["PSST_DOMAIN"],
                "http://transfer.example.test",
            )
            self.assertNotIn(
                "PSST_EXTERNAL_PROXY_CIDRS", services["external-proxy"]["environment"]
            )
            self.assertEqual(
                services["external-proxy"]["networks"]["proxy"]["ipv4_address"],
                gateway_ip,
            )
            self.assertEqual(
                services["caddy"]["networks"]["proxy"]["ipv4_address"], inner_ip
            )
            for name in ("private", "proxy"):
                self.assertTrue(config["networks"][name]["internal"])
        self.assertEqual(
            [
                port["published"]
                for port in self.retained_proxy["services"]["external-proxy"]["ports"]
            ],
            ["18080", "18443"],
        )


if __name__ == "__main__":
    unittest.main()
