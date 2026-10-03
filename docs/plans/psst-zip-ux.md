# psst.zip web, Android, and iOS UX improvements

## Goal and scope

Make sending, receiving, and returning to an existing transfer predictable and consistent across the web UI, Android app, and iOS app. Use **psst.zip** as the exact user-facing brand, including capitalization and punctuation.

This plan follows the UX review after account access control was implemented. Prioritize the receive/retry workflow defects, then progress and state handling, navigation, and presentation. Preserve authenticated creation, public shared links, end-to-end encryption, account isolation, link revocation, and self-hosted server configuration.

Implementation is complete and the bounded closure review concluded **CLOSED_WITH_DEFERRALS**. Platform build/device checks remain separate from implementation; unchecked validation items below identify those limitations. Final commit/deployment delivery is being completed. Every applicable mobile improvement includes both Android and iOS, with equivalent capabilities adapted to native platform conventions. Lack of local iOS testing is a validation constraint, not an implementation exclusion. This parity requirement also applies to future mobile changes unless the user explicitly agrees to an exception.

## Existing iOS parity gaps and prerequisites

The preceding authentication and revocation changes were implemented for Android without corresponding iOS UI support. Include that catch-up work here so the iOS UX can actually operate against the secured backend.

- [x] Compare the current iOS main app, share extension, shared Kotlin bridge, and history storage with Android’s implemented capabilities. Record concrete gaps and map each mobile task below to Android and iOS files and verification steps.
- [x] Add iOS server/account login, logout, session-expiry handling, and QR pairing with secure Keychain-backed device sessions. Keep credentials bound to the configured server and out of logs, share links, and ordinary preferences.
- [x] Integrate authenticated uploads in the iOS main app and share extension. If sharing credentials between targets is required, configure the minimum necessary Keychain access group/entitlements and document signing requirements; do not copy bearer tokens into unprotected App Group preferences.
- [x] Persist resource deletion capabilities and account/server ownership in iOS history with a data-preserving migration. Match Android’s remote-first revocation, failure/retry behavior, account isolation, and explicit handling of legacy records.
- [x] Preserve public download and invited-upload access without login, existing encryption/link compatibility, download acknowledgements, and already-saved files. Apply account-switch cancellation and cache/navigation clearing to both the iOS app and extension.

Acceptance: iOS can authenticate and use the secured server, issue public links, revoke its own resources, and switch accounts without exposing another account’s history or keys. Implementation and verification status must be tracked separately; unrun iOS build/device checks remain explicitly pending.

## Branding and terminology

- [x] Inventory visible names in the web layout, page titles, login, Android and iOS app display names and screens, share-extension text, accessibility labels, README, and public project metadata. Replace inconsistent Psst, Psst, Secure Transfer, and Secure File Transfer branding with **psst.zip**. Include iOS main-app and share-extension surfaces in the same branding pass.
- [x] Consolidate web brand text, Android string resources, and iOS localized strings so future wording changes do not require editing many screens. Reconcile the existing unstaged Android branding edits with the requested name without losing unrelated work.
- [x] Keep installed-app identity, package/bundle identifiers, App Groups, database names, Keystore/Keychain identifiers, storage keys, API paths, cookie names, and the existing pairing protocol compatible. A display-name change must preserve installed users' data and sessions. Do not hardcode `psst.zip` as the operator's server address or change deployment hostnames.
- [x] Use the same task language across clients: **Send files**, **Receive link**, **Creating link**, **Save files**, **Copy link**, and **Share**. Replace user-facing “drop slot” and repetitive encryption/decryption verbs; explain automatic encryption briefly near the relevant flow.
- [x] Update home descriptions to explain sharing by link as well as QR, including sharing with someone elsewhere.

Acceptance: public pages, authenticated pages, and both mobile apps visibly use psst.zip. Existing installations, saved accounts, old links, and pairing codes remain compatible.

## Approved color theme

