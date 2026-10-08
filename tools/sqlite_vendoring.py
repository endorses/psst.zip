"""Bounded standard-library syntax verification; never execute upstream generators."""

from contextlib import contextmanager
import os
from pathlib import Path
import subprocess
import tempfile

from release_artifacts import DIGEST, fields, matches, read_json, require
from publish_container_release import sha256

MAX_SOURCE = 40 * 1024**2


class VendoringVerifier:
    def __init__(self, source, directory):
        require(
            isinstance(source, bytes) and 0 < len(source) <= 128 * 1024,
            "Invalid committed vendoring verifier source",
        )
        self.source_sha256 = sha256(source)
        self.directory = directory
        self.program = directory / "comparator.go"
        self.program.write_bytes(source)
        self.binary = directory / "comparator"
        self.built = False
        self.environment = {
            **os.environ,
            "GOTOOLCHAIN": "local",
            "GO111MODULE": "off",
            "GOWORK": "off",
            "GOFLAGS": "",
            "GOPROXY": "off",
            "GOSUMDB": "off",
            "CGO_ENABLED": "0",
        }

    def run(self, arguments, timeout):
        try:
            result = subprocess.run(
                arguments,
                cwd=self.directory,
                env=self.environment,
                capture_output=True,
                timeout=timeout,
                check=False,
            )
        except (OSError, subprocess.TimeoutExpired):
            require(
                False,
                "Vendoring syntax verifier unavailable or exceeded its time budget",
            )
        require(
            result.returncode == 0,
            "Vendoring syntax verifier rejected source inputs or could not compile",
        )
        require(len(result.stdout) <= 4096, "Vendoring verifier output exceeds bounds")
        return result.stdout

    def __call__(self, original, vendored):
        require(
            all(
                isinstance(raw, bytes) and 0 < len(raw) <= MAX_SOURCE
                for raw in (original, vendored)
            ),
            "Invalid vendoring source input bytes",
        )
        if not self.built:
            self.run(
                ["go", "build", "-trimpath", "-o", str(self.binary), str(self.program)],
                60,
            )
            self.built = True
        origin = self.directory / "original.go.input"
        target = self.directory / "vendored.go.input"
        origin.write_bytes(original)
        target.write_bytes(vendored)
        try:
            result = read_json(
                self.run([str(self.binary), str(origin), str(target)], 45)
            )
        finally:
            origin.unlink(missing_ok=True)
            target.unlink(missing_ok=True)
        fields(
            result,
            {
                "kind",
                "expected_structural_sha256",
                "target_structural_sha256",
                "original_sha256",
                "vendored_sha256",
                "added_alias_count",
                "generated_output_reproduction_verified",
            },
            "vendoring syntax comparison",
        )
        for name in (
            "expected_structural_sha256",
            "target_structural_sha256",
            "original_sha256",
            "vendored_sha256",
        ):
            matches(result[name], DIGEST, "Invalid vendoring comparison checksum")
        require(
            result["kind"] == "sqlite-vendoring-go-ast-correspondence"
            and result["expected_structural_sha256"]
            == result["target_structural_sha256"]
            and result["original_sha256"] == sha256(original)
            and result["vendored_sha256"] == sha256(vendored)
            and type(result["added_alias_count"]) is int
            and result["added_alias_count"] > 0
            and result["generated_output_reproduction_verified"] is False,
            "Vendoring comparison does not bind the supplied source bytes",
        )
        return {**result, "verifier_source_sha256": self.source_sha256}


@contextmanager
def build_verifier(source):
    """Compile selected committed checker once, reuse it, and remove private inputs."""
    with tempfile.TemporaryDirectory(prefix="psst-sqlite-vendoring-") as temporary:
        yield VendoringVerifier(source, Path(temporary))
