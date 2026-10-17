# Draft: New App — psst.zip

This is a submission draft, not a filed inclusion request. Publish the prepared
`android-v0.1.1` release and complete the actual upstream comparison before using
this text in a merge request.

psst.zip is an AGPL-3.0-only Android client for the self-hostable psst.zip encrypted
file-transfer server. It supports sending files, receive invitations, QR/link guest
downloads and Android sharing. Users configure their own server. A regular account
is needed to create links; there is no public signup. No proprietary service or
runtime SDK is required.

Source: https://github.com/endorses/psst.zip

Proposed package: `zip.psst.android`; candidate version: `0.1.1` / `4`.

The recipe uses the upstream developer-signed APK through `Binaries`, restricted
to the existing production certificate with `AllowedAPKSigningKeys`. Keeping the
same certificate matters for updates from the existing GitHub APK distribution.
The build preserves the sibling Android/shared composite project, uses pinned
Gradle/SDK tools and explicitly selects the unsigned app output.

Upstream handles the scanner's normal wrapper cleanup without restoring binaries
or adding a scanner exemption. Exact unstaged wrapper deletions retain the source
revision; modified or untracked real build inputs do not. No production key is
needed by the build or this submission.

Upstream signing preserves zipalign's padding with current Build Tools 37. Run
fdroidserver 2.4.5's signature copier on Python 3.13; Python 3.14 changes generated
ZIP metadata flags. The packet records both compatibility checks.

English and German descriptions, changelog and approved-brand icons are supplied
upstream in Fastlane metadata. The local dependency review found only FLOSS runtime
libraries; bundled AndroidX native libraries originate in the Maven dependency.
See the upstream `fdroid/validation.md` record for the exact local checks and limits.

Before submission:

- [ ] Link the published `android-v0.1.1` APK and exact source commit here.
- [ ] Attach/reference successful official recipe build, binary scan and comparison
      against that published APK, with the expected production certificate.
- [ ] Add real disposable-data screenshots if available; never pairing credentials.
- [ ] Replace this draft's pending status with the actual GitLab pipeline evidence.
