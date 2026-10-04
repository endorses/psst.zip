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

History merges the account’s server metadata with local titles and Keychain capabilities. Cross-device entries without keys remain manageable and explain why their full links are unavailable. Revocation happens before removing local history. The previous JSON history is migrated on the first successful coordinated file write; decoding or disk errors never overwrite it. Existing records retain unknown ownership: they remain hidden from mobile account History, and administrators manage server resources through the website. No migration assigns old records to an arbitrary first account.

Receive/detail/history polling runs while visible and active, stops on navigation/account changes, and backs off after failures. Stale states retain last-known data. QR modules remain black on white in both appearances, with H error correction and a small centered brand symbol. Localization lives in `Shared/en.lproj/Localizable.strings`.

## Verification status

- [x] Formatted all changed Swift sources with official SwiftFormat 0.62.1 (`--swiftversion 5.9`); `git diff --check -- ios` passes. This formatter check does not type-check the shared framework bridge.
- [x] Linux source/configuration gates: property-list and JSON parsing, unchanged application identities, shared Keychain entitlement alignment, credential-storage/redirect checks, authenticated client bridge and regression-test wiring.
- [x] Added native regression cases for the original receive slot after reopening, partial-save deduplication, receipt eligibility, missing local files, backward-compatible history decoding, legacy-history isolation, all-or-nothing share-provider selection and temporary-copy cleanup, origin validation, fractional/nonfractional RFC3339 server timestamps, byte-weighted progress, and cancellation/reauthentication callback ownership.
- [ ] Run the Xcode main-app and extension build and the XCTest suite above. Neither compilation nor XCTest execution has been performed on Linux; source gates are not a substitute.
- [ ] Verify real Keychain access/signing, upgrade a previous installation with existing history, and exercise same-account reauthentication and cross-account changes during uploads in both targets.
- [ ] On a simulator/device, create a receive link, leave it, upload from a browser, reopen History and save. Interrupt the second file, retry, confirm no duplicate first file or new slot, and confirm the sender receipt appears only after all saves.
- [ ] Verify stop/back/background/extension dismissal, cleanup failure, app termination after a partial save, and retry/revocation after relaunch. Confirm temporary attachment cleanup and profile extension memory.
- [ ] Verify actual camera QR pairing, expiry/reuse/cancellation, public no-login links, LAN development HTTP, trusted HTTPS and rejected cross-origin redirects.
- [ ] Verify Settings → Appearance switches the app and open settings sheet immediately, survives relaunch and sign-out, and applies when opening the share extension. In System mode, verify changes to the device appearance are followed.
- [ ] Inspect light/dark screens, small phones, long names/addresses, Dynamic Type, VoiceOver, external-keyboard focus, reduced motion, system appearance changes and QR readability. Capture screenshots on macOS; no iOS screenshots or physical-device accessibility results are claimed here.

## Scan to receive

The Scan tab is available on first launch and after sign-out. Entering Scan immediately opens the embedded camera only while that tab is visible and foregrounded. The scanner automatically prefers a usable rear camera, falling back to another camera when necessary. There is no camera switch control; a supported torch remains available and turns off when capture stops. Initialization or runtime failure shows a Retry camera action. Camera denial offers help and camera-free alternatives. Choose QR image decodes one code locally from a bounded thumbnail. Scan an existing download QR to immediately receive, or use Paste and then **Receive files**. Camera denial leaves manual input and the system Paste control available; the app never reads the clipboard automatically. The scanned origin is shown and always uses a fresh anonymous client, even if it matches the active account. Fragments remain local, keys live in Keychain, and account credentials are not reused. Both HTTP LAN origins and trusted HTTPS origins use the normal shared URL validation; redirects and TLS trust bypasses are forbidden.

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
- [ ] Verify camera lifecycle on tab switches/backgrounding, native permission recovery, denied/no-camera and QR-image alternatives, actual optical recognition, rear/front-only/no-camera fallback, runtime retry and torch.
- [ ] Verify Back from completed ordinary sends and share-extension sends, history reopening, same-account login recovery, account-change clearing, large-file cancellation and storage exhaustion. Profile app/extension memory above 100 MiB and compare decrypted bytes across Android/web/iOS.

