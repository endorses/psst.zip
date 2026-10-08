# Container publication preparation and transport

`tools/publish_container_release.py` implements publication preparation and
reservation/readiness boundaries. `tools/github_release_transport.py` provides
the GitHub/GHCR transport, and `tools/github_release_evidence.py` authenticates gate
reports. The release workflow still verifies candidates only; these libraries
have not published a release. They do not change package visibility, sign
attestations or deploy production.

## Reviewed identity and exact artifacts

`prepare_publication` requires a strict `vMAJOR.MINOR.PATCH` tag, the event SHA,
the separately reviewed full commit, a checkout at that commit, an exact trusted
GitHub origin and ancestry from `origin/main`. Tracked checkout changes fail.
Remote protected-branch/tag settings and review authentication remain the future
trusted workflow's responsibility; local ancestry alone does not establish them.

The detached manifest and deployment-ready bundle pass the existing bounded
artifact parser. The actual bytes of each registry index must hash to its
manifest digest and advertise exactly the authenticated AMD64/ARM64 child map.
Duplicate architectures, unexpected runnable platforms, wrong variants, invalid
descriptors and overlapping pair digests fail. Buildx attestation descriptors are
allowed only as `unknown/unknown` attachments referencing a selected child.
This follows the registry distinction between manifests addressed by digest and
mutable tags; clients must check returned content. [OCI distribution specification](https://github.com/opencontainers/distribution-spec/blob/main/spec.md).

The preparation enumerates the updater's eight mandatory provenance subjects:
detached manifest, deployment bundle, backend/web indexes and their four
architecture children. Every separately distributed corresponding-source asset
has its own file digest and additional provenance subject. Source assets are
regular files with safe names, hashed incrementally with a 2 GiB per-file limit;
the existing smaller manifest/report/bundle limits remain unchanged. An archive
name or checksum does not establish corresponding-source completeness.

## Verification gates

Every receipt binds the repository, version ref, source commit, release workflow
and exact image/file subjects. Changing a source archive invalidates earlier
receipts. The injected `EvidenceVerifier` must authenticate the report and its
issuer or authorized reviewer before returning `VerifiedEvidence`; a caller's
JSON `passed: true` is not an authentication method. `GhEvidenceVerifier` checks
private report snapshots with `gh attestation verify`, requiring the exact
repository, workflow, signer/source commit, version ref, certificate identity,
GitHub OIDC issuer, hosted runner and SLSA v1 predicate. It checks the verified
subject against the snapshot checksum. Verification has a 55-second process-group
deadline and 4 MiB combined output bound; credentials/configuration are explicit,
and inherited authentication/trust configuration is excluded. Live signed reports
and the authorized source/legal review policy remain unverified. Fixture tests
use a separate test adapter.

| Required gate        | Required evidence                                                   |
| -------------------- | ------------------------------------------------------------------- |
| Source CI            | All five jobs passed for the selected reviewed source               |
| Source scanners      | Backend/web source, scanner versions, DB/date, findings/disposition |
| Final-image scanners | All four exact children, completed scans and findings/disposition   |
| Final-image smoke    | Selected pair on both architectures, recording native/emulated      |
| Runtime notices      | Reviewed final runtime inventory, full notices and binary binding   |
| Corresponding source | Complete exact sources, recipes and patches for distributed code    |
| Distribution review  | Authorized review of the final images and corresponding sources     |
| Upgrade/recovery     | Actual relevant disposable upgrade and restore evidence             |

Missing/extra reports, failed receipts, wrong report checksums or another release's
binding fail preparation. Scanner errors fail even when a report claims success;
missing targets, missing tool/database/date evidence and unresolved findings also
fail. Findings may be fixed or supported as not applicable. The trusted verifier
must assess the substance of those reasons and actual execution rather than
accepting the report's fields mechanically. Collection-only runtime outputs,
source scans alone and local candidate smoke do not satisfy final distribution
or upgrade gates.

The CLI only validates registry index inputs and writes an exclusively created
inspection record with `publication_authorized: false`. It cannot read success
JSON and unlock publication. Full preparation is a library boundary requiring
the trusted verifier implementation.

## Exact source coverage and authorized distribution review

The publication verifier now rejects a signed source or distribution report with
empty, partial, stale or substituted detail records. The corresponding-source
record must cover every exact source asset and all four final child images, with
notice-inventory hashes and complete application, runtime and component-specific
module/generator coverage. Each coverage item retains its evidence hash. The
committed distribution policy is identified by source commit, Git blob and byte
hash. Distribution review must match that source record's policy, image notices,
source subjects, complete coverage hash and report hash. Structural validation
alone cannot establish that a source review was performed.

`tools/generate_distribution_review.py` implements the authorized-review producer.
It first authenticates the complete corresponding-source report and validates its
policy against the exact committed `tools/container-distribution-policy.json`.
The current policy selects the `container-release` environment and reviewer
`endorses`; it grants no approval by itself. The producer reads GitHub's selected
version-tag workflow attempt, environment required reviewers and review history.
It requires an approved decision from the configured authorized user, with the
same GitHub user ID, environment ID and exact approval comment:

```text
psst.zip distribution review: <binding SHA256>; run <run ID>; attempt <attempt>; source report <source-report SHA256>
```

The workflow must present the final image/source/notice records for review and
provide this exact comment before waiting on the environment. The comment binds
all final subjects and the source report to the selected attempt. Earlier review
comments cannot approve different artifacts or a rerun. Missing approvals,
rejections, ambiguous decisions, different required reviewers, incomplete API
responses and changes during verification fail. Only GET requests are used.
The producer returns a gate report plus the retained API evidence; the trusted
workflow must attest both. It does not sign, publish or infer source completeness.
See GitHub's [review-history API](https://docs.github.com/en/rest/actions/workflow-runs#get-the-review-history-for-a-workflow-run)
and [environment API](https://docs.github.com/en/rest/deployments/environments#get-an-environment).

The environment has not been provisioned and this producer is not wired into the
candidate-only workflow. Complete preferred-form source production, actual signed
reports, real reviewer approval and hosted publication remain pending. Fixture
checks exercise authorization and substitution failures, not live approval.

## Reports from completed checks

`prepare_inputs` shares the publication core's exact source/tag, bundle, manifest,
index and source-asset validation. It returns `PublicationInputs.binding` before
gate verification so producers and the eventual `PublicationPlan` use identical
subjects. This resolves report/preparation ordering without weakening any input
check; `PublicationInputs` has no verified gates or publishing authorization.

`tools/generate_release_gate_reports.py` is a library of bounded producers, with
no generic success-report constructor. `source_ci_report` queries the selected
version-tag release-run attempt using an explicit Actions read credential. It
requires the exact source SHA, repository, workflow, version tag and all five
completed successful reusable CI jobs, retaining job IDs/completion times. It
checks every job page and rechecks the run identity; API failures, changed counts,
unexpected CI jobs and unfinished/skipped/failed jobs produce no success report.
A prior successful main CI run cannot satisfy this producer.
[GitHub workflow-job attempt API](https://docs.github.com/en/rest/actions/workflow-jobs#list-jobs-for-a-workflow-run-attempt).

`collect_native_measurement` runs the actual final-image smoke harness with the
matrix-local `NativeSourceContext(repository, version, commit, platform)`, runtime
pack and image configuration IDs. It requires all application, runtime
offer, authentication/restart and cleanup checks. Before and after the harness,
it validates the actual local OCI exports against the tested configuration IDs
and verifies immutable pack/source-asset bytes and intended release URLs. The
schema 2 typed result contains checked source identity and local facts, with
`publication_authorized: false`. It needs no other architecture, deployment bundle
or global binding, so matrix jobs can finish before post-matrix assembly. Candidate
smoke without a runtime pack and emulated execution cannot produce this record.

`aggregate_native_reports` first authenticates both architecture measurements
with `GhEvidenceVerifier.authenticate`, preserving the same signer/source/ref,
snapshot and process policies as gate-report verification. Only then does it
generate complete `final-image-smoke` and `runtime-notices` gate objects bound to
all release subjects. Aggregation requires the complete binding from
`prepare_inputs`, matching both source contexts, actual child/configuration maps
and native source assets to the final subjects. Another repository, source,
architecture, source archive or child fails even when the record is signed. The
smoke report records native execution and all
four tested configuration digests for transport. Runtime evidence retains notice
hashes, source-asset identity and the requirement for distribution review. The
trusted workflow must sign the exact emitted report bytes before preparation;
these producers neither sign reports nor grant publishing authorization.

`source_asset_measurements` checks every corresponding-source asset against its
bound digest and records sizes. It explicitly leaves source completeness
unverified and creates no `corresponding-source` success gate. Hash equality and
source-pack preparation flags cannot establish complete corresponding sources or
replace the independent distribution review.

`tools/generate_corresponding_source_review.py` now independently replays the
application archive against the selected Git commit and the release packaging
recipe. It verifies the publication-bound digest and canonical archived paths,
modes and contents without using working-tree edits or extracting files. This
produces an application-source fact, not a passed corresponding-source gate.

The committed upstream catalog also retains two original backend source trees
omitted from the Go module ZIPs: SQLite C 3.49.1 for `modernc.org/sqlite v1.37.0`
and musl for `modernc.org/libc v1.65.0`. SQLite's official mirror commit
`3cd92ce875fd4e5601e535c35fef33494a6684e3` contains `manifest.uuid`
`873d4e274b4988d260ba8354a9718324a1c26187a4ab4c1cc0227c03d0f10e70`,
matching the generated Go source identity. Musl is retained from its canonical
commit `7ada6dde6f9dc6a2836c3d92c2f762d35fd229e0`, as named by the libc
generator. Both complete archives, build files and original notices are kept
unchanged. SQLite endorses its [official Git mirror](https://www2.sqlite.org/download.html);
musl's [canonical commit page](https://git.musl-libc.org/cgit/musl/commit/?id=7ada6dde6f9dc6a2836c3d92c2f762d35fd229e0)
links the exact snapshot.

Go associations require exact committed `backend/go.mod` versions, forbid
associated module replacements, and retain both backend lock files and hashes.
Musl acquisition accepts only its fixed canonical HTTPS snapshot route; existing
GitHub inputs retain their full-commit codeload routes. Redirects, alternate hosts,
changed source bytes, missing inspected inputs and substituted locks fail. This
retention closes the identified missing-original inputs; exact generated-source
relationships, remaining generator inputs and full completeness still require
verification before publication. Collection and replay do not grant approval.

`verify_backend_source_inputs` now authenticates both native backend compiler
measurements and reuses their existing final-image reproduction checks. It
compares the compiler's application snapshot against one independently read Git
archive, then independently replays dependency originals once per platform. Each
compiled dependency must match a retained module's path, version, H1 checksum and
original ZIP digest. Additional verified build-only modules may remain in the
offering. Source/runtime/image subjects and retained bytes are checked again
before returning. This adds no compiler execution or scanner pass and still
produces partial source facts, not a passed completeness gate.

The runtime collection now retains the complete Go source tree selected by
`tools/go-runtime-sources.json`. The current Go 1.26.8 original is pinned to commit
`c293dd49cbe25e1fe8d97d94a5cb618e7b6d831e`, archive SHA256
`061b4e784db7ce97cd9ae99ea71a857a2ff8455c6400495e5d1a98b8accd2542`.
The collector checks the resolved official tag against that committed pin and
retains original runtime, standard-library, compiler and build sources. Go test
archives remain untouched source-tree fixtures; they are not recursively parsed
as distributed libraries. The bounded reader preserves all 38 original notice
files found in this tree.

Packaging reads actual backend and Caddy executable metadata without executing
either program, requires both `GoVersion` values to match the retained tree, and
adds the original Go notices to the backend overlay as well as the web overlay.
Independent runtime replay checks the archived policy against the trusted selected
source, the full original archive and VERSION, and both final executable versions.
These Go sources are separately pinned; Caddy's signatures do not authenticate
the Go archive. The existing AMD64 executable pair was checked against this
original in three seconds. An updated final AMD64 pair from
`4ae5954223e6ec93f191a6578722551f60fdb54f` passed actual image smoke checks and
independent runtime/browser replay in 348.6 seconds. Its runtime offering retains
the complete Go tree and both final notice overlays. ARM64 execution, full
application-source completeness and hosted authentication remain pending.

The browser builder capture also retains the reviewed installed Svelte 5.57.1
compiler inputs and vite-plugin-svelte 5.1.1 JavaScript sources. Its finite catalog
includes the 227 compiler JavaScript files, six external relative inputs, the
CommonJS compiler entrypoints and all 21 plugin JavaScript files. The capture and
replay require the package identities and entrypoints, bound each recipe to 512
members, and reuse locked npm integrity/member checks. The ESM compiler resolves
to `src/compiler/index.js`; retaining the CommonJS bundle does not establish that
it executed. Retained helpers likewise do not imply every helper executed or that
outputs reproduce. A real capture from
`7d0f42a8ffb2b44a38ae25c635c4ddefe4eaf1df` verified all 256 inputs against their
locked npm originals and all 233 Svelte source files against the pinned upstream
tree. Build/capture/replay took 21.4/0.7/1.9 seconds. The prior AMD64 final-image
receipt predates this additional capture; complete final-image and preferred-source
coverage remain pending.

`tools/browser_preferred_source_relationships.py` maps already authenticated
capture/npm facts and verified pinned originals into per-file source/build-recipe
relationships. It covers every actually rendered package module, checks Lucide
icon objects against original SVG/metadata and checks QR scanner's embedded
source-map contents against decoder/worker originals. Unknown inputs or changed
bytes fail. Captured compiler/plugin sources are a separate mapping. The helper
neither authenticates its caller's evidence nor creates a completeness gate.
External Noble build configuration is recorded with its exact locked identity,
not asserted to be retained or independently reproduced.

The upstream offering additionally retains full `cznic/sqlite` and `cznic/libc`
project trees from the Go module origin commits, including generator modules
excluded from proxy ZIPs. Only their canonical GitLab full-commit archive paths
are accepted. Exact H1-replayed proxy bytes match all 1,323 SQLite and 4,153 libc
files in those source trees. These project originals complement the original
SQLite C and musl trees; remaining complete generator relationships and both
authenticated final-image source reviews still precede publication.

### Native image scanner measurements

`tools/measure_release_image_scans.py` validates the actual OCI archive graph,
release/source labels, platform and layer/configuration correspondence before
running tools. It copies the checked layout into private temporary storage;
Trivy receives an OCI directory, rather than the OCI tar archive. Layout bytes,
original archive, scanner executable and frozen vulnerability database must remain
unchanged through completion. `Metadata.ImageID` must equal the actual smoke-tested
configuration digest; both Alpine package and application Go-binary inventories
must appear in the result.

The producer snapshots hash-pinned native Cosign 2.6.5 and Trivy 0.75.0 assets.
Before extracting or executing Trivy, it verifies the official Sigstore bundle
online with exact reusable-release workflow/tag identity, GitHub OIDC issuer,
repository, source commit, event and workflow name, requiring SCT and Rekor checks.
AMD64 and ARM64 use separate official archive/bundle pins; an ARM64 executable
checksum is derived only from successfully authenticated release bytes, with ELF
architecture checked before execution. The command environment excludes inherited
credentials, trust/proxy overrides and Trivy configuration. Explicit empty
configuration and ignore policy, all severities, unfixed findings and suppressed
finding detection prevent ambient filtering. Commands have a 16 MiB output bound,
55-second verification/version deadline and 660-second scan deadline with process
group cleanup. [Official Trivy signature verification](https://github.com/aquasecurity/trivy/blob/v0.75.0/docs/getting-started/signature-verification.md).

`measure_image_scan` accepts the same matrix-local source context and returns a
typed measurement plus the full raw JSON bytes. It records exact OCI/configuration
identity, tool/source/signature/verifier hashes, database/schema/metadata hashes,
update/download/scan times and every OS/module finding with a canonical finding
hash. `Status: fixed` describes available remediation; it is not an approved
disposition. Unknown severity and missing fixes remain findings.

The producer cannot emit a `final-image-scanners` success report, even when it
finds no vulnerabilities. Measurements explicitly leave publication unauthorized
and review pending. The final gate still needs authenticated measurements for
both architectures, authenticated finding dispositions bound to exact
image/binary/source/advisory hashes.
Caller-supplied `not-applicable` JSON cannot satisfy those requirements.

By default the authenticated scanner downloads its own database into the private
cache from the fixed official `ghcr.io/aquasecurity/trivy-db:2` repository. The
download has a 330-second process-group deadline; the measurement retains its
tool/repository, start/completion times, database bytes hash and metadata hash.
The subsequent scan freezes those bytes and disables updates. Supplying
`--database` instead records a retained unapproved snapshot; supplied metadata
cannot create acquisition evidence. The final gate accepts the authenticated
owned-download measurement, and rejects a retained snapshot without trusted
acquisition. [Official Trivy database flags](https://github.com/aquasecurity/trivy/blob/v0.75.0/pkg/flag/db_flags.go).

The CLI accepts `--repository`, `--version`, `--commit`, `--platform`,
`--component`, `--archive`, `--tested-config`, `--tool-archive`, `--tool-bundle`,
`--cosign` and `--output`, with optional `--database`. A supplied database is a
retained directory containing `trivy.db` and `metadata.json`; output must not
already exist. Only after completed
measurement does the CLI reserve a private directory and atomically write
`scan.json`, then `measurement.json`, without overwriting either file. Scanner or
write failures produce no usable completed output directory.

On 2026-10-07, the actual private AMD64 pair at source `750f440` completed this
producer with the pinned official Trivy signature verified again and database
`sha256:5f4b978a55284b1997dc31e9f2fc3f4f1abae80829f51451ade221d5675a69b9`.
It retained zero OS findings, 21 backend module findings and one Caddy module
finding; no finding dispositions or publication approval were inferred. The owned
download CLI subsequently repeated both AMD64 scans with the same database bytes
and retained independent acquisition metadata. Twelve
focused fixtures passed, including the ARM64 asset/signature path. Native ARM64
execution and live signed scanner measurements remain pending.

### Deriving the image scanner gate

`tools/aggregate_release_image_scans.py` authenticates both completed native
smoke/runtime measurements and all four scanner measurements before deriving a
`final-image-scanners` report. It matches every child/configuration/source subject
to the final binding and checks the full raw scanner report against its measured
hash, retaining all findings. It requires the pinned scanner/signature profile and
owned official database acquisition; missing architecture coverage, modified raw
reports, snapshot-only acquisition and OS/non-application/non-Go findings fail.

For Go findings, the aggregator authenticates the typed actual native compiler
measurement and reads its hashed raw dependency graph and official Go advisory
bytes. Binary/configuration identity, toolchain, module versions/h1 checksums,
build settings, native platform and runtime source asset must correspond exactly.
The backend proof requires byte-identical reproduction using the actual release
builder and Dockerfile layout. The upstream Caddy proof instead requires the
fresh official source/checksum signatures, signed source/executable archive
bindings, actual executable member hash and original vendor/module/build settings;
an identical Caddy rebuild is not required.

The only implemented dismissal is that **every** affected Go import path listed
by the official advisory is absent from that exact compiler graph. It unions paths
across all advisory ranges conservatively rather than approximating version or
symbol reachability. Missing imports, uncertain aliases/withdrawal, changed
module versions or any present affected package prevent a success report.
The derived finding retains its original content/hash, source inputs, binary,
graph and advisory hashes, affected paths and explanation. A caller's disposition
or review flag cannot authorize a finding. [Public Go vulnerability database and OSV format](https://go.dev/doc/security/vuln/database).

Eight fixture checks passed for this aggregation boundary. On the private AMD64
`750f440` pair, the actual 236-package backend graph reproduced its executable
byte-for-byte and supported structural derivation for all 21 module findings;
the 970-package Caddy graph matched 147 module identities and the signed upstream
source/executable evidence, supporting the one remaining finding. Those local
facts produced no signed, complete release gate. Native ARM64 measurements,
trusted workflow signing and full-binding live aggregation remain pending.

- [ ] Add completed source/image scanner producers with exact targets, tool/database/date and reviewed finding dispositions.
- [ ] Add full corresponding-source completeness evidence and authenticated distribution review.
- [ ] Add relevant completed upgrade/recovery evidence.
- [ ] Produce and sign actual registry/anonymous-pull/asset-readback/provenance observations during held publication.
- [ ] Wire producers and signed native measurements into the trusted workflow and exercise them live.

## Reservation and publication order

`reserve_draft` is a context manager around the transport's repository-wide
serialization lease. The lease remains held until the caller exits the context;
every publishing mutation must occur inside it. The adapter must reject failed,
unauthorized or incompletely paginated lookups rather than represent them as
absence. Inspect all authenticated release pages for matching drafts and inspect
both version tags. Existing drafts, releases or either registry version tag fail
fresh reservation, even when their bytes appear identical.

Only a newly created empty stable draft is accepted, with an exact remote source
tag, enabled immutable-release policy and `make_latest: false`. GitHub's
`target_commitish` is ignored when the tag already exists, so a transport must
resolve and recheck the tag rather than trusting that request parameter.
[GitHub release API](https://docs.github.com/en/rest/releases/releases).

The prepared order is: reserve; push four reviewed children and two indexes by
digest; verify the registry pair and anonymous pulls; attest all mandatory and
source subjects; upload source assets, bundle and detached manifest without
replacement; verify downloaded assets and provenance; create absent version tags;
verify both tags and remote source; publish the complete immutable draft with
`make_latest: false`; verify public assets/pulls; then advance convenience tags.
GitHub recommends attaching every asset to a draft before publishing an immutable
release. [Immutable release preparation](https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository).

GHCR version tags are mutable registry references. The transport enforces absence
immediately before each write under the held lease and never replaces an existing
version tag. This is a publishing policy, not a claim that
GHCR prevents an administrator from retagging. Deployment always selects the
authenticated image digests rather than trusting a convenience tag.

`ready_release_request` requires the reserved draft ID, complete uploaded assets
with exact checksums, the complete version tag pair and independently verified
registry, anonymous-pull, asset-readback and provenance reports. Provenance must
cover every updater and source subject. The evidence adapter enforces repository,
signer workflow, exact source digest/ref, GitHub OIDC issuer and hosted-runner
policy consistently with `deploy/update.py`.
[GitHub CLI attestation verification](https://cli.github.com/manual/gh_attestation_verify).

## Transport contract

The caller prepares an authenticated `PublicationPlan` with `GhEvidenceVerifier`
and creates `GitHubReleaseTransport` with explicit workflow/inspection credentials
and `WorkflowContext.from_environment(plan)`. Publishing requires job `publish`
in the selected active, hosted, version-tag push run of
`.github/workflows/release.yml`. The transport reads that run and exact committed
workflow source, requiring the literal workflow-level concurrency group
`container-release-publication` and `cancel-in-progress: false`. Its local lock
remains held throughout `reserve_draft`; a local lock alone cannot serialize
other hosted runners or external writers.

The inspection credential only reads immutable-release policy. That endpoint
requires Administration read permission; unavailable/disabled policy fails
closed. No permissions or repository settings are changed.
[Immutable release policy API](https://docs.github.com/en/rest/repos/repos#check-if-immutable-releases-are-enabled-for-a-repository).

Inside the reservation, `push_images(archives, indexes, tested_configs=...)`
requires all four OCI exports and their exact native smoke-tested configuration
digests. `assemble_release_oci.inspect_archive` validates every blob, descriptor,
configuration, release/source label and decompressed layer identity before any
registry write. The caller must take configuration digests from the authenticated
native smoke evidence. Skopeo's raw manifest inspection must agree with both that
graph and the prepared child digest. Fixed `skopeo copy --preserve-digests`
commands push children by digest using a temporary private authentication file;
the two deterministic index documents are uploaded unchanged. Returned bytes and
registry digest headers must match. No image rebuild or Docker archive conversion
occurs in the transport.
[Skopeo copy contract](https://github.com/podman-container-tools/skopeo/blob/main/docs/skopeo-copy.1.md).

`inspect_pair` records registry manifests; `anonymous_pull` additionally copies
all four children to disposable OCI layouts with `--src-no-creds` and an empty
auth file, checking preserved manifest digests. `upload_assets` requires the
complete image pair and uploads corresponding sources first, bundle next and
detached manifest last. It refuses any existing asset name. `inspect_assets`
downloads all assets and checks bytes, sizes and checksums. Authentication is
removed before following an allowlisted asset-storage redirect.

`create_version_tags` requires both linked container packages to be public and
the exact digest pair to be anonymously readable. `publish` calls
`ready_release_request` itself with freshly inspected remote state and the
authenticated registry, anonymous-pull, asset-readback and provenance reports;
caller-supplied publication JSON cannot bypass those gates. `verify_public`
checks the immutable public release, both version tags, downloaded assets and
complete anonymous child pulls. Convenience-tag advancement is not implemented.

HTTP requests use fixed HTTPS hosts, bounded JSON/assets and deadlines without
ambient proxy configuration. GitHub lookups are completely paginated within a
fixed bound. Registry absence is recognized only from a structured 404
`MANIFEST_UNKNOWN`/`NAME_UNKNOWN`; authentication, throttling, server errors or
incomplete lookups fail. Commands have bounded output/time and private process
groups. Tokens are not placed in command arguments or durable receipts.

## Interrupted publication

Neither images nor release assets form a cross-service atomic transaction.
Children/indexes or one version tag may exist after a failure, but the draft stays
unpublished and convenience tags stay unchanged. A failed asset upload can leave
a `starter` entry; readiness rejects it. [GitHub asset API](https://docs.github.com/en/rest/releases/assets).

`GitHubReleaseTransport.reconcile` reads bounded, private, source-bound local
receipts and reports unresolved mutation intents alongside authenticated remote
release/assets, both index/child maps and version tags. A missing local journal is
reported explicitly; a substituted or incomplete journal fails. Intent and
completion/uncertainty records are flushed and fsynced before and after every
mutation. An existing journal is never reopened for mutation. The core's
`recovery_report` also records partial state; both paths disallow automatic
resume, cleanup and ready advertisement. Preserve the draft, original run receipts,
assets and registry digests. An operator must authenticate every existing artifact
against its original binding before choosing recovery; a new reviewed version is
the default path. Published immutable releases are incidents, not rewrite targets.
There is no unattended delete-and-reupload or overwrite path.

## Remaining integration

- [x] Implement bounded authenticated gate verification and test its exact identity/snapshot policies.
- [x] Implement GitHub/GHCR transport, durable mutation receipts and read-only interruption reconciliation with fixtures.
- [ ] Validate signed live workflow reports and authorized source/legal review approval policy.
- [ ] Enforce one repository-wide publishing concurrency group across every writer; local locking does not serialize hosted runners.
- [ ] Wire permissions and pinned attestation actions into the trusted release workflow after all distribution gates pass.
- [ ] Verify immutable-release settings, branch/tag protection and supported registry behavior on disposable live publication.
- [ ] Make both packages public through the approved repository setup and validate anonymous installation.
- [ ] Exercise partial push/upload/publication failures and recovery against the live APIs.

GitHub concurrency groups restrict simultaneous writers, but ordering is not
guaranteed and queued runs can replace other pending runs. Do not infer ownership
or a recovery right from workflow ordering. [GitHub concurrency](https://docs.github.com/en/actions/concepts/workflows-and-actions/concurrency).
New container packages default to private; repository visibility alone does not
establish anonymous availability. [GHCR access and visibility](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).

The preparation and transport fixture checks do not validate live package visibility, registry
writes, live attestations or an actual public release. No runtime/legal completion
is claimed by adding this core.

Validation: publication, transport, evidence and OCI fixture checks exercise
absent gates, altered source/configuration/artifacts, scanner errors, partial
pushes/uploads, exclusive draft reservation, authenticated API failures, anonymous
pull/download boundaries and durable interruption receipts. The transport's
17 tests passed locally. At the report-producer checkpoint, 13 producer fixtures
and the combined `test_release_*.py` suite passed 139 checks in 7.79 seconds; Ruff
and Prettier checks passed. Live publication and recovery remain pending.

### Publication command

`tools/publish_verified_release.py` drives the real transport and official signing
bridge. It accepts an absolute-path JSON input map with exactly `manifest`,
`bundle`, `indexes` (`backend`/`web`), `archives` (all four component/architecture
targets), `source_assets` (asset name to path), and `reports` (all eight
pre-publication gates). The command also requires `--root`, `--reviewed-commit`
and an absolute private `--state` directory. Repository, tag, source SHA, active
hosted workflow/job identity and run attempt come from the workflow environment.
No configuration identity, signer command or approval boolean can be supplied.

The command snapshots inputs privately and verifies the full release binding and
authenticated gates. It derives the four tested configurations from the signed
native smoke report and validates all four actual OCI exports before publication
transport operations. The production adapters check the exact current workflow and
existing public, repository-linked GHCR packages before draft reservation. An
absent/private package requires separate approved setup; this command cannot
bootstrap a package or change its visibility, and stops before a release version
is reserved.

Under the held repository lease, it pushes the reviewed children and indexes,
checks registry contents and actual anonymous pulls, signs and independently
verifies all updater/source subjects, uploads sources/bundle/manifest, measures
downloaded assets, signs and verifies the actual readback reports, then creates
the version tag pair and publishes the immutable draft. A receipt is written
only after anonymous release/image/asset checks complete. Convenience tags are
not advanced by this command.

Subject signing and every readback signature have durable intent, completion or
uncertainty records alongside transport mutations. After the publication lease
is entered, any failure preserves exact private input snapshots and their
binding record. Snapshot files and all containing directories are synced before
the first remote mutation. An existing version journal blocks a subsequent invocation;
there is no automatic retry, resume, draft replacement or remote cleanup. The
CLI retains only explicit credentials and checked workflow/OIDC inputs for the
official signing adapter, and prints no tokens, API payloads or tool diagnostics.

Ten fixture checks exercise the actual transport lifecycle, snapshot durability, ordering, absent
gates, substituted native configurations/archives/reports, first-package policy,
interrupted pushes/signing, retained inputs and the no-retry boundary. They use
explicit fixture signers/verifiers and confer no publication authority. Official
hosted signing, live package policy/readbacks and workflow integration remain
unverified; the candidate workflow continues to create no releases.
