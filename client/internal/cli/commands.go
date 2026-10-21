package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/endorses/psst.zip/client"
	"github.com/endorses/psst.zip/client/internal/state"
	"github.com/endorses/psst.zip/client/protocol"
)

const help = `psst — encrypted file sharing with an operator-selected psst.zip server
Project: https://psst.zip

  auth login --server ORIGIN --username NAME [--password-stdin]
  auth pair --json-file FILE|-       redeem a private web pairing document
  auth status | auth logout
  send FILE... [--expires 24h] [--max-downloads N] [--qr | --json]
  send --resume OPERATION_ID         resume persisted encrypted uploads
  fetch --link-stdin --output-dir DIR
  upload FILE... --link-file FILE|-  submit files to a Receive invitation
  upload --resume OPERATION_ID --server ORIGIN
  receive create [--expires 24h] [--max-files N] [--qr | --json]
  receive list | receive show ID [--qr] | receive revoke ID
  receive fetch ID --output-dir DIR
  transfers list | transfers show ID [--qr] | transfers revoke ID
  qr --json-file FILE|- [--plain]    offline QR from saved CLI Send/Receive JSON
  vault backup --output FILE --backup-passphrase-fd N
  vault restore --input FILE --backup-passphrase-fd N
  --version | help

Common flags (before or after the command):
  --json --non-interactive --timeout 10m --profile NAME --state-dir DIR
  --vault-mode keyring|passphrase --vault-passphrase-fd N
  --server ORIGIN (login or guest upload resume; never overrides an account)

Links may be supplied using --link-stdin or --link-file FILE. Credentials never
belong in command arguments. Headless jobs must explicitly configure a passphrase
vault and private descriptors (N >= 3). Login uses a regular account, not admin.
Share JSON and QR codes contain the same capabilities as their complete links.
Send keys and Receive private keys stay encrypted locally. Pairing/login does not
recover private keys from another device. Sessions currently expire after 30 days.
See docs/cli.md for schemas, exit codes, backup/recovery and automation examples.
`

type profile struct {
	Origin  string            `json:"origin"`
	Owner   string            `json:"owner"`
	Session *protocol.Session `json:"session,omitempty"`
}

type scopedStore struct {
	store  *state.Store
	prefix string
}

func (s scopedStore) Get(k string, v any) error {
	e := s.store.Get(s.prefix+k, v)
	if errors.Is(e, state.ErrNotFound) {
		return client.ErrNotFound
	}
	return e
}
func (s scopedStore) Put(k string, v any) error { return s.store.Put(s.prefix+k, v) }
func (s scopedStore) Delete(k string) error     { return s.store.Delete(s.prefix + k) }
func (s scopedStore) Keys(prefix string) ([]string, error) {
	keys, e := s.store.Keys(s.prefix + prefix)
	for i := range keys {
		keys[i] = strings.TrimPrefix(keys[i], s.prefix)
	}
	return keys, e
}

func scope(v *state.Store, origin, owner string) (scopedStore, error) {
	p, e := state.ScopeKey(origin, owner, "records/")
	return scopedStore{store: v, prefix: p}, e
}

func (a *App) execute(ctx context.Context, o options, args []string) (any, error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" || args[len(args)-1] == "--help" || args[len(args)-1] == "-h" {
		if o.json {
			return map[string]any{"help": help}, nil
		}
		_, e := io.WriteString(a.Out, help)
		return nil, e
	}
	if args[0] == "--version" || args[0] == "version" {
		if len(args) != 1 {
			return nil, usage("version takes no operands.")
		}
		return map[string]string{"product": "psst.zip", "version": a.Version, "revision": a.Revision}, nil
	}
	if args[0] == "qr" {
		return a.qrCommand(ctx, o, args[1:])
	}
	// Parse and reject invalid commands before accessing private state.
	if !oneOf(args[0], "auth", "vault", "send", "fetch", "upload", "receive", "transfers") {
		return nil, usage("Unknown command; run psst help.")
	}
	return a.onlineCommand(ctx, o, args)
}

