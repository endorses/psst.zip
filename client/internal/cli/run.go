// Package cli implements the psst command. The SDK remains independent of this
// presentation, credential-store, and process-exit layer.
package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/endorses/psst.zip/client"
	clientcrypto "github.com/endorses/psst.zip/client/internal/crypto"
	"github.com/endorses/psst.zip/client/internal/state"
	"golang.org/x/term"
)

// App makes command execution injectable without changing process-global streams,
// opening the user's keyring, or sending a request in offline QR tests.
type App struct {
	In            io.Reader
	Out           io.Writer
	Err           io.Writer
	HTTP          *http.Client
	Version       string
	Revision      string
	OpenState     func(state.Config) (*state.Store, error)
	TerminalWidth int
}

type options struct {
	json            bool
	noninteractive  bool
	profile         string
	server          string
	dir             string
	mode            string
	passFD          int
	timeout         time.Duration
	timeoutExplicit bool
}

func (a *App) defaults() {
	if a.In == nil {
		a.In = os.Stdin
	}
	if a.Out == nil {
		a.Out = os.Stdout
	}
	if a.Err == nil {
		a.Err = os.Stderr
	}
	if a.OpenState == nil {
		a.OpenState = state.Open
	}
	if a.Version == "" {
		a.Version = "dev"
	}
	if a.Revision == "" {
		a.Revision = "unknown"
	}
}

func commonFlags(f *flag.FlagSet, o *options) {
	f.BoolVar(&o.json, "json", false, "write one versioned JSON result")
	f.BoolVar(&o.noninteractive, "non-interactive", false, "never prompt")
	f.StringVar(&o.profile, "profile", "default", "local account profile")
	f.StringVar(&o.server, "server", "", "operator-selected server origin")
	f.StringVar(&o.dir, "state-dir", "", "private local state directory")
	f.StringVar(&o.mode, "vault-mode", "auto", "remembered storage mode; auto, keyring or passphrase")
	f.IntVar(&o.passFD, "vault-passphrase-fd", -1, "read vault passphrase from a private file descriptor")
	f.DurationVar(&o.timeout, "timeout", 10*time.Minute, "whole-command deadline")
}

