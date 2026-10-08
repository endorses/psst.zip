# Runtime source collection before publication

Runtime collection is a release preparation gate. Its inventories always report
`review_required: true`; a successful collection does not approve publication.
No collector accesses production volumes, operator credentials or the Docker
socket from inside a source helper.

The application images inherit Alpine packages and the web image inherits Caddy,
its Go runtime and distribution assets. Application-only dependency inventories
do not account for those components. Source packages and notices must be retained
and authenticated alongside the released image pair. Runtime source archives are
separate release assets, not members of the bounded deployment bundle.

## Alpine sources

Build the local helper and select a freshly built final image:

```sh
docker build --file tools/runtime-sources.Dockerfile --tag psst-runtime-source-helper:local tools
python3 tools/collect_runtime_notices.py \
  --image psst-release-backend:local \
  --helper-image psst-runtime-source-helper:local \
  --output /private/release-review/backend-runtime
```

Python 3.11 or later, Docker and network access to Alpine/GitHub source hosts are
required. Output directories must not already exist. Remove only your helper tag
and temporary collection outputs after validation; preserve reviewed source
outputs used for an actual release.

The collector reads the final APK database and APK databases retained in saved
lower layers. Upgraded package bytes in lower layers still require source/notice
accounting. Each package records its actual name, version, architecture, declared
license, origin, packaging commit and APK checksum. It resolves the complete
origin recipe at that exact aports commit and verifies every fetched Git blob.

APKBUILD executes only in an unprivileged, read-only, capability-free container
with bounded scratch space and its own fresh writable output. The helper has no
application checkout, deployment state, registry credential or GitHub token.
`abuild fetch srcpkg` retains the original remote inputs, local patches/helpers,
build recipe and installation scripts. The host independently checks original
source SHA512 checksums; it never regenerates them. Numeric UID/GID execution
preserves output ownership and does not require a passwd entry in the helper.
Each inventory records the exact helper image ID and its actual package graph.

Nested source notices are read without extracting archive members onto the host.
Traversal, duplicate paths, oversized expansion and excessive nesting fail
collection. Original nested source archives must parse. Deeper upstream test
fixtures which intentionally are not valid archives are retained with their
hashes and flagged for review. Alpine's own copyright-bearing helper files are
preserved in full, so utility notices do not disappear behind the upstream origin
license.

## Caddy sources

Use the same digest-pinned Caddy base selected for the web build:

```sh
python3 tools/collect_caddy_sources.py \
  --image psst-release-web:local \
  --base 'docker.io/library/caddy@sha256:ACTUAL_SELECTED_INDEX_DIGEST' \
  --output /private/release-review/caddy
```

This collector also needs Go, Docker Buildx and GitHub CLI read access. The
example digest is a placeholder; use the release's recorded resolved base.
The immutable native image descriptor binds its Docker recipe revision. That
recipe selects the Caddy release, architecture-specific executable checksum and
exact dist source revision.

The collector retains the official full buildable artifact (wrapper, go.mod,
go.sum and vendor tree), checksums, upstream signature/certificate files and
exact Docker/dist source archives. It matches the running image's Caddy bytes to
the recipe-checksummed official executable archive. Binary build information
must match the wrapper's source revision, vendored dependency versions and
embedded module sums. Unbound replacements fail collection. Go standard-library
notices are captured from the actual embedded toolchain's source version.

Public source download redirects are restricted to GitHub's HTTPS download
hosts and carry no credentials. Collected assets record upstream URLs and actual
hashes. Retaining upstream signatures does not verify them: the inventory reports
that verification separately and currently leaves it false.

### Caddy legacy Sigstore signature verification

