# Scan to receive files

## Goal and agreed experience

Add **Scan QR code** to the Android and iOS main entry points, with **Paste link** available from the scanner. Scanning an existing psst.zip download QR starts downloading, decrypting and saving its files without a login or an extra confirmation step. The same QR remains usable by a regular camera opening the web download page.

The scanner shows the source server and progress, supports cancellation and retry, and finishes with “1 file saved” or “3 files saved”, the save location, and Open / Share actions. Files never open automatically. A saved transfer appears in local Received history. Android saves to Downloads/psst.zip; iOS saves to the app's folder in Files.

System camera permission and, where required on older Android versions, storage permission remain native prompts. Denying camera access leaves Paste link available. Pasting is an explicit action followed by **Receive files**; do not read the clipboard automatically. Receiving from another server does not replace the configured account or server.

Implementation and its bounded review are complete, with the external validation deferrals listed below. Checked implementation items reflect source inspection and the available automated/emulator evidence below. Unrun macOS and physical-device checks remain explicitly pending; no iOS compilation or device results are claimed.

## Existing implementation and constraints

The existing `/d/{id}#{key}` and `/u/{id}#{key}` links already carry the encryption key in a URL fragment. `shared/.../model/UrlHelper.kt` parses the ID, type and decoded key, but does not validate the complete server origin or key length and does not return the origin. It needs strengthening before using arbitrary scanned input for network access. Server configurations currently support an origin, not an arbitrary path prefix; preserve that contract.

`TransferApi.kt` already provides metadata, bounded manifest/blob downloads and idempotent download acknowledgements. `ReceivedDownload.kt` and the native receive implementations provide decryption, per-file checkpoints and receipt handling, but their main workflow is collecting child transfers from a receive slot. A scanned download is a direct transfer and must not be mistaken for a receive slot or an owned link.

Pairing QRs use the existing versioned JSON payload (`psst-pairing`), not a URL route. Preserve that protocol identity. Upload-link handling can reuse the web's existing slot-child creation and scoped upload capability contract; the shared `SlotApi` currently lacks that creation operation.

The first version keeps the existing bounded, sequential file processing limits. It does not introduce streaming cryptography, resumable byte-range downloads, OS background download services, or universal/app-link registration for arbitrary self-hosted domains. No QR format change or routine backend API change is expected.

## 1. Shared input handling and network isolation

- [x] Add one typed input classifier for download links, upload links and existing pairing payloads, usable by both apps. Reuse/harden `UrlHelper` without breaking existing link builders and their callers.
- [x] Parse complete HTTP(S) URLs; extract and normalize the origin including a nondefault port, resource kind, UUID and exactly 32 decoded key bytes. Reject credentials, unsupported schemes, malformed paths/IDs/keys, unexpected query strings and oversized input. Permit surrounding pasted whitespace, supported LAN HTTP origins and IPv6 hosts. Keep rejected input local.
- [x] Keep the fragment out of all API requests, logs, analytics, crash messages and visible errors. Persist keys using platform protected storage when retry/reopening requires them; history holds a reference, not an exposed full link. Do not put keys in Android navigation-route strings or saved UI bundles.
- [x] Create an anonymous client bound to the scanned origin, including when it matches a signed-in server. Never attach the current account token/cookies, change account settings, or substitute the configured server for the scanned origin. Restrict or reject cross-origin redirects for metadata, manifests, blobs and receipts; never downgrade trusted HTTPS or bypass certificate validation.
- [x] Debounce repeated camera callbacks and allow only one active scan/download operation. Classify before requesting permissions beyond camera or starting network work. Unknown codes show a recoverable error with Scan again / Paste link.

## 2. Direct-transfer receive pipeline

