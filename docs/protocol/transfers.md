# Transfer behavior and API

Send transfers encrypt files before upload. A Send link carries its decryption key
in the URL fragment; recipients decrypt locally. Receive invitations carry a
public key, and each sender wraps a fresh transfer key to it. The invitation alone
cannot decrypt submissions. See the [receive cryptographic design](../security/receive-crypto-design.md)
for key ownership and HPKE, and [chunked files](chunked-files.md) for framing and
interoperability vectors.

## File sizes and encryption

An administrator can change **Settings → Server upload limit** in the web UI. The
setting is stored on the server and defaults to 25 MiB per file. The operator's
`MAX_FILE_SIZE` environment setting defines its ceiling (5 GiB by default, at most
1 TiB). Web, Android, iOS and the iOS share extension read the server's current
limit before uploading. New upload reservations are rejected above that limit;
previous reservations and existing downloads remain available after it is lowered.

Files are encrypted and decrypted sequentially in 4 MiB authenticated chunks,
with bounded working buffers and native temporary-file publication. The complete
file's size is subject to the administrator's limit; chunk size is internal.
Large web saves use File System Access or OPFS on supported secure origins.
Browsers without those APIs, including plain LAN HTTP pages, retain a 25 MiB
small-file save fallback and explain how to use a capable HTTPS browser or the
native app for larger files. Web ZIP downloads remain limited to 25 MiB total;
individual large-file saves do not require ZIP aggregation.

See [the chunked-v1 file format](chunked-files.md) for authenticated
framing and deterministic interoperability vectors. This development format
requires updated clients; there is no old file-ciphertext fallback. Manifests stay
encrypted using the transfer key and a nonce/ciphertext/GCM-tag envelope, and now
include each file's encoding, chunk size and unique encryption context.

## Receive uploads and events

Transfer and slot creation return `id`. Uploaders create a child transfer with
`POST /api/v1/slots/{id}/transfers`, upload its files and manifest through the
transfer endpoints, then complete it. Receivers download completed child
transfers through authenticated owner-only endpoints; owner-only
`transfer_complete` SSE notifications include `transfer_id`. Public
`GET /api/v1/slots/{id}/availability` returns only submission policy. An uploader
can query its own minimal `GET /api/v1/transfers/{id}/upload-status` with its
resource capability to resolve a lost completion response without reading inbox contents.

Inbox [event streams](../security/inbox-events.md) share one lifecycle poller
per active inbox, have bounded queues and connection lifetimes, and disconnect
slow or no-longer-authorized subscribers. Clients refresh authorized inbox
metadata after missed notifications; events are not a durable replay log.

## Link limits and revocation

Send creation offers an optional **Maximum downloads per file**. Receive creation
offers an optional **Maximum files received**. Zero/unset disables only that
creator-selected cap; server restrictions still apply. Limits are checked against
the created server resource before a shareable link is shown. Receive
`max_files` counts allocated files cumulatively, including abandoned uploads.
Deletion never replenishes the allowance. Already allocated uploads can finish
when it reaches zero. Owner History distinguishes completed files from used allowances.

`max_downloads` limits GET attempts for each file independently, so every file in
a multi-file transfer remains retrievable. Interrupted downloads consume an
attempt. Transfer `download_count` counts complete sets of file requests (the
minimum per-file request count), not completed downloads. Once every file exhausts
its allowance, cleanup removes the encrypted file blobs; transfer metadata and the
encrypted manifest remain until the original expiry so download confirmations can
still be recorded and read.
Expired resources are rejected immediately and their stored data is removed periodically.
Choosing **Revoke and delete** in mobile history revokes the transfer on its original server before
removing the local record. Deleting a receive entry revokes its upload slot and
all child transfers. A failed request keeps the entry so deletion can be retried.
Files already saved by a recipient are unaffected. Explicit revocation cancels
active server streams and rejects subsequent requests. Routine exhausted-payload
cleanup waits for the final permitted download to finish before removing its bytes.

New transfer, slot, and slot-upload creation responses include a private
`delete_token`. Store it separately from the encryption key and never include it
in a shared link. Revocation uses `DELETE /api/v1/transfers/{id}` or
`DELETE /api/v1/slots/{id}` with `Authorization: Bearer <delete_token>`. Successful
deletion returns `204`; `404` means the resource is already gone. Only a hash of
the deletion token is stored on the server, and GET responses never expose it.
If payload removal fails, revocation remains in force and cleanup retries it.

Transfers created before deletion tokens were introduced cannot establish owner
identity. Operators may set `ALLOW_LEGACY_DELETION=true` to allow deletion of
those older resources by ID; anyone with an old link can then revoke it. This
compatibility option defaults to `false` and never bypasses token checks on new
resources. Older client versions that discard creation tokens cannot later revoke
their protected transfers.

## Download confirmations

After downloading and successfully decrypting every file, a receiver sends
`POST /api/v1/transfers/{id}/downloaded` with an empty body. This idempotent endpoint
returns `204`; transfer metadata exposes the first confirmation as `downloaded_at`
(otherwise `null`). Partial downloads, manifest reads, and completed HTTP responses
alone do not set it. Confirmations report client success; they do not independently
prove the recipient saved or opened the files. No encryption key or plaintext is
included in the confirmation.

Sent history distinguishes **Ready to download**, **Download started**
(all files requested), and **Downloaded** (receiver confirmation). Native receivers
confirm after saving their decrypted files. The browser confirms after handing the
decrypted files or ZIP to its download mechanism; it cannot verify the subsequent
filesystem save. Failed confirmations can be retried without downloading again:
the web page provides a retry button, Android retries from saved history, and iOS
keeps a pending queue and retries when the app becomes active. Older clients do not
send confirmations, so their downloads may remain **Download started**.