// Global flags work before/after commands and file operands. Command-specific
// flags are subsequently parsed with the same interspersed-argument convention.
func parseCommon(args []string) (options, []string, error) {
	var o options
	f := flag.NewFlagSet("psst", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	commonFlags(f, &o)
	var global, rest []string
	for i := 0; i < len(args); i++ {
		v := args[i]
		if v == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		name := strings.TrimLeft(strings.SplitN(v, "=", 2)[0], "-")
		known := f.Lookup(name)
		if !strings.HasPrefix(v, "-") || known == nil {
			rest = append(rest, v)
			continue
		}
		if name == "timeout" {
			o.timeoutExplicit = true
		}
		global = append(global, v)
		boolean, ok := known.Value.(interface{ IsBoolFlag() bool })
		if !strings.Contains(v, "=") && !(ok && boolean.IsBoolFlag()) {
			if i+1 >= len(args) {
				return o, nil, usage("A global flag requires a value.")
			}
			i++
			global = append(global, args[i])
		}
	}
	if err := f.Parse(global); err != nil {
		return o, nil, usage("Invalid global flags; run psst help.")
	}
	if o.timeout <= 0 || o.profile == "" || len(o.profile) > 100 || strings.ContainsAny(o.profile, "/\\\r\n\x00") {
		return o, nil, usage("Use a positive timeout and a simple nonempty profile name.")
	}
	if !oneOf(o.mode, "auto", "keyring", "passphrase") || o.passFD >= 0 && o.passFD < 3 {
		return o, nil, usage("Select auto, keyring or passphrase vault mode; private descriptors must be 3 or greater.")
	}
	if o.mode == "keyring" && o.passFD >= 0 {
		return o, nil, usage("Use auto or passphrase mode with a private vault passphrase descriptor.")
	}
	return o, rest, nil
}

func parseFlags(f *flag.FlagSet, args []string) error {
	var flags, operands []string
	for i := 0; i < len(args); i++ {
		v := args[i]
		if v == "--" {
			operands = append(operands, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(v, "-") || v == "-" {
			operands = append(operands, v)
			continue
		}
		name := strings.TrimLeft(strings.SplitN(v, "=", 2)[0], "-")
		item := f.Lookup(name)
		if item == nil {
			return usage("Unknown command flag; run psst help.")
		}
		flags = append(flags, v)
		boolean, ok := item.Value.(interface{ IsBoolFlag() bool })
		if !strings.Contains(v, "=") && !(ok && boolean.IsBoolFlag()) {
			if i+1 >= len(args) {
				return usage("A command flag requires a value.")
			}
			i++
			flags = append(flags, args[i])
		}
	}
	if err := f.Parse(append(append(flags, "--"), operands...)); err != nil {
		return usage("Invalid command arguments; run psst help.")
	}
	return nil
}

func (a *App) Run(ctx context.Context, args []string) int {
	a.defaults()
	o, args, err := parseCommon(args)
	if err == nil && (o.timeoutExplicit || len(args) == 0 || args[0] != "serve") {
		ctx2, cancel := context.WithTimeout(ctx, o.timeout)
		defer cancel()
		ctx = ctx2
	}
	var data any
	if err == nil {
		data, err = a.execute(ctx, o, args)
	}
	if err != nil {
		f := classify(err)
		var serving *servingFailure
		if o.json && !errors.As(err, &serving) {
			_ = writeJSON(a.Out, nil, f)
		} else {
			_, _ = fmt.Fprintln(a.Err, "psst:", safeText(f.Message))
		}
		return f.exit
	}
	if data != nil {
		if o.json {
			err = writeJSON(a.Out, data, nil)
		} else {
			err = humanResult(a.Out, data)
		}
		if err != nil {
			return ExitStorage
		}
	}
	return ExitOK
}

func classify(err error) *Failure {
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	var operation *client.OperationError
	if errors.As(err, &operation) {
		f := classify(operation.Cause)
		f.OperationID = operation.OperationID
		f.Message += " Resume the retained operation using its operation_id."
		return f
	}
	var receipt *client.ReceiptError
	if errors.As(err, &receipt) {
		return &Failure{Code: "receipt_pending", Message: "Files were saved. Repeat fetch to retry the pending completion receipt without downloading them again.", exit: ExitTransport}
	}
	var api *client.APIError
	if errors.As(err, &api) {
		code, exit := "resource_unavailable", ExitUnavailable
		// Capacity can change after preflight. The backend also uses 403 for
		// Receive policy failures; renewing credentials cannot resolve them.
		policyForbidden := oneOf(api.Code, "receive_file_limit", "receive_batch_limit", "legacy_receive_disabled")
		if api.Status == 401 || api.Status == 403 && !policyForbidden {
			code, exit = "authentication_required", ExitAuth
		}
		return &Failure{Code: code, Message: api.Error(), ServerCode: api.Code, RetryAfterSeconds: int64(api.RetryAfter / time.Second), exit: exit}
	}
	var policy *client.PolicyError
	if errors.As(err, &policy) {
		return &Failure{Code: policy.Code, Message: policy.Message, exit: ExitUnavailable}
	}
	if errors.Is(err, clientcrypto.ErrInvalidData) {
		return &Failure{Code: "crypto_validation", Message: "Encrypted data or its authenticated context failed validation.", exit: ExitCrypto}
	}
	if errors.Is(err, client.ErrMissingKey) {
		return &Failure{Code: "missing_key", Message: "This device does not hold the inbox's private key. Login does not synchronize it; restore its encrypted vault backup.", exit: ExitStorage}
	}
	if errors.Is(err, client.ErrCheckpointInvalid) {
		return &Failure{Code: "checkpoint_invalid", Message: "A previously saved file was changed or removed. Choose a fresh output directory to download again if the server still permits it.", exit: ExitStorage}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &Failure{Code: "interrupted", Message: "The command was cancelled or its deadline expired; retained operations can be resumed.", exit: ExitTransport}
	}
	var transport *client.TransportError
	if errors.As(err, &transport) || errors.Is(err, client.ErrRedirect) {
		return &Failure{Code: "transport_failure", Message: "Could not connect to the configured server without a redirect; check its final origin and connection.", exit: ExitTransport}
	}
	if errors.Is(err, state.ErrUnavailable) {
		return storageFailure("OS credential storage is unavailable or locked. Start or unlock your desktop keyring, or rerun with --vault-mode passphrase to use an encrypted local vault.")
	}
	if errors.Is(err, state.ErrLocked) {
		return storageFailure("Another command holds this vault. Wait for it to finish; do not delete its lock file.")
	}
	if errors.Is(err, state.ErrUnlock) {
		return storageFailure("Could not unlock the encrypted vault or backup with the supplied private credential.")
	}
	if errors.Is(err, state.ErrPassphraseRequired) {
		return storageFailure("This vault uses a passphrase. Supply --vault-passphrase-fd for noninteractive commands.")
	}
	if errors.Is(err, state.ErrModeMismatch) {
		return storageFailure("This vault uses a different storage mode. Omit --vault-mode to reuse its saved setting; use a separate --state-dir for a new vault.")
	}
	if errors.Is(err, state.ErrCorrupt) || errors.Is(err, state.ErrConflict) {
		return storageFailure("Private state is invalid, unsupported, or conflicts with the backup; existing keys were retained.")
	}
	if errors.Is(err, client.ErrProtocol) {
		return &Failure{Code: "protocol_error", Message: "The server returned an unsupported or inconsistent protocol response.", exit: ExitUnavailable}
	}
	var path *os.PathError
	if errors.As(err, &path) {
		return storageFailure("A local file or private-state operation failed; check permissions, free space and the selected destination.")
	}
	// Never print an arbitrary provider, transport or server error: it may include
	// a request URL, ciphertext key, passphrase, or untrusted terminal content.
	return &Failure{Code: "operation_failed", Message: "The operation could not complete. Private state was retained where available.", exit: ExitUnavailable}
}

func (a *App) flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}

func (a *App) width() int {
	if a.TerminalWidth > 0 {
		return a.TerminalWidth
	}
	if f, ok := a.Out.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		w, _, err := term.GetSize(int(f.Fd()))
		if err == nil {
			return w
		}
	}
	return 0
}

func (a *App) readInput(ctx context.Context, path string, limit int64) ([]byte, error) {
	var r io.Reader = a.In
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, storageFailure("Could not open the requested input file.")
		}
		defer f.Close()
		r = f
	}
	b, err := readContext(ctx, r, limit+1)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || int64(len(b)) > limit {
		return nil, usage("Input is unreadable or exceeds the permitted size.")
	}
	return b, nil
}

