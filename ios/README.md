# psst.zip iOS development

The app and share extension are generated from `project.yml` using XcodeGen. Display branding is psst.zip; existing bundle identifiers, App Group, protocol identifiers and storage keys remain compatible.

## Building and testing on macOS

Use macOS with Xcode 16 or later, XcodeGen, and a JDK supported by the repository Gradle wrapper. Set `JAVA_HOME`; the shared Gradle project also configures Android, so install its SDK and set `ANDROID_HOME`.

From the repository root:

```sh
python3 ios/scripts/check_sources.py
cd ios
xcodegen generate
xcodebuild -project Psst.xcodeproj -scheme Psst \
  -sdk iphonesimulator -configuration Debug CODE_SIGNING_ALLOWED=NO build
xcodebuild -project Psst.xcodeproj -scheme Psst \
  -destination 'platform=iOS Simulator,name=iPhone 16' \
  CODE_SIGNING_ALLOWED=NO test
```

Select an installed simulator name from `xcrun simctl list devices available` if necessary. Format Swift with `swiftformat ios --swiftversion 5.9` from the repository root before committing subsequent changes. The generated project is ignored; edit `project.yml` and regenerate it.

The pre-build script builds the shared Kotlin framework, including the authenticated `ApiClient` initializer. The same shared Swift send implementation, theme, login form, scanner and strings are included in the app and extension.

For physical-device builds, select the same development team for both targets and register `group.zip.psst.ios`. Both targets must be provisioned for the shared Keychain access group `$(AppIdentifierPrefix)zip.psst.ios.shared`. The `SharedKeychainGroup` Info.plist value must resolve to the same prefix as that entitlement. Sessions and new resource keys/deletion capabilities use `AfterFirstUnlockThisDeviceOnly` Keychain items; bearer tokens are never copied to App Group preferences. A Keychain signing mismatch is an error, not a fallback to unprotected storage. Camera permission is needed for QR pairing in either target.

## User workflows and limits

Home, Scan, Receive and History are the main destinations. Scan opens without an account; authenticated link creation remains in Send and Receive. Settings groups Appearance, Server & account, and Connection, with Sign out separate. Account editing is a dedicated draft form with explicit pairing confirmation; cancellation preserves the current session. Appearance defaults to System, updates the app immediately and is saved in App Group preferences independently of the account. The share extension uses the same saved choice; QR codes always retain a white background. Login checks the API and public link pages before saving the server. Sessions are origin-bound; authenticated redirects are refused. Session expiry offers login on the authenticated task while preserving the selected task for reauthentication to the same account. Choosing a different account clears selected work and private navigation state.

The share extension supports login in place: attachments remain available while signing in, so a handoff that loses access is unnecessary. It shows a selected-file summary before sending. Any oversized or unreadable attachment rejects the entire selection; valid attachments are never silently sent as a subset, and temporary copies are removed on rejection. Both app and extension read the selected server’s advertised file-size limit before allocating a transfer. Files use authenticated 4 MiB chunks, with one chunk in memory; encrypted wire sizes include framing overhead. The extension copies provider attachments to temporary files without loading them whole. Actual extension memory and large-file performance still need profiling on a device. Uploads are not background services: the main app stops them on backgrounding; extension dismissal asks for confirmation and cancels work. Cancellation attempts to remove partial server data. If cleanup cannot finish, its persisted record remains actionable in History.

Receive links are saved when created. Reopening one from History uses the original slot/key and offers Save files. Per-file checkpoints survive a partial failure; Retry saving uses the original slot, skips successful writes and acknowledges a child transfer only when every file is durably saved. Files are stored in Documents/Received and available through document preview, export and the Files app. Already-saved copies remain when links are revoked.

History merges the account’s server metadata with local titles and Keychain capabilities. Cross-device entries without keys remain manageable and explain why their full links are unavailable. Revocation happens before removing local history. The previous JSON history is migrated on the first successful coordinated file write; decoding or disk errors never overwrite it. Existing records retain unknown ownership: administrators can manage them only on their original server; ordinary accounts cannot see them. No migration assigns old records to an arbitrary first account.

Receive/detail/history polling runs while visible and active, stops on navigation/account changes, and backs off after failures. Stale states retain last-known data. QR modules remain black on white in both appearances, with H error correction and a small centered brand symbol. Localization lives in `Shared/en.lproj/Localizable.strings`.

## Verification status

- [x] Formatted all changed Swift sources with official SwiftFormat 0.62.1 (`--swiftversion 5.9`); `git diff --check -- ios` passes. This formatter check does not type-check the shared framework bridge.
- [x] Linux source/configuration gates: property-list and JSON parsing, unchanged application identities, shared Keychain entitlement alignment, credential-storage/redirect checks, authenticated client bridge and regression-test wiring.
- [x] Added native regression cases for the original receive slot after reopening, partial-save deduplication, receipt eligibility, missing local files, backward-compatible history decoding, admin-only legacy isolation, all-or-nothing share-provider selection and temporary-copy cleanup, origin validation, fractional/nonfractional RFC3339 server timestamps, byte-weighted progress, and cancellation/reauthentication callback ownership.
- [ ] Run the Xcode main-app and extension build and the XCTest suite above. Neither compilation nor XCTest execution has been performed on Linux; source gates are not a substitute.
- [ ] Verify real Keychain access/signing, upgrade a previous installation with existing history, and exercise same-account reauthentication and cross-account changes during uploads in both targets.
- [ ] On a simulator/device, create a receive link, leave it, upload from a browser, reopen History and save. Interrupt the second file, retry, confirm no duplicate first file or new slot, and confirm the sender receipt appears only after all saves.
- [ ] Verify stop/back/background/extension dismissal, cleanup failure, app termination after a partial save, and retry/revocation after relaunch. Confirm temporary attachment cleanup and profile extension memory.
- [ ] Verify actual camera QR pairing, expiry/reuse/cancellation, public no-login links, LAN development HTTP, trusted HTTPS and rejected cross-origin redirects.
- [ ] Verify Settings → Appearance switches the app and open settings sheet immediately, survives relaunch and sign-out, and applies when opening the share extension. In System mode, verify changes to the device appearance are followed.
- [ ] Inspect light/dark screens, small phones, long names/addresses, Dynamic Type, VoiceOver, external-keyboard focus, reduced motion, system appearance changes and QR readability. Capture screenshots on macOS; no iOS screenshots or physical-device accessibility results are claimed here.