func oneOf(v string, values ...string) bool {
	for _, item := range values {
		if v == item {
			return true
		}
	}
	return false
}

func (a *App) qrCommand(ctx context.Context, o options, args []string) (any, error) {
	f := a.flags("qr")
	path := f.String("json-file", "", "saved CLI result file, or - for stdin")
	plain := f.Bool("plain", false, "plain full-block output")
	if e := parseFlags(f, args); e != nil {
		return nil, e
	}
	if *path == "" || f.NArg() != 0 || o.json {
		return nil, usage("Use psst qr --json-file FILE|- without --json.")
	}
	b, e := a.readInput(ctx, *path, maxSavedJSON)
	if e != nil {
		return nil, e
	}
	link, e := linkFromJSON(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	return nil, a.displayQR(link, *plain)
}

func (a *App) displayQR(link string, plain bool) error {
	art, e := qrArt(link, a.width(), plain)
	if e != nil {
		return e
	}
	if _, e = fmt.Fprintln(a.Out, link); e != nil {
		return e
	}
	_, e = io.WriteString(a.Out, art)
	return e
}

func (a *App) onlineCommand(ctx context.Context, o options, args []string) (any, error) {
	command := args[0]
	rest := args[1:]
	sub := ""
	if oneOf(command, "auth", "vault", "receive", "transfers") {
		if len(rest) == 0 {
			return nil, usage("This command requires a subcommand; run psst help.")
		}
		sub, rest = rest[0], rest[1:]
	}
	f := a.flags(command + " " + sub)
	var user, device, jsonFile, linkFile, resume, output, input, title, after *string
	var passwordInput, linkStdin, showQR, plain *bool
	var expires *time.Duration
	var maximum, limit, backupFD *int
	switch command {
	case "auth":
		switch sub {
		case "login":
			user = f.String("username", "", "regular account username")
			passwordInput = f.Bool("password-stdin", false, "read password from stdin")
			device = f.String("device-name", "psst CLI", "device session label")
		case "pair":
			jsonFile = f.String("json-file", "", "private pairing JSON file or -")
			device = f.String("device-name", "psst CLI", "device session label")
		case "status", "logout":
		default:
			return nil, usage("Unknown auth subcommand.")
		}
	case "vault":
		backupFD = fdFlag(f, "backup-passphrase-fd")
		if sub == "backup" {
			output = f.String("output", "", "new encrypted backup file")
		} else if sub == "restore" {
			input = f.String("input", "", "encrypted backup file")
		} else {
			return nil, usage("Use vault backup or vault restore.")
		}
	case "send":
		expires = f.Duration("expires", 0, "requested lifetime, 0 uses server default")
		maximum = f.Int("max-downloads", 0, "per-file attempt limit, 0 uses server default")
		title = f.String("title", "", "shared descriptive title")
		resume = f.String("resume", "", "retained operation ID")
		showQR = f.Bool("qr", false, "display the complete share link as a QR code")
		plain = f.Bool("plain", false, "plain QR blocks")
	case "fetch", "upload":
		linkFile = f.String("link-file", "", "complete link file or -")
		linkStdin = f.Bool("link-stdin", false, "read complete link from stdin")
		if command == "fetch" {
			output = f.String("output-dir", ".", "download directory")
		} else {
			resume = f.String("resume", "", "retained guest operation ID")
		}
	case "receive", "transfers":
		switch sub {
		case "create":
			if command != "receive" {
				return nil, usage("Use send FILE to create a transfer.")
			}
			expires = f.Duration("expires", 0, "requested lifetime")
			maximum = f.Int("max-files", 0, "cumulative file allocation limit")
			title = f.String("title", "", "shared descriptive title")
			showQR = f.Bool("qr", false, "display the invitation QR")
			plain = f.Bool("plain", false, "plain QR blocks")
		case "show":
			showQR = f.Bool("qr", false, "display the complete share link QR")
			plain = f.Bool("plain", false, "plain QR blocks")
		case "list":
			after = f.String("after", "", "next-page cursor")
			limit = f.Int("limit", 50, "page size, 1..100")
		case "fetch":
			if command != "receive" {
				return nil, usage("Use fetch --link-stdin for public downloads.")
			}
			output = f.String("output-dir", ".", "download directory")
		case "revoke":
		default:
			return nil, usage("Unknown resource subcommand.")
		}
	}
	if e := parseFlags(f, rest); e != nil {
		return nil, e
	}
	if showQR != nil && *showQR && o.json {
		return nil, usage("Choose either --qr or --json before creating a resource.")
	}
	if plain != nil && *plain && (showQR == nil || !*showQR) {
		return nil, usage("--plain requires --qr.")
	}
	if backupFD != nil && !fdValid(*backupFD) {
		return nil, usage("A private backup credential descriptor must be 3 or greater.")
	}
	if expires != nil {
		if _, e := parseSeconds(*expires); e != nil {
			return nil, e
		}
	}
	if maximum != nil && (*maximum < 0 || int64(*maximum) > 2147483647) {
		return nil, usage("A file/download limit must be a nonnegative supported integer.")
	}
	if limit != nil && (*limit < 1 || *limit > 100) {
		return nil, usage("Page limit must be between 1 and 100.")
	}
	needsID := oneOf(command, "receive", "transfers") && oneOf(sub, "show", "fetch", "revoke")
	if needsID && (f.NArg() != 1 || !protocol.ValidID(f.Arg(0))) {
		return nil, usage("This resource command requires exactly one valid resource ID.")
	}
	if !needsID && command != "send" && command != "upload" && f.NArg() != 0 {
		return nil, usage("Unexpected command operands.")
	}
	if command == "send" && (f.NArg() == 0) == (*resume == "") {
		return nil, usage("Choose input files or --resume OPERATION_ID.")
	}
	if resume != nil && *resume != "" && !protocol.ValidID(*resume) {
		return nil, usage("A resume operation ID must be a valid UUID.")
	}
	if command == "upload" && *resume != "" && (f.NArg() > 0 || *linkFile != "" || *linkStdin) {
		return nil, usage("Guest resume uses its operation ID, not new files or a new link.")
	}
	if command == "upload" && *resume == "" && f.NArg() == 0 {
		return nil, usage("Select files to upload.")
	}
	if command == "auth" && sub == "login" && (*user == "" || o.server == "") {
		return nil, usage("Login requires --server ORIGIN and --username NAME.")
	}
	if command == "auth" && sub == "pair" && *jsonFile == "" {
		return nil, usage("Pairing requires --json-file FILE|-.")
	}
	var parsed protocol.Link
	if linkFile != nil && (resume == nil || *resume == "") {
		if (*linkFile != "") == *linkStdin {
			return nil, usage("Choose exactly one --link-file FILE or --link-stdin.")
		}
		path := *linkFile
		if *linkStdin {
			path = "-"
		}
		b, e := a.readInput(ctx, path, protocol.MaxLinkBytes)
		if e != nil {
			return nil, e
		}
		parsed, e = protocol.ParseLink(strings.TrimSpace(string(b)))
		if e != nil {
			return nil, usage("Input is not a complete supported psst.zip share link.")
		}
		if command == "fetch" && parsed.Kind != "send" || command == "upload" && parsed.Kind != "receive" {
			return nil, usage("This share link belongs to a different workflow.")
		}
	}
	var inputs []client.InputFile
	if (command == "send" || command == "upload") && f.NArg() > 0 {
		var e error
		inputs, e = client.FileInputs(f.Args())
		if e != nil {
			return nil, usage("Input files must be readable regular files with safe, distinct filenames.")
		}
	}
	v, e := a.openVault(ctx, o)
	if e != nil {
		return nil, e
	}
	defer v.Close()
	if command == "vault" {
		return a.vaultCommand(ctx, o, v, sub, output, input, *backupFD)
	}
	if command == "auth" {
		return a.authCommand(ctx, o, v, sub, user, passwordInput, device, jsonFile)
	}
	var c *client.Client
	var store scopedStore
	if command == "fetch" || command == "upload" {
		origin := parsed.Origin
		if resume != nil && *resume != "" {
			origin = o.server
		}
		if origin == "" {
			return nil, usage("Guest upload resume requires --server ORIGIN.")
		}
		c, e = client.New(origin, a.HTTP, nil)
		if e != nil {
			return nil, usage("Invalid server origin.")
		}
		store, e = scope(v, c.Origin(), "guest")
	} else {
		c, store, e = a.accountClient(ctx, o, v)
	}
	if e != nil {
		return nil, e
	}
	progress := a.progress(o)
	var data any
	switch command {
	case "send":
		if *resume != "" {
			data, e = c.ResumeSend(ctx, *resume, store, progress)
		} else {
			seconds, _ := parseSeconds(*expires)
			data, e = c.Send(ctx, inputs, client.SendOptions{Title: optionalTitle(*title), ExpiresInSeconds: seconds, MaxDownloads: *maximum, SpoolDir: spoolDir(o), Progress: progress}, store)
		}
	case "fetch":
		data, e = c.Fetch(ctx, parsed, *output, store)
	case "upload":
		if *resume != "" {
			data, e = c.ResumeUpload(ctx, *resume, store, progress)
		} else {
			data, e = c.UploadWithOptions(ctx, parsed, inputs, client.SendOptions{SpoolDir: spoolDir(o), Progress: progress}, store)
		}
	case "receive", "transfers":
		kind := "send"
		if command == "receive" {
			kind = "receive"
		}
		switch sub {
		case "create":
			seconds, _ := parseSeconds(*expires)
			data, e = c.CreateReceive(ctx, client.ReceiveOptions{Title: optionalTitle(*title), ExpiresInSeconds: seconds, MaxFiles: *maximum}, store)
		case "list":
			k := "transfer"
			if kind == "receive" {
				k = "slot"
			}
			data, e = c.ListResources(ctx, k, *after, *limit)
		case "show":
			data, e = c.Show(ctx, kind, strings.ToLower(f.Arg(0)), store)
		case "fetch":
			data, e = c.FetchInbox(ctx, strings.ToLower(f.Arg(0)), *output, store)
		case "revoke":
			id := strings.ToLower(f.Arg(0))
			if kind == "receive" {
				e = c.RevokeReceive(ctx, id)
			} else {
				e = c.RevokeTransfer(ctx, id)
			}
			data = map[string]any{"id": id, "kind": kind, "revoked": e == nil}
		}
	}
	if e != nil {
		return nil, e
	}
	if showQR != nil && *showQR {
		s, ok := data.(client.Share)
		if !ok || s.Link == "" {
			return nil, usage("This device has no complete share link; login does not recover its missing private key.")
		}
		return nil, a.displayQR(s.Link, *plain)
	}
	return data, nil
}

func optionalTitle(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func spoolDir(o options) string {
	if o.dir != "" {
		return filepath.Join(o.dir, "ciphertext")
	}
	base, e := os.UserConfigDir()
	if e != nil {
		return ""
	}
	return filepath.Join(base, "psst.zip", "cli", "ciphertext")
}

func (a *App) progress(o options) client.Progress {
	if o.json || o.noninteractive {
		return nil
	}
	var last time.Time
	return func(done, total int64) {
		if done != total && time.Since(last) < time.Second {
			return
		}
		last = time.Now()
		_, _ = fmt.Fprintf(a.Err, "Uploaded %d/%d encrypted bytes\n", done, total)
	}
}
