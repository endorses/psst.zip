# F-Droid candidate validation — October 17, 2026

This is local preparation evidence, not F-Droid admission or verification of a
published 0.1.1 APK. No production signing key or phone data was accessed. No new
GitHub release, GitLab fork, merge request or F-Droid repository was published.

## Verified results

| Check                                 | Actual result                                                                                                                                      |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Official metadata formatting/lint     | fdroidserver 2.4.5 `rewritemeta -l` and `lint -f`: passed with official fdroiddata category configuration/icons                                    |
| Update checks                         | Properties parse as 0.1.1 / 4; Android-only tag expression rejects generic server/iOS/prerelease tags                                              |
| Official whole-source scanner         | Refreshed SUSS rules; zero fatal problems and warnings; normal wrapper removal, no exemptions                                                      |
| Official recipe build and binary scan | `fdroid build --test --no-tarball --scan-binary --stop zip.psst.android:4`: one build succeeded; source/APK class/signing-block scans passed       |
| Isolated second build                 | Official buildserver image, JDK 21, separate source/output/cache path, no Gradle build cache: all 73 tasks executed; build passed                  |
| Reproducibility                       | JDK 25 host and JDK 21 container unsigned APKs: byte-identical, SHA256 below                                                                       |
| APK release verifier                  | Both APKs passed package/version/revision, SDK 26–37, non-debuggable, notice bytes and four-ABI 16 KB ELF/ZIP alignment checks                     |
| Signature-copy integration            | Current signing helper, disposable RSA4096 certificate, Build Tools 37; official F-Droid signature copy and verification passed on Python 3.13.12  |
| Negative signature check              | Changing the packaged release metadata before copying the same signature was rejected by apksigner                                                 |
| Focused regressions                   | 45 Android tooling tests in 0.33 seconds; five CI scope tests in 1.22 seconds; public-certificate scanner integration in 0.25 seconds; Ruff passed |
| Listings                              | English/German text length, encoding and whitespace checks passed; approved icon export is 512×512 in both locales                                 |

Built source commit:

```text
9713d2945df3a0024ff51eba6f23262d0811ab7b
```

Both unsigned APKs: **3,803,375 bytes**; SHA256:

```text
b56a3515aee6d1bff5f335a4e66f67e8cb59dd030c2ea11a67d524af6f033808
```

That commit contains the provenance/unsigned-build fix and version bump. The
subsequent signing-helper padding fix was exercised directly against these built
APKs with a disposable key. Listing/packet changes are later commits too. A final
public release will therefore have its **own source revision and APK checksum**;
these candidate hashes must not be reused as release evidence for another commit.

## Environments and commands

Official buildserver image resolved on the review date:

```text
registry.gitlab.com/fdroid/fdroidserver@sha256:9cb68105642ca4e7b295f0ceab10f069f5b3247dc18fa7c36046e9d81aa469a8
```

It provides Debian 13, JDK 21 and Python 3.13.5. The separate local environment used
JDK 25.0.3, Python 3.14.7 and fdroidserver 2.4.5 for lint/build/scanning. An isolated
Python 3.13.12 environment with the same fdroidserver release passed its full
signature-copy check. The image's Python 3.13.5 independently produced the same
signed bytes too. Python 3.14 is unsuitable for that operation with this tool
release; its ZIP writer clears the UTF-8 flags on the three recreated ASCII-named
v1 signature entries, invalidating their enclosing v2/v3 digest. This is a measured
runtime compatibility exception, not a reason to downgrade the Android SDK.

The local recipe harness changed only its source repository/commit to the local
candidate checkout, removed the unavailable future `Binaries` URL and omitted the
SDK installation command because those exact components were already installed.
It used installed Gradle 9.8.1 in place of F-Droid's launcher. Removing `Binaries`
was solely for **unsigned candidate testing**; the submitted recipe retains it and
the production signer allowlist. The first checkout was seeded before the build
because 2.4.5 initializes `SOURCE_DATE_EPOCH` before fetching a missing checkout.

The isolated image build ran the recipe's SDK manager command and then:

```sh
export SOURCE_DATE_EPOCH="$(git log -1 --format=%ct)"
gradle -p android -Ppsst.unsignedRelease=true \
  :app:assembleRelease --no-daemon --no-build-cache
```

Its checkout had the scanner's six wrapper deletions. Public Maven dependency
artifacts were mounted read-only to avoid downloading them again; the Gradle
working/task cache and outputs were separate. Installed Gradle's distribution
checksum matched the checked-in pin and F-Droid's transparency log. The container
was not given signing credentials, an operator home directory, an ADB socket or
production-server access. The source build inside the container took **2m36s**;
the successful full official local recipe command including source/binary scans
took **86 seconds**. Temporary harness corrections were not successful validations.

For final public verification, use the unmodified recipe in a configured fdroiddata
checkout, with stable fdroidserver on Python 3.13:

```sh
fdroid rewritemeta -l zip.psst.android
fdroid lint -f zip.psst.android
fdroid checkupdates --allow-dirty zip.psst.android
fdroid build --test --scan-binary --stop zip.psst.android:4
```

That final command must download the actual published candidate APK, compare it
and verify its expected certificate. The preparation commands above did not do
that. They establish that the recipe can build and the signature-copy path works
with disposable signing, not that an unpublished production artifact passed.

The compatibility investigation reproduced the default Build Tools 37 padding
failure, then the Python 3.14 ZIP-flag failure. Keeping `--alignment-preserved true`
and running the supported Python 3.13 comparison resolved them without changing
payloads, disabling cryptographic verification or accessing the production key.
The padding behavior is documented by the
[signature copier's maintainer](https://github.com/obfusk/apksigcopier/issues/105).

The commit hook initially classified the recipe's public certificate fingerprint
as a generic API key. The reviewed exception permits only that exact published
digest in the `AllowedAPKSigningKeys` field of `fdroid/metadata/zip.psst.android.yml`.
The real-scanner integration confirms that a changed digest or the same value in
another file remains detected. No detector, directory or credential type was
disabled.

## Remaining external work

- [ ] Merge/review the preparation and publish a new 0.1.1 / code 4 Android tag/APK
      through the existing protected signing workflow. Preserve 0.1.0 unchanged.
- [ ] Run the official recipe against that final public source and APK, retaining
      the actual source hash, certificate and successful comparison evidence.
- [ ] Add real disposable-data English/German screenshots if available. This is
      listing polish; no fabricated screenshots or private phone data were used.
- [ ] Submit the packet from an operator-controlled GitLab account after the
      operator is ready, then address F-Droid's independent review/build results.

No native iOS build or device journey was run for this Android packaging-only
phase; no iOS/shared feature or protocol implementation was changed. The ordinary
required CI checks still apply when these source changes are pushed. No additional
full browser/backend/iOS suite or long-running CI job was added.
