# Revoke transfers when deleting Android history

- [x] Add authenticated transfer and slot deletion, revoke child uploads, and safely remove payloads with cleanup retries.
- [x] Issue private deletion tokens for new resources and support explicitly enabled legacy deletion by ID on the development server.
- [x] Persist tokens in Android history and remove local entries only after successful remote revocation, with useful failure feedback.
- [x] Verify authorization, deletion races/failures, shared-link behavior, database upgrades, and Android builds.
- [x] Deploy the server update, rebuild the APK, and verify Android deletion against the development API.

Validation: Go race tests and vet passed; 20 Android and 67 shared tests passed; 11 web integration tests and 14 browser tests passed. An emulator upgrade preserved Room v2 history, retained entries when the old server rejected deletion, and successfully revoked a legacy transfer after the server upgrade. A new receive slot rejected unauthenticated deletion and was successfully revoked from Android using its stored token. The rebuilt APK is available under `android/app/build/outputs/apk/debug/`.

The development server at `http://192.168.178.29` was upgraded with `ALLOW_LEGACY_DELETION=true`, preserving its data. The deployed DELETE route responds correctly. Temporary test containers, volumes, and emulator were removed after verification.
