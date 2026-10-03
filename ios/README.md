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

Send, Receive and History are the main destinations. Settings shows the active account/server and offers password login or QR pairing. Login checks the API and public link pages before saving the server. Sessions are origin-bound; authenticated redirects are refused. Session expiry opens login while preserving the selected task for reauthentication to the same account. Choosing a different account clears selected work and private navigation state.

The share extension supports login in place: attachments remain available while signing in, so a handoff that loses access is unnecessary. It shows a selected-file summary before sending. Any oversized or unreadable attachment rejects the entire selection; valid attachments are never silently sent as a subset, and temporary copies are removed on rejection. The app supports up to 25 MiB of plaintext per file; the extension limits files to 10 MiB because of its lower memory allowance. Both encrypt one file at a time and report actual encrypted bytes uploaded. These bounds are not streaming support or a measured guarantee of extension memory use. Uploads are not background services: the main app stops them on backgrounding; extension dismissal asks for confirmation and cancels work. Cancellation attempts to remove partial server data. If cleanup cannot finish, its persisted record remains actionable in History.

Receive links are saved when created. Reopening one from History uses the original slot/key and offers Save files. Per-file checkpoints survive a partial failure; Retry saving uses the original slot, skips successful writes and acknowledges a child transfer only when every file is durably saved. Files are stored in Documents/Received and available through document preview, export and the Files app. Already-saved copies remain when links are revoked.

History merges the account’s server metadata with local titles and Keychain capabilities. Cross-device entries without keys remain manageable and explain why their full links are unavailable. Revocation happens before removing local history. The previous JSON history is migrated on the first successful coordinated file write; decoding or disk errors never overwrite it. Existing records retain unknown ownership: administrators can manage them only on their original server; ordinary accounts cannot see them. No migration assigns old records to an arbitrary first account.

Receive/detail/history polling runs while visible and active, stops on navigation/account changes, and backs off after failures. Stale states retain last-known data. QR codes remain black on white in both appearances. Localization lives in `Shared/en.lproj/Localizable.strings`.

## Verification status

- [x] Formatted all changed Swift sources with official SwiftFormat 0.62.1 (`--swiftversion 5.9`); `git diff --check -- ios` passes. This formatter check does not type-check the shared framework bridge.
- [x] Linux source/configuration gates: property-list and JSON parsing, unchanged application identities, shared Keychain entitlement alignment, credential-storage/redirect checks, authenticated client bridge and regression-test wiring.
- [x] Added native regression cases for the original receive slot after reopening, partial-save deduplication, receipt eligibility, missing local files, backward-compatible history decoding, admin-only legacy isolation, all-or-nothing share-provider selection and temporary-copy cleanup, origin validation, fractional/nonfractional RFC3339 server timestamps, byte-weighted progress, and cancellation/reauthentication callback ownership.
- [ ] Run the Xcode main-app and extension build and the XCTest suite above. Neither compilation nor XCTest execution has been performed on Linux; source gates are not a substitute.
- [ ] Verify real Keychain access/signing, upgrade a previous installation with existing history, and exercise same-account reauthentication and cross-account changes during uploads in both targets.
- [ ] On a simulator/device, create a receive link, leave it, upload from a browser, reopen History and save. Interrupt the second file, retry, confirm no duplicate first file or new slot, and confirm the sender receipt appears only after all saves.
- [ ] Verify stop/back/background/extension dismissal, cleanup failure, app termination after a partial save, and retry/revocation after relaunch. Confirm temporary attachment cleanup and profile extension memory.
- [ ] Verify actual camera QR pairing, expiry/reuse/cancellation, public no-login links, LAN development HTTP, trusted HTTPS and rejected cross-origin redirects.
- [ ] Inspect light/dark screens, small phones, long names/addresses, Dynamic Type, VoiceOver, external-keyboard focus, reduced motion, system appearance changes and QR readability. Capture screenshots on macOS; no iOS screenshots or physical-device accessibility results are claimed here.
