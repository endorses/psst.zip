"""Release input and archive boundary regressions using disposable Git trees."""

from __future__ import annotations

import copy
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import sys
import tempfile
import unittest

import release_artifacts as release


def digest(character: str) -> str:
    return "sha256:" + character * 64


def manifest(commit: str = "a" * 40) -> dict:
    return {
        "schema_version": 1,
        "version": "v1.2.3",
        "payload_profile": "artifact-foundation",
        "source": {
            "repository": "endorses/psst.zip",
            "commit": commit,
            "archive_url": f"https://github.com/endorses/psst.zip/archive/{commit}.tar.gz",
        },
        "platforms": release.PLATFORMS.copy(),
        "images": {
            component: {
                "index": f"ghcr.io/endorses/psst-zip-{component}@{digest(index)}",
                "platform_digests": {
                    "linux/amd64": digest(amd),
                    "linux/arm64": digest(arm),
                },
            }
            for component, index, amd, arm in (
                ("backend", "1", "2", "3"),
                ("web", "4", "5", "6"),
            )
        },
        "bundle": {"name": "psst.zip-deployment-v1.2.3.tar.gz", "sha256": "b" * 64},
        "requirements": release.REQUIREMENTS.copy(),
        "notes": {
            "migration": "Review schema migrations.",
            "rollback": "Restore matching stopped checkpoint into isolated volumes.",
            "checkpoint_required": True,
        },
        "build": {
            "base_images": {
                name: f"docker.io/library/{name}@{digest('7')}"
                for name in ("golang", "node", "alpine", "caddy")
            },
            "toolchains": {"go": "1.26.8", "node": "22.23.3", "buildx": "0.29.1"},
        },
    }


class ManifestChecks(unittest.TestCase):
    def test_valid_manifest_and_explicit_repository_trust(self) -> None:
        value = manifest()
        release.validate_manifest(value)
        value["source"]["repository"] = "another/project"
        value["source"][
            "archive_url"
        ] = f"https://github.com/another/project/archive/{value['source']['commit']}.tar.gz"
        for component, image in value["images"].items():
            image["index"] = image["index"].replace("/endorses/", "/another/")
        with self.assertRaises(release.InvalidRelease):
            release.validate_manifest(value)
        release.validate_manifest(value, "another/project")

    def test_malformed_manifest_fails_closed(self) -> None:
        changes = [
            ("version", "v1.02.3"),
            ("version", "v1.2.3; id"),
            ("version", "v1.2.3-rc1"),
            ("version", "../v1.2.3"),
            ("schema_version", True),
            ("schema_version", 2),
            ("payload_profile", "ready"),
            ("payload_profile", []),
            ("platforms", ["linux/amd64"]),
            ("platforms", ["linux/amd64", "linux/amd64"]),
            ("platforms", ["linux/arm64", "linux/amd64"]),
            ("requirements", {"docker": "1.0.0", "compose": "2.24.4"}),
        ]
        for field, value in changes:
            with self.subTest(field=field, value=value):
                candidate = manifest()
                candidate[field] = value
                with self.assertRaises(release.InvalidRelease):
                    release.validate_manifest(candidate)
        candidate = manifest()
        candidate["unknown"] = "ignored?"
        with self.assertRaises(release.InvalidRelease):
            release.validate_manifest(candidate)

    def test_source_bundle_and_checkpoint_must_match_contract(self) -> None:
        changes = [
            ("source", "repository", "endorses/../psst.zip"),
            ("source", "repository", "endorses/psst.zip;id"),
            ("source", "commit", "a" * 39),
            ("source", "archive_url", "https://example.invalid/source.tar.gz"),
            ("bundle", "name", "../psst.zip-deployment-v1.2.3.tar.gz"),
            ("bundle", "sha256", "SHA256:" + "a" * 64),
            ("notes", "checkpoint_required", False),
            ("notes", "migration", "   "),
            ("notes", "rollback", ""),
        ]
        for group, key, value in changes:
            with self.subTest(group=group, key=key):
                candidate = manifest()
                candidate[group][key] = value
                with self.assertRaises(release.InvalidRelease):
                    release.validate_manifest(candidate)

    def test_image_pair_constrains_registry_names_and_architecture_digests(
        self,
    ) -> None:
        for reference in (
            "ghcr.io/attacker/psst-zip-backend@" + digest("1"),
            "docker.io/endorses/psst-zip-backend@" + digest("1"),
            "ghcr.io/endorses/psst-zip-web@" + digest("1"),
            "ghcr.io/endorses/psst-zip-backend:latest",
            "ghcr.io/endorses/psst-zip-backend@" + digest("A"),
        ):
            candidate = manifest()
            candidate["images"]["backend"]["index"] = reference
            with self.subTest(reference=reference), self.assertRaises(
                release.InvalidRelease
            ):
                release.validate_manifest(candidate)
        for replacement in (
            {"linux/amd64": digest("2")},
            {"linux/amd64": digest("2"), "linux/arm64": digest("2")},
            {"linux/amd64": digest("1"), "linux/arm64": digest("3")},
        ):
            candidate = manifest()
            candidate["images"]["backend"]["platform_digests"] = replacement
            with self.assertRaises(release.InvalidRelease):
                release.validate_manifest(candidate)

    def test_build_records_require_resolved_bases_and_toolchain_versions(self) -> None:
        for group, name, value in (
            ("base_images", "node", "node:22-alpine"),
            ("toolchains", "go", ""),
            ("toolchains", "node", "22;id"),
        ):
            candidate = manifest()
            candidate["build"][group][name] = value
            with self.assertRaises(release.InvalidRelease):
                release.validate_manifest(candidate)
        candidate = manifest()
        del candidate["build"]["base_images"]["caddy"]
        with self.assertRaises(release.InvalidRelease):
            release.validate_manifest(candidate)

    def test_duplicate_json_keys_are_rejected(self) -> None:
        with self.assertRaises(release.InvalidRelease):
            release.read_json(b'{"source":{"commit":"first","commit":"second"}}')
        for value in (b'{"value":NaN}', b'{"value":Infinity}', b'{"value":-Infinity}'):
            with self.assertRaises(release.InvalidRelease):
                release.read_json(value)


