# Mobile navigation, scanning, and brand identity

## Status and scope

Implementation complete, with external platform validation explicitly pending below. Subsequent user requests add configurable large-file limits, chunked file processing and branded QR codes. Completed implementation is distinguished from unrun native/device checks.

Improve Android and iOS settings and navigation, integrate downloaded transfers into the existing History screen, embed the camera in the mobile scanner, add an authenticated web scanner, and introduce a consistent **psst.zip** logo across the product, app icons and favicons. Android and iOS receive equivalent improvements using native conventions; include the iOS share extension where relevant.

This plan builds on [Scan to receive files](scan-to-receive.md), [Mobile appearance settings](mobile-appearance.md) and the existing branding/UX plans. Preserve their encryption, durable-saving, receipt, account-isolation and link-revocation behavior. This work does not reopen the previously completed receive implementation or claim its pending device checks have passed.

## Agreed experience

Settings becomes a compact, grouped screen rather than a combined onboarding, login and transfer menu. Signed-out mobile users can reach Home, Scan and History; sending files and creating receive links still require an account. Existing History becomes the sole history destination, with All / Sent / Receive links / Downloaded filters. The scanner contains no separate history list.

Opening Scan QR code on either mobile platform immediately displays an embedded camera preview, requesting permission when necessary. Paste link remains available without camera access. A valid scanned download starts the existing receive flow; pasted downloads retain their explicit Receive files action. Upload and pairing codes retain their deliberate confirmation flows.

The web scanner is available **only after signing in**. Existing public download and upload links remain public. Web scanning decodes locally and opens a validated transfer destination; it does not grant upload rights, redeem mobile pairing codes or introduce a server-side URL fetcher.

Use the mouth-and-finger shushing symbol with the exact **psst.zip** wordmark. Use that same symbol, without the wordmark, for Android and iOS app icons and web favicons. Do not substitute an exclamation mark for the dot or turn the lettering into the mouth. The generated concept establishes the direction; refine it into production vector artwork before deriving platform assets.

## Current implementation boundaries

| Area                          | Existing entry points and implications                                                                                                                                                                                                                                                                                       |
| ----------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Android navigation/settings   | `android/app/src/main/java/zip/psst/android/ui/navigation/NavGraph.kt`, `ui/screens/ServerConfigScreen.kt`, `HomeScreen.kt`, and `ServerConfigViewModel.kt`. Settings currently carries guest shortcuts and authentication callbacks; separate the presentation without losing intended destinations or pending shared files. |
| Android history/scanning      | `HistoryScreen.kt` links to a separate local-received route. `ScanScreen.kt` uses JourneyApps `ScanContract` to launch another camera surface. `ScanViewModel` and the dedicated guest store already own receive state; reuse them rather than creating duplicate jobs or stores.                                            |
| Mobile send navigation        | Android `SendScreen.kt`, `SendViewModel.kt`, `TransferDetailScreen.kt` and `NavGraph.kt`; iOS `HomeView.swift`, `TransferDetailView.swift` and `Shared/SendViewModel.swift`. The reported back loop must be reproduced before assigning a root cause.                                                                        |
| iOS settings/history/scanning | `Psst/ContentView.swift`, `Views/ServerConfigView.swift`, `HistoryView.swift`, `ScanReceiveView.swift` and `Shared/PairingScanner.swift`. History currently opens local downloads in another sheet; scanning also presents separately.                                                                                       |
| Web scanner                   | `web/src/routes/+page.svelte` owns the authenticated application shell; public transfer pages live under `/d/` and `/u/`. `backend/internal/api/middleware.go` currently emits `Permissions-Policy: camera=()`. Inspect the actual document-serving/reverse-proxy headers as well as API headers before changing policy.     |
| Brand assets                  | Android uses `ic_launcher` adaptive-icon resources; iOS has an `AppIcon.appiconset` without an image filename in its current manifest; `web/src/app.html` references `favicon.png`. Verify actual assets and build references before replacing them.                                                                         |

## 1. Settings and account setup

