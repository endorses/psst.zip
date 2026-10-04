# Abuse contact

An administrator can publish an optional contact email in **Server settings →
Abuse contact**. The default is blank, which disables the contact. Use an address
you are comfortable making public. Clearing the setting removes the contact from
newly refreshed clients; previously copied addresses or drafts cannot be recalled.

The web footer and native client help/report views offer **Report abuse** when the
resource's server publishes a valid contact. Reporting remains useful for an
expired or unavailable link: its opaque resource ID can identify retained server
metadata or security activity without access to the file contents.

The local report reference contains only the instance origin and, when available,
the resource type and UUID. Send links use `transfer`; receive links use `slot`.
It excludes link fragments, query strings, encryption keys, credentials,
filenames and file contents. The recipient can copy the contact and reference or
explicitly open a draft in their own mail app. Nothing is sent automatically.
If no mail app or clipboard is available, the contact and reference remain
visible for manual copying. A server-level report has no resource ID.

## Server behavior

`GET /api/v1/config` exposes `abuse_contact_email` as a string; the empty string
means unconfigured. Existing clients may ignore the new field. Native external
link lookups use the anonymous client for that link's exact origin; they do not
forward an account's credentials to another instance. The web reads its own
origin's public configuration only; scanning another server's link still requires
explicit navigation before its contact is loaded.

Administrator `GET` and `PATCH /api/v1/admin/abuse-contact` use `{ "email": "..." }`.
Mutation requires a current administrator session with recent authentication,
revalidated in the database transaction. The contact persists independently of
file-size or quota settings. A bounded security event records a change without
including the email address; failure to record an ordinary settings change rolls
back that change.

Accepted addresses are a single ordinary ASCII address, at most 254 characters,
with a local part of at most 64 characters. Supported local characters are letters,
digits, dot, underscore, plus, percent and hyphen; leading, trailing or repeated
dots are rejected. The domain contains at least two valid DNS labels. Display
names, multiple recipients, whitespace, header fields and URL syntax are not
accepted. Client validation also rejects malformed values from another server.
Mail drafts encode recipient, subject and body separately.

This is a contact-only deployment option. There is no report-submission endpoint,
mail relay, file attachment handler, report database or server fetch of a supplied
report URL. Existing request admission/rate limits and small control-body limits
apply to configuration access and updates. Report retention and mail filtering
belong to the operator's chosen email service; the application does not retain
submitted reports or add unbounded report storage.

## Operator response

Ask for the instance, resource type, resource ID and a description of the concern.
Do not ask for decryption fragments or copies of private files. Use the
[resource manager](resource-management.md) to inspect the reported ID, its owner
and retained activity, then revoke it or use [incident controls](incident-response.md)
when appropriate. A report is an unverified allegation: the reference identifies
a resource, not proof of its contents or sender identity. Do not automatically
follow links or open attachments in received reports.

Encryption does not establish that hosted material is safe or remove the need
for an operator to respond to misuse. This contact path supplies an operational
way to receive concerns; it does not certify uploaded files or promise that
abuse cannot occur.
