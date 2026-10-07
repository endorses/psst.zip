"""Exact Git policy and real archive-byte tests; no upstream scripts execute."""

from __future__ import annotations

import copy
import gzip
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import package_upstream_application_sources as upstream
from release_artifacts import InvalidRelease, json_bytes, read_json


def upstream_fixture(
    root: Path, packages: dict[str, str] | None = None
) -> dict[str, bytes]:
    """Write a disposable tracked catalog before committing; retain the given lock.

    The test's exact committed policy authorizes these tiny fake inputs only in
    its disposable Git repository. No production constant or verifier is bypassed.
    """
    packages = packages or {"fixture-package": "1.0.0"}
    record = {
        "id": "fixture-upstream",
        "repository": "fixture-owner/fixture-upstream",
        "commit": "a" * 40,
        "packages": packages,
        "inspect_paths": ["package.json", "src/input.ts"],
    }
    prefix = "fixture-upstream-" + record["commit"]
    payload = upstream.tar_gzip(
        {
            prefix
            + "/package.json": json_bytes(
                {
                    "name": next(iter(packages)),
                    "version": next(iter(packages.values())),
                    "scripts": {"build": "DO_NOT_RUN_THIS"},
                }
            ),
            prefix
            + "/src/input.ts": b"// original fixture input; no script execution\n",
            prefix + "/LICENSE": b"Fixture license input\n",
        }
    )
    record["archive"] = {
        "file": prefix + ".tar.gz",
        "sha256": upstream.sha256(payload),
        "size": len(payload),
    }
    path = root / upstream.CATALOG
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(
        json_bytes(
            {
                "schema_version": 1,
                "kind": "pinned-upstream-application-source-inputs",
                "upstreams": [record],
            }
        )
    )
    return {upstream.url(record): payload}


