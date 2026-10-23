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
  --web-builder-config "$WEB_BUILDER_CONFIG" --source-root "$SOURCE_CHECKOUT" \
  --dependency-collection "$DEPENDENCY_COLLECTION" \
  --output "$NEW_OUTPUT_DIRECTORY"
```

Build the source helper from `tools/runtime-sources.Dockerfile` using the shared
resolved Alpine index and pass its actual configuration digest. Cosign must match
the native executable pin in `verify_caddy_source_signatures.py`. The local
`default` Buildx builder must use the Docker driver; native host and daemon
architecture must match. ARM64 requires a native ARM64 runner.

The output retains the original build record and save, complete runtime source
pack, final Docker save, two OCI archives, smoke report, native measurement, source
replay, observed browser builder inputs and their verification, and a schema-2
file/hash/size descriptor. The descriptor is written only after all
checks pass. Failure may leave diagnostic inputs in the new output directory;
reconcile these before starting a fresh attempt. Temporary image aliases are
removed without pruning shared Docker caches.

OCI export preserves the original configuration and uncompressed layer bytes.
A general Docker-to-OCI converter can reserialize otherwise equivalent JSON and
change the configuration digest. The exporter verifies the resulting OCI graph
against the exact configuration IDs used by the smoke checks.

Load the `web-build` stage from the same cached Docker build and pass its actual
configuration digest as `WEB_BUILDER_CONFIG`. Collect application dependencies
before preparation and keep the exact Git checkout available. Browser capture
runs a fixed selection script as an unprivileged user with no network, retaining
observed application/package/generated inputs, Vite metadata and static outputs.
Replay checks application bytes against committed Git blobs, package bytes against
integrity-bound npm archives and static bytes against the final tested OCI image.
Transfer and recovery require this evidence and independently replay it. Historical
schema-1 fixtures need an explicit library opt-in; the current CLI has no bypass.
These observations still leave complete browser source closure and upstream build
reproduction pending.

Complete local AMD64 preparation on committed `f90be19b543b31ef7738187a61ceb45aec518680`
passed in 368.9 seconds, including source/signature checks, final native smoke,
byte-preserving OCI export and schema-2 browser input replay. The browser evidence
pack was 18,688,000 bytes, SHA256
`d3091d7de9fad5d5e8b58d484ced29881d3d235b06e0bb6b847ca2b2024a66a9`.
Independent browser replay with the exact verifier from that commit passed in
13.7 seconds: 98 Git inputs, 16 npm archives and 99 final static files. Private
evidence remains in `/tmp/psst-native-browser-pair-review-f90be19b543b/`; disposable
tool caches and owned image tags were removed. The source-helper image was rebuilt
before resuming preparation because its earlier temporary alias had been removed.

That evidence predates the expanded static/generator recipe associations below.
It does not establish their new committed final-image path, native ARM64,
authenticated reports or publication approval.

The expanded profile Git-binds the original copied `appearance.js`/`language.js`,
application template and reviewed `browser-source-recipes.json`. It retains
integrity-bound Vite/Kit generator recipes and manifests. Finite safe hash/path
grammars associate the current 23 virtual inputs and 11 generated Kit inputs,
distinguishing five rendered files from six excluded ones. Unfamiliar origins
remain explicitly unresolved, and every reproduction/closure/authorization flag
stays false. The final helper-license copy must equal its original locked npm
member. Current installed recipe/notice replay against the retained dependency
archive passed in 1.5 seconds; this does not verify the new committed final image.

The profile also captures all copied static originals, not only JavaScript,
and the locked static adapter's manifest, `index.js` and `platforms.js`. Its one
known generated static input, `licenses/release.json`, must match the exact native
repository/version/revision/license/notice identity and built copy. Other untracked
static files remain rejected. The Dockerfile is a Git-bound recipe input.

A real AMD64 builder at `e3a5c8354b862647331d11298e5fc18759607c13` built in
20.9 seconds; captured-input verification passed in 1.8 seconds with 122 Git
inputs, 18 npm archives, 21 unchanged static originals, 23 virtual associations
and 11 generated associations. The adapter's two implementation files also
matched the full pinned Kit originals. The 17,972,224-byte capture has SHA256
`55891d37cdb5b62d3f308cf868ce43dee06bdcde3c80cfedb1e173b8b55b003e`.
A field-name error in the diagnostic's dependency descriptor reader was corrected
before resuming replay of the retained capture; no rebuild or download was needed.
Private evidence is under `/tmp/psst-browser-static-review-e3a5c8354b86/`.
The owned container/image tag and temporary replay directory were removed.
This focused check verifies builder inputs, not the current final native OCI pair,
complete source coverage, hosted ARM64, transfer/recovery or approval.

The records remain unsigned and do not authorize publication. Both architectures,
authenticated measurements, vulnerability gates, full corresponding-source and
distribution review, and post-matrix release assembly are still required.

## Application dependency inputs

Each collection has an architecture-specific asset name, for example
`psst.zip-dependency-inputs-v0.1.0-amd64.tar.gz`. After both native jobs,
`prepare_release_inputs.py` requires `--dependencies PLATFORM=DIRECTORY` and
`--source-scan PLATFORM=MEASUREMENT` for each platform. It independently replays
package inputs against the exact committed locks and raw source-scanner receipts,
then copies the verified runtime and dependency archives into its output. It also
requires `--upstream DIRECTORY`, produced by the upstream source collector below.
The common upstream archive is replayed against the exact tracked catalog and web
lock before it is copied and bound. The result retains eight release assets and
binds fourteen subjects. These checks establish package integrity and retention;
upstream preferred-source review, measurement authentication and public delivery
remain required.

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

The workflow first runs exact source CI, then both native runners execute source
preparation, final smoke, fresh source/image scans, compiler correspondence and
application dependency retention. Successful native jobs now upload separately
checked retained inputs for post-matrix assembly and recovery; bounded diagnostic
summaries are still uploaded separately on failure. Compiler summaries keep a
distinct partial-evidence kind and cannot substitute for full authenticated inputs
in final gate aggregation.

The workflow retains read-only repository permissions and disabled checkout
credentials. Local actionlint, shell/Python syntax checks, thirteen real-Git
candidate tests and replay of the report copier against all thirteen actual
AMD64 records passed. The copier retained every finding without truncation;
malformed, oversized, linked and protected-data records were rejected. Actual
hosted execution of this extended workflow remains pending.

## Retained inputs across hosted jobs

`tools/prepare_candidate_transfer.py` stages only a fixed, validated inventory:
all twelve native descriptor artifacts, manifest-owned legal overlays, exact source
scanner raw receipts, both compiler/advisory/signature proofs, both final-image
scanner reports, and the architecture-specific dependency archive. It preserves
relative names so the descriptor remains valid after download. Tools, caches,
private execution diagnostics and privileged recovery state are excluded. Declared
scanner stderr receipts remain included because they are hashed replay inputs.

The metadata binds every retained file's actual size and SHA256 to repository,
version, commit, platform and source kind. Download validation checks the entire
inventory, record references and unexpected files. These are structural facts;
`publication_authorized` remains false and authentication remains required.

Each successful native matrix job uploads a separately named artifact containing
its staged tree. The common pinned upstream inputs are collected once. Artifact
names include the exact run ID and attempt; consumers download explicit names into
separate architecture directories. They do not merge wildcard results or retrieve
inputs from earlier runs. Retention is one day for these candidate exercises.

The assembly job validates both transfers before running release-input assembly.
For a planned main dispatch, assembly requires the real main ref/event/source,
trusted origin, clean tracked checkout, main ancestry and absence of the planned
version tag. It creates no Git reference and emits `planned-candidate-inputs` with
unverified source-CI/signer flags. Tagged preparation still requires the reviewed
actual tag; the publisher retains its mandatory tag and authenticated-gate checks.
The internal bundle profile describes its updater contents, not approval to deploy.

After signed assembly, tagged AMD64 and ARM64 recovery jobs authenticate the
completed corresponding-source and final-image-smoke reports for the exact
binding, run and attempt. They download both small descriptors, the manifest,
deployment bundle and two indices, plus only their own saved Docker pair and two
OCI exports. The other architecture's image facts come from authenticated smoke
evidence. Each runner independently checks its selected OCI bytes, tested
configurations and all loaded layer diff IDs; changed inputs or extra native
projection files fail. Publication still retains all complete source archives.
Unsigned planned candidates retain the full two-tree validation path.

Each recovery job executes the prepared candidate on its own native runner. It measures
ordinary and originally paused upgrade/reapply/isolated restore and injected
startup failure, using the exact same candidate version for reapplication. Only
terminal sanitized measurement JSON is uploaded, after bounded fixture cleanup.
These exercises cannot establish public provenance, an independent off-host
provider restore or browser/mobile behavior.

Ten transfer fixtures passed, covering relocation, incomplete raw/overlay/source
inputs, links, hidden files, unsafe paths and copy-time substitutions. Actual
AMD64 staging and relocation replay also passed on source
`c73a5da9bbbec4fb5de63586eb498879ec73a28c`: 112 retained files totaling 939,932,806
bytes. The 19,431-byte transfer metadata SHA256 was
`b84657993ac965f666096433c56233c49726f69253845f4aee220273cb8d0a0f`.
Only disposable transport copies were removed; the original measured inputs
were preserved. Fifteen assembly tests passed, including planned ref/event/version
and checkout guards; twenty-one publication tests preserve the tag-only boundary.

When retrying a hosted candidate, rerun the complete workflow so every prerequisite
produces artifacts for the same attempt. Rerunning only failed jobs can leave
prerequisite artifacts at an earlier attempt; explicit download names reject that
mixture rather than silently reuse earlier results.

The original full-input hosted transfer/assembly/recovery path passed on both
architectures in `v0.1.2`, run `37799619020`, attempt 1. The compact tagged
projection above and four-worker native signature verification have passed focused
local regressions; their hosted execution and elapsed-time improvement remain
pending the next necessary release attempt. They do not establish public delivery
or authorize production deployment.

## Upstream application source inputs

The root Docker context explicitly permits the browser inventory plugin imported
by Vite while excluding other web scripts and host-generated inputs. A disposable
pinned-base AMD64 web build with this corrected context completed in 21.6 seconds,
and the final image retained the inventory plus embedded Apache and supplemental
MIT notices. Private diagnostic evidence is in
`/tmp/psst-web-docker-context-review-c9e636d/`. Its owned container/image were
removed; this `dev`/`main` working-tree build is not a release-source proof.
Native preparation still needs retained original builder inputs and final tested
configuration correspondence; the local static verifier alone does not provide it.

Some locked npm archives contain generated JavaScript but omit original source or
build scripts. `tools/upstream-application-sources.json` pins immutable full-commit
source archives for twelve upstream inputs, their original hashes and sizes, and the exact
locked package versions they are being investigated for. The catalog is source
policy, not a completeness or build-reproduction approval. Version changes require
reviewing and updating those source pins.

```sh
python3 tools/package_upstream_application_sources.py --mode collect \
  --root . --repository endorses/psst.zip --version v0.1.0 \
  --commit "$SOURCE_COMMIT" --output "$NEW_UPSTREAM_DIRECTORY"