func (a *App) secret(ctx context.Context, o options, fd int, prompt string) ([]byte, error) {
	if fd >= 3 {
		f := os.NewFile(uintptr(fd), "private-credential")
		if f == nil {
			return nil, usage("Cannot open the private credential descriptor.")
		}
		defer f.Close()
		b, err := readContext(ctx, f, 4097)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil || len(b) > 4096 {
			return nil, usage("Cannot read a bounded private credential.")
		}
		b = []byte(strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r"))
		if len(b) == 0 {
			return nil, usage("A private credential cannot be empty.")
		}
		return b, nil
	}
	if o.json || o.noninteractive {
		return nil, usage("A private credential descriptor is required in noninteractive/JSON mode.")
	}
	f, ok := a.In.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil, usage("A private credential descriptor is required without an interactive terminal.")
	}
	_, _ = fmt.Fprint(a.Err, prompt)
	old, err := term.MakeRaw(int(f.Fd()))
	if err != nil {
		return nil, usage("Could not disable echo for private credential input.")
	}
	defer term.Restore(int(f.Fd()), old)
	b, err := readPrivateLine(ctx, f)
	_, _ = fmt.Fprint(a.Err, "\r\n")
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return nil, context.Canceled
	}
	if err != nil || len(b) == 0 {
		return nil, usage("Could not read a nonempty private credential.")
	}
	return b, nil
}

