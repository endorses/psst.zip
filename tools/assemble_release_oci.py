"""Thin native/recovery adapter for the shared Go OCI verifier.

No OCI graph, layer, label or index policy is implemented in Python. The retained
StrictTarInfo class only bounds the separate Docker-save reader used by recovery.
"""

from __future__ import annotations

import tarfile
import tempfile
from pathlib import Path

from prepare_release_candidate import IMAGE_TYPES as IMAGE_TYPES
from prepare_release_candidate import _invoke
from release_artifacts import PLATFORMS as PLATFORMS
from release_artifacts import json_bytes, read_json, require


class StrictTarInfo(tarfile.TarInfo):
    def _proc_member(self, archive):
        require(
            self.type in {tarfile.REGTYPE, tarfile.AREGTYPE, tarfile.DIRTYPE},
            "Docker save contains unsupported headers or entry types",
        )
        return super()._proc_member(archive)


def inspect_archive(
    path: Path,
    *,
    platform: str,
    repository: str,
    version: str,
    commit: str,
    tested_config: str,
    component: str,
) -> dict:
    return read_json(
        _invoke(
            Path(__file__).resolve().parents[1],
            "inspect-oci",
            "--archive",
            str(path.absolute()),
            "--platform",
            platform,
            "--repository",
            repository,
            "--version",
            version,
            "--commit",
            commit,
            "--tested-config",
            tested_config,
            "--component",
            component,
        )
    )


def assemble(
    archives: dict[str, Path],
    tested_configs: dict[str, str],
    *,
    repository: str,
    version: str,
    commit: str,
    output: Path,
) -> dict:
    with tempfile.TemporaryDirectory(prefix="psst-oci-inputs-") as temporary:
        inputs = Path(temporary) / "inputs.json"
        inputs.write_bytes(
            json_bytes(
                {
                    "repository": repository,
                    "version": version,
                    "commit": commit,
                    "archives": {
                        key: str(path.absolute()) for key, path in archives.items()
                    },
                    "tested_configs": tested_configs,
                    "output": str(output.absolute()),
                }
            )
        )
        return read_json(
            _invoke(
                Path(__file__).resolve().parents[1],
                "assemble-oci",
                "--inputs",
                str(inputs),
            )
        )