## Scan to receive

The Scan tab is available on first launch and after sign-out. Entering Scan immediately opens the embedded camera only while that tab is visible and foregrounded. Torch and camera switching appear when supported. Choose QR image decodes one code locally from a bounded thumbnail. Scan an existing download QR to immediately receive, or use Paste and then **Receive files**. Camera denial leaves manual input and the system Paste control available; the app never reads the clipboard automatically. The scanned origin is shown and always uses a fresh anonymous client, even if it matches the active account. Fragments remain local, keys live in Keychain, and account credentials are not reused. Both HTTP LAN origins and trusted HTTPS origins use the normal shared URL validation; redirects and TLS trust bypasses are forbidden.

Downloads are sequential, authenticated in 4 MiB chunks, with at most 100 files and the shared 1 TiB per-file format ceiling. Plaintext remains in a protected temporary file until all frames, lengths and ordering are authenticated. Downloads do not use a newly lowered server upload policy to reject existing completed transfers. The authenticated file list is validated before blobs are fetched. Each decrypted file is atomically published with a collision-safe name in **Files → On My iPhone/iPad → psst.zip → Received → Guest**. A durable publication intent with a content digest reconciles termination between publishing and recording a successful write. One History screen combines account links and device downloads, with All / Sent / Receive links / Downloaded filters. The scanner contains no history list; completed receives can open History with Downloaded selected. Open uses native document preview; Share exports a chosen saved file. No file opens automatically. Local files and history survive account changes and remote expiry. Removing an entry removes its key/history only and leaves files and the sender’s link untouched. Pending delivery receipts are retained independently of that entry.

Stops/backgrounding cancel foreground work. Already-published files remain. Retry/rescan reuses the origin/ID record and only fetches absent or inaccessible outputs; saved transfers reopen offline. Missing previously saved files require an explicit redownload action. Server expiry, quota consumption, or revocation may prevent retries. The app persists the saved state before queuing a public receipt; failed receipts retry on foreground and rescan without downloading again.

Scanning an upload-link QR shows its origin and a separate guest file picker; sending requires a deliberate Send action. Only the newly created child’s resource-scoped capability is used. Failed/cancelled uploads journal cleanup in Keychain for reconnect, and cleanup never deletes a finalized transfer. A pairing QR shows its target server and requires Connect before replacing the active account. The share extension retains pairing/sending and uses the same classifier for pairing; direct downloads only run in the main app.

### Scan validation

- [x] Linux source/configuration gates cover the guest entry, isolated client wiring, local-store persistence and Keychain separation, Files visibility, scene cancellation, shared pairing classification and XCTest registration.
- [x] Added native tests for origin identity, duplicate names, partial checkpoints, interrupted publication, pending-file cleanup, removal retaining files and receipts, missing/truncated output, corrupt-history preservation, unavailable storage, traversal/sanitization, wrong declared length, cancellation, receipt recovery and earlier guest-array decoding.
- [ ] Run the app/extension Xcode build and XCTest commands above on macOS. The added tests have not been executed on Linux, and SwiftFormat/source checks do not prove Swift/Kotlin bridge compilation.
- [ ] Use [the disposable fixture workflow](../docs/testing/scan-to-receive.md) on a physical iPhone/iPad to verify camera permission/denial, app/web QRs, paste, a different scanned server while signed in, LAN HTTP and trusted HTTPS, failed authentication, expiry/quota/revocation, `/u/` guest send, pairing confirmation, restart/background/account change, Files persistence and Open/Share. Compare saved file bytes to the fixture originals and confirm the sender’s receipt only follows the final durable save.
- [ ] Verify small screens, Dynamic Type, VoiceOver and both appearance choices, including the embedded camera, image picker and unified History. These device checks remain pending.

### Navigation, scanner and streaming validation

- [x] Added source-level regression wiring for unified history identity/filtering, signed-out access, account changes, completed send start suppression, and native decoding of branded QR codes.
- [x] Added native streaming writer cases for files larger than 100 MiB, truncated/tampered/reordered chunks, empty files, and manifest format fields. File publication and receipt behavior continue using the existing durable checkpoint tests.
- [ ] Run `NavigationHistoryTests`, `StreamedFileTests` and existing suites through the Xcode command above; these XCTest cases have not run on Linux.
- [ ] Verify camera lifecycle on tab switches/backgrounding, native permission recovery, denied/no-camera and QR-image alternatives, actual optical recognition, camera switching and torch.
- [ ] Verify Back from completed ordinary sends and share-extension sends, history reopening, same-account login recovery, account-change clearing, large-file cancellation and storage exhaustion. Profile app/extension memory above 100 MiB and compare decrypted bytes across Android/web/iOS.