- [x] Replace the oversized branding/cloud illustration and centered button stack with a standard Settings title, native back/dismiss navigation and compact grouped rows.
- [x] Group settings into Appearance, Server & account, and Connection, with Sign out visually separated at the bottom. Retain System / Light / Dark and its existing persistence, including the iOS share extension.
- [x] Show the current server, signed-in username and a Change server or account action. When signed in, remove sign-in instructions, editable login fields and the Continue button from the normal Settings view.
- [x] Move server editing, username/password entry and Scan server login QR code into a dedicated account-setup/edit flow. Retain password visibility, connection testing, pairing confirmation and recoverable errors.
- [x] Show a concise security status in normal Settings; put the full HTTP credential warning beside account setup before credentials or pairing codes are sent. Preserve existing transport restrictions and operator-configured URLs.
- [x] Define save/cancel behavior for setup edits: cancelling preserves the existing account; successful configuration returns to the intended authenticated action when appropriate. Preserve pending Android shared files and equivalent iOS send/extension flows, and do not reuse another account's pending work after account replacement.
- [x] Remove Scan and local-history shortcuts from Settings. Make mobile Home and unified History reachable without configuration; prompt for sign-in only when an authenticated operation requires it. Sign-out returns to a coherent guest state without deleting local downloads or appearance preferences.
- [x] Use native grouped layouts, concise labels and existing freely licensed icons; support small screens, keyboard insets, long server names, large text, VoiceOver/TalkBack and both appearances.

## 2. One mobile History screen

- [x] Present All / Sent / Receive links / Downloaded filters in the existing History screen on Android and iOS. All combines applicable account records and downloads on this device in a stable date order, with visible type/status labels.
- [x] Build a presentation adapter over the existing account and guest stores. Keep their ownership, server namespaces, protected key references and persistence semantics separate; do not migrate guest records into account-owned resources just to combine the UI.
- [x] Remove the separate local-history route, sheet and scanner history selector. Where an existing callback needs a destination, route it to History with Downloaded selected. Provide a View in History action from receive completion if useful, without embedding another history list in Scan.
- [x] Keep downloaded entries available offline, without login, and after sign-out/account changes. Restrict account records to the current account/server as before; account refresh failures must not hide local files or block the entire screen.
- [x] Preserve Open, Share, partial-transfer Resume, missing-copy redownload consent and independent receipt retry from the unified history/details flow. Opening or resuming an existing record must not activate the camera or start a duplicate operation.
- [x] Keep destructive actions explicit: account-owned links use Revoke and delete with existing remote semantics; downloaded entries use Remove from history, keep saved files, and never call revocation APIs. Avoid a common delete callback that conflates the two kinds.
- [x] Identify rows by source/type/origin/resource identity so equal IDs on different servers cannot collide. A link that has both an owned record and a local saved copy must retain both meanings and action sets without silently losing either.
- [x] Preserve existing records, timestamps, legacy handling, unfinished saves and pending receipts. Provide filter-specific empty states, source-server details for downloads and accessible filter selection.

## 3. Fix navigation after sending

- [x] Reproduce the reported sequence: upload finishes, the link/QR appears, Back briefly shows In progress, then the QR page reopens. Trace toolbar/system Back, completion callbacks, retained state and the actual back stack; document the cause before changing it.
- [x] Make navigation on successful creation a one-time consumed event. Persist transfer completion separately from screen navigation so recomposition, foregrounding, returning to a screen or refreshing status cannot replay navigation or restart uploading.
- [x] After a newly created send link is ready, one Back action returns to Home. Opening a transfer from History returns to History. Apply equivalent behavior to iOS navigation and check receive-link creation for the same failure pattern.
- [x] Ask whether to stop only while a transfer is actually active. Leaving a completed QR/detail screen must not revoke its link, clear its saved key, show a cancellation prompt or reset its remote status to In progress.
- [x] Check launch via Android share intent, authenticated-action redirects and iOS share-extension handoff so no path depends on a Home entry that is absent from the navigation stack.
- [x] Add focused regression coverage for one-time completion navigation, Back destinations and reopening completed transfers; exercise Android on an emulator as well as testing state transitions. Equivalent iOS tests are implemented; native execution remains pending below.

## 4. Embedded mobile camera

