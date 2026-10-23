# Runtime sources and final-image correspondence

The maintained Go release tool retains the exact original sources and legal texts for the backend/web runtime pair. Collection prepares evidence; publication still requires the completed release evaluation, current scan policy and the protected GitHub publication approval. The runtime source archive is a separate release asset, outside the bounded deployment bundle.

Use the [native preparation runbook](container-native-preparation.md) for the complete workflow. The source collector receives an original paired Docker save and exact configuration digests. It reads the final APK database and retained lower-layer databases, because upgrading a package does not remove its old bytes from the distributed image layers. Each native architecture has its own runtime offering.

Build the release tool and the native source helper with the release's immutable Alpine base. This example shows the source operation used by native preparation; all uppercase values must come from that release's actual build records:

```sh
release_tool=$(sh tools/release/build.sh)
"$release_tool" --root . sources runtime collect \
  --repository endorses/psst.zip --version "$VERSION" --commit "$SOURCE_COMMIT" \
  --platform linux/amd64 \
  --images-tar "$ORIGINAL_PAIRED_DOCKER_SAVE" \
  --backend-config "$ORIGINAL_BACKEND_CONFIG" --web-config "$ORIGINAL_WEB_CONFIG" \
  --helper-image "$NATIVE_SOURCE_HELPER_CONFIG" \
  --caddy-base "$RESOLVED_CADDY_INDEX" --cosign "$PINNED_COSIGN_EXECUTABLE" \
  --output "$NEW_RUNTIME_COLLECTION"
```

The helper is built from `tools/runtime-sources.Dockerfile`. Host, Docker daemon, application pair and helper must use the same Linux architecture. Collection needs public source download access, Docker/Buildx, Go and the pinned upstream signature verifier. Output must be a new directory. The helper receives no application checkout, production state, Docker socket or credentials; only its bounded source-fetch work runs inside the isolated container.

## Original Alpine inputs and legal texts

For every retained package origin/version/Aports revision, collection retains the complete exact recipe, local patches and installation helpers, and original upstream distfiles. Git object identities and the recipe's original SHA512 source checksums are independently checked; checksums are never regenerated to accept changed inputs. Helper identity and its real package graph are recorded as build tooling rather than shipped application components.

Archive replay rejects traversal, duplicate paths, unsafe links and excessive expansion. Sources are inspected without extracting their member paths onto the host. Reviewed exceptional fixtures stay inert: the two exact malformed BusyBox unzip fixtures remain in the original source, with their paths and hashes, rather than being silently omitted or parsed as valid nested archives.

`tools/runtime-legal/review.json` records the bounded treatment of exact recipe/source revisions whose original inputs do not supply a conventional license document. The retained evidence includes full applicable terms and all original notices. Alpine generated release metadata and public signing-key data retain their original MIT declarations and complete terms; the tool does not invent an owner from a maintainer name. Unknown revisions fail instead of inheriting a previous review.

Embedded notices also matter. The checker preserves the complete supplied certificate helper comments and exact curl copyright/contributor documents, and the original Debian netbase attribution associated with the retained protocol/service inputs. Standard SPDX texts are pinned by Git object and SHA256. Template placeholders are not substituted for real upstream attribution. Copying a license alone does not replace retention and delivery of its covered source.

## Caddy and compiler originals

The exact official Caddy image recipe selects the executable release, architecture-specific checksum and distribution revision. Collection binds the image executable to the official binary archive, then to the signed SHA512 list and full buildable source artifact. That artifact retains the wrapper, `go.mod`, `go.sum` and vendor tree. The embedded executable build metadata must agree with those original module versions and sums. Docker recipe and distribution source trees are retained separately.

Caddy 2.11.7 uses legacy detached Sigstore signatures/certificates. The collector verifies both its buildable artifact and checksum file with the exact upstream release-workflow certificate identity, OIDC issuer, repository, tag and source commit. Certificate transparency and Rekor verification are mandatory. An unknown signature format or unavailable verification service fails the online producer; there is no weaker fallback.

The compatibility verifier is official Cosign 2.6.5, source commit `3e82f50a2839855693aacf7b3d0e7e2f30774cb4`. The supported executable SHA256 pins are recorded once in `tools/release/internal/source/caddy.go` and are checked before execution:

| Platform    | Executable SHA256                                                  |
| ----------- | ------------------------------------------------------------------ |
| Linux AMD64 | `c3b4f5410e608af03a5eb0aaac84a4313d8da131248e08ff1759ac70c79d1644` |
| Linux ARM64 | `426193b4c5da4d4d643e822f48fe0cc8a476ca1782a272704831f5a0cef716d7` |

This older verifier is an explicit signature-format compatibility dependency. The producer runs it with a clean environment and no credential, alternate trust-root or caller Cosign option environment. Later offline replay checks retained source/signature bindings against the authenticated native producer observation; saved successful-looking text does not become an independent cryptographic proof.

`tools/go-runtime-sources.json` binds each executable's actual Go compiler/runtime version to a complete original Go tree. The backend compiler and Caddy compiler are checked independently; they need not be the same version. Original compiler licenses remain associated with the binary that used them.

## One source inventory and notice overlays

Each collection contains `psst.zip-VERSION-runtime-sources-ARCH.tar.gz` and `source-inventory.json`. The identical versioned inventory bytes also live inside the archive. It records source originals, member inventories, exact recipes/legal references, compiler originals, signatures, helper facts and both image/platform associations. Shared origins in the backend and web pair are retained once. The final release manifest binds the actual source archive bytes; the archive does not contain a recursive self-digest.

Native preparation generates the legal-only overlay contexts with:

```sh
"$release_tool" --root . sources runtime overlays \
  --repository endorses/psst.zip --version "$VERSION" --commit "$SOURCE_COMMIT" \
  --platform linux/amd64 --collection "$NEW_RUNTIME_COLLECTION"
```

The overlays add original terms/notices and source-discovery metadata to `/app/licenses/runtime/` and `/srv/web/licenses/runtime/`. The web legal metadata points to the exact retained offering and its digest. These additions must preserve the original executable bytes and runtime configuration. Source and original legal byte substitution fail verification.

After the overlays and actual native smoke checks, the strict OCI reader authenticates the complete image graph and uncompressed layer identities. Runtime replay applies whiteouts and opaque-directory rules, compares final/lower-layer package coverage and checks the exact legal additions. Browser and runtime verification share this already-verified image/source state rather than replaying the same originals independently. The smoke report must identify the same tested configuration digests, inventory and complete checks.

The public archive includes `SOURCE.md` with retained-input and rebuild instructions. It explains the bounded source relationships and explicitly leaves executable reproduction unclaimed. Source provenance is also distinct from APK binary-signature verification: the original APK binary envelopes are not retained by this source collector.

## Validation and delivery

The focused offline tests cover archive bounds, APK recipe/checksum identity, original notices, retained layers and whiteouts, compiler originals, Caddy/vendor bindings, unchanged runtime configuration and final notice substitution:

```sh
go -C tools/release test ./internal/source ./internal/oci ./internal/browser
```

Actual original-source and native browser rehearsal measurements are recorded in [container source tooling verification](../testing/container-source-tooling.md). These bounded checks do not establish a fresh complete release. Both native architectures, current upstream signature verification, final-image smoke checks, current scan policy, final provenance and anonymous source retrieval remain required for each published image pair. Remove owned temporary collection outputs and helper aliases after a rehearsal; preserve the reviewed artifacts of an actual release.
