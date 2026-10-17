# Official F-Droid submission packet

This packet prepares **psst.zip** (`zip.psst.android`) for the official F-Droid
repository using the existing developer signing certificate. It has **not** been
submitted to GitLab. The operator requested packet preparation first.

The candidate is **0.1.1 / code 4**. Its `android-v0.1.1` tag and signed APK still
need to be published through the protected Android release workflow. The recipe's
`Binaries` URL intentionally refers to that future release. Do not submit it as a
working public download, remove signature verification, or repoint the immutable
`android-v0.1.0` release. The existing installed app and production signer stay
unchanged.

## Contents and identity

`metadata/zip.psst.android.yml` is the file to copy into a fork of
[fdroiddata](https://gitlab.com/fdroid/fdroiddata). The upstream listing lives in
`../fastlane/metadata/android/{en-US,de-DE}/`: descriptions, release notes and
512px icons exported from the existing approved brand SVG. F-Droid reads these
from the selected source tag; they do not need duplicating in fdroiddata.

The production certificate SHA256 is:

```text
6f10e624c6f577544bd01928b33d834ad5434e54363313617e30fe2dfa835b15
```

`AllowedAPKSigningKeys` and the versioned upstream `Binaries` URL require
F-Droid's independent comparison before it can distribute that developer-signed
APK. This preserves updates between the intended GitHub and F-Droid channels.
No private signing key, password, mapping identity or GitHub token belongs in the
submission. F-Droid generates no replacement production identity for this recipe.

## Eligibility review

The captured Android runtime inventory has 123 FLOSS artifacts: 122 Apache-2.0
and one MIT. See the committed
[dependency inventory](../android/app/src/main/assets/licenses/dependency-inventory.json)
and [notices](../android/app/src/main/assets/licenses/THIRD_PARTY_NOTICES.txt).
No proprietary SDK, Firebase, Google Play Services, advertising or analytics
runtime dependency was found. Runtime dependencies resolve from Google Maven and
Maven Central; Gradle plugins also use the Gradle Plugin Portal.

The app uses the AGPL-3.0-only repository license. Nunito includes its SIL OFL
license; the brand assets use the repository license. The brand README's stale
Apache license description was corrected. Native AndroidX graphics-path libraries
for four ABIs come from its FLOSS Maven AAR, rather than committed application
binaries. This is not a claim that every native dependency was rebuilt from its
own source. The tracked binary review identified the two Gradle wrapper JARs and
the licensed Nunito font; the official scanner removes the wrappers normally.

The server is self-hostable and AGPL-licensed. Its address is operator configured,
not tied to a mandatory hosted service. Creating transfers requires an
administrator-provisioned regular account; receiving guest download links does
not require signing in. The listing documents operational metadata and device-held
receive keys instead of promising that the server sees no metadata.

## Build choices

The recipe builds from `android/` with the sibling `shared/` composite intact and
selects the explicit `app/build/outputs/apk/release/app-release-unsigned.apk` output.
`-Ppsst.unsignedRelease=true` opts into unsigned release packaging; signing remains
a separate protected operation. The existing environment-variable opt-in continues
to work for GitHub. An unset or false opt-in still blocks release packaging.

The signer explicitly uses `--alignment-preserved true` after `zipalign -P 16`.
Build Tools 35 and newer otherwise replace ZIP padding in a way the signature
copier cannot reproduce. This retains Build Tools 37 and the existing certificate;
it does not weaken signature checks or 16 KB alignment verification. See the
[upstream compatibility explanation](https://github.com/obfusk/apksigcopier/issues/105).

F-Droid removes both projects' `gradlew`, `gradlew.bat` and
`gradle/wrapper/gradle-wrapper.jar`. The source-revision helper accepts only those
six exact **unstaged deletions**. Modified wrappers, staged deletions, changed
Android/shared sources, changed license/helper or untracked build inputs still
produce an empty revision, which the publication verifier rejects. The recipe
has no `scanignore`, `scandelete`, source patch or wrapper-restoration workaround.

The inspected official buildserver image provides JDK 21. This is a supported
compatibility boundary for [AGP 9.4](https://developer.android.com/build/releases/agp-9-4-0-release-notes)
and Gradle 9.8.1; upstream GitHub release CI uses JDK 27. The recipe uses the
supported F-Droid JDK instead of adding Debian testing repositories. Its trusted
Gradle launcher reads the existing pinned wrapper properties after wrapper cleanup.
The live F-Droid transparency log contains Gradle 9.8.1 with the same SHA256 as
our checked-in distribution pin.

SDK `platforms;android-37.0` and app Build Tools `37.0.0` are explicit. The recipe
also installs the shared Android library's AGP-default Build Tools `36.0.0` and
platform-tools. Both modules compile against SDK 37; this does not lower the app's
target SDK. The existing Kotlin 2.4.20/SKIE compatibility exception remains.
No Android/iOS app feature, protocol, account or signing migration is introduced.

## Reproduce and submit later

Use the current stable fdroidserver (validated here with **2.4.5**) in an isolated
F-Droid build environment. Run its signature-copy comparison with **Python 3.13**
(also verified with 3.13.12): Python 3.14's ZIP writer changes ASCII filename flags
that this release's bundled signature copier expects to preserve. No monkey patch
or signature-verification bypass is used. A normal fdroiddata checkout includes its category
configuration and icons, which standalone metadata lint also needs. Do not copy
an invented category configuration into this app's recipe.

After publishing the candidate's real Android tag and signed upstream APK:

```sh
# Run in your disposable fdroiddata checkout, with fdroidserver configured.
cp /path/to/psst.zip/fdroid/metadata/zip.psst.android.yml metadata/
fdroid rewritemeta -l zip.psst.android
fdroid lint -f zip.psst.android
fdroid checkupdates --allow-dirty zip.psst.android
fdroid build --test --scan-binary --stop zip.psst.android:4
```

Keep `Binaries` and `AllowedAPKSigningKeys` enabled for that final build. A
successful assembly alone is insufficient: the downloaded upstream APK must also
pass F-Droid's signature-copy comparison and expected-certificate check. If the
comparison fails, compare the actual APK contents; do not silently switch signers,
strip source identity or rebuild until a lucky match appears.

F-Droid's [official container instructions](https://f-droid.org/en/docs/Submitting_to_F-Droid_Quick_Start_Guide/#build-environment)
mount both fdroiddata and fdroidserver into the buildserver image. The image alone
contains the build toolchain, not the `fdroid` CLI. Use a pinned stable fdroidserver
checkout alongside it. Record the resolved image digest, tool versions and source
commit with the final comparison.

Review [validation.md](validation.md) for the actual local evidence and its
limits. After the upstream release comparison passes, use
[submission.md](submission.md) as the proposed merge-request description, adding
the final release URL, source hash and F-Droid pipeline result. Creating a GitLab
fork, pushing a branch or submitting an inclusion request remains a later operator
action; none has been performed.

Screenshots are intentionally absent rather than fabricated. Add real English and
German app screenshots using disposable data before final submission if available.
Do not include the account-pairing QR, private links, device identifiers or user
history. Hardware-accelerated emulator capture is unavailable in this environment
(`/dev/kvm` is absent); no existing AVD or operator phone was modified.

The admission rules and final decision belong to F-Droid:
[inclusion policy](https://f-droid.org/en/docs/Inclusion_Policy/),
[build metadata](https://f-droid.org/en/docs/Build_Metadata_Reference/), and
[developer-signed reproducible builds](https://f-droid.org/en/docs/Reproducible_Builds/).
