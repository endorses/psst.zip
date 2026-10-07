# Optional Docker Hub mirror

GHCR is the primary psst.zip registry. Mirroring is optional and starts only after
the complete immutable GitHub release, source assets, attestations and anonymous
GHCR pulls have passed their publication checks. Production continues to select
the GHCR digests recorded in that release's authenticated manifest. A failed or
partial mirror must not change the manifest, undo the GHCR release, advance
convenience tags, or make production choose a different image.

This document defines the operator procedure. No Docker Hub namespace, token,
workflow, or live mirror has been configured or verified for this project.

## Repository and credential setup

Create public `psst-zip-backend` and `psst-zip-web` repositories in the selected
Docker Hub namespace. Restrict automated version-tag writes to one trusted
publisher, serialize both repositories under one mirror job, and enable immutable
version tags where available. An existing destination version requires inspection
and explicit recovery; never overwrite it or automatically adopt a partial pair.

Use a dedicated automation identity and token with the minimum read/write access
needed for these two repositories, without delete or repository administration.
A personal token inherits its account's repository access: a token name alone
does not restrict it to these repositories. For an organization, use a scoped
organization access token when supported by the account's plan. These choices
follow Docker's [repository access
documentation](https://docs.docker.com/docker-hub/repos/manage/access/) and
[organization token
documentation](https://docs.docker.com/security/access-tokens/organization-access-tokens/).

Store the token in a dedicated protected mirror environment. Keep it out of pull
request jobs, the primary build, the VPS, release bundles, and logs. Give the
mirror job read-only GitHub access, pin its Actions and copying tool, and require
the reviewed workflow on the protected branch. Authenticate with password-stdin
into a temporary mode-0600 auth file inside a mode-0700 directory; destroy that
directory on success or failure. Rotate the token independently from GHCR and
production SSH credentials. Do not pass it in command arguments or print it.

## Copy the already released image pair

Authenticate and validate the release manifest using the policy in
[container publication](container-publication.md). Take each component's complete
`images.<component>.index` reference from that manifest, including its digest.
Use the exact release identifier as the mirror version tag. Confirm the selected
namespace and both destination version tags are absent through authenticated
registry inspection. Network, authentication, pagination, and permission failures
must fail the check; an error is not evidence of absence.

Copy each complete multi-platform index and its children without rebuilding,
loading/resaving through Docker, recompressing, or converting manifest formats:

```sh
skopeo copy --all --preserve-digests --src-no-creds \
  --dest-authfile "$mirror_auth_file" --retry-times 0 \
  "docker://$verified_ghcr_backend_index" \
  "docker://docker.io/$reviewed_namespace/psst-zip-backend:$release_version"
skopeo copy --all --preserve-digests --src-no-creds \
  --dest-authfile "$mirror_auth_file" --retry-times 0 \
  "docker://$verified_ghcr_web_index" \
  "docker://docker.io/$reviewed_namespace/psst-zip-web:$release_version"
```

The variables above are validated operator inputs, not executable snippets from
release notes. TLS verification stays enabled. Skopeo's
[copy documentation](https://github.com/podman-container-tools/skopeo/blob/main/docs/skopeo-copy.1.md)
defines `--all` as copying the complete list and its images, and
`--preserve-digests` as refusing a copy that cannot retain the digests. Tool exit
success alone is insufficient: inspect the destination bytes afterward. If the
registry cannot accept the original bytes, report an unsupported mirror rather
than generating a different release under the same version.

## Verification and partial-copy recovery

Read each destination index as raw bytes through an anonymous client with no
saved registry credentials. Compute SHA-256 over those exact bytes and compare it
with the primary manifest's index digest. Verify the destination index's complete
AMD64/ARM64 child map against `platform_digests`, including any original attached
attestation descriptors. Reject additional runnable platforms or changed child
digests. Pull all referenced children and layers anonymously into disposable OCI
stores using `--all --preserve-digests --src-no-creds`; a metadata lookup does not
prove a public complete pull. Remove the stores after recording the result.

Record the release/source commit, both source and destination repository
references, exact index and child digests, copying-tool version, UTC date, and
verification results. Source correspondence remains the same immutable GitHub
release assets. OCI attachment copying is not a replacement for the GitHub
attestation identity check; attestations must still verify against the trusted
primary repository, workflow and source commit.

Only advertise a complete mirror after both components pass. If one copy fails,
record the surviving destination artifacts and inspect them before any retry.
Do not automatically replace, delete, retag, or rebuild them. Keep the complete
GHCR release usable while the mirror is repaired. Advance optional mirror
convenience tags only after the complete pair's anonymous readback passes; they
remain discovery tags and are never used by production update tooling.

## Verification status

- [x] Document separate credentials, digest-preserving paired copying, anonymous
      complete readback, corresponding-source/attestation references, and
      partial-mirror failure handling.
- [ ] Choose and configure a Docker Hub namespace and public repositories if a
      mirror is wanted.
- [ ] Run and verify this procedure against an actual published GHCR release.