class UpstreamInputs(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="psst-upstream-input-test-")
        self.addCleanup(self.temporary.cleanup)
        self.folder = Path(self.temporary.name)
        self.root = self.folder / "repo"
        self.root.mkdir()
        self.git("init", "-q")
        self.git("config", "user.name", "Upstream fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        self.git("config", "core.hooksPath", os.devnull)
        lock = self.root / upstream.LOCK
        lock.parent.mkdir()
        lock.write_bytes(
            json_bytes(
                {
                    "lockfileVersion": 3,
                    "packages": {"node_modules/fixture-package": {"version": "1.0.0"}},
                }
            )
        )
        self.fetch_map = upstream_fixture(self.root)
        self.commit()
        self.output = self.folder / "collected"

    def git(self, *args):
        return subprocess.run(
            ["git", "-C", str(self.root), *args],
            check=True,
            capture_output=True,
            env={
                **os.environ,
                "GIT_CONFIG_GLOBAL": os.devnull,
                "GIT_CONFIG_NOSYSTEM": "1",
            },
        ).stdout

    def commit(self):
        self.git("add", ".")
        self.git("commit", "-qm", "Disposable upstream policy fixture")
        self.revision = self.git("rev-parse", "HEAD").decode().strip()

    def collect(self, **kwargs):
        return upstream.collect(
            self.root,
            "endorses/psst.zip",
            "v1.2.3",
            self.revision,
            self.output,
            fetch=self.fetch_map.__getitem__,
            **kwargs,
        )

    def verify(self):
        return upstream.verify(
            self.root, "endorses/psst.zip", "v1.2.3", self.revision, self.output
        )

    def catalog(self):
        return read_json((self.root / upstream.CATALOG).read_bytes())

    def catalog_commit(self, value):
        (self.root / upstream.CATALOG).write_bytes(json_bytes(value))
        self.commit()

    def replace_payload(self, raw):
        catalog = self.catalog()
        record = catalog["upstreams"][0]
        record["archive"].update(sha256=upstream.sha256(raw), size=len(raw))
        self.fetch_map[upstream.url(record)] = raw
        self.catalog_commit(catalog)

    def test_real_catalog_and_lock_satisfy_collection_policy_without_downloads(self):
        # The synthetic archives exercise the parser; also validate the inputs
        # shipped by this project so a catalog typo cannot block a real release.
        for name in (upstream.CATALOG, upstream.LOCK):
            (self.root / name).write_bytes((upstream.ROOT / name).read_bytes())
        self.commit()
        upstream.policy(
            self.root, upstream.context("endorses/psst.zip", "v1.2.3", self.revision)
        )

    def test_collect_replays_original_inputs_deterministically_without_approval(self):
        first = self.collect()
        replay = self.verify()
        self.assertEqual(replay["asset"], first["asset"])
        self.assertEqual(replay["source"]["commit"], self.revision)
        self.assertTrue(replay["package_inputs_replayed"])
        for flag in upstream.UNAPPROVED:
            self.assertIs(first[flag], False)
            self.assertIs(replay[flag], False)
        other = self.folder / "repeat"
        second = upstream.collect(
            self.root,
            "endorses/psst.zip",
            "v1.2.3",
            self.revision,
            other,
            fetch=self.fetch_map.__getitem__,
        )
        self.assertEqual(first, second)
        raw = (self.output / first["asset"]["name"]).read_bytes()
        self.assertEqual(raw, (other / first["asset"]["name"]).read_bytes())
        outer = {
            m.name: content for m, content in upstream.tar_members(raw, upstream=False)
        }
        self.assertEqual(
            outer[first["upstreams"][0]["archive"]["file"]],
            next(iter(self.fetch_map.values())),
        )
        self.assertEqual(
            outer["inputs/" + upstream.LOCK],
            self.git("show", self.revision + ":" + upstream.LOCK),
        )

    def test_working_catalog_and_lock_are_not_caller_policy(self):
        original = self.collect()
        (self.root / upstream.CATALOG).write_bytes(b"Uncommitted caller policy")
        (self.root / upstream.LOCK).write_bytes(b"Uncommitted caller lock")
        self.assertEqual(self.verify()["asset"], original["asset"])

    def test_changed_committed_package_version_or_archive_pin_is_refused(self):
        catalog = self.catalog()
        catalog["upstreams"][0]["packages"]["fixture-package"] = "2.0.0"
        self.catalog_commit(catalog)
        with self.assertRaisesRegex(InvalidRelease, "version differs"), patch.object(
            upstream, "official_fetch"
        ) as fetch:
            self.collect()
        fetch.assert_not_called()
        catalog["upstreams"][0]["packages"]["fixture-package"] = "1.0.0"
        catalog["upstreams"][0]["archive"]["sha256"] = "sha256:" + "b" * 64
        self.catalog_commit(catalog)
        with self.assertRaisesRegex(InvalidRelease, "differs from committed pin"):
            self.collect()
        self.assertFalse(self.output.exists())

    def test_bad_application_source_context_and_linked_git_policy_are_refused(self):
        for repository, version, commit in [
            ("../repo", "v1.2.3", self.revision),
            ("endorses/psst.zip", "v1.2.3-rc1", self.revision),
            ("endorses/psst.zip", "v1.2.3", "HEAD"),
            ("endorses/psst.zip", "v1.2.3", "b" * 40),
        ]:
            with self.subTest(commit=commit), self.assertRaises(InvalidRelease):
                upstream.collect(
                    self.root,
                    repository,
                    version,
                    commit,
                    self.output,
                    fetch=self.fetch_map.__getitem__,
                )
        path = self.root / upstream.CATALOG
        path.unlink()
        path.symlink_to("../web/package-lock.json")
        self.commit()
        with self.assertRaisesRegex(InvalidRelease, "regular exact committed"):
            self.collect()

    def test_linked_output_and_outer_asset_are_refused(self):
        target = self.folder / "linked-output"
        target.mkdir()
        self.output.symlink_to(target, target_is_directory=True)
        with self.assertRaisesRegex(InvalidRelease, "new real directory"):
            self.collect()
        self.output.unlink()
        first = self.collect()
        asset = self.output / first["asset"]["name"]
        original = self.folder / "original.tar.gz"
        asset.rename(original)
        asset.symlink_to(original)
        with self.assertRaisesRegex(InvalidRelease, "Unsafe"):
            self.verify()

    def test_unsafe_links_traversal_pax_and_wrong_commit_roots_are_refused(self):
        prefix = "fixture-upstream-" + "a" * 40
        for name, kind in [
            ("../outside", tarfile.REGTYPE),
            ("/absolute", tarfile.REGTYPE),
            (prefix + "/symlink", tarfile.SYMTYPE),
            (prefix + "/hardlink", tarfile.LNKTYPE),
            ("wrong-root/input", tarfile.REGTYPE),
        ]:
            stream = io.BytesIO()
            with tarfile.open(fileobj=stream, mode="w:") as archive:
                member = tarfile.TarInfo(name)
                member.type = kind
                member.linkname = (
                    "target" if kind in (tarfile.SYMTYPE, tarfile.LNKTYPE) else ""
                )
                member.size = 0
                archive.addfile(member, io.BytesIO())
            self.replace_payload(gzip.compress(stream.getvalue(), mtime=0))
            with self.subTest(name=name), self.assertRaises(InvalidRelease):
                self.collect()
        stream = io.BytesIO()
        with tarfile.open(
            fileobj=stream,
            mode="w:",
            format=tarfile.PAX_FORMAT,
            pax_headers={"path": "../rewrite"},
        ) as archive:
            member = tarfile.TarInfo(prefix + "/input")
            archive.addfile(member)
        self.replace_payload(gzip.compress(stream.getvalue(), mtime=0))
        with self.assertRaisesRegex(InvalidRelease, "global source commit comments"):
            self.collect()

    def linked_payload(self, links, directories=()):
        original = upstream.tar_members(
            next(iter(self.fetch_map.values())), upstream=True
        )
        stream = io.BytesIO()
        with tarfile.open(
            fileobj=stream, mode="w:", format=tarfile.USTAR_FORMAT
        ) as archive:
            for member, content in original:
                archive.addfile(member, io.BytesIO(content))
            for name in directories:
                member = tarfile.TarInfo(name)
                member.type = tarfile.DIRTYPE
                archive.addfile(member)
            for name, target in links:
                member = tarfile.TarInfo(name)
                member.type = tarfile.SYMTYPE
                member.linkname = target
                archive.addfile(member)
        return gzip.compress(stream.getvalue(), mtime=0)

    def test_original_internal_links_are_inventoried_without_following_or_extraction(
        self,
    ):
        prefix = "fixture-upstream-" + "a" * 40
        raw = self.linked_payload(
            [(prefix + "/test/compiled/test/misc", "../../misc")],
            [prefix + "/test/misc"],
        )
        self.replace_payload(raw)
        result = self.collect()
        self.assertEqual(self.verify()["asset"], result["asset"])
        inventory = upstream.inspect(raw, self.catalog()["upstreams"][0])
        link = next(item for item in inventory["members"] if item["kind"] == "symlink")
        self.assertEqual(link["link_target"], "../../misc")
        self.assertEqual(link["resolved_target"], prefix + "/test/misc")
        self.assertEqual(link["size"], 0)
        self.assertFalse((self.root / "test").exists())
        with tarfile.open(self.output / result["asset"]["name"], "r:gz") as archive:
            self.assertEqual(
                archive.extractfile(result["upstreams"][0]["archive"]["file"]).read(),
                raw,
            )
        with self.assertRaises(InvalidRelease):
            upstream.tar_members(raw, upstream=False)

    def test_links_cannot_escape_chain_recurse_or_supply_inspected_input(self):
        prefix = "fixture-upstream-" + "a" * 40
        cases = [
            ([(prefix + "/link", "../outside")], []),
            ([(prefix + "/link", "/absolute")], []),
            ([(prefix + "/link", "missing")], []),
            ([(prefix + "/link", "link")], []),
            ([(prefix + "/link", "second"), (prefix + "/second", "src/input.ts")], []),
            ([(prefix + "/src/recursive", ".")], [prefix + "/src"]),
            ([(prefix + "/src", "package.json")], []),
            ([(prefix + "/link", "src//input.ts")], []),
            ([(prefix + "/link", "src\\input.ts")], []),
        ]
        original = next(iter(self.fetch_map.values()))
        for links, directories in cases:
            self.fetch_map[next(iter(self.fetch_map))] = original
            raw = self.linked_payload(links, directories)
            with self.subTest(links=links), self.assertRaises(InvalidRelease):
                upstream.inspect(
                    raw,
                    {
                        **self.catalog()["upstreams"][0],
                        "archive": {"sha256": upstream.sha256(raw), "size": len(raw)},
                    },
                )
        self.fetch_map[next(iter(self.fetch_map))] = original
        raw = self.linked_payload([(prefix + "/alias.ts", "src/input.ts")])
        record = copy.deepcopy(self.catalog()["upstreams"][0])
        record["inspect_paths"] = ["alias.ts"]
        record["archive"].update(sha256=upstream.sha256(raw), size=len(raw))
        with self.assertRaisesRegex(InvalidRelease, "inspected inputs are missing"):
            upstream.inspect(raw, record)

    def test_exact_package_self_test_links_are_metadata_only_and_not_a_general_bypass(
        self,
    ):
        prefix = "fixture-upstream-" + "a" * 40
        path = "packages/demo/test/node_modules/current-package"
        raw = self.linked_payload(
            [(prefix + "/" + path, "../..")], [prefix + "/packages/demo"]
        )
        self.replace_payload(raw)
        catalog = self.catalog()
        record = catalog["upstreams"][0]
        with self.assertRaisesRegex(InvalidRelease, "recursive"):
            upstream.inspect(raw, record)
        record["source_fixture_links"] = {path: "../.."}
        self.catalog_commit(catalog)
        result = self.collect()
        self.assertEqual(self.verify()["asset"], result["asset"])
        inventory = upstream.inspect(raw, record)
        link = next(item for item in inventory["members"] if item["kind"] == "symlink")
        self.assertTrue(link["source_fixture_metadata_only"])
        self.assertFalse((self.root / "packages").exists())
        for registered in (
            {path: "../../.."},
            {"packages/../test/node_modules/current-package": "../.."},
            {"elsewhere/current-package": "../.."},
            {"packages/missing/test/node_modules/current-package": "../.."},
        ):
            with self.subTest(registered=registered), self.assertRaises(InvalidRelease):
                upstream.inspect(raw, {**record, "source_fixture_links": registered})

    def embedded_record(self):
        first = self.catalog()["upstreams"][0]
        second = copy.deepcopy(first)
        second.update(
            id="embedded-source",
            repository="fixture-owner/embedded-source",
            commit="b" * 40,
        )
        second["relationship"] = {
            "kind": "embedded-component",
            "name": "fixture-decoder",
            "version": "0.1.0",
            "package": "fixture-package",
        }
        prefix = "embedded-source-" + second["commit"]
        raw = upstream.tar_gzip(
            {
                prefix
                + "/package.json": json_bytes(
                    {"name": "fixture-decoder", "version": "0.1.0"}
                ),
                prefix + "/src/input.ts": b"original embedded fixture source\n",
            }
        )
        second["archive"] = {
            "file": prefix + ".tar.gz",
            "sha256": upstream.sha256(raw),
            "size": len(raw),
        }
        self.fetch_map[upstream.url(second)] = raw
        return first, second

    def test_embedded_originals_have_explicit_relationship_and_one_locked_primary(self):
        first, second = self.embedded_record()
        catalog = self.catalog()
        catalog["upstreams"] = [second, first]
        self.catalog_commit(catalog)
        result = self.collect()
        embedded = next(
            item for item in result["upstreams"] if item["id"] == second["id"]
        )
        self.assertEqual(embedded["relationship"], second["relationship"])
        self.assertEqual(self.verify()["asset"], result["asset"])
        self.assertFalse(result["package_source_association_verified"])
        self.assertNotIn(
            "node_modules/fixture-decoder",
            json.loads((self.root / upstream.LOCK).read_bytes())["packages"],
        )

    def test_case_sensitive_upstream_trees_preserve_case_without_changing_release_policy(
        self,
    ):
        self.assertEqual(
            upstream.upstream_repository("fixture-owner/jsQR"), "fixture-owner/jsQR"
        )
        with self.assertRaises(InvalidRelease):
            upstream.context("endorses/Psst.zip", "v1.2.3", self.revision)
        for name in (
            "owner/../../source",
            "owner/source?token=private",
            "owner/source#fragment",
            "owner/source/extra",
        ):
            with self.subTest(name=name), self.assertRaises(InvalidRelease):
                upstream.upstream_repository(name)

    def test_duplicate_primary_or_orphan_and_invalid_embedded_relationships_are_refused(
        self,
    ):
        first, second = self.embedded_record()
        variants = [
            [second],
            [
                first,
                {key: value for key, value in second.items() if key != "relationship"},
            ],
        ]
        for changes in (
            {"kind": "direct-package"},
            {"name": "fixture-package"},
            {"package": "other"},
            {"package": []},
            {"version": "latest"},
        ):
            invalid = copy.deepcopy(second)
            invalid["relationship"].update(changes)
            variants.append([first, invalid])
        for records in variants:
            catalog = self.catalog()
            catalog["upstreams"] = records
            self.catalog_commit(catalog)
            with self.subTest(records=records), self.assertRaises(InvalidRelease):
                self.collect()

    def test_global_commit_comment_is_checked_without_inferring_correspondence(self):
        record = self.catalog()["upstreams"][0]
        raw = next(iter(self.fetch_map.values()))
        original = upstream.tar_members(raw, upstream=True)
        for commit in ("a" * 40, "b" * 40):
            stream = io.BytesIO()
            with tarfile.open(
                fileobj=stream,
                mode="w:",
                format=tarfile.PAX_FORMAT,
                pax_headers={"comment": commit},
            ) as archive:
                for member, content in original:
                    archive.addfile(member, io.BytesIO(content))
            rewritten = gzip.compress(stream.getvalue(), mtime=0)
            pin = copy.deepcopy(record)
            pin["archive"].update(
                size=len(rewritten), sha256=upstream.sha256(rewritten)
            )
            if commit == record["commit"]:
                proof = upstream.inspect(rewritten, pin)
                self.assertIs(proof["package_output_correspondence_verified"], False)
            else:
                with self.assertRaisesRegex(InvalidRelease, "source commit differs"):
                    upstream.inspect(rewritten, pin)

    def test_source_manifest_version_difference_is_observed_and_never_approved(self):
        raw = next(iter(self.fetch_map.values()))
        members = {
            member.name: content
            for member, content in upstream.tar_members(raw, upstream=True)
        }
        package = next(name for name in members if name.endswith("/package.json"))
        value = read_json(members[package])
        value["version"] = "0.1.0"
        members[package] = json_bytes(value)
        self.replace_payload(upstream.tar_gzip(members))
        actual = self.collect()
        self.assertEqual(
            actual["upstreams"][0]["packages"], {"fixture-package": "1.0.0"}
        )
        self.assertEqual(
            actual["upstreams"][0]["observed_inputs"]["package.json"][
                "package_identity"
            ]["version"],
            "0.1.0",
        )
        self.assertIs(self.verify()["package_output_correspondence_verified"], False)

    def test_cli_failure_does_not_leak_tracebacks_or_caller_input(self):
        result = subprocess.run(
            [
                "python3",
                str(Path(upstream.__file__)),
                "--root",
                str(self.root),
                "--repository",
                "invalid-secret-caller-data",
                "--version",
                "v1.2.3",
                "--commit",
                self.revision,
                "--output",
                str(self.output),
            ],
            capture_output=True,
            timeout=10,
            env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"},
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(b"Traceback", result.stderr)
        self.assertNotIn(b"invalid-secret-caller-data", result.stderr)
        self.assertFalse(self.output.exists())

    def test_concatenated_hidden_truncated_and_expansion_limited_gzip_are_refused(self):
        valid = next(iter(self.fetch_map.values()))
        for raw in (valid + gzip.compress(b"hidden"), valid[:-10], valid + b"extra"):
            with self.subTest(size=len(raw)), self.assertRaises(InvalidRelease):
                upstream.expanded_gzip(raw)
        with self.assertRaisesRegex(InvalidRelease, "oversized"), patch.object(
            upstream, "MAX_EXPANDED", 100
        ):
            upstream.expanded_gzip(valid, maximum=100)
        tar_raw = gzip.decompress(valid)
        hidden = gzip.compress(tar_raw + b"hidden" + b"\0" * 506, mtime=0)
        self.replace_payload(hidden)
        with self.assertRaisesRegex(InvalidRelease, "Hidden or incomplete"):
            self.collect()

    def test_tampered_record_inventory_original_archive_and_hidden_outer_members_are_refused(
        self,
    ):
        first = self.collect()
        asset = self.output / first["asset"]["name"]
        original_asset = asset.read_bytes()
        record_path = self.output / upstream.RECORD
        original_record = record_path.read_bytes()
        changed = read_json(original_record)
        changed["publication_authorized"] = True
        record_path.write_bytes(json_bytes(changed))
        with self.assertRaisesRegex(InvalidRelease, "independent replay"):
            self.verify()
        record_path.write_bytes(original_record)
        outer = {
            m.name: content
            for m, content in upstream.tar_members(original_asset, upstream=False)
        }
        for name in (
            first["upstreams"][0]["inventory"]["file"],
            first["upstreams"][0]["archive"]["file"],
            "hidden.txt",
        ):
            modified = dict(outer)
            modified[name] = modified.get(name, b"") + b"changed"
            raw = upstream.tar_gzip(modified)
            asset.write_bytes(raw)
            rehashed = copy.deepcopy(first)
            rehashed["asset"].update(digest=upstream.sha256(raw), size=len(raw))
            record_path.write_bytes(json_bytes(rehashed))
            with self.subTest(name=name), self.assertRaises(InvalidRelease):
                self.verify()
        asset.write_bytes(original_asset)
        record_path.write_bytes(original_record)
        (self.output / "unrecorded-asset").write_bytes(b"hidden")
        with self.assertRaisesRegex(InvalidRelease, "Hidden or missing"):
            self.verify()

    def test_official_fetch_uses_fixed_https_and_refuses_redirects(self):
        response = unittest.mock.Mock()
        response.status = 302
        connection = unittest.mock.Mock()
        connection.getresponse.return_value = response
        with patch.object(
            upstream.http.client, "HTTPSConnection", return_value=connection
        ) as client:
            with self.assertRaisesRegex(InvalidRelease, "redirects are refused"):
                upstream.official_fetch(next(iter(self.fetch_map)))
        client.assert_called_once_with("codeload.github.com", timeout=30)
        self.assertNotIn("Authorization", str(connection.request.call_args))
        connection.close.assert_called_once()
        for candidate in (
            "https://evil.invalid/archive",
            "https://codeload.github.com/owner/repo/tar.gz/main",
            "https://codeload.github.com@evil.invalid/owner/repo/tar.gz/" + "a" * 40,
        ):
            with self.subTest(candidate=candidate), self.assertRaises(InvalidRelease):
                upstream.official_fetch(candidate)


if __name__ == "__main__":
    unittest.main()
