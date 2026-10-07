# Container publication preparation

`tools/publish_container_release.py` implements publication preparation and
reservation/readiness boundaries. It has no GitHub/GHCR transport, credential
handling, attestation signer or production deployment command. The existing
release workflow still verifies candidates only. This preparation does not
approve a release, make packages public or establish live publication success.

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
JSON `passed: true` is not an authentication method. No production verifier is
implemented. The verifier used by fixture tests is deliberately a test adapter.

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

## Reservation and publication order

`reserve_draft` is a context manager around the future adapter's repository-wide
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

GHCR version tags are mutable registry references. The future publisher must
enforce absence immediately before each write under the held lease and must never
replace an existing version tag. This is a publishing policy, not a claim that
GHCR prevents an administrator from retagging. Deployment always selects the
authenticated image digests rather than trusting a convenience tag.

`ready_release_request` requires the reserved draft ID, complete uploaded assets
with exact checksums, the complete version tag pair and independently verified
registry, anonymous-pull, asset-readback and provenance reports. Provenance must
cover every updater and source subject. A future verifier must enforce repository,
signer workflow, exact source digest/ref, GitHub OIDC issuer and hosted-runner
policy consistently with `deploy/update.py`.
[GitHub CLI attestation verification](https://cli.github.com/manual/gh_attestation_verify).

## Interrupted publication

Neither images nor release assets form a cross-service atomic transaction.
Children/indexes or one version tag may exist after a failure, but the draft stays
unpublished and convenience tags stay unchanged. A failed asset upload can leave
a `starter` entry; readiness rejects it. [GitHub asset API](https://docs.github.com/en/rest/releases/assets).

`recovery_report` records observed partial state and explicitly disallows automatic
resume, cleanup and ready advertisement. Preserve the draft, original run receipts,
assets and registry digests. An operator must authenticate every existing artifact
against its original binding before choosing recovery; a new reviewed version is
the default path. Published immutable releases are incidents, not rewrite targets.
There is no unattended delete-and-reupload or overwrite path.

## Remaining integration

- [ ] Implement and review the authenticated evidence verifier and source/legal review approval policy.
- [ ] Implement bounded GitHub/GHCR transport, durable mutation receipts and interruption reconciliation.
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

The preparation's fixture checks do not validate package visibility, registry
writes, live attestations or an actual public release. No runtime/legal completion
is claimed by adding this core.

Validation: 16 publication fixture tests passed together with artifact/candidate
regressions (42 tests, 5.42 seconds). Ruff checks and formatting passed for the
two new Python files, and Prettier checks passed for this document. Fixtures cover
the absent gates, altered source/artifacts, scanner errors, partial pushes/uploads,
exclusive draft reservation and API failure/lease release paths.