- [x] Introduce a direct-download coordinator/state model separate from receive-slot creation: inspecting → downloading → decrypting → saving → saved, with cancellable and recoverable failure states. Share existing download/decrypt/save helpers where their semantics match.
- [x] Fetch public transfer metadata and encrypted manifest, authenticate/decrypt the manifest locally, then validate supported file counts/sizes, unique blob IDs, names and total-size arithmetic before fetching file bodies. Reject incomplete, expired, revoked or unavailable transfers with useful messages.
- [x] Process one file at a time within current buffered limits. Add a bounded progress callback to the shared download API if needed, showing actual byte progress and current file count. Do not invent a percentage for decryption or filesystem writes.
- [x] Sanitize filenames and reject path traversal; use collision-safe output names without overwriting existing files. Authenticate/decrypt the complete file and check its declared length before publishing plaintext. Treat MIME values as hints, never instructions to launch content.
- [x] Persist each successful file's blob ID, actual output URI/path and display name, followed by the transfer's complete saved state. Use pending/temporary writes and atomic publication where supported; reconcile interrupted writes/checkpoints after restart. Delete incomplete output on failure/cancellation while retaining successfully saved files.
- [x] On retry or rescan, reopen the existing local record identified by origin + resource kind + ID. Skip checkpoints only when the saved destination still exists and is accessible. Avoid duplicate writes/jobs and do not let a conflicting key overwrite a working local record. If a local copy was removed, offer an explicit redownload when the server still permits it.
- [x] Queue the existing public download acknowledgement only after all files are durably saved. Retry failed receipts independently, without re-fetching files or turning a saved transfer into a failed download. Preserve pending receipts across restart and sign-out.
- [x] Preserve download-quota semantics: retries may be impossible after a server counts a blob attempt or expires/deletes it. Report this honestly rather than promising resumability. Cache validated manifest/checkpoint data needed to reopen saved files even after server cleanup.
- [x] Keep cancellation and lifecycle behavior explicit. Survive ordinary view recomposition/rotation without restarting work; on app backgrounding/interruption use the established foreground-only cancellation policy and retain checkpoints. Do not claim continued downloading after the OS stops the app.

## 3. Local received history and permissions

- [x] Add an explicit local downloaded-transfer record distinct from owned sent transfers and receive slots. Prefer a dedicated guest-download store or an equivalently isolated schema; do not disguise guest rows as account-owned resources. Namespace records/checkpoints by server origin and resource identity to avoid collisions between self-hosted servers.
- [x] Keep local downloads visible without signing in and across account changes. Label their local/device scope and source server; signed-in server history must retain its existing account isolation and revocation rules.
- [x] Provide Received history entries with filename/summary, saved count, size, date, status and actions to open/share saved files or resume a partial download. Keep saved copies usable offline and after link expiry/revocation; remote expiry must not override their local saved status. Distinguish receiving a direct transfer from creating a receive link in labels and filters.
- [x] Removing a local received entry removes its local history/key/checkpoint metadata only; it must never call server revocation endpoints, even if legacy ID-based deletion is enabled. Clearly state that saved files remain. Do not add implicit local file deletion.
- [x] Provide backward-compatible migrations/decoding as needed. Preserve all existing history, keys, account associations, receipts and saved-file records; failed persistence must not silently discard them.

## 4. Android interface and saving

- [x] Add the Home **Scan QR code** action and a guest-accessible entry from first-run/sign-in Settings. Keep Send / Receive link creation authenticated. Route the scanner and local received history around the existing account setup gate without weakening authenticated operations.
- [x] Reuse the installed camera-scanning integration with permission-denied handling and Paste link. Show the download screen immediately after an intentional scan; release the camera before network processing. Use existing psst.zip styling and the saved appearance setting.
- [x] Extract/reuse the current MediaStore saving path, publishing to Downloads/psst.zip on Android 10+. Persist returned content URIs for Open/Share and missing-file checks. On supported Android 8–9, implement the public Downloads path with the required runtime permission and content-URI sharing; handle denial explicitly. Do not silently fall back to an invisible private directory.
- [x] Use platform content intents with temporary read grants for Open/Share, and handle missing viewers/files gracefully. Provide clear Back/cancellation behavior and accessibility descriptions. Verify small screens, enlarged fonts and both appearances.

## 5. iOS interface and saving

