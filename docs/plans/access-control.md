# Account access control and mobile pairing

Only authenticated users may create standalone transfers and receive slots. Generated download links and uploads into an existing receive slot remain public capabilities. Accounts are created by administrators; there is no public registration. This implementation covers the backend, web UI, and Android app with shared Kotlin API support.

## Implementation

- [x] Add persistent users, password hashes, hashed revocable sessions, single-use pairing codes, and resource ownership through database migrations.
- [x] Bootstrap the first administrator from operator-provided credentials, require authentication on transfer/slot creation, and enforce owner/admin authorization on resource mutations.
- [x] Implement login/logout, current-user and device-session management, administrator account creation/disable/password reset, and rate-limited pairing issuance/redemption.
- [x] Protect browser sessions with HttpOnly cookies and same-origin mutation checks; default to HTTPS, with an explicit development HTTP option. Keep credentials and encryption keys out of logs and public responses.
- [x] Keep public download links and receive-slot uploads functional, with bounded slot lifetime, transfer count, and reserved upload bytes.
- [x] Build the authenticated web dashboard for encrypted sending/receiving, account-scoped resource management, administrator user management, device revocation, and expiring mobile-pairing QR codes.
- [x] Add Android username/password login, secure device-session storage, logout, and QR pairing in server configuration. Apply credentials only to the configured server and preserve public link access.
- [x] Add security and integration tests for anonymous denial, role/ownership boundaries, disabled accounts, session revocation, CSRF, pairing expiry/reuse, public link flows, and slot limits. Run web and Android checks/builds and Go race tests.
- [x] Document self-hosted administrator setup, HTTPS/development configuration, credential/device lifecycle, migration behavior, and client compatibility.
- [x] Verify the integrated development deployment and APK, update this plan with validation evidence, and commit the implementation.

## Contract and decisions

The first administrator is initialized through `ADMIN_USERNAME` and `ADMIN_PASSWORD` only when the user database is empty. Existing downloads remain accessible through their public links; ownership cannot be inferred from old anonymous records. Legacy receive slots must be recreated after login to accept new uploads, preventing ownerless upload access after migration. Private resource deletion tokens remain supported for their existing holders, and administrators can manage legacy resources. New transfers and slots belong to the authenticated creator; public slot children inherit slot ownership.

Browser sessions use HttpOnly cookies; Android uses a separate bearer session encrypted with Android Keystore. Password changes and account disabling revoke sessions and pairing grants. Users can list/revoke their devices. Pairing QR content is JSON with `type: "psst-pairing"`, `version: 1`, `server_url`, and a short-lived single-use `code`; it never contains a password or existing session token. Redemption issues a distinct device session.

Server history stores metadata, not encryption keys. Web links generated on that browser may be remembered locally, scoped by account; resources created on another device can be managed but their encryption keys cannot be recovered from the server. Existing iOS account-login UI is outside this Android-focused change; old clients cannot create resources once authentication is required.

Development HTTP is an explicit operator opt-in because credentials are otherwise exposed in transit. Normal self-hosted deployment uses trusted HTTPS through the existing Caddy setup. No certificates are embedded in APKs, and no authentication-disable switch is introduced.

## Validation

Go race tests and vet passed, including ownership, CSRF, password resets, disabled accounts, pairing expiry/concurrent redemption, and concurrent cumulative slot quotas. Web check/build passed with zero Svelte warnings/errors; all 11 web integration tests and 17 browser tests passed against an isolated LAN HTTP deployment. Android assembled successfully with 25 app tests and 73 shared tests passing.

A bounded review identified cross-account Android local history exposure. Room v4 now stores account ownership; list/detail/deletion checks, background jobs, cached keys, and navigation react to account changes. Focused isolation/deletion tests cover this fix. On the emulator, upgrading Room v3 to v4 preserved one legacy record, signing in created a new account-owned receive link, and switching to a regular second account showed empty history. Manual Android login and the scanner's camera permission/preview were exercised; QR generation, single-use redemption, and replay rejection passed automated tests. Optical decoding of the virtual camera scene was not confirmed.

The existing development stack at `http://192.168.178.29` was upgraded with its data preserved. Smoke checks confirmed health, anonymous create denial, administrator login, and session invalidation after logout. Generated administrator credentials are in the ignored, mode-0600 `.env`; no credentials are committed. Development HTTP and the previously authorized legacy deletion option remain explicit local settings. The rebuilt APK is `android/app/build/outputs/apk/debug/app-debug.apk`.

Temporary test services, volumes, emulator, formatter, and credential-bearing test fixtures are removed after verification. Existing user branding edits are preserved separately from this commit.
