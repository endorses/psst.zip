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
runtime pack and image configuration IDs. It requires all application, runtime
offer, authentication/restart and cleanup checks. Before and after the harness,
it validates the OCI exports against the selected child/configuration identities
and verifies immutable pack/source-asset bytes. The typed result is a measurement
with `publication_authorized: false`; candidate smoke without a runtime pack
cannot produce it.

`aggregate_native_reports` first authenticates both architecture measurements
with `GhEvidenceVerifier.authenticate`, preserving the same signer/source/ref,
snapshot and process policies as gate-report verification. Only then does it
generate complete `final-image-smoke` and `runtime-notices` gate objects bound to
all release subjects. The smoke report records native/emulated execution and all
four tested configuration digests for transport. Runtime evidence retains notice
hashes, source-asset identity and the requirement for distribution review. The
trusted workflow must sign the exact emitted report bytes before preparation;
these producers neither sign reports nor grant publishing authorization.

`source_asset_measurements` checks every corresponding-source asset against its
bound digest and records sizes. It explicitly leaves source completeness
unverified and creates no `corresponding-source` success gate. Hash equality and
source-pack preparation flags cannot establish complete corresponding sources or
replace the independent distribution review.

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