`tools/verify_caddy_source_signatures.py` now verifies the retained buildable
source archive and checksum-file signatures and emits a separate evidence file.
It leaves the collector inventory and its distribution review state unchanged.
Caddy v2.11.7's [exact release workflow](https://github.com/caddyserver/caddy/blob/72dd0fb067f6d7826c7f79907670ba4a713bfe37/.github/workflows/release.yml)
pins cosign v2.6.5 for legacy detached signatures/certificates; its [signing configuration](https://github.com/caddyserver/caddy/blob/72dd0fb067f6d7826c7f79907670ba4a713bfe37/.goreleaser.yml)
also selects SHA512 release checksums. These are Sigstore blob signatures, not
GitHub artifact attestations. New-format releases need an explicit verifier
migration rather than a weaker fallback.

Download the [official cosign v2.6.5 executable](https://github.com/sigstore/cosign/releases/tag/v2.6.5)
into your private review tooling directory. The verifier checks its actual bytes
and build identity before execution. Supported official executable SHA256 pins
are:

| Executable           | SHA256                                                             |
| -------------------- | ------------------------------------------------------------------ |
| `cosign-linux-amd64` | `c3b4f5410e608af03a5eb0aaac84a4313d8da131248e08ff1759ac70c79d1644` |
| `cosign-linux-arm64` | `426193b4c5da4d4d643e822f48fe0cc8a476ca1782a272704831f5a0cef716d7` |

Both pins were matched against the official release asset digests and
`cosign_checksums.txt`. The executable reports clean source commit
`3e82f50a2839855693aacf7b3d0e7e2f30774cb4`.

```sh
python3 tools/verify_caddy_source_signatures.py \
  --collection /private/release-review/caddy \
  --cosign /private/release-review/tooling/cosign-linux-amd64 \
  --output /private/release-review/caddy-signature-verification.json
python3 tools/test_caddy_source_signatures.py
```

The output must be a new file. Verification requires public network access to
Sigstore trust/transparency services and no credentials. Subprocesses receive
only a system executable path and `SIGSTORE_NO_CACHE=1`; no inherited token,
proxy, alternate trust-root or cosign option environment is passed. Public trust
metadata remains in memory. Certificate transparency and Rekor verification
remain mandatory; failures or service unavailability reject verification.

The exact certificate policy for v2.11.7 is:

| Claim                            | Expected value                                                                         |
| -------------------------------- | -------------------------------------------------------------------------------------- |
| Subject alternative-name URI     | `https://github.com/caddyserver/caddy/.github/workflows/release.yml@refs/tags/v2.11.7` |
| OIDC issuer                      | `https://token.actions.githubusercontent.com`                                          |
| Workflow repository/name/trigger | `caddyserver/caddy` / `Release` / `push`                                               |
| Workflow ref                     | `refs/tags/v2.11.7`                                                                    |
| Workflow commit                  | `72dd0fb067f6d7826c7f79907670ba4a713bfe37`                                             |

The version and expected source commit come from the collected binary/source
binding. Verification uses exact claims, never broad identity regular
expressions. It authenticates both signatures, then matches the retained
buildable source and architecture-specific executable archive against the
signed SHA512 list. The evidence binds the input inventory, verifier and covered
assets by hash and rechecks them before returning success.

On 2026-10-07, real online verification of the official v2.11.7 downloads
succeeded with this pinned verifier: both signatures returned exit 0 and
`Verified OK`; the signed list matched both retained archives. These download
tests used a collector-format replay inventory, not a new complete final-image
collection. The verified buildable archive SHA256 was
`b430516910839fbaf35c0a9e9df80d1e2e30aa792530293c39f4a97a1b2c9060`, and the
verified checksum-file SHA256 was
`5d27b76b95f496638d79208c55250317b8755fd9e90005f7466aa32956944a90`.
Real negative checks also rejected a different workflow subject URI, OIDC
issuer, source commit and modified checksum-file bytes, each with exit 1.

This proof authenticates the two Caddy assets and their signed checksum
bindings. It does not authenticate Alpine APK sources, other Docker/dist/Go
source archives, or complete runtime legal compliance. Those gates and final
image/source correspondence remain pending. Overall `review_required` remains
true even after the Caddy signature gate succeeds.

## Pinned missing-notice review evidence

`tools/runtime-legal/review.json` retains a bounded review of the four origins
missing notice documents in the initial collections. It is evidence, not a
distribution approval or a replacement for collection. Its inputs are the final
AMD64 filesystems of these immutable bases:

- Alpine 3.21 index
  `sha256:ce64758a109eb420d874a118f87920e625e12d3634e03b4a5573fd9f6e5d3507`.
- Caddy 2 Alpine index
  `sha256:d8542f48d34a9cf4e4c11a478865229840e87e4c96ea3f439101f31a5d35f75f`.

Eight origin/version/aports-commit combinations are recorded. The evidence does
not cover other versions retained in image layers, ARM64 package builds, or a
future final image graph. Each new combination must be reviewed against its own
original source inputs. Standard license documents are retained at SPDX license
list data commit `d46e94e2c78ceede1cfc63cfa0396472d2798d4c`, with Git blob and
SHA256 checksums. The MIT document is explicitly a standard template; its
placeholder copyright line is not attribution to an Alpine copyright owner.

The verified findings are:

| Origin                                  | Supported notice treatment                                                                                                                                                                                                                                                                                                                        |
| --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `alpine-baselayout` 3.6.8-r1 / 3.7.2-r0 | Preserve the complete GPL-2.0-only terms, exact Debian netbase 6.4 copyright, and all supplied recipe/helper/source notices. Original protocols/services checksums match netbase commit `a0c50d66656551d076c545cd42a83c2e4140ea0e`. Headerless Alpine configuration files retain the upstream package declaration without fabricated attribution. |
| `ca-certificates` 20260909-r0           | Preserve the declared MPL-2.0/MIT terms, full `certdata.txt` and Timo Teräs `c_rehash.c` headers, and exact curl COPYING/THANKS for `mk-ca-bundle.pl`. Preserve all original source files, including headerless package files, under their upstream package declaration.                                                                          |
| `alpine-base` 3.21.8-r0 / 3.23.6-r0     | `alpine-release` contains recipe-generated release metadata. Preserve the exact recipe/source inputs, its MIT declaration, full MIT terms and every notice actually supplied. The source supplies no copyright owner for these data; no owner or new grant is invented.                                                                           |
| `alpine-keys` 2.5-r0 / 2.6-r0           | Preserve every exact public signing-key input, recipe, upstream MIT declaration, full MIT terms and supplied notice. Public-key bytes contain no copyright-bearing notice to replace or fabricate. This does not authenticate APK signatures.                                                                                                     |

The ca-certificates archive's `mk-ca-bundle.pl` bytes match curl commit
`0ada20387c31c638cfd7f6b4ae7e5cab5b318caf`. Its [actual curl copyright document](https://raw.githubusercontent.com/curl/curl/0ada20387c31c638cfd7f6b4ae7e5cab5b318caf/COPYING)
and referenced [contributors](https://raw.githubusercontent.com/curl/curl/0ada20387c31c638cfd7f6b4ae7e5cab5b318caf/docs/THANKS)
are preserved without substituting the APKBUILD's outdated GPL-script comment.
This helper runs during certificate generation and remains part of the retained
source archive. The [netbase copyright](https://salsa.debian.org/md/netbase/-/raw/a0c50d66656551d076c545cd42a83c2e4140ea0e/debian/copyright)
is bound through the original recipe-checked protocols/services bytes, not a
current unrelated Debian package.

Filename-only notice scanning misses these embedded comments. The review data
names the exact source member, complete comment range and full member hash.
Match those against freshly collected sources before adding them to a release's
notice assets; preserve the original complete archives as corresponding source.
The [MPL distribution requirements](https://www.mozilla.org/en-US/MPL/2.0/)
include making the covered source available by reasonable, timely means and
preserving licensing notices. A copied MPL license text alone does not fulfill
that source requirement. GPL-2.0 source delivery must likewise accompany its
applicable distribution path; do not substitute an unrelated project archive.

Verify the checked-in evidence offline, then optionally match a collector output
without mutating it:

```sh
python3 tools/runtime-legal/verify.py
python3 tools/runtime-legal/test_verify.py
python3 tools/runtime-legal/verify.py --collection /private/release-review/backend-runtime
```

The collection check binds recipe bytes, original input hashes and embedded
source-member/comment bytes. Unknown scoped source revisions fail rather than
inherit a prior version's review. Successful verification still reports
`review_required: true`; `--require-complete` intentionally fails while the
listed distribution gates remain unresolved. No notices are inserted into
images, no collection status is cleared, and no source asset is published by
this verifier.

## Remaining distribution gates

- [x] Review the four pinned missing-notice origins, preserve all supplied
      notices and complete applicable terms, and document the bounded metadata/
      public-key treatment without fabricated ownership.
- [x] Preserve and match the embedded helper notices and two exact malformed
      BusyBox test fixtures against original source bytes.
- [x] Verify the available retained Caddy signatures and preserve exact
      verification evidence; distinguish other source checksum provenance and
      unverified APK binary envelopes explicitly.
- [x] Prepare a complete AMD64 source pack and legal overlays; validate
      inherited layers, unchanged binaries/configuration and exact APK graphs.
- [ ] Collect and validate the final ARM64 native pair and its retained layers
      against complete source/notice/provenance coverage.
- [ ] Publish retained exact sources, checksum/provenance bindings and rebuild
      instructions with the actual released pair; verify anonymous retrieval.
- [ ] Complete final release provenance/security and public anonymous
      installation checks before distribution.

Source helper images are private build tooling, not published application
runtimes. Private pack preparation does not itself publish or deploy a release.

## Complete source packs and notice-only overlays

`tools/package_runtime_sources.py` prepares one native architecture at a time.
It validates every origin/version/aports-commit in the union of retained layer
and installed package databases, rejects missing or unknown source revisions,
rechecks source SHA512 checksums and original notice bytes, and matches the
collected graph to fresh Docker-save layer databases from the actual image.
The curated four-origin evidence must match exact recipe/source hashes.

All license alternatives declared by retained packages need full terms; a
permissive alternative does not silently remove the other declaration. Public
domain packages retain all notices and original sources actually supplied.
The complete standard license references are pinned to the same SPDX data
commit, with hashes; template copyright placeholders are never substituted
for actual upstream attribution. The complete source archive preserves supplied
copyright notices, installation helpers, patches and packaging recipes.

The documented treatment of generated metadata and public signing-key data is
limited to the eight exact curated origin revisions. The MIT condition is to
preserve the copyright/permission notice supplied with the material, as stated
in the [full MIT terms](https://opensource.org/license/mit). Inventing an owner
from a maintainer name or withholding preparation until an absent notice exists
would not preserve upstream evidence. Unreviewed revisions still fail closed.

Two intentionally malformed BusyBox 1.37.0 unzip test fixtures are retained,
with exact approved paths/hashes, as part of the original upstream source test
data. They are not omitted or opened as valid nested archives. Any other
malformed nested source input needs a separate scoped review.

```sh
python3 tools/package_runtime_sources.py package \
  --backend-runtime /private/release-review/backend-runtime \
  --web-runtime /private/release-review/web-runtime \
  --caddy-sources /private/release-review/caddy \
  --cosign /private/release-review/tooling/cosign-linux-amd64 \
  --version v1.2.3 --revision ACTUAL_FULL_SOURCE_COMMIT \
  --source-base-url https://github.com/endorses/psst.zip/releases/download/v1.2.3 \
  --output /private/release-review/pack
```

The packer reruns online Caddy signature verification rather than accepting a
successful-looking sidecar as proof. It reconstructs Caddy notices directly
from retained original archives and checks the embedded executable against the
signed architecture-specific official executable archive. Its source asset
contains complete APK source packages, Caddy vendor/wrapper source, exact
Docker/dist source, Go notices, legal review evidence and rebuilding
instructions. Helper packages are recorded as build tooling, not falsely
reported as shipped runtime components.

The output contains `psst.zip-VERSION-runtime-sources-ARCH.tar.gz`,
`runtime-pack.json`, and backend/web overlay build contexts. Each overlay adds
only legal/source discovery files. The web overlay preserves the exact hosted
version/revision/source metadata and adds its runtime notice URL only after the
runtime files are included; this makes them discoverable from `/legal`. Its
source link includes the archive SHA256;
the web inventory/notices account for both application runtime images. Source
publication remains explicitly pending. Per-artifact provenance records the two
Caddy signatures and signed-checksum bindings separately from original recipe/
SHA512 source provenance. APK binary signatures remain unverified: filesystem
package databases and source packages do not contain the original signed APK
envelopes. No blanket signature success is claimed. The source archive is a
separate GitHub release asset and is not embedded in the small deployment bundle.

```sh
docker build --build-arg ORIGINAL_IMAGE=psst-release-backend:local \
  --tag psst-release-backend-overlay:local /private/release-review/pack/overlays/backend
docker build --build-arg ORIGINAL_IMAGE=psst-release-web:local \
  --tag psst-release-web-overlay:local /private/release-review/pack/overlays/web
python3 tools/package_runtime_sources.py verify-overlays \
  --pack /private/release-review/pack \
  --backend-image psst-release-backend-overlay:local \
  --web-image psst-release-web-overlay:local \
  --output /private/release-review/overlay-verification.json
python3 tools/test_runtime_source_packaging.py
```

Overlay verification compares every inherited layer, runtime configuration,
executable hash, final APK graph and installed legal file against the source
pack. OCI export additionally needs `tools/assemble_release_oci.py` to validate
all descriptor/config/layer bytes against the image config actually tested.
A full Docker-save to OCI conversion is acceptable when the builder cannot
export OCI, provided the tested config and every uncompressed layer digest
match. This establishes a new OCI child digest; it does not preserve a
previous registry manifest that never existed. Never flatten the filesystem.

On 2026-10-07 a disposable AMD64 pair built from application revision
`dffeac44c0913c6dbf0dee4e4156e918f2f4d9c5` passed actual complete collection and
source packaging: backend 17 retained package versions / 11 origins, web
32 versions / 20 origins, and 11 retained Caddy/source/signature/Go-notice
assets. Both Caddy signatures were verified against the actual collected final
image binding. The notice-only pair preserved all inherited layers, exact
runtime configuration, executable bytes and final package databases. The native
`verify_release_images.py --runtime-pack` gate also passed actual HTTP retrieval
of every served runtime file, exact source offer and appended discovery metadata.
The two final native OCI exports passed descriptor/config/blob and raw-layer
verification against the exact configs tested by that smoke gate. These are
private local preparation results, not published release provenance.

ARM64 native validation, final release provenance/security gates, source-asset
publication and anonymous download/hash verification remain separate required
checks. Retain the complete private source pack until the publisher can deliver
and verify it alongside the actual released pair; do not upload unchecked
review outputs as Actions artifacts.

## Replay the exact retained source asset

`tools/verify_runtime_source_pack.py` produces technical runtime source
completeness measurements from an existing asset and actual native OCI exports.
It requires the externally bound source asset SHA256, exact release identity
and the native smoke measurement that identifies the configurations tested.
Existing `source_pack_complete`, `review_required`, or cached successful
signature flags do not grant approval or replace these checks.

```sh
python3 tools/verify_runtime_source_pack.py \
  --pack /private/release-review/pack \
  --backend-archive /private/release-review/export/backend-amd64.oci.tar \
  --web-archive /private/release-review/export/web-amd64.oci.tar \
  --smoke-report /private/release-review/smoke-report.json \
  --cosign /private/release-review/cosign-linux-amd64 \
  --repository endorses/psst.zip --version v1.2.3 \
  --revision FULL_APPLICATION_COMMIT \
  --source-sha256 sha256:BOUND_RUNTIME_SOURCE_ASSET_SHA256 \
  --output /private/release-review/source-completeness-verification.json
python3 tools/test_runtime_source_replay.py
```

The verifier hashes and safely expands the original archive without rewriting
or repackaging it. It compares archived legal evidence with the trusted pinned
notice documents, verifies complete origin coverage, checks original source
SHA512 sums against the retained APKBUILD declarations, and compares copied
recipe/helper bytes. Full aports recipe directories also retain unused patch
files; those need not be present inside abuild's source package unless declared
as source inputs. The full recipe remains in the hash-bound outer asset.

OCI validation replays every content-addressed blob and uncompressed layer
against the configuration actually smoke-tested. The verifier independently
reads each retained APK database, checks the original layer prefix and runtime
configuration, reconstructs the complete lower-layer package graph, and
compares the final database, executable, legal files and discovery metadata.
It rebuilds full supplied notices from original source bytes. Caddy's source
and checksum signatures are freshly verified through the pinned Cosign tool,
including public transparency checks. The actual final Caddy executable must
match the signed architecture-specific binary and its embedded module graph
must match the authenticated vendor/wrapper sources and recorded Go settings.

The output distinguishes `runtime_source_inputs_verified` from application
source verification, APK binary signature verification, anonymous source
publication and distribution authorization. It is an unsigned technical
measurement until the publisher authenticates its producer and binds its
actual source/archive/manifest subjects to the release. The caller must obtain
the expected source SHA256 and trusted smoke evidence from that release binding;
passing arbitrary caller-created metadata does not establish release authority.
The replay does not rerun Alpine builds or independently renew every historical
aports Git tree lookup: it checks the exact retained collector inputs and source
checksums against the bound asset, preserving their recorded immutable recipe
provenance. Caddy proof authenticates its covered artifacts only, never APKs.

On 2026-10-07 the private patched AMD64 source asset for application revision
`750f440d143d6766d83b13e1ce789436471ae029` replayed successfully without changing
archive SHA256 `4ce28130c2d926558568fdcc58cdd78193da92fefe54873c827fb3454d45bad6`.
It covers backend 20 retained package versions / 13 origin revisions and web
33 versions / 21 origins, including replaced OpenSSL and zlib lower-layer
versions. Native OCI bytes, full notices and fresh Caddy signatures were checked.
This remains private preparation evidence; application source delivery, ARM64
native evidence, authenticated release provenance and authorized distribution
review are separate gates.
