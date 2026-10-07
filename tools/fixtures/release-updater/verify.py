#!/usr/bin/python3
"""Protected fixture hook; every returned observation follows a real assertion."""

import argparse
import importlib.util
import json
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--transaction", type=Path, required=True)
parser.add_argument("--compose", type=Path, required=True)
args = parser.parse_args()
spec = importlib.util.spec_from_file_location("fixture_flows", "/opt/fixture/flows.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
try:
    report = module.Probe(
        json.loads(args.compose.read_text()), json.loads(args.transaction.read_text())
    ).verify()
except BaseException:
    import traceback

    error = Path("/opt/fixture/flow-error.log")
    error.write_text(traceback.format_exc())
    error.chmod(0o600)
    raise
print(json.dumps(report))