Approved direction: **deep teal, dark ink, and soft off-white**, shared across web, Android, iOS, and the iOS share extension. The intended character is quiet confidence, privacy, and approachability. These are design intentions rather than universal psychological effects or evidence of security. Keep most surfaces neutral and use teal to emphasize primary actions and selected navigation.

### Light theme

| Role           | Color                 | Use                                                           |
| -------------- | --------------------- | ------------------------------------------------------------- |
| Primary        | Deep teal `#0F766E`   | Primary buttons, links, selected navigation                   |
| On primary     | White `#FFFFFF`       | Text and icons on filled teal buttons                         |
| Main text      | Ink `#172B2A`         | Headings and body text                                        |
| Secondary text | Muted slate `#526561` | Supporting information and timestamps                         |
| Background     | Off-white `#F6F8F7`   | Page and screen backgrounds                                   |
| Surface        | White `#FFFFFF`       | Cards, forms, dialogs                                         |
| Subtle accent  | Pale mint `#DDF4EC`   | Selected backgrounds and informational panels, with dark text |

### Dark theme

Use deep green-black `#122321` as the surface foundation and light teal `#5EEAD4` for primary accents. Filled light-teal buttons use dark ink text, not white. Derive complementary background, elevated-surface, primary-text, secondary-text, border, and interaction-state tokens during implementation, verifying each actual foreground/background combination. Do not simply invert the light palette.

- [x] Define semantic theme tokens in web styles, Android's theme, and iOS color assets/styles. Use the same approved palette and semantic roles while respecting native controls and platform conventions, including the iOS share extension.
- [x] Implement light and dark appearances, following the browser/device appearance preference by default. Apply the theme to authenticated screens, public link pages, dialogs, and loading/error/empty states.
- [x] Define and verify hover, pressed, selected, focus, disabled, and elevated-surface treatments. Keep the main action visually prominent without filling large portions of the interface with saturated color.
- [x] Reserve red for errors and destructive actions, amber for warnings, and a distinct success treatment for completed actions. Accompany status colors with readable labels or icons; color alone must not communicate status.
- [x] Keep teal as the action/selection color rather than an unconditional “secure” or “finished” signal. Communicate encryption, transfer completion, and server connection state with explicit, accurate text; use restrained success styling only when the corresponding operation has completed.
- [x] Render QR codes with dark modules on a white background and an adequate quiet zone in both themes. Do not tint, invert, or apply dark-mode filters to QR images.
- [ ] Verify WCAG AA contrast for actual rendered text and controls in both themes: at least 4.5:1 for normal text, 3:1 for large text, and 3:1 for essential control boundaries, state indicators, and focus indicators against adjacent colors. Include muted text and selected/error states in the checks.