- [x] Replace the second scanner activity/sheet with a live camera preview inside Scan QR code. Entering the scanner is the intentional camera action; request permission there and start immediately once granted, with no second Scan button.
- [x] Reuse the installed Android decoder through its embeddable camera view where suitable, and the existing iOS capture/decoder components inside the SwiftUI screen. Inspect existing APIs and lifecycle support before deciding whether a new dependency is necessary.
- [x] Keep Paste link visible below the preview and retain explicit Receive files for pasted downloads. Add Choose QR image as a camera-free alternative using native photo/file pickers and on-device decoding; reject unsupported or ambiguous images with a recoverable message.
- [x] Provide torch and camera switching only when supported. Prefer the rear mobile camera, keep the preview within screen bounds and preserve useful controls in landscape and with enlarged text.
- [x] Reuse the existing typed classifier for download, upload and pairing inputs. Debounce repeated detections, release capture before processing a valid code, and allow exactly one active receive/upload/pairing operation.
- [x] Maintain explicit states for initial permission, live scanning, denied/restricted access, unavailable camera, invalid code, processing and Scan again. Do not repeatedly prompt after denial; leave paste/image alternatives usable and offer native settings recovery where appropriate.
- [x] Start capture only while the scanner is visible and foregrounded; stop on navigation, tab changes, backgrounding, detection and teardown. Do not open the camera merely because an off-screen iOS tab is mounted or a historical transfer is opened.
- [x] Retain existing transfer lifecycle, cancellation, durable checkpoints and receipt behavior. Keep pairing account replacement deliberate, including pairing-only account setup and the iOS share extension.

## 5. Authenticated web scanner

- [x] Add Scan QR code to the signed-in web navigation using the current navigation design. Require a valid session before initializing the scanner; a direct scanner URL while signed out shows sign-in rather than a camera surface. Stop camera/decoding when the session expires or the user signs out.
- [x] Show an inline camera panel with camera selection, Paste link and Choose QR image. Request camera permission only when the signed-in user enters the scanner; request no microphone access. Decode frames and selected images locally without uploading them to the server.
- [x] Choose a maintained, freely licensed QR decoder with documented attribution and a browser fallback where native barcode detection is unavailable. Bound image dimensions/decoding work and release frame buffers and image object URLs when finished.
- [x] Validate supported HTTP(S) psst.zip transfer-link structure, resource IDs and key lengths before navigation. Reject arbitrary URLs and schemes, userinfo, malformed/oversized payloads and unexpected paths. Match the shared classifier's accepted protocol contract with web test fixtures.
- [x] Open valid download links in the existing public download flow and upload links in the existing deliberate file-selection flow. Show the destination server and require an explicit Open action before navigating to a different origin. Do not attach the signed-in account's credentials or change its configured origin.
- [x] Recognize mobile pairing payloads and explain that they belong in the mobile app; do not redeem a pairing code as a side effect of webcam scanning. Keep keys in URL fragments and out of requests, logs and error messages.
- [x] Inspect document response headers from the deployed server/proxy. Permit camera access for the same-origin scanner document where needed, keep microphone/geolocation disabled, and retain restrictive policy for unrelated surfaces where feasible. Header permission is not session authorization; enforce the signed-in scanner flow independently.
- [x] Explain camera availability on HTTPS and the localhost development exception. Plain LAN HTTP cannot provide normal browser webcam access: disable that action with a concise explanation and keep signed-in Paste link / Choose QR image usable. Do not require browser flags, certificate bypasses or weakening transport validation.
- [x] Keep public `/d/` and `/u/` routes working without login, with no public scanner added. Do not introduce server-side fetching, broad CORS permissions, new guest upload rights, or a promise that the browser can silently save files like the native app.
- [x] Handle denied permission, absent/busy camera, multiple cameras, unreadable codes, repeated detections and navigation cleanup; support keyboard interaction, screen readers and both appearances.