## Administration accounts, temporary passwords and recognizable History

Administrator accounts are for managing the service on the website. The app and share extension reject administrator logins with an explanation, best-effort revoke newly issued administrator sessions, and block already-stored administrator sessions from authenticated transfer work. Foreground/account refresh checks current server role and password state; the backend remains authoritative during network failures. Native guest scanning, public-capability uploads and local downloaded files remain available independently of the account.

Administrator authenticator enrollment and recent-authentication prompts are web
administration features. The server rejects administrator device login before
issuing a token, with or without an enrolled factor; native clients already map
`admin_transfer_forbidden` before decoding/saving credentials. Main app and share
extension use the same account forms and preserve ordinary-account temporary
password replacement. See [administrator security](../docs/security/administrator-authentication.md).

- [x] Read-only source review confirmed both native clients handle administrator rejection before saving a session. The direct shared login regression verifies no bearer forwarding/retry, safe error text and a subsequent regular temporary-password login; twelve focused `AuthApiTest` cases passed.
- [ ] On iOS, attempt administrator device login against an enrolled and an unenrolled account, in both app and share extension. Verify the existing session remains intact, no factor/token is requested or saved, and ordinary-account login/replacement still works. There is no existing mock transport seam for the native HTTP client; these runtime checks remain pending.

A newly created or administrator-reset regular account must replace its temporary password. The shared account form shows Current password, New password and Confirm password with password-manager support, explicit mismatch feedback and a visibility control. The new password must differ and meet the server's 12–72 UTF-8-byte policy. Successful replacement revokes sessions and requests another sign-in; it does not automatically replay a transfer. Account identity remains stable through same-account replacement/re-login so selected files can be retried deliberately. Signing in as a different account clears private selections. The Settings password form uses the same confirmed replacement flow. Password fields and error responses are never logged.

History labels each row Sent, Receive link or Downloaded and shows creation date/time, file count, available size and status. Known filenames include an additional-file count and truncate in the middle, preserving extensions. Receive creation accepts an optional name; owned Sent/Receive link entries offer Rename through a leading swipe or context menu. Names are local to this device, scoped by server/account/resource type/ID, and preserved through polling, saving checkpoints, coordinated App Group persistence and relaunch. Clearing a custom name restores the automatic filename/date title. Existing automatic titles migrate unchanged; no title is sent to the server. Technical identifiers stay in expanded details.

### Administration/UX validation

- [x] Linux source/configuration gates and SwiftFormat checks cover the current source wiring. They do not compile Swift or execute UI workflows.
- [x] Added native test cases for persisted local-title migration and reload, distinct resource-type/account identities, Unicode filenames/counts, concurrent refresh preserving renames and clears, administrator/restricted-session local-only History, backward-compatible session decoding, password confirmation/UTF-8 policy, and rear-first/front-only/no-camera selection ordering.
- [ ] Execute the Xcode app/share-extension build and XCTest commands above on macOS. All added XCTest cases remain unrun in the Linux environment.
- [ ] With a real server, verify initial temporary-password login → mismatch/wrong current password/unchanged replacement → successful change → explicit same-account sign-in → deliberate resume in both app and share extension. Verify cancel, expiry/reset races, relaunch, and cross-account selection clearing.
- [ ] Verify newly entered and previously stored administrator sessions cannot send/create receive links or reveal personal account History; guest scanning and existing device downloads must remain usable.
- [ ] Exercise mixed/large histories, rename/edit/cancel/clear, same IDs across resource types, names on a second server/account, relaunch and offline/local file access with Dynamic Type and VoiceOver in both appearances.
- [ ] On real devices, test automatic rear selection, front-only/unavailable-camera behavior, permission recovery, interruption/retry, repeated entry/exit, rotation and background/foreground, torch accuracy, QR-image/paste alternatives and optical decoding for public and pairing links in both targets.

## Private receive inboxes and link limits

