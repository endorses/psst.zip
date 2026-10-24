# Using psst.zip

Use a regular account to create Send and Receive links. Recipients can open a
Send link or submit files to a Receive invitation without an account. Administrators create accounts; there is no public registration. Each
installation has its own server address and accounts.

## Contents

- [Receive invitations](#receive-invitations)
- [Everyday use](#everyday-use)
- [Accounts and connected devices](#accounts-and-connected-devices)
- [Languages](#languages)
- [QR scanning](#qr-scanning)
- [Security and privacy](#security-and-privacy)

## Receive invitations

Receive links work in reverse: choose **Receive**, create a link, and share its QR
or use **Copy link** / **Share**. Anyone with that link can send files without an
account, including people elsewhere who can reach your server. Return to the
original link in History to see arrivals and choose **Save files**. New receive
invitations contain only a public key: `/u/{id}#v2.{public-key}`. The private key
stays on the creating device. Each sender encrypts its own fresh file key to that
public key using HPKE; knowing the invitation cannot decrypt other submissions.
Inbox listing, events, child downloads and delivery receipts require the owning
regular account. Pairing another device does not copy its private keys.

Older receive links no longer accept new submissions. Their authorized owner can
save existing uploads and create a replacement invitation. Update the backend,
web assets and apps together; an old app is not a compatible private-inbox client.
See [the receive protocol and validation record](security/receive-crypto-design.md).

## Everyday use

**Send**, **Receive**, and **History** are the main account destinations. Account,
connected-device, and administrator controls live under Settings. A receive item
in History opens its existing link; retrying a save keeps that link and preserves
files already saved. Owned-link history stays scoped to the current account and server.

The mobile apps also offer **Scan QR code**, available before signing in. Scan a
download QR from either app or the web UI to immediately download, decrypt and
save its files. Alternatively, paste the complete link and choose **Receive
files**. A scanned server does not replace your configured server or receive its
login credentials. Upload-link QRs open a deliberate file-selection/send flow;
account-pairing QRs ask before connecting the account.

Scanned downloads save to **Downloads/psst.zip** on Android or the app's
**Received/Guest** folder in iOS Files. Android 8–9 requests storage permission;
later Android versions use scoped Downloads storage. **History → Downloaded**
shows saved downloads alongside the Sent and Receive links filters and remains
available after sign-out. Local downloads retain their own storage and actions. Open and Share use the saved copies, including offline or after the
link expires. Removing a local entry keeps its files and does not revoke the
sender's link. Files never open automatically.

Keep the app in the foreground while receiving. Retry reuses files already
saved, and delivery confirmation follows the final durable save; receipt retries
do not download the files again. Server expiry, revocation or download limits
can prevent retrying an unfinished file. Direct receiving supports up to 100
files, with the upload limit chosen by the server administrator (25 MiB by default). See the [native verification workflow](testing/scan-to-receive.md)
for disposable interoperability fixtures and platform checks.

Empty receive links show the QR and **Copy link** / **Share** actions first.
After files arrive, the inbox puts received files and saving first; **Show QR / Share link**
keeps the invitation available. Expand details to inspect the complete URL.
Missing encryption keys on another device are
explained on the corresponding history entry. Revoking a link prevents further
requests, but does not remove copies someone already saved.

Uploads show preparation separately from transferred-byte progress. Web uploads
remain visible when moving between the primary destinations; mobile navigation
that would stop an active upload requires confirmation. The apps do not promise
background transfers after the OS or share extension stops them. Failed cleanup
remains actionable so a partially created resource can still be revoked.

The web header offers **System**, **Light**, and **Dark** appearance on every page,
including login and public links. The choice is saved in that browser; System
follows the browser/device preference. Android and iOS offer the same choices
under **Settings → Appearance**, saved on that device independently of the
server account. System is the default; iOS also applies the choice to its share
extension.
The interface uses deep teal primary
actions, neutral surfaces, and dark-on-white QR codes in both themes. Statuses
also use text, including the distinction between files received by the server,
files saved locally, and a recipient-confirmed download.

[Recipient safeguards](security/recipient-safeguards.md) document download
preflight, filename handling, cancellation and storage boundaries. Browser
storage quota checks do not guarantee free space in the destination folder;
downloaded files stay inert until the recipient chooses to open them.

See [transfer behavior and API](protocol/transfers.md) for file sizes, link limits,
revocation and download-confirmation semantics.

## Accounts and connected devices

Administrators land on **Overview** and manage **Users**, **Traffic** and **Server
settings**. Admin accounts are administration-only: they cannot create transfers,
receive links, use the web scanner, or pair a mobile app. Create a separate regular
account for personal file transfers. Regular users land on **Send** and manage
their own links and History. Administrators can inspect operational metadata and
revoke server resources, but cannot access encryption keys or plaintext filenames.

New regular accounts use a temporary password and must replace it on first login.
An administrator reset reinstates that requirement. Existing accounts migrate
without a forced reset; administrators are exempt from mandatory first change.
Password changes require the current password and a different replacement of
12–72 UTF-8 bytes, with confirmation in the web and mobile forms. Password changes,
resets and disabling an account revoke its sessions and unused pairing grants;
sign in again after changing the password. Restricted accounts can only inspect
their sign-in state, change the password and sign out until replacement succeeds.
The last enabled administrator cannot be disabled.

Administrators can enroll an authenticator in **Account**. Password plus a fresh
authenticator code or one-use recovery code is then required for web sign-in.
Sensitive administrative changes require authentication within five minutes;
an expired proof prompts verification and an explicit resubmission. Existing
administrators without a factor see a setup warning without being locked out
during upgrade. Factor changes revoke existing sessions. See
[administrator authentication and local recovery](security/administrator-authentication.md)
for enrollment, recovery codes, database protection and the explicit
`server admin-factor-reset --username NAME --confirm` operator command.

[Authentication metadata limits](security/metadata-retention.md) cap accounts,
active sessions and pairing records. Successful sign-in at the session limit
replaces an older session; legacy excess records are reconciled in bounded batches.

Administrators can review [security activity](security/security-activity.md)
in the web workspace. The bounded history separates administrative changes,
link revocations and authentication-failure summaries, without collecting
credentials, filenames or encryption keys. Recording gaps are surfaced explicitly.

The administrator [resource manager](security/resource-management.md) supports
exact ID lookup, owner/type filters, current storage counters and related security
activity. Revocation denies access immediately; persistent cleanup status shows
pending work and failures until server-file removal is confirmed.

[Stored-file recovery](security/storage-recovery.md) repairs unpublished
upload offsets and blocks inconsistent published payloads. The resource manager
shows the progress and failures of bounded database-file checks.
Separate counter checks rebuild derived storage summaries from canonical rows
without resetting historical link allowances; missing summaries do not hide
resources from administrators.
Orphan file checks inventory the payload volume and remove unreferenced regular
files after a one-hour observation period and fresh safety checks. Unexpected
entries and incomplete scans remain visible. Run one server process per storage
directory and stop it before restoring or moving that storage. Follow the
[cold backup and isolated restore runbook](security/backup-restore.md) to
preserve the complete database/payload snapshot and review rolled-back security
state before reopening public access.

An optional [abuse contact](security/abuse-contact.md) lets administrators
publish a contact email. Web and mobile clients prepare a local report reference
without encryption keys or file contents; the server does not send or store reports.

For incidents, administrators can persistently pause payload transfers or shut
down an account and revoke all its links. These are separate from disabling
future sign-in. See [incident response and local recovery](security/incident-response.md)
for scope, cleanup behavior and the `server pause`, `resume` and `incident-status`
commands. Web mutations require recent administrator authentication; local
commands use OS/database access as their recovery authorization boundary.

The API enforces these rules for existing cookie and app sessions, including
upload continuation. Older clients may need an update to display the dedicated
password/role messages. Existing admin-owned public links remain valid until
expiry or revocation; ownership and keys are not moved. Public-link capabilities
remain independent of an ambient admin or restricted session.

The website uses an HttpOnly session cookie. Android stores its device session
encrypted with Android Keystore; iOS uses Keychain, with a shared access group for
the main app and share extension. Passwords are not saved in either app. Connected
device sessions can be revoked from the website. A pairing QR is a short-lived,
single-use login grant: display it only when connecting a device. It contains no
password or browser session token, but whoever redeems it first gets access to
that account. The website shows **Phone connected** after redemption. Canceling
an unused code or replacing it invalidates that grant; if a phone connected first,
revoke its session from **Connected devices** instead.

History from the server includes resource metadata, never encryption keys.
The browser remembers links it created locally, scoped to the signed-in account.
It can revoke resources created on another device, but cannot recover their
encryption keys or reconstruct their complete share links.

Existing anonymous download links survive the migration. Old resources cannot
automatically be assigned to an account; admins can manage them and existing
private deletion tokens remain usable. Receive links created before accounts
must be recreated after signing in before they can accept further uploads;
their already-uploaded files remain downloadable. Older apps cannot create transfers once authentication is
required. Account login and pairing are available in the web UI and both mobile
apps. See the [iOS build and device validation requirements](../ios/README.md).

Both mobile apps keep new local history scoped to the signed-in account and server.
Signing out hides account history, and signing into a different account does not
expose the previous account's links or encryption keys. Admin accounts no longer
open personal mobile History; older records are preserved locally without being
reassigned. Native downloaded files remain available independently of server
login. Sent and receive-link entries support an optional shared title at creation
or through **Rename** in History. Explicit titles are readable by the server and
people using the link; they also appear on other signed-in devices. Private
filenames are never automatically published as titles. Renaming or clearing a
title leaves the URL and encryption keys unchanged. Existing local names remain
local fallbacks until explicitly saved as shared titles; downloaded records keep
a title snapshot for offline access. See [shared link titles](security/shared-link-titles.md).

On-device **All** History merges account records and downloaded files into one
chronological list with one **Load more** control. Type filters query the matching
records rather than only the current page. Single-page lists hide pagination.
Once every file in a limited send link has used its download attempts, the link
closes automatically. Already admitted final downloads can finish; an interrupted
attempt still counts and does not imply successful saving.

## Languages

The web UI, Android app, iOS app and iOS share extension support English and
German. Choose System, English or Deutsch in the language selector. The choice
stays on your device and does not change your server or account.

See [translation contributions](localization/contributing.md) for resource
locations, terminology and verification guidance.

## QR scanning

On mobile, opening Scan QR code starts its embedded camera after permission is
granted. A rear camera is selected automatically, with another camera as fallback
when needed; mobile scanning has no camera switcher. The torch appears only when
supported. Camera failures offer retry, and leaving the screen releases capture.
Paste link and Choose QR image remain available without camera access.
Scan no longer contains a separate history screen: all records are available from
History, with All, Sent, Receive links and Downloaded filters. Settings separates
appearance and account information from the dedicated account-editing flow.

The web scanner is available only to fully enabled regular accounts. It decodes locally, asks
before opening a transfer on another server, and never redeems mobile pairing
codes. Webcam access requires HTTPS or the localhost development exception;
signed-in paste/image decoding remains available on LAN HTTP. Public transfer
links continue working without login.

The shushing symbol appears in app icons, favicons and generated QR codes. QR
rendering uses high error correction and preserves its outer quiet zone. Editable
vector masters, font license and reproducible export instructions are in
[assets/brand](../assets/brand/README.md).

## Security and privacy

Accounts authorize creation and management; generated links intentionally grant
access to their specific transfer or receive slot. Direct file uploads require
a fully enabled regular owner session; admin sessions cannot upload. Public slot uploaders receive a private capability
for uploading only their newly created child transfer. Receive slots enforce
expiry, transfer-count, and aggregate storage limits on the server.

Browser sessions use same-origin mutation checks and HttpOnly cookies, with
Secure enabled unless development HTTP is explicitly permitted. Passwords are
hashed and session/pairing secrets are stored as hashes. See the
[OWASP session guidance](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
and [password storage guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)
for the security controls informing this design.

Keep the backend on the private Docker network and expose Caddy's public origin.
`PUBLIC_URL` identifies that trusted HTTPS entry point; it does not add TLS to a
separately exposed backend port. Sessions expire after 30 days and pairing codes
after five minutes. Login and pairing redemption are rate limited. Login also
has normalized-account throttling and bounded password work. Behind a proxy,
configure explicit `TRUSTED_PROXIES` to resolve the actual client address; otherwise
clients intentionally share the proxy's bucket.

HTTP LAN mode encrypts file contents but does not authenticate delivery of the
web app itself. An active network attacker could replace its JavaScript and steal
keys or plaintext. Use HTTP only on a trusted network; use trusted HTTPS for
untrusted networks or Internet-facing deployments. Encryption also assumes the
client application itself is trustworthy.

Successful decryption does not verify the sender's identity or certify that a
file is safe. The service cannot inspect encrypted content. Operators still need
to follow their hosting provider's rules and respond to abuse; configure the
[abuse contact](security/abuse-contact.md) and use the
[incident controls](security/incident-response.md) when needed. Encryption
does not remove those responsibilities. Follow the
[dependency, image and release verification workflow](security/deployment.md#updates-and-vulnerability-review)
when updating a self-hosted instance.

- **File encryption**: AES-256-GCM. Send links share the symmetric decryption key. Receive invitations share only a public X25519 key; RFC 9180 HPKE wraps a separate symmetric key for each submission.
- **Key in URL fragment**: The `#key` portion of URLs is not sent to the server by browsers (per RFC 3986). The server only sees the transfer ID.
- **Encrypted storage**: The backend stores encrypted file contents and filenames, plus visible operational metadata. A compromised server delivering altered browser JavaScript can compromise browser-side secrecy; trusted HTTPS protects transport, not a malicious web application.
- **Resumable uploads**: The tus protocol supports retrying interrupted uploads within the client size limits. All data is encrypted before upload.
- **Automatic expiry**: Transfers expire after a configurable duration; exhausted download quotas remove encrypted payloads while retaining status metadata until expiry.
