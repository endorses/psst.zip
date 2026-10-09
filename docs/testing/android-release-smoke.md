# Optimized Android APK smoke

Use this compact check for the first APK release and changes to the SDK, native
libraries, R8 rules or protected-storage/update paths. It complements the existing
source tests; it is not another full transfer suite or a prerequisite for every
documentation/container change.

Test the normal public APK, without `PSST_ANDROID_DEVICE_TEST_*` inputs or diagnostic
classes. Verify its source, version, native alignment and disposable signature with
`tools/android_release.py` before installation. A temporary signer exercises the
release configuration but does not establish production-key custody or the
operator's installed certificate. Keep those decisions separate from this check.

## Disposable setup

Use owned AVD data and a separate ADB server. Never uninstall or clear an existing
phone's app to prepare a fixture. Use the latest stable emulator and the stable
API 37 16 KB system image; the minimum-supported API 26 path has a separate legacy
Downloads-permission check. Android 17's emulator requires at least 4 GB RAM.
See [official emulator release notes](https://developer.android.com/studio/releases/emulator).

The existing `web/tests/browser/server.mjs` creates a temporary database/storage
directory and removes it when stopped. Start it with a private
`PSST_TEST_STATE_FILE` under the fixture directory to retain its exact runner PID
for cleanup. The Vite gateway runs on loopback port 4173. Create a synthetic regular
account using the existing browser authentication-fixture procedure; administrators
cannot create transfers. No production accounts or passwords are needed.

Reverse only the owned emulator's port to the local gateway:

```sh
adb -s "$fixture_serial" reverse tcp:4173 tcp:4173
```

Links use `http://127.0.0.1:4173` on both sides. The disposable backend explicitly
allows development HTTP; this does not change production TLS or DNS. Testing LAN
permission requires the gateway address instead of loopback, since loopback is
exempt from that permission.

Generate small encrypted inputs using the existing web implementation:

```sh
PSST_TEST_BASE_URL=http://127.0.0.1:4173 \
PSST_NATIVE_BASE_URL=http://127.0.0.1:4173 \
PSST_TEST_USERNAME="$synthetic_username" \
PSST_TEST_PASSWORD="$synthetic_password" \
node web/tests/native-fixture.mjs "$fixture_dir/inputs"
```

Only its `single` transfer is needed here; do not run the corruption/quota scenarios
as another release suite. Keep `fixtures.json`, QR images, tokens and links private.
Use its expected plaintext/hash for an exact-byte comparison.

## Compact public flows

| Flow                     | Concrete evidence                                                                                                                                                                                                                                                    |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Guest QR/download        | Import `single.png` through Scan QR code → Choose QR image → native picker, then compare the saved plaintext to the fixture.                                                                                                                                         |
| QR account pairing       | Create a five-minute grant using a synthetic regular user's `POST /auth/pairings`, encode `{type:"psst-pairing",version:1,server_url,code}`, import it through Settings → account QR scanner, confirm setup, then independently check the grant reports `connected`. |
| Document/photo Send      | Share one MediaStore image into the app, add the small text document using DocumentsUI, send both, then use the web `decryptManifest` and `decryptFileStream` implementations to compare every downloaded byte.                                                      |
| Saved-file sharing       | Open the guest file's Share action and check Android's native chooser resolves its filename through the production FileProvider. This alone does not prove delivery to an external receiver.                                                                         |
| Retained Receive key     | Create a Receive-v2 link before updating, encrypt one 32-byte submission to its public key, then save/decrypt it with the updated APK and compare all bytes. Reuse the submission sequence in `receive-confidentiality.spec.ts`.                                     |
| Newer same-signer update | Install a normal higher-code APK with `adb install -r`; retain the app UID and paired session, recover the exact Send key from local history, decrypt both sent files again, and redownload a missing synthetic guest file through retained device history.          |

Normal `startActivity` sharing carries URI permissions through `ClipData`. A shell
`am start` stimulus does not perform the same preparation automatically. Supply the
same MediaStore URI as intent data as well as `EXTRA_STREAM` so the platform can
grant read access:

```sh
adb -s "$fixture_serial" shell am start \
  -a android.intent.action.SEND -t image/png \
  -d "$fixture_photo_uri" \
  --eu android.intent.extra.STREAM "$fixture_photo_uri" \
  --grant-read-uri-permission -n zip.psst.android/.MainActivity
```

Use a URI actually registered in the owned emulator's MediaStore. A bare file URI
or missing read grant is not an equivalent sharing test.

For the same-signer check, both APKs must be normal clean-source builds with their
own version metadata and the same disposable signer. Do not use a fixture version
override and then describe that APK as public. Keep the old signer until both
artifacts are signed, then remove the temporary key and passwords during cleanup.
The separate [private signing-transition diagnostic](android-release-updates.md)
tests debug-to-production lineage and should not be substituted for these flows.

## Camera and minimum API

The stable emulator supports `-camera-back imagefile:<private-PNG-path>`. Generate
the same synthetic download QR on a white canvas, positioning the complete code
inside the preview's central decoder crop. A visible code near the preview edge
can still be outside that crop. Start the owned AVD with that
input, grant camera permission, and let the production camera decoder read it;
do not press Choose QR image during this check. This exercises the optimized camera
path, while real-device optics and OEM behavior remain separate evidence.

On API 26, save a received upload, deny the storage prompt, refresh the inbox and
check the explanation/Retry remain visible. Grant permission on Retry and compare
the decrypted file bytes. API 29+ uses a different Downloads storage API and cannot
substitute for that minimum-API check.

Record APK source revisions, codes, API/page size and outcomes without keys or
private URLs. Stop only the owned emulator/ADB server and exact disposable server
processes, then remove the fixture/cache directory. Preserve the observed original
debug APK until the real-phone transition is verified. Physical-device signing,
NetGuard and production-key recovery remain explicit first-release prerequisites.