White on the approved deep teal measures approximately 5.47:1. This validates that specific text/button pairing, not the entire interface. Use [W3C text contrast guidance](https://www.w3.org/WAI/WCAG21/Understanding/contrast-minimum) and [non-text contrast guidance](https://www.w3.org/WAI/WCAG21/Understanding/non-text-contrast) when validating the finished components.

Acceptance: all three clients use the approved teal theme, respond to system appearance changes, retain readable status and interaction states, and present scannable dark-on-white QR codes. Record contrast and visual checks for both light and dark appearances.

## Reliable receiving and retries

Primary files: Android `ReceiveScreen.kt`, `ReceiveViewModel.kt`, `TransferDetailScreen.kt`, `TransferDetailViewModel.kt`, and `NavGraph.kt`; iOS `ReceiveViewModel.swift`, `TransferDetailView.swift`, `HistoryView.swift`, `ContentView.swift`, and shared history models.

- [x] Give the receive flow explicit new-link and existing-link entry points. Opening a receive item from History must load its original slot, key, account, server, arrivals, and saved-file state instead of creating another slot.
- [x] Reuse one receive-detail experience for newly created and historical receive links, including **Save files** for completed uploads that have not already been saved.
- [x] Separate link-creation, connection/status, and saving errors. **Retry saving** retries the existing download; **Reconnect** restores monitoring; only retrying a failed creation may create a link. Preserve the existing link and key after unrelated failures.
- [x] Preserve successful saves when another file fails, avoid duplicate saves on retry, and retain the existing acknowledgement behavior. Account and server checks must apply to reopened links and every retry.
- [x] After saving, show the number of files and their location, with **Open file** or **Open Downloads** where supported. Use platform-safe file access: Android content URIs and temporary read grants, and iOS document preview/export or Files integration as appropriate.

Acceptance: create a receive link, leave the screen, receive an upload later, reopen from History, and save it. Simulated download failure followed by Retry must use the same slot and create no new receive link. Switching accounts must hide the prior account's content and cancel access to it.

## Accurate progress and safe interruption

Primary files: web `SendPanel.svelte` and public upload/download pages; Android and iOS send/receive view models and screens, the iOS share extension, and shared tus/upload code as needed.

- [x] Replace web per-file completion percentages with progress based on transferred bytes and ensure Android, iOS, and the iOS share extension use the same honest progress semantics. Show the current file, total bytes, and a separate **Preparing files** state while encryption is running. Do not invent a percentage for work that cannot be measured.
- [x] Add a clear cancellation action. Keep web transfer-job state above individual tabs so navigating within the app does not silently discard selection or hide an active upload. Show an accessible way back to the active transfer.
- [x] Warn before closing/reloading a page with unfinished work where the browser supports it. Do not promise restoration of browser file access after a reload.
- [x] On Android and iOS, confirm **Stop upload?** before navigation or dismissal that cancels an active transfer. Handle share-extension dismissal explicitly. Background transfer services are not required for this iteration; do not imply work continues when the OS or extension lifecycle stops it.
- [x] Bind jobs, selected files, encryption keys, and completion callbacks to the originating account and server. Logout or account switching must stop and clear that account's work; late callbacks must never save its links into another account's history.
- [x] Preserve an actionable record of a failed or canceled server transfer so it can be revoked or retried appropriately. Clean up partial uploads when authorized and possible, and communicate cleanup failure rather than implying the server data disappeared.
- [x] Show the per-file size limit before selection in both mobile apps, explain the iOS share extension’s separate memory limit, validate selected files immediately across clients, and display selected file count and total size. Explain any separate ZIP-download limit before that action is chosen.

Acceptance: a throttled single-file upload visibly advances between 0% and 100%; multi-file progress reflects bytes. Tab changes, Back, cancellation, network loss, logout, and late completion produce predictable results without lost links or cross-account disclosure.

## Live status and reconnect behavior

- [x] Update web receive arrivals automatically while the relevant view is open, using existing slot events where practical and bounded polling as a fallback. Keep a manual refresh option for recovery.
- [x] Refresh visible active transfer/history status on web, Android, and iOS so **Downloaded** and newly received files appear without reopening the screen. Use lifecycle-aware polling where no event endpoint exists.
- [x] Stop subscriptions and polling when views are inactive or accounts change. Bound request volume, avoid overlapping requests, and back off after connection failures.
- [x] Show **Reconnecting**, **Offline — last updated …**, or equivalent feedback while retaining the last known information. Do not interpret a network failure as an expired or deleted transfer.
- [x] Preserve the distinction between ready to download, download started, receiver-confirmed download, files received, and files saved locally.

Acceptance: another device uploading or acknowledging a download updates the open view. Losing and restoring connectivity shows an honest stale/reconnecting state and recovers without creating a new link.

## Navigation and task continuity

- [x] Make **Send**, **Receive**, and **History** the primary destinations across web, Android, and iOS. Use suitable desktop and phone layouts; group **Account**, **Connected devices**, and administrator-only **Users** under Settings or the account menu.
- [x] Give web destinations URLs so refresh, browser Back/Forward, and direct navigation preserve the selected destination. Keep temporary transfer jobs independent of route component lifetimes.
- [x] Show a compact current server/account indicator in Android and iOS so server changes and account-scoped history are understandable.
- [x] Retain the intended destination through login and session expiry. Android share-sheet actions and iOS share-extension handoffs should return to the intended task after successful login to the same account/server. Preserve valid Android URI permissions and iOS file/security-scoped access where applicable. If an iOS extension must hand off to the main app, explain the required step and retain eligible pending files safely.
- [x] Treat first-time login and a change of account/server explicitly: preserve a user-initiated pending task only when appropriate, and never restore another account's private history, link, or cached key. Continue clearing obsolete navigation entries on account changes.
- [x] Make Settings entry and return behavior consistent, including a visible way back to the current task.

Acceptance: primary actions remain easy to reach on a phone, admin controls do not crowd normal workflows, browser navigation behaves normally, and reauthentication continues an authorized interrupted task without exposing an earlier account's content.

## Consistent link screens and useful history

- [x] Use a shared visual hierarchy for sent and receive links: brief status, QR code, **Copy link / Share** actions, then expandable URL and details. Reuse components within each client where practical.
- [x] Keep QR and sharing actions visible without scrolling on typical phone sizes at normal text scale. At large font sizes, allow accessible scrolling rather than clipping content or shrinking text excessively.
- [x] Keep the full link selectable and copyable, with clear copy confirmation. Use the native share sheet on Android and iOS and the web share mechanism when supported, with copy as the fallback.
- [x] Make History entries recognizable with a locally available filename or optional title, file count, total size, relative expiry, and readable status. Keep titles/filenames local or encrypted; do not add plaintext metadata to the server for convenience.
- [x] Add **Sent / Receive links** filters, a clear empty state, and an obvious refresh/reconnection control. Keep server/account scoping explicit.
- [x] Explain unavailable encryption keys on individual cross-device entries instead of silently omitting actions: the item can still be managed, but its complete link or contents require the device that holds its key.
- [x] Present expired/revoked states clearly. Keep **Revoke and delete** distinct from ordinary navigation and preserve the confirmation explaining that already-saved copies remain.

Acceptance: users can distinguish several same-day transfers, reopen a receive link, and copy/share its full URL without hunting through metadata. Missing local keys, stale state, expiry, and revocation are understandable.

## Focused mobile pairing

- [x] Put **Connect mobile app** prominently above the session list in web device settings. Display its QR in a focused panel or accessible dialog with expiry and brief app instructions.
- [x] Add a non-secret pairing identifier and an authenticated status mechanism, if needed, so the issuing browser can distinguish pending, connected, expired, and canceled pairing. Restrict status access to the issuer; never expose the pairing secret, resulting device token, or password through status responses.
- [x] Replace the QR with **Phone connected** and the device name after successful redemption, and refresh the connected-device list. Provide an obvious regenerate action on expiry.
- [x] Define cancellation clearly: closing/canceling the pairing flow should revoke an unused grant, and generating a replacement should invalidate the previous grant for that flow. Preserve single-use redemption and atomic race handling.
- [x] Keep manual Android and iOS login available, make scanner cancellation return cleanly to settings, and show useful messages for expired, already-used, or invalid QR codes.

Acceptance: open pairing, scan on a phone, observe connection confirmation on the website, and find the new revocable device session. Expired, canceled, and reused codes cannot sign in another device. A non-issuing account/session cannot inspect another pairing.

## Errors, visual consistency, and accessibility

- [x] Replace generic error headings and raw network messages with a specific explanation and useful action: retry the operation, reconnect, sign in, or ask the sender for a new link. Avoid exposing secrets in diagnostic text.
- [x] Apply consistent spacing, typography, button hierarchy, focus styling, and touch target sizes across authenticated and public web pages and both mobile apps.
- [x] Add password visibility controls and appropriate keyboard submit/autofill behavior to login forms.
- [ ] Check keyboard operation of file pickers, navigation, dialogs, and menus; ensure controls have accessible names and dialogs restore focus when closed. Announce important progress, copy results, arrivals, and errors without excessive repeated announcements.
- [ ] Verify narrow screens, long filenames, long server addresses, large font settings including iOS Dynamic Type, TalkBack, VoiceOver, web screen-reader navigation, contrast, and reduced-motion behavior. Move changed Android text into string resources and iOS text into localized strings; full translation is a separate effort.

## Validation and delivery

- [x] Add focused Android and iOS regression tests for reopening receive links, retrying the same download, saved-file deduplication, and correct acknowledgement behavior.
- [ ] Test upload progress, cancellation/navigation, and account changes during in-flight work, including late callbacks and reauthentication with selected files.
- [ ] Test live updates, reconnect/backoff, and subscription cleanup without leaving background polling active.
- [x] Test pairing status authorization, successful completion, expiry, replacement, cancellation, and redemption races. Run backend race tests and vet if backend code changes.
- [x] Run web checks, integration/browser tests, and production build; run relevant Android/shared tests and assemble the APK. Add/update iOS tests and project configuration, and perform available source/shared-API checks on Linux. Preserve public no-login upload/download behavior, account isolation, revocation, LAN HTTP development support, and normal HTTPS operation.
- [ ] On macOS, generate the Xcode project and build/test the iOS main app and share extension against the updated shared framework. Verify signing, entitlements/Keychain access, simulator flows, and physical-device camera/share-extension behavior. If macOS or a device is unavailable, leave these checks pending and record exact commands, prerequisites, and unverified behavior; continue all implementation and checks available locally.
- [ ] Manually verify the receive/history/retry journey and actual QR scanning on a device or suitable emulator. Record any physical-device or accessibility checks that remain unverified rather than marking them complete.
- [ ] Capture and inspect representative web, Android, and iOS screens in both light and dark themes at desktop/phone widths and enlarged text, including success, error, offline, and empty states. Verify system appearance changes and QR readability in both themes.
- [x] Update README and relevant documentation with psst.zip terminology and final workflows. Record actual validation results and check off only verified tasks in this plan.
- [ ] Format changed files, commit the implementation and completed plan while preserving unrelated edits, then update the development deployment and provide the rebuilt APK and an explicit iOS build/validation handoff. Clean temporary services, test credentials, screenshots, and caches created for verification.

## Implementation order and completion evidence

First close the existing iOS authentication/revocation parity gaps needed by these workflows, then implement receiving/retries on both mobile platforms, followed by transfer progress and account-safe job handling, then live status and navigation. Apply common link/history layouts and pairing improvements on that foundation. Branding, terminology, and accessibility should be incorporated throughout the touched screens, with a final consistency pass.

Initial iOS inventory identified missing authentication and ownership integration in the main app and share extension, unscoped local-only history deletion, temporary automatic receive downloads without a durable slot record, retries that allocated new slots, and per-file upload percentages without cancellation. Work is assigned across `ios/Shared` authentication/history/upload services, iOS view models and views, target entitlements, and regression tests. Android equivalents span account storage, Room history, send/receive view models, navigation and shared link views; web equivalents span route state, upload jobs, receive/history components, and device settings.

Backend pairing tracking and cancellation are being verified independently of the client changes. Final validation evidence and any unavailable device checks will be recorded here before delivery.

### Verification recorded during implementation

- Backend: `go test -race ./...` and `go vet ./...` passed. The pairing tests cover issuer-session authorization, secret-free status, completion, expiry, cancellation, replacement, single-use redemption and cancel/replace races. A real version-17 database upgrade preserves outstanding grants and their single-use behavior; its added regression passed under the race detector.
- Shared/web protocol: all 11 web crypto/transfer integration tests passed, including public receive uploads and explicit download acknowledgements.
- Android: the first completed build passed 32 app tests and 73 shared tests and produced the debug APK. The final cross-device history build passed 39 Android tests and 76 shared tests, with no failures or skips. Actual emulator checks confirmed remote entries display missing-key guidance without exposing a QR or save action.
- Android emulator: created a receive link, left its screen, uploaded two files from an anonymous Chromium page, reopened the same History entry, and saved both files to Downloads (33 and 34 bytes). The entry retained its key and saved count after APK replacement. Inspected the QR/copy/share/save screen in system light/dark appearances and at 150% font scale; controls were readable and the QR retained a white background. Actual optical camera scanning and TalkBack remain unverified.
- Web: browser regression coverage now includes intermediate byte progress under throttling, cancellation cleanup, route continuity, account isolation, same-account reauthentication, offline recovery, pairing completion/cancellation, and receive-history retry that downloads only the failed file and allocates no new slot. The full 26-test browser suite passed; after scoped public-link validation changes, all six affected public-link/upload/download tests passed. The audited pairing follow-up test also passed, connecting two phones consecutively without leaving Devices.
- Theme: checked web token contrast using WCAG relative luminance: primary/white 5.47:1, muted/surface 6.19:1 light and 9.39:1 dark, dark primary-button text 10.03:1, error/background 6.23:1 light and 8.36:1 dark. Essential border/hover contrasts exceed 3:1 in both appearances. Android token checks passed (normal text/button pairs ≥5.30:1 light and ≥7.51:1 dark; tested essential borders ≥4.19:1). iOS token checks passed (text/status pairs ≥5.37:1 light and ≥6.66:1 dark; tested essential borders ≥3.63:1). These token results do not substitute for native iOS rendered checks. Web login/public-upload/error screens were captured in both themes at 390×844 and 200% text; receive was checked at phone/1280×900 widths, with no horizontal overflow, and offline/empty History screens were inspected.
- iOS: added native regression tests and passed available source/configuration gates. Changed Swift files were formatted with SwiftFormat 0.62.1. This is not a native build or XCTest result; macOS/signing/device commands and pending checks are documented in `ios/README.md`.

### Closure decision and external validation

The single closure campaign concluded **CLOSED_WITH_DEFERRALS** after one discovery review, one merged repair batch, and one integrated review. Three concrete findings were repaired: pairing cancellation now invalidates pending grants even during their last fractional second; connecting another phone starts a fresh pairing flow; and the iOS extension rejects an incomplete attachment selection explicitly instead of silently omitting oversized/unreadable files. The cancellation/replacement boundary passed the full Go race suite and vet, and the two-phone flow passed its browser regression. The iOS selection regressions are added but require XCTest execution on macOS. No material findings remain from the bounded review.

Available verification covers the web/Android flows and shared/backend contracts described above. The remaining unchecked mixed verification tasks reflect these specific external cuts, not missing platform implementation:

- [ ] On macOS, run the Xcode build and XCTest commands in `ios/README.md`, verify signing and the shared Keychain access group, and exercise the main app and share extension on simulator/device. Linux source and formatting checks do not establish native compilation or runtime behavior.
- [ ] Capture and inspect iOS light/dark/Dynamic Type screens and test VoiceOver, external-keyboard focus, optical QR scanning, share-extension lifecycle/memory, and an installed-user history upgrade. Follow the exact workflows in the iOS handoff.
- [ ] Verify physical Android QR scanning and TalkBack. Android emulator receive/history/save, persisted state after APK replacement, cross-device missing-key guidance, and light/dark/150% text layout were checked; a real pre-change Room installation upgrade and every device-specific interruption path were not exercised.
- [ ] Complete assistive-technology verification across clients. Web automated checks cover keyboard controls, accessible labels, contrast tokens, narrow/large text, reduced motion, and no horizontal overflow, but are not a screen-reader certification. iOS rendered contrast remains part of native validation.

The plan explicitly permits unavailable native/device checks to remain pending with a concrete handoff. These limitations do not exclude iOS from the delivered implementation or claim those checks passed.

During final delivery verification, a throttled Android upload exposed chunk-only progress reporting in the shared tus client. The shared uploader now reports bytes written within each PATCH while retaining server-confirmed offsets for durable resume. Two regressions verify intermediate progress before the HTTP response and correct resumed-byte totals across chunks. All 39 Android and 76 shared tests passed with the rebuilt APK. This shared fix applies to Android, iOS, and the iOS share extension; native iOS execution remains pending as above.