python3 tools/package_upstream_application_sources.py --mode verify \
  --root . --repository endorses/psst.zip --version v0.1.0 \
  --commit "$SOURCE_COMMIT" --collection "$NEW_UPSTREAM_DIRECTORY" \
  --output "$NEW_UPSTREAM_REPLAY_JSON"
```

The collector reads both the catalog and npm lock from the exact Git commit. It
retains untouched upstream archives, those tracked inputs, and a measured source
inventory in one deterministic `psst.zip-upstream-inputs-v0.1.0.tar.gz`. It does not
extract archives or execute upstream package scripts. Replay derives the expected
contents again from the committed policy and actual archive bytes. Substituted
archives, unsafe paths, changed locks and false approval flags are rejected.

One primary upstream is required for each selected locked package. The decoder
has a distinct embedded-component relationship to qr-scanner, so its original
source can accompany that exact scanner without claiming a separate app-lock
entry. The collector preserves GitHub source-tree case independently of the
lowercase trusted release-repository policy.

Original internal symbolic links are inventoried as metadata, never extracted or
followed. Targets must remain inside the full-commit root and exist as regular
files or directories; links cannot chain, escape, recurse or supply an inspected
source file. Four noble test links and four framework links passed this check. Outer offering links
and hard links remain rejected. Acquisition is bounded at 64 MiB per original
archive, 128 MiB per expanded archive and 128 MiB for the offering. These bounds
retain the decoder's 45,404,815-byte original test/source tree and all twelve
90,991,337 bytes of original inputs without dropping fixtures from their sources.

Post-matrix assembly makes this archive mandatory and uses the replay's measured
asset digest when copying it, including a check for changes after replay. Signing,
publication and anonymous retrieval cover this additional asset through the same
full release binding. Full source completeness and reproduction remain pending;
see the [package source review](application-package-source-review.md).

## Standard hosted final signing

Final signing runs as a normal pinned `actions/attest` workflow step in the single
protected publication job. Preparation supplies exact file and image subjects
through the supported checksum input; publication verifies standard provenance
directly before exposing the immutable release. Repository tooling does not
execute downloaded action JavaScript or discover runner Node runtimes.

See [container publication](container-publication.md) for the approval, provenance,
readback and partial-failure boundaries. Local native measurements retain their
existing cross-job authentication; retiring the custom signing bridge does not
turn unsigned incoming measurements into trusted evidence.