func (a *App) openVault(ctx context.Context, o options) (*state.Store, error) {
	dir := o.dir
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, storageFailure("Cannot find the private configuration directory.")
		}
		dir = filepath.Join(base, "psst.zip", "cli")
	}
	var pass []byte
	var err error
	if o.mode == "passphrase" || o.passFD >= 0 {
		pass, err = a.secret(ctx, o, o.passFD, "Vault passphrase: ")
		if err != nil {
			return nil, err
		}
		defer clear(pass)
		if o.passFD < 0 {
			if _, err := os.Lstat(filepath.Join(dir, "vault.json")); errors.Is(err, os.ErrNotExist) {
				confirm, err := a.secret(ctx, o, -1, "Confirm new vault passphrase: ")
				if err != nil {
					return nil, err
				}
				matches := bytes.Equal(pass, confirm)
				clear(confirm)
				if !matches {
					return nil, usage("The new vault passphrases did not match; no vault was created.")
				}
			}
		}
	}
	input, isFile := a.In.(*os.File)
	interactive := isFile && term.IsTerminal(int(input.Fd()))
	config := state.Config{Dir: dir, Mode: state.Mode(o.mode), Passphrase: pass, Context: ctx, LockTimeout: 3 * time.Second, NonInteractive: o.json || o.noninteractive || !interactive}
	open := func(config state.Config) (*state.Store, error) {
		vault, err := a.OpenState(config)
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return vault, err
	}
	vault, err := open(config)
	if errors.Is(err, state.ErrPassphraseRequired) && o.mode == "auto" && o.passFD < 0 {
		if config.NonInteractive {
			return nil, usage("This vault remembers passphrase storage. Supply --vault-passphrase-fd for JSON/noninteractive commands.")
		}
		pass, err = a.secret(ctx, o, -1, "Vault passphrase: ")
		if err != nil {
			return nil, err
		}
		defer clear(pass)
		config.Mode, config.Passphrase = state.ModePassphrase, pass
		return open(config)
	}
	return vault, err
}

// A stuck credential provider or an unclosed stdin pipe cannot defeat the
// command deadline. This worker only reads bounded input; it cannot mutate state.
func readContext(ctx context.Context, r io.Reader, limit int64) ([]byte, error) {
	type outcome struct {
		b   []byte
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		b, err := io.ReadAll(io.LimitReader(r, limit))
		if ctx.Err() != nil {
			clear(b)
			b = nil
		}
		done <- outcome{b, err}
	}()
	select {
	case value := <-done:
		return value.b, value.err
	case <-ctx.Done():
		if c, ok := r.(io.Closer); ok {
			_ = c.Close()
		}
		return nil, ctx.Err()
	}
}

func readPrivateLine(ctx context.Context, r io.Reader) ([]byte, error) {
	type outcome struct {
		b   []byte
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		b := make([]byte, 0, 128)
		var one [1]byte
		for {
			_, err := io.ReadFull(r, one[:])
			if err != nil {
				clear(b)
				done <- outcome{nil, err}
				return
			}
			if ctx.Err() != nil {
				clear(b)
				done <- outcome{nil, ctx.Err()}
				return
			}
			switch one[0] {
			case '\r', '\n':
				done <- outcome{b, nil}
				return
			case 3, 4:
				clear(b)
				done <- outcome{nil, context.Canceled}
				return
			case 8, 127:
				if len(b) > 0 {
					_, n := utf8.DecodeLastRune(b)
					clear(b[len(b)-n:])
					b = b[:len(b)-n]
				}
			case 21:
				clear(b)
				b = b[:0]
			default:
				if one[0] < 32 {
					continue
				}
				if len(b) >= 4096 {
					clear(b)
					done <- outcome{nil, io.ErrShortBuffer}
					return
				}
				b = append(b, one[0])
			}
		}
	}()
	select {
	case value := <-done:
		return value.b, value.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func fdFlag(f *flag.FlagSet, name string) *int {
	return f.Int(name, -1, "read private credential from a file descriptor (3 or greater)")
}
func parseSeconds(v time.Duration) (int, error) {
	if v < 0 || v%time.Second != 0 || int64(v/time.Second) > 2147483647 {
		return 0, usage("Expiry must be a nonnegative whole-second duration within the supported limit.")
	}
	return int(v / time.Second), nil
}
func fdValid(n int) bool  { return n == -1 || n >= 3 }
func fileID(i int) string { return strconv.Itoa(i) }
