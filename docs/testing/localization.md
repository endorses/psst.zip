# English and German verification

Translations are bundled with the clients. These checks verify localization;
existing transfer/security tests continue to verify protocol behavior.

## Available local checks

From `web/`:

```bash
npm run check:localization
npm test
npm run check
npm run build
PSST_TEST_BACKEND_PORT=18795 PSST_TEST_BACKEND_URL=http://127.0.0.1:18795 npx playwright test tests/browser/localization.spec.ts tests/browser/workflow-ux.spec.ts tests/browser/receive-confidentiality.spec.ts tests/browser/ux.spec.ts
```

Browser tests create disposable data. The backend port and Vite proxy target must
match. Run one browser harness at a time because its web server uses port 4173.
Finish builds before starting it; avoid changing generated Vite modules during
browser checks.

From the repository root, with the configured JDK and Android SDK:

```bash
PYTHONDONTWRITEBYTECODE=1 python3 android/localization/check_resources.py
./android/gradlew -p android :app:assembleDebug :app:testDebugUnitTest :shared:testDebugUnitTest --no-daemon
PYTHONDONTWRITEBYTECODE=1 python3 ios/scripts/check_localization.py
PYTHONDONTWRITEBYTECODE=1 python3 ios/scripts/check_sources.py
PYTHONDONTWRITEBYTECODE=1 python3 ios/scripts/test_localization.py
go -C backend test ./internal/api -count=1
```

The portable iOS localization harness runs Foundation production helpers in a
Swift Docker container with platform-only boundaries stubbed. It does **not**
compile SwiftUI, the Kotlin framework bridge, app entitlements or native resource
packaging. See [iOS build instructions](../../ios/README.md) for the native gates.

## Native and layout checks

Use System, English and Deutsch in each client. Include a system preference list
with an unsupported language before de-AT or de-CH, an English regional preference,
and a list with no supported language.

- [ ] Confirm the choice survives relaunch, sign-out and changing servers. Verify
      Android OS per-app selection and app/extension agreement on iOS.
- [ ] Check German and English at narrow phone widths, enlarged text and both
      appearances. Keep primary actions visible, QR codes readable and navigation
      labels usable without horizontal page overflow.
- [ ] Check keyboard, TalkBack/VoiceOver labels and password visibility. Native
      camera/network permission prompts must use the intended OS language.
- [ ] Switch languages while files are selected, a login or administrator form is
      unsaved, the camera is active, and an error is visible. Preserve inputs and
      navigation; errors and formatted arguments should update together.
- [ ] Switch during upload/download and receipt retry. Confirm no restarted
      payload requests, extra allowance usage, missing checkpoints or lost save
      locations. Confirm cancellation still removes only the intended partial
      data.
- [ ] Check History filters, server/device selection, pagination and offline
      records. User titles such as `Settings` and filenames such as `Receive link`
      must remain unchanged even when they match catalog keys.
- [ ] Check counts 0/1/2, large values, decimal separators, date ordering, expiry,
      percentages and rates. Preserve MB/MiB semantics and explicit accounting UTC.
      Requests must keep raw numeric values and original identifiers.
- [ ] Verify unknown errors use a safe localized fallback. Arbitrary response
      bodies and credentials must never appear in notices or reports.

Executed evidence and unrun native checks are recorded in the
[implementation plan](../plans/english-german-localization.md). Contribution rules,
resource locations and terminology are in
[translation contributions](../localization/contributing.md).