class BundleChecks(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory(prefix="psst-release-test-")
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.root = self.directory / "repo"
        self.root.mkdir()
        self.env = {
            **os.environ,
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_CONFIG_NOSYSTEM": "1",
        }
        self.git("init", "-q")
        self.git("config", "user.name", "Release fixture")
        self.git("config", "user.email", "release@example.invalid")
        self.git("config", "core.hooksPath", os.devnull)
        for name in release.REQUIRED_FILES | {"backend/licenses/example/LICENSE"}:
            self.write(name, f"Fixture {name}\n")
        # Both tracked and untracked sensitive/local files must be excluded.
        for name in (
            ".env",
            ".retcon-private/private-key.pem",
            "docs/research/private.md",
            "backend/data/psst.db",
            "uploads/secret.txt",
            "deploy/extra.env",
            "backend/licenses/example/.env",
        ):
            self.write(name, "private fixture\n")
        self.git("add", ".")
        self.git("commit", "-qm", "Fixture release")
        self.commit = self.git("rev-parse", "HEAD").decode().strip()
        self.write("operator-private.env", "untracked fixture\n")

    def git(self, *args: str) -> bytes:
        return subprocess.run(
            ["git", "-C", str(self.root), *args],
            env=self.env,
            check=True,
            capture_output=True,
        ).stdout

    def write(self, name: str, content: str) -> None:
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    def build(
        self, destination: str = "dist", profile: str = "artifact-foundation"
    ) -> tuple[Path, dict]:
        path = release.build_bundle(
            self.root, self.directory / destination, "v1.2.3", profile
        )
        value = manifest(self.commit)
        value["bundle"]["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
        return path, value

    def test_deterministic_allowlisted_archive_and_source_binding(self) -> None:
        first, value = self.build()
        second, _ = self.build("other")
        self.assertEqual(first.read_bytes(), second.read_bytes())
        release.validate_manifest(value)
        release.validate_bundle(value, first)
        with tarfile.open(first, "r:gz") as archive:
            self.assertEqual(
                set(archive.getnames()),
                release.REQUIRED_FILES
                | {release.METADATA, "backend/licenses/example/LICENSE"},
            )
            for entry in archive:
                self.assertTrue(entry.isfile())
                self.assertEqual(entry.mode, 0o644)
                self.assertEqual((entry.uid, entry.gid), (0, 0))
        candidate = copy.deepcopy(value)
        candidate["source"]["commit"] = "b" * 40
        with self.assertRaises(release.InvalidRelease):
            release.validate_bundle(candidate, first)

    def test_dirty_staged_missing_and_untracked_required_inputs_rejected(self) -> None:
        name = "deploy/compose.release.yml"
        original = (self.root / name).read_text()
        self.write(name, "dirty\n")
        with self.assertRaises(release.InvalidRelease):
            self.build()
        self.git("add", name)
        self.write(name, original)
        with self.assertRaises(release.InvalidRelease):
            self.build()
        self.git("reset", "-q", "HEAD", "--", name)
        (self.root / name).unlink()
        with self.assertRaises(release.InvalidRelease):
            self.build()
        self.write(name, original)
        self.git("rm", "--cached", name)
        self.git("commit", "-qm", "Untracked required input")
        with self.assertRaises(release.InvalidRelease):
            self.build()

    def test_release_destination_is_never_overwritten(self) -> None:
        path, _ = self.build()
        before = path.read_bytes()
        with self.assertRaises(FileExistsError):
            self.build()
        self.assertEqual(path.read_bytes(), before)

    def test_symlink_release_input_rejected(self) -> None:
        name = "deploy/compose.release.yml"
        target = self.root / name
        target.unlink()
        target.symlink_to("release.env.example")
        with self.assertRaises(release.InvalidRelease):
            self.build()
        self.git("add", name)
        self.git("commit", "-qm", "Symlink input")
        with self.assertRaises(release.InvalidRelease):
            self.build()

    def test_deployment_ready_profile_requires_tracked_updater(self) -> None:
        foundation, value = self.build()
        release.validate_bundle(value, foundation)
        with self.assertRaises(release.InvalidRelease):
            self.build("missing-ready-updater", "deployment-ready")
        self.write("deploy/update.py", "print('fixture updater')\n")
        with self.assertRaises(release.InvalidRelease):
            self.build("untracked-updater")
        self.git("add", "deploy/update.py")
        self.git("commit", "-qm", "Add updater")
        self.commit = self.git("rev-parse", "HEAD").decode().strip()
        default, value = self.build("still-foundation")
        release.validate_bundle(value, default)
        ready, value = self.build("ready", "deployment-ready")
        with self.assertRaises(release.InvalidRelease):
            release.validate_bundle(value, ready)
        value["payload_profile"] = "deployment-ready"
        release.validate_bundle(value, ready)

    def rewrite_archive(self, path: Path, value: dict, modify) -> None:
        with tarfile.open(path, "r:gz") as archive:
            entries = [
                (member, archive.extractfile(member).read()) for member in archive
            ]
        entries = modify(entries)
        buffer = io.BytesIO()
        with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
            for member, content in entries:
                archive.addfile(
                    member, io.BytesIO(content) if member.isfile() else None
                )
        path.write_bytes(buffer.getvalue())
        value["bundle"]["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()

    def test_unsafe_archive_entries_rejected_even_with_matching_checksum(self) -> None:
        for unsafe in (
            "../LICENSE",
            "/LICENSE",
            "deploy/../LICENSE",
            "backend/licenses/../secret/LICENSE",
            "backend//licenses/example/LICENSE",
            "backend/licenses/example/.env",
            "docs/research/secret.md",
        ):
            with self.subTest(unsafe=unsafe):
                path, value = self.build(hashlib.sha256(unsafe.encode()).hexdigest())

                def rename(entries):
                    entries[0][0].name = unsafe
                    return entries

                self.rewrite_archive(path, value, rename)
                with self.assertRaises(release.InvalidRelease):
                    release.validate_bundle(value, path)
        for kind in (
            tarfile.SYMTYPE,
            tarfile.LNKTYPE,
            tarfile.DIRTYPE,
            tarfile.CHRTYPE,
        ):
            path, value = self.build(kind.decode())

            def change_type(entries):
                entries[0][0].type = kind
                entries[0][0].linkname = "/etc/passwd"
                return entries

            self.rewrite_archive(path, value, change_type)
            with self.assertRaises(release.InvalidRelease):
                release.validate_bundle(value, path)

    def test_duplicate_missing_and_executable_entries_rejected(self) -> None:
        for label, modify in (
            ("duplicate", lambda entries: entries + [entries[0]]),
            ("missing", lambda entries: entries[1:]),
            ("executable", lambda entries: self.make_executable(entries)),
        ):
            path, value = self.build(label)
            self.rewrite_archive(path, value, modify)
            with self.assertRaises(release.InvalidRelease):
                release.validate_bundle(value, path)

    def make_executable(self, entries):
        entries[0][0].mode = 0o755
        return entries

    def test_cli_manifest_creation_and_validation(self) -> None:
        path, value = self.build()
        script = Path(release.__file__)
        output = self.directory / "release-manifest.json"
        command = [
            sys.executable,
            str(script),
            "create-manifest",
            "--version",
            "v1.2.3",
            "--source-commit",
            self.commit,
            "--bundle",
            str(path),
            "--output",
            str(output),
            "--migration-notes",
            value["notes"]["migration"],
            "--rollback-notes",
            value["notes"]["rollback"],
        ]
        for component, image in value["images"].items():
            command.extend([f"--{component}-index", image["index"]])
            for architecture in ("amd64", "arm64"):
                command.extend(
                    [
                        f"--{component}-{architecture}",
                        image["platform_digests"][f"linux/{architecture}"],
                    ]
                )
        for key, flag in (
            ("base_images", "--base-image"),
            ("toolchains", "--toolchain"),
        ):
            for name, entry in value["build"][key].items():
                command.extend([flag, f"{name}={entry}"])
        result = subprocess.run(command, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(output.read_text()), value)
        result = subprocess.run(
            [
                sys.executable,
                str(script),
                "validate",
                "--manifest",
                str(output),
                "--bundle",
                str(path),
            ],
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("provenance verification is also required", result.stdout)
        repeated = subprocess.run(command, capture_output=True, text=True)
        self.assertNotEqual(repeated.returncode, 0)
        self.assertEqual(json.loads(output.read_text()), value)

    def test_archive_expansion_is_bounded(self) -> None:
        path, value = self.build()

        def enlarge(entries):
            member = entries[0][0]
            content = b"x" * (release.MAX_BUNDLE_BYTES + 1)
            member.size = len(content)
            return [(member, content)] + entries[1:]

        self.rewrite_archive(path, value, enlarge)
        self.assertLess(path.stat().st_size, release.MAX_BUNDLE_BYTES)
        with self.assertRaises(release.InvalidRelease):
            release.validate_bundle(value, path)

    def test_hidden_pax_and_global_metadata_expansion_is_bounded(self) -> None:
        comment = "x" * (release.MAX_BUNDLE_BYTES + 1)
        for header_type in ("global", "member"):
            with self.subTest(header_type=header_type):
                path, value = self.build(header_type)
                with tarfile.open(path, "r:gz") as archive:
                    entries = [
                        (member, archive.extractfile(member).read())
                        for member in archive
                    ]
                if header_type == "member":
                    entries[0][0].pax_headers = {"comment": comment}
                buffer = io.BytesIO()
                with tarfile.open(
                    fileobj=buffer,
                    mode="w:gz",
                    format=tarfile.PAX_FORMAT,
                    pax_headers=(
                        {"comment": comment} if header_type == "global" else None
                    ),
                ) as archive:
                    for member, content in entries:
                        archive.addfile(member, io.BytesIO(content))
                path.write_bytes(buffer.getvalue())
                value["bundle"]["sha256"] = hashlib.sha256(
                    path.read_bytes()
                ).hexdigest()
                self.assertLess(path.stat().st_size, 64 * 1024)
                with self.assertRaisesRegex(
                    release.InvalidRelease, "expanded bundle exceeds"
                ):
                    release.validate_bundle(value, path)

    def test_untrusted_file_reads_are_bounded(self) -> None:
        oversized = self.directory / "oversized.json"
        with oversized.open("wb") as stream:
            stream.truncate(release.MAX_BUNDLE_BYTES + 1)
        with self.assertRaisesRegex(release.InvalidRelease, "exceeds size limit"):
            release.read_bounded_file(oversized)
        result = subprocess.run(
            [
                sys.executable,
                release.__file__,
                "validate",
                "--manifest",
                str(oversized),
            ],
            capture_output=True,
            text=True,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("exceeds size limit", result.stderr)
        self.assertNotIn("Traceback", result.stderr)
        link = self.directory / "linked.json"
        link.symlink_to(oversized)
        with self.assertRaisesRegex(release.InvalidRelease, "regular file"):
            release.read_bounded_file(link)

    def test_checksum_and_filename_mismatch_rejected(self) -> None:
        path, value = self.build()
        value["bundle"]["sha256"] = "0" * 64
        with self.assertRaises(release.InvalidRelease):
            release.validate_bundle(value, path)
        wrong = path.with_name("unexpected.tar.gz")
        path.rename(wrong)
        with self.assertRaises(release.InvalidRelease):
            release.validate_bundle(value, wrong)


if __name__ == "__main__":
    unittest.main()
