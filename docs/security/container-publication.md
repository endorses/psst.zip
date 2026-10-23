# Container publication

Container publication uses the exact version-tag commit, authenticated preparation
artifacts, one protected GitHub environment approval, and standard final
provenance. Main-branch candidate dispatches remain read-only. Publication has no
production credentials and does not deploy the VPS.

## Preparation and approval

The release workflow checks main ancestry and the public repository tag-push
identity. It runs exact source CI for security/backend/web, native image builds
and smoke checks on amd64/arm64, source and final-image scans, final-image notice
and corresponding-source verification, and upgrade/recovery experiments. Incoming
cross-job bytes remain authenticated against the exact source, workflow and run
attempt; a JSON success flag is insufficient.

The final preparation summary identifies the version, source commit and retained
artifacts. Review those artifacts before approving **Publish verified immutable
container release** in the `container-release` environment. There is one approval;
no special comment or separately signed reviewer report is required. GitHub owns
the environment's required-reviewer rules. Repository publication opt-in must
also be enabled.

The committed `tools/container-distribution-policy.json` retains the environment
identity and explicit authorized owners of narrow vulnerability exceptions. The
corresponding-source check binds its exact Git blob and bytes. Removing the old
custom reviewer protocol does not remove scan coverage or extend an exception's
scope or expiration. The final scanner policy is rechecked before mutations,
version tags and immutable publication because an exception may expire while a
run waits.

## One cross-job result

`psst-release publication-result` consolidates completed source, scanner, image
smoke and notice facts into one versioned `release-result.json`. It binds the
exact repository, tag, source commit, run/attempt, file/image subjects and both
native platform results. Raw corresponding-source replay evidence remains in the
preparation artifact and is referenced by digest. Raw scanner/native diagnostics
remain with their producing artifacts.

The source job attests this one result. Native recovery reads its authenticated
source and smoke sections, runs the same real experiments, and produces native
measurements. Aggregation authenticates those measurements, extends the exact
source result with completed recovery, and attests the final combined result.
Already incorporated per-check envelope files are removed. There are no separate
signed prepublication gate files or redundant report-only verification modes.

Publication downloads the final result and verifies its one signature once per
exact byte snapshot, while applying the same source, scan, notice, native-image
and recovery semantics to its sections. The Go tool owns both result production
and publication. This is an internal workflow
contract; it does not change published deployment manifests or source offerings.

## Final provenance

Immediately before transport, `psst-release container-publish prepare --checksums FILE`
authenticates the fixed downloaded input layout and writes one ephemeral checksum
input for the pinned normal `actions/attest` step. It covers the detached manifest,
deployment bundle, every source asset, both image indexes and all four native
image manifests. Subject names and digests come from the actual verified release
binding. The checksum file is an action input, not an approval or a signed report.

The normal action generates SLSA provenance and publishes it through GitHub's
attestation API. Registry attachment and storage records are disabled, matching
the existing deployment verifier. GitHub manages the action's Node runtime;
repository scripts neither download action JavaScript nor discover a runner's
Node installation. The action keeps the existing `.github/workflows/release.yml`
identity, version-tag ref and source commit.

The publication driver snapshots and authenticates the same incoming bytes again
before any remote mutation. Before pushing the exact image subjects it verifies
the one standard multi-subject provenance statement through the manifest with
`gh attestation verify`, then requires that same verified statement to contain the
exact complete file/image name-and-digest set. OCI index and child subjects retain
their distinct digests even though they share a repository name. Retained files
are rehashed and registry contents are independently read back. Verification
constrains the repository, source/workflow
commit, tag, issuer, hosted runner, run and attempt. The installed updater's final
manifest, bundle filenames, required parser and certificate identity are
unchanged. There is no additional signed provenance-about-provenance report or
signed local readback envelope.

## Remote mutation and partial failure

The workflow retains its repository-wide concurrency group with cancellation
disabled. The driver independently checks that literal trusted workflow policy
and creates an exclusive runner-local version journal. Immutable-release
policy, source identity and package visibility are checked before mutation.
An explicit first-package setup setting can permit creation of both absent
packages; a mixed existing/missing pair remains an error. Registry package
visibility may require the operator to make freshly created packages public.

The shared GitHub adapter creates a fresh draft, uploads frozen assets without
replacement, then downloads and hash-checks every asset. The container adapter
uses the shared strict OCI reader to retain only the tested runnable manifests,
configurations and layers. It assembles the exact prepared pair index without
rebuilding or recompressing image bytes, then uses supported Skopeo commands to
push that index and both native children by digest. It checks index/child maps
and performs complete anonymous child pulls before creating the two absent
version tags and publishing the complete immutable draft with `make_latest: false`.
After publication, anonymous release assets, exact image digests and version tags
are checked before the success receipt is written. Existing
versions, assets and tags are never overwritten or automatically adopted.

CLI publication shares the bounded GitHub source/CI and draft/readback adapters.
Tokens remain in environment or private registry auth files; command arguments,
public diagnostics and receipts contain no credential values. Failed private
registry credentials are removed even when snapshots remain for reconciliation.

`tools/release_http.py` and `tools/release_process.py` temporarily support the
remaining Python measurement and recovery producers. They contain only a fixed
read-only API reader and bounded process execution, with no release or registry
publisher. Remove them when the last of those producers moves to Go. Helpers
captured inside supported immutable deployment bundles keep their original bytes.

Remote writes are journaled with durable intent/completion boundaries. An
interruption can leave a draft, digest-addressed images or a partial pair of
version tags. Cross-service atomicity is not promised. A failed attempt retains
its exact private runner-local snapshot and journal for that runner's lifetime;
the workflow uploads only sanitized diagnostics. Do not automatically rerun,
resume, delete or reconcile that state. Inspect the original identities and
remote state explicitly; a newly reviewed version is the usual recovery path.

Successful runs retain a small publication receipt containing immutable release
identity and public-readback results. Private journals/snapshots and credentials
are never uploaded as ordinary workflow artifacts. The driver removes its own
private state only after verified success and receipt upload.

## Verification boundary

Focused offline fixtures exercise actual preparation, transport ordering,
immutable/absence checks, exact OCI configuration mapping, scanner expiration,
partial writes, durable snapshots, safe downloads and public readback. They never
sign or publish live artifacts. Standard-provenance tests exercise exact subjects
and rejected verifier outputs using the independent verification policy.

A candidate rehearsal and a later approved real tag run are separate validation:
local tests cannot prove hosted OIDC issuance, GitHub environment configuration,
registry visibility or live release immutability. No publication should be
claimed until those actual external checks complete.