New receive links use `/u/{id}#v2.{public-key}`. Only the public key is shared; the private key is stored in the existing account/server/resource-scoped Keychain entry, separately from History metadata. Each guest submission generates a fresh symmetric file key and wraps it with RFC 9180 HPKE. Inbox listing, events, child metadata, files and receipts require the owning regular account. A paired or second device without the private key can manage/revoke a link but cannot save its contents. Pairing does not copy inbox private keys. Existing receive links are read-only: their owner can save old submissions, but guests must obtain a new receive link to send more files. Deploy the compatible backend and clients together; do not describe older clients as private inboxes.

Send and Receive offer a collapsed **Link limits** section. Send limits apply separately to each file's started download responses; interruptions use an attempt. Receive limits count cumulative file allocations, including unfinished uploads, and deletion does not restore them. Zero/unset leaves only the optional creator limit disabled; server policy still applies. The share extension offers the same send limit before upload. A new main-app link starts with limits unset, while retry keeps the original job's selection.

Scanned-download details display known remaining attempts without interpreting a missing counter as zero. An exhausted file cannot be fetched again, but its saved local copy stays openable and shareable. If only some files remain available, the app requests explicit permission to save that subset and does not acknowledge the whole transfer. Metadata is refreshed after attempts/errors. Counts are advisory; another recipient can consume an allowance before a request reaches the server.

Both scanned downloads and owner inbox saves preflight manifests and free space. Receives above 100 MiB require confirmation of the file list and total size; an altered manifest invalidates that consent. The 256 MiB free-space reserve and 1 TiB aggregate ceiling apply independently of server upload settings.

### Receive encryption validation

Run `python3 ios/scripts/test_receive_crypto.py` on Linux with Docker to compile the exact portable adapter, safety policy, limit parser, history collector and incident error mapper against pinned Apple `swift-crypto` 3.12.3. The harness uses temporary workspaces and removes its caches. It tests browser/Tink/Swift ciphertext fixtures, context and ciphertext mutation, low-order X25519 input rejection, framing bounds, disk reserve, integer limits, bounded history and strict pause/revocation status-code matching. This verifies portable implementations; it does **not** compile the app's SwiftUI or Kotlin bridge or establish native CryptoKit behavior.

- [x] Seven portable Swift tests passed through the Docker harness, including all three provider fixtures and low-order inputs. All iOS Swift files also passed compiler syntax parsing and the source/configuration gate.
- [ ] Run the macOS Xcode build and XCTest commands above for both the app and share extension. `ReceiveCryptoTests` includes the same bundled fixtures and invalid-point checks against native CryptoKit.
- [ ] Verify owner-only receive retrieval and acknowledgements, private-key survival after relaunch, a second paired device without keys, lost finalization responses, receive-file exhaustion and one-attempt multi-file sends on physical iOS.
- [ ] Verify large-inbox confirmation, changed manifests while confirming, exhausted-file subsets, retries retaining local files and limits, new-link/account-switch draft reset, and share-extension limit entry with VoiceOver and Dynamic Type.

## Bounded account history

History collects at most 100 pages of 100 compact summaries, with a 1 MiB body
bound and 30-second aggregate deadline. A later-page failure, repeated cursor,
oversized response or page ceiling preserves the previous local history. Only a
complete snapshot can reconcile missing server records. Compact inbox counts use
current completed files and never interpret an empty child list as zero or infer
that every current child is saved. Local saved paths remain available; detailed
inbox refresh determines exact saved status. Updates commit in one coordinated
batch, preserving local names and records from other contexts.

- [x] The portable Swift harness passes eleven crypto/safety/history tests, including four pagination cases. Swift syntax, formatting and source gates pass.
- [ ] Run `NavigationHistoryTests.testBatchSnapshotKeepsOtherRecordsAndConcurrentLocalNames` and the complete XCTest suite on macOS/iOS. The added coordinated-persistence test has not run in Linux.
- [ ] Verify multi-page History against the real server, interrupted second-page loading, account switching, restored local files and very large history on a device. Portable collector tests do not validate native URLSession/UI/file-coordination behavior.

## Incident-control validation

Pause/revocation errors use local messages, with strict HTTP status/code matching and
typed Kotlin exceptions through the documented NSError bridge. Sending in the app
and share extension, owner inbox saving, and guest upload/download preserve existing
saved files. History/inbox polling stops after lost authentication or link revocation.
See [incident response](../docs/security/incident-response.md) for server behavior.

