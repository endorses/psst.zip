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

## Application dependency inputs

Each collection has an architecture-specific asset name, for example
`psst.zip-dependency-inputs-v0.1.0-amd64.tar.gz`. After both native jobs,
`prepare_release_inputs.py` requires `--dependencies PLATFORM=DIRECTORY` and
`--source-scan PLATFORM=MEASUREMENT` for each platform. It independently replays
package inputs against the exact committed locks and raw source-scanner receipts,
then copies the verified runtime and dependency archives into its output. The
result retains seven release assets and binds thirteen subjects. These checks
establish package integrity and retention; upstream preferred-source review,
measurement authentication and public delivery remain required.

The current collector and independent verifier were exercised on actual AMD64
inputs from `c73a5da`: 35 Go modules, 173 npm packages and nine additional sums.
The 249,889,401-byte archive has SHA256
`64185cd10474cf2ed71cf9f437d1474b0429d97c1f2b261d4d4e18280cd7b583`.
Its independent replay record has SHA256
`8764f15de8f60bc0c2ec72b0512ab47c241feb94a868c5e54ceda3b5cd0d482a`.
This local record is unsigned and supplies no ARM64 or distribution approval.

Retain the original package inputs selected by the committed application locks:

```sh
python3 tools/package_application_dependencies.py \
  --root . --repository endorses/psst.zip --version v0.1.0 \
  --commit "$SOURCE_COMMIT" --platform linux/amd64 \
  --go-image "$RESOLVED_GO_INDEX" --output "$NEW_DEPENDENCY_DIRECTORY"
```

The collector reads the three committed lock files through Git, uses the official
pinned Go builder with the public checksum database, and retains every selected
Go module ZIP, module file and version record. Original sums remain unchanged;
additional sums needed for the full graph are retained separately. It verifies
the original ZIP/module H1 checksums independently. Every npm registry archive is
checked against lock SHA512 integrity, including development and optional
platform packages; no package scripts run.

The archive also retains the original locks, expanded authenticated sums, raw Go
download/module records, tool settings and a checksum inventory. Untracked files
are excluded. Package distribution inputs can contain generated files, so this
collector explicitly leaves preferred-form upstream source review and
publication approval pending.

A real collection from `2cc2f72` retained 35 Go modules and 173 npm packages in a
249,889,564-byte archive, SHA256
`c6856650ae00b50297971f53e46122867b4701107d8c95ca47b2742f0997d86e`.
Nine focused tests passed, including changed/missing original sums, unsupported
registry inputs, checksum/identity failures, unsafe archives and untracked-file
exclusion. The temporary download/build cache was removed after collection;
the private review archive remains retained.

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
AMD64 preparation subsequently passed on exact committed source `c73a5da`, using
genuine Buildx records and its original saved application pair. Source collection,
fresh Caddy signatures, overlays, all ten native application checks, byte-preserved
OCI export and independent runtime source replay completed. The native measurement
SHA256 is `88946ba4ca737b189019f5db6c25f1497425e12c684fe3232c27335012c69dbf`;
the corresponding runtime source archive SHA256 is
`9feb647140d603bb6b81a3495cf7ae6cd90bb8f2d37ab5d7dd9ead3a978f4515`.
These are private planned-version inputs, not a published release. Native ARM64
execution, authenticated measurements and final publication gates remain pending.

The corrected source/compiler/scanner commands also passed on those exact AMD64
inputs: source scanning retained 21 module-only Go findings and zero npm findings;
the 236-package backend graph reproduced the executable bytes, and Caddy's
970-package graph matched its signed source/binary and 147 embedded module pairs.
Fresh official database scans retained all 21 backend and one Caddy module
finding, with zero OS findings. These unsigned measurements remain separate from
authenticated finding dispositions and release approval.

## Read-only hosted verification

After the workflow changes are pushed, run native verification before creating
the first release tag:

```sh
gh workflow run release.yml --ref main -f planned_version=v0.1.0
```

The dispatch path requires the exact checked-out main commit, main ancestry and
an unused strict version. It creates no tag. The existing tag-push path still
validates the exact tag and event commit. A main dispatch cannot establish the
tagged source-CI gate needed for publication.

Both native runners execute exact source CI, source preparation, final smoke,
fresh source/image scans, compiler correspondence and application dependency
retention. Only bounded candidate JSON records and full scanner JSON are uploaded;
image/source payloads and private diagnostics are removed after measurement.
Compiler summaries have a distinct partial-evidence kind and cannot substitute
for the full authenticated inputs needed by final gate aggregation.

The workflow retains read-only repository permissions and disabled checkout
credentials. Local actionlint, shell/Python syntax checks, thirteen real-Git
candidate tests and replay of the report copier against all thirteen actual
AMD64 records passed. The copier retained every finding without truncation;
malformed, oversized, linked and protected-data records were rejected. Actual
hosted execution of this extended workflow remains pending.
