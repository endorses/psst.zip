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
all ten native descriptor artifacts, manifest-owned legal overlays, exact source
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

After assembly, native AMD64 and ARM64 recovery jobs each validate both retained
trees and execute the prepared candidate on their own native runner. They measure
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

The complete hosted transfer/assembly/recovery pipeline remains unrun until the
reviewed checkpoint is pushed and dispatched. A prior AMD64 source preparation
or a fixture test does not prove the new ARM64 or hosted path.

## Upstream application source inputs

Some locked npm archives contain generated JavaScript but omit original source or
build scripts. `tools/upstream-application-sources.json` pins immutable full-commit
Lucide, fflate and hpke archives, their original hashes and sizes, and the exact
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

Post-matrix assembly makes this archive mandatory and uses the replay's measured
asset digest when copying it, including a check for changes after replay. Signing,
publication and anonymous retrieval cover this additional asset through the same
full release binding. Full source completeness and reproduction remain pending;
see the [package source review](application-package-source-review.md).

## Official hosted signing bridge

`tools/github_release_attestor.py` supplies the publication driver's concrete
`WorkflowAttestor`. It executes the bundled Node action from
[actions/attest at immutable commit 1e69f48acb82d1966a394da916b4c1698aa569d6](https://github.com/actions/attest/tree/1e69f48acb82d1966a394da916b4c1698aa569d6).
All executable inputs are checked before and after each invocation against these
SHA256 pins:

| Official file   |     Bytes | SHA256                                                             |
| --------------- | --------: | ------------------------------------------------------------------ |
| `action.yml`    |     4,048 | `9e4a1b808433f9ec87120b534e11fc35469a039bdbdc62b019441444c9ad0449` |
| `package.json`  |     2,757 | `221767f43cc74afbd71dc9531af4e8e7494d94f3530acfbd749e09105c7d2c8d` |
| `dist/index.js` | 4,868,977 | `3ca89e06ffcb09ff97e9b1633575865cad0b23310ac82176d72990d7554836b5` |

The action declares Node24. The bridge discovers that runtime only under the
official hosted runner installation roots, verifies its native Linux architecture
and Node24 version, and measures its executable SHA256. It checks the executable
again around signing; it does not accept a caller-selected signing command or an
ambient `PATH` runtime. The action's embedded package version is `4.1.0`; the
immutable commit and file hashes identify the code, rather than that version
string or the wrapper's release comment.

Construction requires the exact public repository tag-push event, source and
workflow commit, `publish` job, hosted runner and positive run/attempt IDs. It
also requires the real GitHub OIDC request URL/token and official GitHub API
endpoints. It only downloads pinned public source and prepares private files;
signing starts inside the driver's journaled publication lease. The publication
job needs `id-token: write` and `attestations: write`, in addition to its separately
reviewed release/registry permissions. No local fixture obtains an OIDC identity.

Each action invocation receives only checked context, a private event snapshot,
private output/home/temp paths, explicit GitHub token and exact subject inputs.
Proxy settings, runtime token overrides, arbitrary `INPUT_*`, `NODE_OPTIONS`,
Sigstore identity overrides and ambient registry credentials are excluded.
Action stdout/stderr and Actions commands are drained within size/time bounds and
withheld from logs. A zero exit code or bundle path is insufficient: the bundle
must contain the expected subject and default SLSAv1 source/workflow/run facts,
and the independent GitHub CLI must verify certificate identity, issuer, exact
source/ref and hosted-runner policy against the freshly generated bundle via
`--bundle`. A separate default API lookup verifies that the same exact subject
can be retrieved from GitHub. Predicate contents do not replace that certificate
verification.

The bridge signs each exact readonly file snapshot, both image indexes, all four
native child manifests and every corresponding-source asset. Image repository
names come from the checked manifest, including the distinction between the
`psst.zip` GitHub repository and `psst-zip-*` GHCR packages. It creates
`provenance.json` only after all subjects verify, signs those exact full-binding
report bytes, then independently verifies the report again. Readback reports
follow the same snapshot/sign/verify boundary. File changes, source/hash mismatch,
missing subjects, malformed output and action/verifier failure stop publication;
the driver journals uncertain remote writes without automatic retry.

The action uses GitHub's attestation API with registry attachment and optional
storage-record creation disabled. The
[GitHub CLI documentation](https://cli.github.com/manual/gh_attestation_verify#loading-artifacts-and-attestations)
confirms that default OCI verification fetches attestations from GitHub's API;
registry referrer lookup requires the separate `--bundle-from-oci` flag. OCI
manifest metadata still comes from the registry. A fresh verification home
contains no Docker/Podman credential configuration, so public package metadata
must be available anonymously; otherwise verification fails. This does not
establish live anonymous availability before the driver's actual readback.

Sixteen focused offline tests passed, covering complete subject coverage,
namespace differences, source/runtime substitutions, exact context/event checks,
environment isolation, output escape/link rejection and process failure/timeouts.
The actual downloaded immutable action passed all three hash checks and a syntax
check under the local Node26 runtime; that syntax check did not execute the action
or validate hosted Node24 behavior.

- [ ] Run the bridge in the exact hosted tag publication job with actual OIDC.
- [ ] Verify the resulting file/image/report attestations with the independent
      exact-source policy and retain journaled outcomes.
- [ ] Complete all other authenticated publication gates and anonymous readbacks
      before claiming a published deployment-ready release.