- [x] Thirteen portable Swift tests passed, including bounded/malformed incident responses and mismatched status/code pairs. Swift syntax parsing, source gates and formatting pass.
- [ ] Build the app and extension on macOS to verify the generated Kotlin exception bridge; the Linux harness deliberately cannot import `Shared`.
- [ ] On an iPhone/iPad, pause during upload, manifest retrieval and a multi-file receive; verify explicit retry after resume preserves previously saved files and does not silently restart payload work. Shut down the signed-in account and confirm polling stops, sign-in recovery appears, old links remain unavailable after reenable, and local Open/Share still works.
- [ ] Verify URLSession's lower-level retry behavior for interrupted quota-consuming downloads. There is no application-level automatic payload retry, but disabling every transport retry has not been established on iOS.

## Transfer traffic budgets

Settings offers an account-scoped Transfer traffic view, loaded once with explicit
refresh. It displays the account allowance, charged/remaining bytes, cycle end and
enforcement state. Details distinguish observed bytes, active reservations and
conservative charges. A failed refresh preserves previous figures with a warning;
global exhaustion can block an account that still has its own allowance left.

The app and share extension map exact budget exhaustion, accounting unavailable
and policy-changed responses to local explanations. Retry dates are accepted only
as bounded UTC timestamps. Saved files remain available and resume is explicit.
For recognized transport interruptions, a bounded metadata-only status probe can
explain a server-side stop without consuming another payload attempt. Guest
downloads remain anonymous, guest uploads use only their own submission capability,
and owner operations retain the matching account scope. Crypto/local-file errors
do not trigger probes. See [traffic protection](../docs/security/traffic-limits.md).

- [x] Sixteen portable Swift tests passed, including exact status/code pairs, malformed/oversized responses, hostile retry text, safe retry timestamps, and minimal traffic-status parsing. Swift syntax, source/configuration gates and formatting pass.
- [ ] Build app and share extension on macOS and run XCTest to verify new shared DTO/exception exports, the Kotlin throwable classifier and SwiftUI usage view. Portable Linux checks cannot establish these bridges.
- [ ] Exercise exhausted manifest/file uploads and downloads, partial multi-file saves, administrator limit changes, accounting failure, explicit recovery and local Open/Share on iOS. Confirm a retry fetches only missing files and no automatic payload request burns another attempt.
- [ ] Verify URLSession transport behavior, three-second probe resource deadlines, cancellation, failed/unsupported status endpoints, backgrounding, account changes, accessibility and appearance modes on a device.

## Contact-only abuse reporting

The scanner and downloaded-file details offer a local **Report abuse** sheet. Settings exposes the configured instance's contact under **Help & contact**. Public config is read anonymously from the exact instance using the bounded shared limits API; no saved account credentials are used. The contact is validated before display or composing mail. A blank, invalid or unavailable address hides reporting actions. Changing scan context clears the previous contact and cancels the old lookup.

Reports contain only the instance origin, resource type and UUID. Report context is extracted independently of encryption-key validation, so an unreadable or unavailable link can still be identified without including its fragment, query, filename or key. **Write email** opens a draft for the user to review and send; it does not submit a report or revoke anything. The address and safe context can also be copied when no mail app is available.

Run `python3 ios/scripts/test_abuse_report.py` on Linux with the existing `swift:6.0-noble` Docker image. This offline harness compiles the exact Foundation helper and its XCTest tests and removes its temporary workspace and caches. It does not validate SwiftUI, Kotlin framework bridging, or device mail handling.

- [ ] On macOS, build the app and share extension using the Xcode workflow above; execute `AbuseReportTests` with the app test suite.
- [ ] On an iPhone, verify valid and invalid-key scanned links, unavailable metadata, switching between two origins during contact lookup, configured-server Help, copy actions, and explicit mail composition with and without a mail app.

### Guest upload capacity

Receive-link uploads refresh public capacity on opening, each cumulative picker
change and immediately before creating a child transfer. The check includes all
selected files, encrypted frame overhead (including empty files), encrypted
manifest space and the current per-file policy. Unknown, malformed, expired or
exhausted snapshots keep the existing selection available for removal and retry.
A request generation prevents an old response from changing another invitation,
including two links on the same server. Source sizes are rechecked before
streaming; server admission remains authoritative if capacity changes afterward.

