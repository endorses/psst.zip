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

| Origin                                  | Retained evidence and outstanding attribution                                                                                                                                                                                                                                                                                                                      |
| --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `alpine-baselayout` 3.6.8-r1 / 3.7.2-r0 | Full GPL-2.0-only text and Debian netbase 6.4 copyright. The recipe's original SHA512 checksums match netbase's protocols/services at commit `a0c50d66656551d076c545cd42a83c2e4140ea0e`. Alpine-authored configuration/helper files contain no copyright headers; their applicable notice treatment still needs review.                                            |
| `ca-certificates` 20260909-r0           | Original source archive SHA512 and complete embedded leading license comments for `certdata.txt` (MPL-2.0), `c_rehash.c` (MIT; Timo Teräs), and `mk-ca-bundle.pl` (curl; Daniel Stenberg and credited contributors). Full MPL/MIT texts and exact curl COPYING/THANKS are retained. Files without a full grant, including `update-ca.c`, remain explicit findings. |
| `alpine-base` 3.21.8-r0 / 3.23.6-r0     | The installed subpackage is `alpine-release`, generated by the pinned recipe. The recipe declares MIT but provides no copyright-bearing grant. Do not invent ownership from its maintainer field or treat the standard MIT template as sufficient notice evidence.                                                                                                 |
| `alpine-keys` 2.5-r0 / 2.6-r0           | The recipe contains public signing keys and declares MIT, with no copyright-bearing license grant. Exact file hashes are recorded; upstream attribution or a documented review of the public-key data remains pending.                                                                                                                                             |

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

- [ ] Review missing origin notices and required full license texts. The verified
      pinned-image collections currently flag alpine-base, alpine-baselayout,
      alpine-keys and ca-certificates. A license identifier alone is insufficient.
- [ ] Review embedded notices, retained invalid upstream archive fixtures and
      component-specific required files against the exact source packages.
- [ ] Verify upstream source signatures and preserve the verification evidence.
- [ ] Add notices and source offers to the final images without changing the
      collected binaries or APK package graph; verify the resulting pair again.
- [ ] Publish the retained exact corresponding sources, checksum/provenance
      bindings and download instructions with the release.
- [ ] Verify hosted runtime notice/source discovery and complete public anonymous
      installation before approving the release.

The source helper image is build tooling, not an application image or a published
runtime. The container release workflow remains read-only while these gates are
unfinished.
