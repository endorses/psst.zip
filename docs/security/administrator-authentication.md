# Administrator authentication and recovery

Administrators manage the service through the website. Regular accounts retain
their existing password and mobile pairing flows. Administrator accounts cannot
sign in through the device-token route or create/redeem mobile pairing grants,
whether or not a second factor is enabled.

This release uses password plus an authenticator's time-based one-time password
(TOTP), implemented with [`pquerna/otp`](https://github.com/pquerna/otp). It supports
the project's self-hosted origins, including explicitly opted-in LAN development
HTTP. It does not make HTTP safe for production. Use trusted HTTPS and trustworthy
client software. TOTP improves password-only authentication but is not
phishing-resistant; passkeys remain a possible future authentication choice.

## Set up an authenticator

Open the administrator's Account page and start authenticator setup. Scan the QR
code with your authenticator or enter the manual secret, then confirm with a
six-digit code. QR generation happens locally; no external QR service receives
the secret. The scheme uses SHA-1 TOTP, a 30-second period and a one-period clock
tolerance. Keep server and authenticator clocks synchronized.

Pending setup is bound to the initiating account/session, expires after five
minutes and has a bounded confirmation-attempt count. It does not enable a factor
until confirmation succeeds. Cancel or let an existing setup expire before
starting another; another session cannot confirm or replace that pending setup.
An already enabled authenticator cannot be overwritten through enrollment.

Successful enrollment displays ten recovery codes once and ends all existing
sessions and pairing grants. Save the codes somewhere separate from the
authenticator before proceeding to sign in again. The browser does not persist
the secret or recovery codes in local storage. The server cannot display the
recovery codes again; it stores only their hashes. If the response was lost but
the authenticator works, sign in and generate a replacement set.

Existing administrators without an authenticator see a prominent warning after
upgrade. Their password still works; upgrading does not silently lock them out.
They must deliberately enroll to gain second-factor protection.

## Sign in and confirm sensitive changes

With an enabled factor, a correct password alone does not create a session.
Complete sign-in with a fresh authenticator code or one unused recovery code.
Accepted authenticator time steps cannot be reused. If a code was just used for
setup or sign-in, wait for the next code before using it for another verification.
Each recovery code works once, including under concurrent attempts.

High-impact administrator mutations require authentication within the last five
minutes. This includes user management, budgets, server settings, emergency
pause/resume, broad account shutdown, resource revocation and factor changes.
An expired proof opens a confirmation dialog. After confirmation, deliberately
submit the original action again; the application preserves its draft and does
not silently replay a destructive request. Ordinary inspection, sign-out and
canceling your own pending enrollment remain available without a recent proof.

For enrolled accounts, confirmation requires the current password and an
authenticator or recovery code. Unenrolled accounts confirm the password.
Five failed administrator proofs cause a five-minute account cooldown. Attempts
during that cooldown do not extend it. The state persists, so restarting does not
provide unlimited factor guesses. Wrong-password login responses remain generic;
factor-specific guidance is shown only after the password succeeds or within an
authenticated identity check. Network-address and account
throttles, and a global expensive-password-work cap, also apply. This does not
guarantee availability against every distributed denial-of-service attack.

Generating a new recovery set invalidates all previous recovery codes and ends
all sessions. Removing the authenticator also removes recovery material and ends
all sessions. Sign in again afterward. Password changes and administrator password
resets do not remove an enabled second factor. Factor state and current credentials
are rechecked transactionally so an in-progress login cannot create a session
using superseded factor state. Sensitive administrative writes also recheck their
initiating session inside the same database transaction as the change. A request
whose body was delayed until after session revocation cannot then commit a budget,
pause, user-management or revocation change under that old authorization.

## Sign-in throttling and admission

Login and pairing redemption share a client-address bucket: ten initial attempts,
then one replenished attempt every five seconds. The address is resolved using
the explicit trusted-proxy policy; arbitrary forwarded headers are not client
identities. Password login also has a separate bucket for the trimmed,
case-insensitive account name: ten initial attempts, then one every thirty
seconds. The server retains hashes of bounded account identifiers rather than
raw names. A denial does not permanently lock the account, and changing addresses
cannot reset its account bucket. Unknown accounts get the same authentication
failure response as incorrect passwords for existing accounts.

Users sharing a NAT have independent account buckets but share the address
bucket. A busy shared address may therefore require a short wait. Distributed
attacks against different accounts still require network/provider defenses;
application throttles cannot guarantee control of their incoming traffic costs.

At most four password-work requests are admitted concurrently, without a waiting
queue. A saturated guard rejects before reading the login body and gives retry
guidance. The broader request guard runs before authentication/database/body
work. Its default 128 application slots and separate 32 recovery slots prevent
public payload saturation from consuming every slot needed for sign-in,
administration, revocation and health. These lanes are bounded too; they are not
an unlimited bypass for administrative URLs. Errors, cancellation and panic
release their admission slots. Tests exercise these properties through the
production router; they do not establish a throughput guarantee for every host.

## Recover without email

If your authenticator is unavailable, use your password and a saved recovery code
at sign-in. A recovery code does not disable the factor by itself. After signing
in, replace the authenticator deliberately or regenerate recovery codes as
appropriate. Keep the one-time replacement codes before leaving their display.

If all factor and recovery material is lost, an operator with access to the server
database can reset one administrator's factor locally. This is a privileged OS
recovery path, not an unauthenticated HTTP endpoint. Stop the backend first to
avoid already admitted administrative work continuing during the reset. Use the
same binary version, database and OS account as the installation:

```sh
DB_PATH=/actual/path/to/server.db ./server admin-factor-reset --username admin --confirm
```

For the supplied Compose installation, using the same project/files/environment:

```sh
docker compose stop backend
docker compose run --rm --no-deps backend admin-factor-reset --username admin --confirm
docker compose up -d backend
```

Replace `admin` with the stored administrator username, including its exact case.
The command requires an
existing supported database and will not create or migrate one. It invalidates
that account's factor, recovery codes, pending enrollment, sessions and pairing
grants. It leaves the password, role, disabled state, other accounts, transfer
data and transfer-pause policy unchanged. A disabled account remains disabled.
After restart, sign in with the existing password and enroll a new authenticator.
This command is not a password reset. Anyone who can modify the server database
already has administrative power; restrict OS access accordingly.

## Credential storage and backups

The database must retain the TOTP secret to verify codes. Unlike passwords and
recovery codes, that secret cannot simply be stored as a one-way hash. Treat the
database, WAL/SHM files and backups as credential material. Do not publish them,
include them in a static web root, or copy them into support logs. Server startup
creates/restricts database files to owner-only access; new database directories
use owner-only permissions. Existing parent-directory permissions are not changed
automatically. Verify volume ownership and protect backups separately.

Restoring an older backup can restore old sessions, factor secrets and recovery
codes that were subsequently revoked or consumed. Restore with the service
stopped, review security state, and invalidate/re-enroll credentials before
reopening a compromised installation. A backup is not a revocation history that
survives rollback. Server backups still do not contain owners' receive private
keys or standalone file decryption keys.

## API and verification boundary

All paths below are relative to `/api/v1`. Security endpoints are administrator
only, use existing cookie/origin protections and send `Cache-Control: no-store`.

| Endpoint                                  | Purpose                                                                      |
| ----------------------------------------- | ---------------------------------------------------------------------------- |
| `GET /admin/security`                     | Enabled state, remaining recovery-code count and recent-proof expiry.        |
| `POST /admin/security/enrollment`         | Start a session-bound pending setup.                                         |
| `DELETE /admin/security/enrollment`       | Cancel your own pending setup.                                               |
| `POST /admin/security/enrollment/confirm` | Confirm `{code}` and return one-time recovery codes.                         |
| `POST /admin/security/reauth`             | Verify `{password, code? , recovery_code?}` and renew recent authentication. |
| `DELETE /admin/security/factor`           | Remove factor/recovery material and end sessions.                            |
| `POST /admin/security/recovery-codes`     | Replace recovery codes and end sessions.                                     |

`POST /auth/login` accepts optional `code` or `recovery_code`; do not submit both.
After the password succeeds, missing/invalid factor responses use
`administrator_factor_required` or `administrator_factor_invalid`. No login token
or cookie is issued before complete verification. Stale administrative mutations
return `403 recent_authentication_required`; bounded verification cooldowns return
`429 administrator_authentication_locked` with retry information.

The wider security implementation includes bounded
[security activity](security-activity.md) and metadata retention. Deployment and
native verification remain open. This authentication checkpoint is not a claim
that every security requirement is complete.

Backend verification runs with `go test -race ./...` from `backend/`. This includes
TOTP vectors, replay/recovery races, persisted cooldowns, enrollment binding,
revoked-session mutation races, database permissions and local recovery tests.
Web parsing/request tests use `npm test`; the mocked UI cases are in
`tests/browser/admin-security.spec.ts`.

Run the real browser lifecycle separately against the disposable harness, from
`web/`, using available local test ports:

```sh
task_state_dir=$(mktemp -d /tmp/psst-admin-security-XXXXXX)
trap 'rm -rf "$task_state_dir"' EXIT
PSST_TEST_STATE_FILE="$task_state_dir/state.json" \
PSST_TEST_BACKEND_PORT=18789 \
PSST_TEST_BACKEND_URL=http://127.0.0.1:18789 \
npm run test:browser -- tests/browser/admin-security-runtime.spec.ts
```

These opt-in tests age sessions only in the harness-created temporary database;
they do not change production authentication timeouts or add a production bypass.
It is skipped before authentication fixtures when `PSST_TEST_STATE_FILE` is unset.
Keep this lifecycle separate from other real-auth suites to avoid exhausting the
deliberately shared login limiter. The test makes paced, explicit UI submissions;
production does not automatically retry authentication or protected mutations.
The same file verifies that an expired worker-session proof rejects account
creation, then that the next test setup explicitly reauthenticates before its
administrative mutation. The fixture never replays a rejected mutation.

The Chromium lifecycle and rendered QR fallback decoding passed. Physical
authenticator scanning, non-Chromium browsers and native iOS administrator-login
rejection remain explicit device/browser validation work; no such pass is implied
by the portable tests.