Browser camera constraints: [MDN getUserMedia privacy and security](https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/getUserMedia#privacy_and_security). Verify browser/library support against primary documentation during implementation.

## 6. Logo, app icons and favicons

- [x] Refine the approved concept into an original, editable SVG master: softly curved feminine-looking lips with a subtle Cupid's bow and one upright finger making a clear shushing gesture. Simplify the finger base and overlapping shapes; avoid a full face, emoji artwork, gradients, textures or detail that disappears at icon sizes.
- [x] Use the existing palette: teal `#0f766e`, mint `#5eead4`, deep green `#172b2a` / `#0b1917`, and light `#f6f8f7`. Create light/dark lockups and a legible monochrome symbol; verify actual contrast and silhouette at small sizes.
- [x] Deliver both a symbol-only master and a horizontal symbol-plus-**psst.zip** lockup. Keep the dot and exact brand spelling. Choose a freely licensed wordmark typeface, record its license/attribution and provide a portable outlined export alongside editable source.
- [x] Store master artwork, license/provenance notes and reproducible export instructions in a dedicated repository brand-assets directory. Treat the generated raster image as a concept reference, not as the production SVG or a source to upscale for icons.
- [x] Derive Android adaptive foreground/background layers, the appropriate monochrome themed-icon layer, round-icon wiring and legacy launcher-density assets required by the supported Android versions. Use only the shushing symbol inside a deliberate background and safe zone; verify launcher masks do not crop the finger or lips.
- [x] Populate the iOS AppIcon asset catalog with correctly referenced opaque artwork and all renditions required by the project's deployment target/toolchain. Use the same symbol, follow platform masking conventions, and verify app/share-extension presentation where applicable.
- [x] Provide web SVG and raster/ICO favicon fallbacks at useful small sizes, plus an Apple touch icon. Wire actual files into `web/src/app.html`; update any existing manifest icon references if present without adding an unrelated PWA project.
- [x] Use the full lockup in appropriate web/app branding locations and the symbol where space is limited. Do not restore oversized decorative branding to compact Settings. Preserve accessible product names; mark purely decorative duplicate graphics appropriately.
- [x] Inspect the favicon at 16/32 pixels and full lockup at navigation sizes; use the symbol rather than shrinking the wordmark into a favicon. Platform launcher-mask checks remain pending below.
- [x] Preserve installed Android/iOS identifiers, signing/update compatibility, app groups, URL protocols, storage keys and operator-configured server addresses. This is a visual identity update, not a protocol or package rename.

## 7. Admin file limits and bounded chunk processing (added scope)

The user requested that the admin account configure the current 25 MiB file limit in the web UI, explicitly chose support above 100 MiB, and approved splitting files into encrypted chunks. The implementation uses 4 MiB internal chunks to fit mobile/share-extension memory budgets. The user confirmed no existing files require a legacy ciphertext fallback. These decisions supersede the earlier plan's assumption that file processing remains unchanged.

- [x] Persist an admin-only plaintext-byte per-file limit, default 25 MiB, with validated updates in web Settings. Publish the effective limit and operator ceiling through a read-only public configuration endpoint; authenticate every settings write and preserve existing CSRF/session rules.
- [x] Enforce the effective limit at new server upload allocation, including bounded chunk encryption overhead, for both normal and slot-scoped uploads. Honor the operator's configured ceiling and retain existing reservations/downloads after a later limit reduction. Verify persistence across server reconstruction and validation of malformed, fractional, negative or excessive values.
- [x] Fetch the correct origin's advertised limit for normal and guest uploads on web, Android, iOS and the share extension. Replace fixed 25 MiB upload labels/checks. Do not silently assume a larger allowance when configuration cannot be loaded.
- [x] Define and document the shared chunked-v1 encrypted-file format, authenticated file identity/index/total length, exact frame counts and lengths, and total encrypted size. Keep the manifest encrypted; no plaintext filename or encryption key enters server metadata.
- [x] Implement sequential 4 MiB encryption/decryption with bounded buffers and native disk IO, never collecting all chunks into one in-memory file. Use the chunked format for every new file; reject unsupported file encodings without maintaining an old-format decryption fallback.
- [x] Stream all normal and guest sends/receives across web, Android and iOS, including iOS share-extension sending. Handle unknown-size input via bounded temporary spooling where necessary, clean temporary files and preserve cancellation, publication journals, collision-safe saves, receipt timing and restart recovery.
- [x] Publish a received file only after all authenticated frames and the complete expected length pass validation. Reject altered, substituted, missing, reordered and trailing chunks. Never acknowledge an incomplete transfer.
- [x] Provide browser large-file saves through a supported File System Access or OPFS path on secure origins. Retain an explicitly bounded small-file fallback where disk-backed browser APIs are unavailable; explain that larger saves require a capable HTTPS browser or the native app. Do not send plaintext to the server to bypass browser limitations or claim arbitrary Blob accumulation is bounded-memory processing. Large browser uploads remain streamed independently of this save limitation.
- [x] Verify deterministic cryptographic vectors and actual Android/web end-to-end files above 100 MiB, including zero bytes, exact boundaries, final short frames, tampering, interruptions and sequential bounded-buffer behavior. Document the separate 25 MiB ZIP aggregation limit. Equivalent iOS vector/stream tests are implemented; their execution remains pending below.

## 8. Branded QR codes (added scope)

- [x] Use the shushing symbol in the centre of generated download, upload and pairing QR codes across web, Android and iOS/share extension where those producers exist.
- [x] Centralize each platform's rendering with high error correction, a small white backing, logo bounds no greater than about 15% of the active matrix, and an intact four-module quiet zone. Keep finder patterns and the external scan margin unobstructed and render modules sharply at output resolution.
- [x] Verify actual decoding of branded download/upload/pairing codes, including long supported server addresses and dense payloads, with independent web/Android decoders. Preserve exact payloads and encryption-key fragments; branding does not change the QR protocol. Physical-camera and native iOS decoder checks remain pending below.

## 9. Validation and delivery

- [x] Add focused history coverage for mixed record types, ordering/filtering, signed-out access, account switches, origin collisions and local removal versus remote revocation. Preserve existing history and pending receipts; iOS test execution remains pending.
- [x] Run available Android navigation checks for completed sends, external share entry/recreation, History reopening and Back destinations. Record native iOS navigation validation separately.
- [x] Verify available scanner permission, denial/re-entry, camera lifecycle and paste/image routing checks on Android and in web automation. Physical optical scanning remains pending.
- [x] Test authenticated web scanning, signed-out direct entry, session expiry, HTTP fallback, denied/absent camera, same/different-origin links, hostile URLs and pairing payloads. Verify public transfer pages and actual header policy through a disposable Caddy deployment.
- [x] Build Android and inspect the installed app; run 62 Android and 99 shared tests. Build/type-check the web UI; run 21 web unit/integration and 44 browser tests, and backend tests.
- [x] Run available iOS source/configuration checks and Swift formatting; add native navigation, history, branded QR and streaming regression tests. Record unavailable Xcode execution separately.
- [x] Update documentation for consolidated navigation, scanner authentication/HTTPS, chunk format, limits and brand exports. Format changed files and inspect the final diff.
- [x] Remove task-owned temporary assets/caches and commit implementation together with this updated plan.

### Pending external validation

- [ ] On macOS, build the iOS app and share extension and run XCTest using the commands in [ios/README.md](../../ios/README.md#building-and-testing-on-macos), including `NavigationHistoryTests` and `StreamedFileTests`. Source checks do not establish native compilation.
- [ ] Exercise iOS send/receive/share-extension navigation, account redirects, history reopening, cancellation, foreground/background changes and larger-file interoperability on simulator/device. Profile extension memory above 100 MiB.
- [ ] Scan actual branded codes with physical Android/iOS cameras; verify camera grant/recovery, switching/torch and optical recognition. Emulator/image-decoder checks do not substitute for optical testing.
- [ ] Check small screens, enlarged text, native screen readers and platform launcher masks/backgrounds on physical Android/iOS devices; verify both appearances and offline local-file actions.
- [ ] Verify webcam capture on a trusted HTTPS origin and the browser's native save picker manually. Automated camera simulation and OPFS coverage do not establish those device/browser interactions.

## Acceptance criteria

The implementation criteria below are satisfied by source review and available tests. Native iOS and physical-device qualification remain explicitly pending above.

- [x] Settings has a compact native structure; signed-in users see account information rather than onboarding controls. Guest mobile access and authenticated creation flows remain correct.
- [x] There is one mobile History destination; scanned downloads appear directly in it and retain local-only removal semantics.
- [x] Completed send navigation is consumed once, with Home/History Back destinations preserved. Android recreation and Back regression checks pass; equivalent iOS implementation awaits native execution.
- [x] Mobile Scan QR code opens directly to an embedded camera with camera-free alternatives and visibility/foreground lifecycle controls on both platforms.
- [x] Web scanning is available only after login, with honest HTTP camera limitations; public transfer links remain usable without an account.
- [x] The shushing symbol is supplied across Android/iOS app icons and web favicons, while larger branding retains the exact psst.zip wordmark.
- [x] Available automated/runtime checks pass, and unavailable platform/device validation is explicitly documented without claiming completion.

## Verification evidence and remaining checks

Implementation is complete. The bounded audit outcome is **CLOSED_WITH_DEFERRALS**: all three findings are resolved, with external platform checks listed above. The campaign used one discovery round, one remediation batch and one integrated post-fix review; there were no supplemental findings. No check below implies that unavailable macOS or physical-device testing ran.

The Android debug build and 62 unit tests pass. A native 101 MiB + 7 byte upload/download round trip produced matching SHA-256 values; a transient network failure recovered through Retry. On the pre-fix APK, rotating after an external-share upload reopened Send with the consumed file. With the fix, rotation retains the completed QR, one Back returns Home, and opening a completed item from History returns to History. Denying/dismissing camera permission and reopening Scan no longer prompts again. Signed-out History retains only device downloads. A pending external share survived landscape/portrait recreation during account setup and reached Send with its original file after sign-in.

The shared test suite passes 99 tests, including exact frame lengths, tamper/reorder/substitution rejection, independent Node AES-GCM vector decryption, bounded processing above 100 MiB, target-origin policy reads and streaming transport cancellation. Backend `go test ./...` passes, including admin/member/guest authorization, persisted settings, exact upload-size boundaries and finishing/downloading an existing reservation after the limit is lowered.

Web type checking and production build pass. The web suite passes 21 unit/integration tests and 44 distinct browser tests; one deployment-only LAN check is intentionally skipped. Browser evidence includes a real 101 MiB file with matching SHA-256, 4 MiB maximum upload reads, OPFS publication, continued download after lowering the upload limit, lost upload-response reconciliation, scanner authorization/lifecycle, appearance and actual branded QR decoding. A disposable Caddy instance verified root-document camera permission and restricted public transfer documents.

The bounded review covered the implemented plan and added file-limit/chunk/QR scope once. Findings: NG1 repeated camera permission prompt on Android re-entry; NG2 consumed external-share Intent replay after Android activity recreation; NG3 iOS retry retaining an earlier lower admin limit. The single primary remediation batch resolves them, and the single integrated post-fix review found no supplemental issues. Android runtime discovered NG1 and reproduced NG2 on the pre-fix APK. The iOS NG3 regression uses a 150 MiB sparse file and policy changes 25 → 200 → 100 MiB; XCTest execution remains pending.

External checks remain pending: macOS app/share-extension build and XCTest, physical optical camera scans, real-device screen-reader/accessibility checks, iOS extension memory profiling, and manual trusted-HTTPS webcam/native browser-save-picker verification. The plan and repository instructions permit recording unavailable platform checks explicitly; this does not exclude any iOS implementation or claim those checks passed.

After explicit user approval, the built backend and web images were deployed to the existing local Docker project. Existing environment settings, LAN/loopback port bindings and named data volumes were retained. The configuration override was passed through standard input; no temporary secrets file was created. The running image IDs match the tested builds. HTTP checks pass for the workspace, public download shell and API configuration on both `http://192.168.178.29` and `http://127.0.0.1:18480`; document camera policies match the intended scope. The effective initial file limit is 25 MiB, configurable by an admin up to the existing 5 GiB operator ceiling. The earlier automatic approval rejection was resolved by the user’s explicit deployment authorization.

Task-owned emulator, disposable servers, fixtures, credentials, screenshots and temporary formatter/export tools were removed after verification. The debug APK remains at `android/app/build/outputs/apk/debug/app-debug.apk`. This plan is delivered in the same commit as the implementation.