- [x] Add an equivalent primary **Scan QR code** entry and guest access from initial setup. Keep local Received history available without an account while preserving the current authenticated tab flows.
- [x] Generalize/reuse `Shared/PairingScanner.swift` for typed scanning results; provide Paste link with explicit user action and camera-denied recovery. Update pairing-only scanner errors and camera permission purpose strings for both supported uses. Apply the shared appearance setting and native accessibility/Dynamic Type conventions.
- [x] Extract/reuse native receive saving into Documents/Received, visible through Files, with atomic writes, collision-safe names and durable output references. Reuse document preview/export for Open/Share. Preserve the existing folder's compatibility and avoid a destination prompt per scan.
- [x] Apply shared input classification, persistence, safe writing and receipt behavior consistently wherever used by the app and share extension. Retain extension pairing and send workflows; downloads belong in the main app rather than launching a long receive job inside the lower-memory share extension. This phase does not add a new share-extension URL-import entry point.
- [x] Handle scene transitions, cancellation and interrupted-save recovery without losing already-saved files. Include native app and share-extension compilation in validation even when most new UI is app-only.

## 6. Other recognized QR types and web compatibility

- [x] For `/u/` links, open a native file-selection/send flow targeted at that existing receive slot. Make the recipient server visible; require deliberate file selection and Send. Do not create a new receive slot, demand a recipient account, or switch the configured server.
- [x] Add the missing shared slot-child creation method and use only the returned resource-scoped capability for upload/finalization/cleanup, following the web flow. Encrypt with the scanned slot key. Keep this guest send isolated from the account's ordinary Send flow and preserve cancellation/partial-upload cleanup.
- [x] For a pairing payload, route into the existing server/account setup flow. Show the target server and require deliberate account setup/replacement before redeeming the single-use code; a receive scan must not silently replace an existing login. Preserve existing Settings pairing behavior.
- [ ] Keep Android, iOS and web-generated download QRs unchanged. Verify all three producers can be received by both native apps and that their links still open the browser download page. No web redesign or new QR payload is required.

## 7. Verification and delivery

- [x] Shared tests: valid generated links, LAN/HTTPS/nondefault ports/IPv6, malformed or hostile URLs/IDs/key lengths, pairing discrimination, anonymous origin isolation, redirects, bounded downloads, progress and slot-scoped upload credentials.
- [x] Add native receive/history tests (Android tests executed; iOS XCTest execution remains pending below): authenticated decryption failure, wrong manifest size, unsafe filenames, duplicate names, partial writes, unavailable storage, cancellation, process interruption, retry/rescan deduplication, missing local files, origin/ID collisions, receipt retry, migration and removal without revocation.
- [ ] End-to-end checks using disposable server data: app/web-generated single- and multi-file QRs, guest first launch, a different scanned server while signed in, LAN HTTP and trusted HTTPS, paste flow, expired/revoked/quota-limited links, scanner permission denial, `/u/` guest uploads and pairing routing. Verify saved bytes against the originals and sender Downloaded only after all files save.
- [x] Run Android/shared unit tests and build the APK. Verify emulator lifecycle/layout and the Android 8–9 storage branch on API 28 as well as API 36.1.
- [ ] Verify camera scanning on a physical Android device. Emulator paste/camera-permission checks and decoding a screenshot do not substitute for optical camera scanning.
- [x] Run iOS source/configuration checks and format Swift sources.
- [ ] On macOS build the app/extension and run XCTest. Verify physical-camera scanning, Files persistence, Open/Share, Dynamic Type, appearance, restart and account changes. Track unavailable macOS/device checks explicitly as pending; source checks do not prove native compilation or runtime behavior.
- [x] Verify unchanged public web download/upload-link compatibility with the disposable encrypted fixtures. Web/backend production code is unchanged; no additional production web/backend test rerun is required for the fixture generator. Existing Android/shared regression suites cover affected native/shared behavior.
- [x] Update README and platform validation documentation with guest scanning, destinations, local history semantics, limits and testing instructions. Format all changed files, verify each task before checking it off, remove task-owned temporary artifacts, and commit implementation plus this plan.

