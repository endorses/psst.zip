# Native release preparation

`tools/prepare_native_release.py` prepares one native architecture from a genuine
`prepare_release_candidate.py record-build` result and its original Docker save.
It collects the retained Alpine package sources and signed Caddy sources, builds
the notice/source overlays, runs the final application smoke checks, exports the
tested images, and independently replays their source correspondence.

Use the selected repository, version, full commit, and native platform throughout:

```sh
python3 tools/prepare_native_release.py \
  --repository endorses/psst.zip --version v0.1.0 --commit "$SOURCE_COMMIT" \
  --platform linux/amd64 \
  --build-record "$BUILD_RECORD" --original-archive "$ORIGINAL_SAVE" \
  --helper-config "$SOURCE_HELPER_CONFIG" --cosign "$PINNED_COSIGN" \
  --output "$NEW_OUTPUT_DIRECTORY"
```

Build the source helper from `tools/runtime-sources.Dockerfile` using the shared
resolved Alpine index and pass its actual configuration digest. Cosign must match
the native executable pin in `verify_caddy_source_signatures.py`. The local
`default` Buildx builder must use the Docker driver; native host and daemon
architecture must match. ARM64 requires a native ARM64 runner.

The output retains the original build record and save, complete runtime source
pack, final Docker save, two OCI archives, smoke report, native measurement, source
replay, and a file/hash/size descriptor. The descriptor is written only after all
checks pass. Failure may leave diagnostic inputs in the new output directory;
reconcile these before starting a fresh attempt. Temporary image aliases are
removed without pruning shared Docker caches.

OCI export preserves the original configuration and uncompressed layer bytes.
A general Docker-to-OCI converter can reserialize otherwise equivalent JSON and
change the configuration digest. The exporter verifies the resulting OCI graph
against the exact configuration IDs used by the smoke checks.

The records remain unsigned and do not authorize publication. Both architectures,
authenticated measurements, vulnerability gates, full corresponding-source and
distribution review, and post-matrix release assembly are still required.

## Validation checkpoint

The real native adapter check built two disposable scratch images, saved them,
exported them, and independently inspected both OCI graphs with unchanged
configuration/layer identities:

```sh
PSST_NATIVE_EXPORT_INTEGRATION=1 PYTHONPATH=tools \
  python3 -m unittest test_native_release_preparation.NativeDockerExport
```

That check passed on AMD64. Twelve focused preparation tests also passed; their
collection and application smoke operations use explicit fixtures. Complete
preparation from authentic application build records and native ARM64 execution
remain separate pending checks.
