package cli

import (
	"io"
	"strings"
	"text/tabwriter"
)

func helpText() string {
	var out strings.Builder
	w := tabwriter.NewWriter(&out, 34, 4, 2, ' ', 0)
	_, _ = io.WriteString(w, `psst — encrypted file sharing through a psst.zip server or directly on a trusted LAN
Project: https://psst.zip

Usage:
  psst COMMAND [options]

Account:
  auth login [options]	Sign in or renew a saved session
    [--server ORIGIN --username NAME] [--password-stdin]
  auth pair --json-file FILE|-	Redeem a private web pairing document
  auth status	Show account and session details
  auth logout	Sign out; retain local encryption keys

Send links:
  send FILE... [options]	Encrypt and upload files to share
    [--expires DURATION] [--max-downloads N] [--qr | --json]
  send --resume OPERATION_ID	Resume a retained encrypted upload
  transfers list	List Send links
  transfers show ID [--qr]	Show a Send link and optional QR code
  transfers revoke ID	Revoke a Send link

Receive links:
  receive create [options]	Create a link for others to upload files
    [--expires DURATION] [--max-files N] [--qr | --json]
  receive list	List Receive links
  receive show ID [--qr]	Show a Receive link and optional QR code
  receive fetch ID [options]	Download and decrypt received files
    --output-dir DIR
  receive revoke ID	Revoke a Receive link

Download and guest upload:
  fetch [options]	Download and decrypt files from a Send link
    --link-stdin --output-dir DIR
  upload FILE... [options]	Submit files to a Receive link
    --link-file FILE|-
  upload --resume OPERATION_ID	Resume a retained guest upload
    --server ORIGIN

Temporary trusted-LAN sharing (no account):
  serve send FILE... [options]	Serve a private encrypted snapshot
  serve receive --output-dir DIR [options]	Collect encrypted browser uploads locally
    [--listen PRIVATE_IP:0] [--expires 1h] [--max-bytes 1GiB] [--qr | --json]
    [--max-downloads 1 (send)] [--max-files 100 (receive)] [--max-streams 2]
    [--tls-cert CERT --tls-key KEY]
  serve cleanup --session-dir DIR	Remove one stopped session’s ciphertext staging

QR codes:
  qr --json-file FILE|- [--plain]	Display a QR code from saved Send/Receive JSON

Local keys and backups:
  vault backup [options]	Create an encrypted backup
    --output FILE --backup-passphrase-fd N
  vault restore [options]	Restore an encrypted backup
    --input FILE --backup-passphrase-fd N

Common flags (before or after the command):
  --json	Write a versioned JSON result
  --non-interactive	Never prompt for input or unlock a wallet
  --timeout DURATION	Whole-command deadline (10m; serve uses --expires unless explicit)
  --profile NAME	Local account profile (default: default)
  --state-dir DIR	Override the private local state directory
  --vault-mode MODE	Storage: auto (default), keyring or passphrase
  --vault-passphrase-fd N	Read passphrase from a private descriptor
  --server ORIGIN	Server for login or guest upload resume

Other:
  help, --help, -h	Show this help
  --version	Show version and revision

Notes:
  LAN links need this process running and a reachable private address; default
  port 0 selects an available port. HTTP assumes a trusted LAN: encryption does
  not authenticate the browser page. TLS is optional with a trusted certificate.
  Login uses a regular account, not admin. First login requires --server and
  --username; later logins reuse saved values. Sessions expire after 30 days.
  Storage selection is remembered; new vaults use the desktop wallet. Passphrase
  vaults need private descriptors (N >= 3) for JSON/headless commands.
  Supply links with --link-stdin or --link-file FILE. Credentials never belong in
  command arguments. --server never overrides a saved account's server.
  Share JSON and QR codes grant the same access as their complete links. Hosted
  keys use encrypted local storage; LAN session keys stay only in memory.
  Login/pairing does not recover keys from another device.

See docs/cli.md for schemas, exit codes, backup/recovery and automation examples.
`)
	_ = w.Flush() // strings.Builder writes cannot fail.
	return out.String()
}
