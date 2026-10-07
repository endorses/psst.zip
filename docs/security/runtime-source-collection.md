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