Run `python3 ios/scripts/test_guest_upload_selection.py` for the offline portable
Swift tests of empty-file overhead, aggregate overflow, manifest reserve and
changed-file rejection. The harness cleans its temporary workspace. It does not
build SwiftUI or the Kotlin framework bridge. On macOS, run the build/test commands
above and exercise multiple Add actions, removal while a refresh is pending,
switching same-origin invitations, unknown/exhausted/restored capacity, provider
files changed after preflight and server allocation races. Verify preserved
selection, no allocation on failed preflight, explicit retry and scoped cleanup.

### Indexed account history

The app and share extension now share an App Group SQLite store. Existing JSON
and preferences remain as migration sources; import is resumable and account
writes wait for completion. See [local history storage](../docs/security/local-history-storage.md)
for record/page bounds, migration exceptions and remaining guest/checkpoint work.

Run `python3 ios/scripts/test_history_database.py` for the offline portable
SQLite and streaming-import XCTest harness. It removes its temporary workspace.
This validates the exact Foundation database/parser, not the iOS application.

- [ ] On macOS, regenerate the Xcode project and build/test the app and share
      extension with the workflow above. Run `HistoryRecordDatabaseTests` and
      `NavigationHistoryTests`, including migration and atomic batch rollback.
- [ ] On device, exercise fresh install, large legacy import interrupted by app
      termination, low storage, changed/missing original data, protected files
      while locked, concurrent app/share-extension writes and account switching.
      Confirm old keys/local files remain usable and failed import offers retry.

### Guest history and retry storage

Guest downloads now use indexed local pages and exact record updates. Both legacy
history formats import incrementally, preserving original sources and Keychain
identities. Receipt and publication-recovery jobs remain independent of visible
history rows; removing an item keeps saved files and pending delivery confirmation.
Owner receipts and unfinished guest uploads use separate indexed retry queues,
with cleanup capabilities retained only in Keychain.

Run `python3 ios/scripts/test_guest_store.py` for the actual guest storage and
local-page models against real SQLite with explicit Apple/crypto/network boundary
stubs. Run `python3 ios/scripts/test_device_retry_queue.py` for the actual queue
and legacy-array parser. These offline harnesses clean their temporary workspaces;
they do not prove Keychain/protection, networking, cryptography or native UI behavior.

- [ ] Build app and extension on macOS and run the native XCTest suite, including
      `GuestDownloadTests`, after regenerating the project.
- [ ] On a device, migrate both legacy guest formats and old receipt/cleanup
      journals, terminate during import, and verify saved paths, pending receipts
      and cleanup capabilities after relaunch. Test locked/unavailable Keychain
      without creating an empty migration marker or replacing an existing key.
- [ ] Exercise downloaded-history pagination and failed navigation, local removal
      with a pending receipt, multiple unavailable origins, corrupted metadata,
      publication interruption, cancellation and low storage. Verify valid jobs
      progress, pending originals remain preserved and recovery feedback is visible.

### Indexed inbox checkpoints

Inbox paths and child completion state now use individually indexed records in the
same transaction domain as account history. Legacy maps move into retained source
rows and import in resumable batches before account history becomes writable.
Normal status refreshes cannot replace already committed saves. New file entries
also retain their expected length; legacy entries without a length keep their
previous existence-only check.

Run `python3 ios/scripts/test_receive_checkpoints.py` for the actual account store,
checkpoint storage and local-link page model using real SQLite with explicit
Apple/account/crypto boundary stubs. It covers large inboxes, bounded child pages,
restart, concurrent updates, scoping, truncation and malformed preserved sources.

- [ ] Run `ReceiveCheckpointTests`, `TransferWorkflowTests` and
      `NavigationHistoryTests` in Xcode and build the app/share extension.
- [ ] On device, interrupt normalization and per-file saving, change accounts,
      exhaust storage and fail a page metadata write. Verify the previous page
      keeps matching save state, successful files are skipped on retry, truncated
      files need saving again and delivery confirmation survives interruption.
