# Native source and license discovery

Android settings and iOS settings provide **Source & licenses**. The iOS share extension exposes the same view. Each client packages the project AGPL text, full third-party notices and a dependency inventory. Client source downloads identify the Git commit used by a clean native source tree; development builds with tracked native edits omit the exact-source link. The project repository remains accessible.

Hosted-server metadata comes from the configured server's `/licenses/release.json`, independently of the authenticated API client. Requests use HTTPS, do not carry account credentials, reject redirects, time out after ten seconds and accept at most 16 KiB. Metadata requires the psst.zip product identity, AGPL identifier, a full source commit and safe HTTPS source links. An unavailable or invalid record displays an explicit fallback to request the deployed source from the operator. `/legal` on that same server provides the hosted web, backend and runtime notices.

## Inventory generation

With Java, the Android SDK and the repository Gradle wrapper available:

```sh
python3 tools/generate_native_notices.py
python3 tools/generate_native_notices.py --check --offline
```

The generator resolves Android's **releaseRuntimeClasspath** and the iOS arm64, simulator arm64 and x64 compile-klib configurations. It excludes the project's own Shared module from third-party resolution. Android R8 may remove unused code; the inventory conservatively retains every resolved runtime dependency. Each inventory records artifact checksums, full effective POM licensing metadata including parent POM hashes, source locations and the hashes of preserved embedded license, notice and copyright files. Nested AAR `classes.jar` notices are included.

Kotlin/Native and SKIE inject runtime code outside the main Maven artifact graph. Their notices are separately captured from the selected upstream versions, including Kotlin's full copyright and notice files and the text files in its native distribution licensing directory. These conservative distribution notices can include components which are not linked into a particular app. Apple SDK license documents are excluded; Apple system frameworks are supplied by the operating system.

The Apache license copy follows the effective POM declaration. MIT dependencies require a reviewed component/version and the actual upstream copyright-bearing text; a generic MIT template is rejected. The current SLF4J snapshot includes its complete upstream license and copyright. `shared/licenses/source-inventory.json` records immutable Git source revisions, original upstream hashes and hashes of the packaged whitespace-normalized text. Only line endings, trailing spaces and extra final blank lines are normalized. Unknown licensing metadata, changed compiler/runtime versions, missing source snapshots or changed notice bytes fail verification and require an explicit refresh/review.

Generated resource locations are:

- Android: `android/app/src/main/assets/licenses/`.
- iOS app and extension: `ios/Shared/LegalResources/`, included through their shared XcodeGen target template.

A lightweight source/build freshness check is also available:

```sh
python3 tools/generate_native_notices.py --check-inputs-only
```

This checks captured upstream notices, build-input hashes, aggregate notice bytes and exact bundled AGPL copies. It does **not** repeat artifact resolution or replace the full `--check` gate. Regenerate inventories after changes to build configuration, dependency versions or the generator itself.

## Validation boundaries

Focused Android JVM tests and portable Swift tests check operator origins, capability/credential rejection, exact source revisions and bounded metadata. Browser checks confirm source discovery is available before sign-in and missing metadata does not fabricate an exact-source offer. The generator tests cover nested notice retention, copyright preservation, oversized inputs, unknown MIT versions and stale upstream snapshots.

Linux can compile and package the Android app, resolve the iOS klib licensing graph and compile the portable Swift metadata helper. iOS UI compilation, actual app/share-extension resource packaging and XCTest execution require the macOS CI job. Source/resource checks alone do not establish those native build results or store approval.
