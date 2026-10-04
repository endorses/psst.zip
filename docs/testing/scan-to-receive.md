# Scan-to-receive verification

Use a disposable server and account for these checks. Do not use a production instance: generating fixtures creates transfers and a receive slot, and receipt/quota checks can consume downloads.

## Interoperability fixtures

From the repository root, with the web dependencies installed:

```sh
PSST_TEST_BASE_URL=http://127.0.0.1:18481 \
PSST_NATIVE_BASE_URL=http://10.0.2.2:18481 \
PSST_TEST_USERNAME=scanadmin \
PSST_TEST_PASSWORD='<disposable-test-password>' \
node web/tests/native-fixture.mjs /tmp/psst-native-fixtures
```

`PSST_TEST_BASE_URL` is the server origin reachable from the host. The optional `PSST_NATIVE_BASE_URL` changes the generated links for the receiving device; Android emulator `10.0.2.2` refers to its host. For a physical phone use a reachable LAN address or trusted HTTPS origin instead. The service must listen on that address and permit the intended development HTTP use.

The generator uses the actual web client encryption implementation and QR encoder. Its output contains single-file and multiple-file transfers, duplicate filenames, Unicode filenames, a transfer with a damaged final blob, a one-download-quota transfer and an upload-slot QR. `fixtures.json` contains full links including secret key fragments; do not publish it or attach it to logs. The expected plaintext files and SHA-256 digests permit byte-for-byte native-save verification. Generation does not validate a native recipient by itself.

## Device workflow checklist

- [ ] Start with no configured server or account. Reach Scan QR code and History → Downloaded without signing in.
- [ ] Scan single.png and multi.png using a physical camera, and separately paste their links. A scan starts automatically; a paste waits for Receive files. Verify every saved file's SHA-256 against fixtures.json. Duplicate names must not overwrite each other.
- [ ] Confirm files appear in Android Downloads/psst.zip or the iOS app's Received folder in Files. Open/Share must work without exposing private app paths or launching files automatically.
- [ ] Deny camera permission and verify Paste link remains usable. Test Android 8–9 storage permission grant/denial and Android 10+ MediaStore publication.
- [ ] Sign in to a different server, receive again, and verify the configured account remains unchanged and no account Authorization/Cookie header reaches the scanned server.
- [ ] Interrupt after the first file saves, restart, and retry. Verify completed files are reused and the remaining files save exactly once. Test interruption between file publication and history checkpoint as well as ordinary cancellation/backgrounding.
- [ ] Receive corrupt.png. The valid earlier file remains saved; the damaged file never appears as completed and the server receives no completion receipt. A wrong key must publish no plaintext.
- [ ] Inject a transient failure for a later blob and a separate failure for the receipt. Retry the transfer, then retry the receipt. A receipt retry must fetch no blob; the sender sees Downloaded only after all files have saved.
- [ ] Rescan quota.png after success; already-saved files must remain accessible without another blob download. Remove a saved file externally and verify explicit redownload consent and a clear quota/expiry error if it cannot be fetched again.
- [ ] Reopen local history offline, after sign-out/account switch, and after remote expiry/revocation. Existing saved copies remain usable. Removing a local history entry must preserve saved files and the sender's remote link.
- [ ] Scan upload.png, select files deliberately and send. Verify they appear in the original receive slot and decrypt with its key. Interrupt or fail an upload; check scoped cleanup and retry without account credentials.
- [ ] Scan an existing server-pairing QR. Confirm the server/account change is shown before redeeming its single-use code, and cancellation leaves the current login unchanged.
- [ ] Check both appearances, small displays, enlarged text, screen-reader labels, keyboard focus, rotation and foreground/background transitions.

## Automated checks

Use the existing JDK/Android SDK setup from the root README:

```sh
./android/gradlew -p android :app:assembleDebug :app:testDebugUnitTest :shared:testDebugUnitTest --no-daemon
python3 ios/scripts/check_sources.py
```

The iOS build and XCTest commands are in `ios/README.md` and require macOS/Xcode. Source gates on Linux do not establish compilation, camera behavior, file-protection behavior or native accessibility. Keep unrun device/platform checks pending in the implementation plan.

## Recorded results

Completed automated and emulator results, the final review outcome, and remaining platform checks are recorded in [the implementation plan](../plans/scan-to-receive.md#implementation-and-verification-evidence). The checklist above remains a reusable device-validation procedure; it does not claim that pending physical-device or iOS checks passed.

Delete task-owned fixture directories, keys, screenshots and temporary servers after testing.

## Navigation, branding and larger files

The follow-up [navigation/scanning/branding plan](../plans/mobile-navigation-scanning-and-branding.md) adds unified History, an embedded camera, branded QR codes and admin-controlled file limits. Fixtures now use [chunked-v1](../protocol/chunked-files.md); there is no old file-ciphertext fallback. Increase the disposable server's limit before generating files above the default 25 MiB. Use its authenticated admin `PATCH /api/v1/admin/settings` endpoint with `{"max_file_size":134217728}` for a 128 MiB test limit. Never reuse production credentials or write test settings to a live user server.

- [ ] Receive and send a deterministic file above 100 MiB; compare exact hashes and verify no individual plaintext read/write exceeds the 4 MiB framing bound. Check app and iOS share-extension cancellation with temporary-file cleanup.
- [ ] Lower the admin upload limit after reserving an upload and after saving a transfer. The reservation can finish, existing downloads remain usable, and new uploads above the lower limit are rejected. Raise the limit and retry a previously rejected send.
- [ ] Complete an Android external-share upload, recreate/rotate the activity, then press Back. The upload must not replay and the existing transfer must remain in History. Check a pending unauthenticated external share survives account setup without switching its intended account silently.
- [ ] Deny camera permission, leave and reopen Scan: no repeated automatic prompt. Grant through system settings and verify immediate embedded preview, then verify camera release on navigation/backgrounding and when mobile Settings covers it.
- [ ] Scan the actual branded download, upload and pairing codes from the web/app screens with physical cameras; preserve the four-module quiet zone and compare payloads exactly.
- [ ] Inspect Android adaptive/round/themed launcher icons and iOS Home-screen icons on actual platform backgrounds. Verify the favicon at 16/32 px and web header logo under System/Light/Dark.

For web coverage, run `npm run test:browser` from `web/` with `PSST_TEST_BACKEND_PORT` and `PSST_TEST_BACKEND_URL` set to a free disposable loopback port. The isolated scanner suite is `npx playwright test --config playwright.scanner.config.ts`. Browser tests cover actual image decoding and simulated camera lifecycle; they do not claim physical-webcam operation or manual native save-picker verification.