## Acceptance criteria

- [ ] A signed-out Android or iOS user can scan an existing download QR from either app or the web UI and receive every supported file with no login, server reconfiguration, browser handoff or per-transfer destination prompt.
- [ ] Paste link provides the same receive capability without camera access. A scan starts automatically; a paste starts after Receive files.
- [ ] Saved files are accessible in the documented location and through Open/Share. Progress, cancellation, partial failure and retry preserve accurate state without duplicating completed saves.
- [ ] No account credentials or key fragment are sent to the scanned server. Received history works without login; its removal cannot revoke someone else's link.
- [ ] Completion is acknowledged only after successful durable saving of the entire transfer. Receipt retries do not download files again.
- [ ] Existing upload and pairing QR codes route to their appropriate deliberate actions. Existing link formats, ordinary browser use, account isolation and mobile appearance choices remain compatible.

## Implementation and verification evidence

Shared `ScanInputClassifier`, `ServerOrigin`, `UrlHelper` and `ManifestValidator` enforce input/origin/key/manifest contracts. Fresh guest clients use the common no-redirect policy; Darwin also disables native cookie and credential storage. Public slot-child creation returns a resource-scoped capability. Android `ScanViewModel` is shared at the activity scope across guest routes; iOS `GuestTransferModel` is app-scoped. Both use separate local download stores, protected key references, publication journals, independent receipts and guest-upload cleanup. No backend or existing QR format change was needed.

The shared suite passed **90 tests**. The Android suite passed **52 tests**, including the new save/checkpoint/cleanup/crypto regressions, and the debug APK built. The iOS source/configuration gates passed and **16 XCTest cases were added but not run**. Changed Kotlin/Swift/XML/Python/Markdown/JavaScript files were formatted with their respective tools; final diff checks are part of delivery.

Disposable fixtures generated with `web/src/lib/crypto.ts` verified browser download plaintext and existing `/d/` and `/u/` guest routes. Android API 36.1 emulator checks verified anonymous multi-file saves against SHA-256 originals, duplicate names, a transient second-blob failure followed by app restart and retry without fetching the first blob again, receipt-only retry after a 503, a damaged final blob producing no completion receipt, one-download-quota rescan, single-file wording, native sharing, camera denial followed by paste, and explicit pairing cancellation/confirmation. After pairing, a different scanned origin received no Authorization/Cookie header or key fragment, and the configured account origin remained unchanged. Removing local received history preserved saved files and a remotely downloadable sender link. Guest `/u/` upload bytes decrypted with the original slot key and matched their source.

Android API 28 verification exercised first-run guest entry, denied storage permission, retry/grant, public Downloads saving and matching plaintext bytes. The final readable-name collision handling is checked on both API 28 and API 36.1. [The fixture workflow](../testing/scan-to-receive.md) documents reproducible setup and the remaining external checks.

External validation remains pending: macOS app/extension compilation and XCTest; physical-camera and accessibility checks on Android/iOS; the full native trusted-HTTPS/device matrix and actual iOS end-to-end interoperability. These limits do not represent implemented behavior as tested. Existing user guidance explicitly includes iOS implementation despite unavailable iOS testing, and this plan permits recording unavailable native checks as pending.

## Review outcome

The bounded review closed with documented platform-validation deferrals after one discovery pass, one remediation batch and one integrated review. Both findings were resolved: iOS requires explicit redownload consent when saved files are missing from partial as well as complete records; Android preserves successful guest-upload finalization after a lost response or local cleanup-journal failure. Focused Android regressions pass; the corresponding iOS XCTest cases await macOS execution. An additional response-loss proxy smoke check did not reach finalization because its upload forwarding failed, so no successful emulator result is claimed for that fault-injection scenario. The two Android finalization cases are covered by passing unit regressions. No supplemental findings remained. Acceptance criteria above stay unchecked where full cross-platform runtime evidence is unavailable.
