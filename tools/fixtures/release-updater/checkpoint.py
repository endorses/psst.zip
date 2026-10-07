#!/usr/bin/python3
"""Fixture encrypted export; separate store simulates off-host, not a provider."""

import argparse
import hashlib
import io
import json
import os
import tarfile
from datetime import datetime, timezone
from pathlib import Path
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

parser = argparse.ArgumentParser()
parser.add_argument("--checkpoint", type=Path, required=True)
checkpoint = parser.parse_args().checkpoint
store = Path("/opt/export-store")
store.mkdir(mode=0o700, exist_ok=True)
keyfile = store / "key"
if not keyfile.exists():
    keyfile.write_bytes(os.urandom(32))
    keyfile.chmod(0o600)
stream = io.BytesIO()
with tarfile.open(fileobj=stream, mode="w") as archive:
    archive.add(checkpoint, arcname="checkpoint")
plain = stream.getvalue()
key = AESGCM(keyfile.read_bytes())
nonce = os.urandom(12)
sealed = nonce + key.encrypt(nonce, plain, checkpoint.name.encode())
export = store / (checkpoint.name + ".aesgcm")
export.write_bytes(sealed)
export.chmod(0o600)
reopened = export.read_bytes()
restored = key.decrypt(reopened[:12], reopened[12:], checkpoint.name.encode())
assert restored == plain
with tarfile.open(fileobj=io.BytesIO(restored)) as archive:
    data = archive.extractfile("checkpoint/checkpoint.json").read()
assert data == (checkpoint / "checkpoint.json").read_bytes()
print(
    json.dumps(
        {
            "checkpoint_sha256": hashlib.sha256(data).hexdigest(),
            "encrypted_off_host_receipt": "fixture separate-store AES-256-GCM: "
            + hashlib.sha256(sealed).hexdigest(),
            "restore_exercise": "authenticated decrypt and archive checkpoint exact-byte restore: "
            + hashlib.sha256(restored).hexdigest(),
            "verified_at": datetime.now(timezone.utc).isoformat(),
        }
    )
)
