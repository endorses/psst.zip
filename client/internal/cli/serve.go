package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/endorses/psst.zip/client"
	"github.com/endorses/psst.zip/client/internal/lan"
	"github.com/endorses/psst.zip/client/internal/lanassets"
	"golang.org/x/term"
)

// servingFailure marks an error after the one startup JSON document was emitted.
// Later failures belong on stderr and the exit status, never a second envelope.
type servingFailure struct{ cause error }

func (e *servingFailure) Error() string { return e.cause.Error() }
func (e *servingFailure) Unwrap() error { return e.cause }

func (a *App) serveCommand(ctx context.Context, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage("Use serve send FILE... or serve receive --output-dir DIR.")
	}
	f := a.flags("serve " + args[0])
	if args[0] == "cleanup" {
		path := f.String("session-dir", "", "exact crashed psst-lan-* ciphertext staging directory")
		if err := parseFlags(f, args[1:]); err != nil {
			return nil, err
		}
		if *path == "" || f.NArg() != 0 {
			return nil, usage("Use serve cleanup --session-dir DIR for one stopped session.")
		}
		if err := lan.Cleanup(*path); err != nil {
			return nil, storageFailure("Cannot clean this LAN staging directory; verify its marker, ownership and that its process has stopped.")
		}
		return map[string]string{"cleanup": "completed"}, nil
	}
	if !oneOf(args[0], "send", "receive") {
		return nil, usage("Use serve send FILE... or serve receive --output-dir DIR.")
	}
	listen := f.String("listen", "", "specific private IP and port (default: automatically selected IP:0)")
	expires := f.Duration("expires", time.Hour, "temporary session lifetime (default 1h, maximum 168h)")
	maxBytes := f.String("max-bytes", "1GiB", "total encrypted payload budget (1MiB–1TiB)")
	maxStreams := f.Int("max-streams", 2, "maximum simultaneous payload streams")
	tlsCert := f.String("tls-cert", "", "trusted TLS certificate file")
	tlsKey := f.String("tls-key", "", "matching TLS private key file")
	showQR := f.Bool("qr", false, "show the LAN share link as a terminal QR code")
	plain := f.Bool("plain", false, "plain QR blocks")
	var output *string
	maximum := 1
	maxFiles := 100
	if args[0] == "send" {
		f.IntVar(&maximum, "max-downloads", 1, "maximum acknowledged downloads")
	} else {
		output = f.String("output-dir", "", "directory for authenticated received plaintext")
		f.IntVar(&maxFiles, "max-files", 100, "cumulative received file limit")
	}
	if err := parseFlags(f, args[1:]); err != nil {
		return nil, err
	}
	if o.server != "" {
		return nil, usage("serve uses this computer; omit --server.")
	}
	if *showQR && o.json || *plain && !*showQR {
		return nil, usage("Use --qr or --json; --plain requires --qr.")
	}
	budget, err := parseByteBudget(*maxBytes)
	if err != nil {
		return nil, err
	}
	config := lan.Config{Kind: args[0], Listen: *listen, Expires: *expires, MaxBytes: budget, MaxDownloads: maximum, MaxFiles: maxFiles, MaxStreams: *maxStreams, TLSCert: *tlsCert, TLSKey: *tlsKey}
	if output != nil {
		config.OutputDir = *output
		if f.NArg() != 0 {
			return nil, usage("serve receive takes no file operands.")
		}
	} else {
		config.Files, err = client.FileInputs(f.Args())
		if err != nil {
			return nil, storageFailure("Select readable regular files with safe, unique filenames; directories and symbolic links cannot be shared.")
		}
	}
	// Validate finite settings before interface prompts or asset preparation.
	if config.Listen == "" {
		config.Listen = "127.0.0.1:0"
	}
	if err = lan.Validate(config); err != nil {
		return nil, usage(err.Error())
	}
	if *listen == "" {
		config.Listen, err = a.lanAddress(ctx, o)
		if err != nil {
			return nil, err
		}
	}
	if err = lanassets.Validate(); err != nil {
		return nil, storageFailure("Matching LAN browser assets are missing or stale; rebuild psst using make build.")
	}
	config.Progress = func(done, total int64) { _, _ = fmt.Fprintf(a.Err, "Prepared %d/%d encrypted bytes\n", done, total) }
	config.Received = func(result client.DownloadResult) {
		_, _ = fmt.Fprintf(a.Err, "Received and authenticated %d file(s).\n", len(result.Files))
	}
	host, err := lan.Start(ctx, config, lanassets.Handler())
	if err != nil {
		// Listener/TLS failures must be useful but cannot print nested request URLs,
		// private file paths or capabilities returned by a transport/provider.
		if errors.Is(err, lan.ErrTLS) {
			return nil, usage("TLS requires a matching key and a currently valid server certificate whose IP subject alternative name covers the selected LAN address.")
		}
		if errors.Is(err, lan.ErrInsufficientSpace) {
			return nil, storageFailure("Insufficient free disk space for encrypted staging and received plaintext; reduce --max-bytes or select a larger destination.")
		}
		var operation *client.OperationError
		var network *net.OpError
		if errors.As(err, &network) {
			return nil, &Failure{Code: "listen_failed", Message: "Cannot bind the selected local address. Check it belongs to this computer; if its port is occupied, use --listen IP:0.", exit: ExitTransport}
		}
		if errors.As(err, &operation) {
			return nil, &Failure{Code: "lan_preparation", Message: "Could not prepare the private encrypted LAN snapshot; check disk space and file readability.", exit: ExitStorage}
		}
		return nil, err
	}
	defer host.Close()
	var art string
	if *showQR {
		art, err = qrArt(host.Startup.Link, a.width(), *plain)
		if err != nil {
			return nil, err
		}
	}
	_, _ = fmt.Fprintf(a.Err, "Serving %s until %s; budget %d encrypted bytes, %d stream(s). Press Ctrl+C to stop.\n", safeText(host.Startup.Hosting.Listen), host.Startup.ExpiresAt.Format(time.RFC3339), budget, *maxStreams)
	if host.Startup.Hosting.TLS {
		_, _ = fmt.Fprintln(a.Err, "HTTPS uses your supplied certificate; receiving devices must trust it.")
	} else {
		_, _ = fmt.Fprintln(a.Err, "Trusted LAN HTTP: files are encrypted, but an active network attacker could replace the browser page.")
	}
	if o.json {
		err = writeJSON(a.Out, host.Startup, nil)
	} else {
		_, err = fmt.Fprintln(a.Out, host.Startup.Link)
		if err == nil && art != "" {
			_, err = fmt.Fprint(a.Out, art)
		}
	}
	if err != nil {
		return nil, &servingFailure{storageFailure("Could not write the LAN startup result.")}
	}
	err = host.Wait()
	closeErr := host.Close()
	_, _ = fmt.Fprintln(a.Err, host.Summary())
	if closeErr != nil {
		return nil, &servingFailure{storageFailure("Transfer stopped, but private staging cleanup failed; use the documented crashed-session cleanup procedure.")}
	}
	if errors.Is(err, context.Canceled) {
		// Ending an open Receive invitation is an ordinary user action. The
		// listener and private staging have already been closed successfully.
		return nil, nil
	}
	if err != nil {
		if errors.Is(err, lan.ErrExpired) {
			return nil, &servingFailure{&Failure{Code: "lan_expired", Message: "LAN session expired without a completed transfer.", exit: ExitUnavailable}}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &servingFailure{&Failure{Code: "timeout", Message: "The explicit LAN command deadline expired; this temporary session cannot be resumed.", exit: ExitTransport}}
		}
		return nil, &servingFailure{err}
	}
	return nil, nil
}

func (a *App) lanAddress(ctx context.Context, o options) (string, error) {
	addresses, err := lan.Addresses()
	if err != nil {
		return "", &Failure{Code: "lan_address", Message: "Could not inspect local interfaces; supply --listen PRIVATE_IP:0.", exit: ExitTransport}
	}
	if len(addresses) == 1 {
		return net.JoinHostPort(addresses[0].IP, "0"), nil
	}
	if len(addresses) == 0 {
		return "", usage("No usable private LAN address found. Connect to a LAN or explicitly select --listen 127.0.0.1:0 for local use.")
	}
	input, ok := a.In.(*os.File)
	if o.json || o.noninteractive || !ok || !term.IsTerminal(int(input.Fd())) {
		return "", usage("Multiple LAN addresses are available (possibly a VPN); select --listen PRIVATE_IP:0.")
	}
	_, _ = fmt.Fprintln(a.Err, "Choose the LAN address reachable from the receiving device:")
	for i, address := range addresses {
		_, _ = fmt.Fprintf(a.Err, "  %d  %s  %s\n", i+1, safeText(address.Interface), address.IP)
	}
	_, _ = fmt.Fprint(a.Err, "Address number: ")
	line, err := readPrivateLine(ctx, a.In)
	if err != nil {
		return "", err
	}
	choice, err := strconv.Atoi(strings.TrimSpace(string(line)))
	if err != nil || choice < 1 || choice > len(addresses) {
		return "", usage("Select one listed address or supply --listen PRIVATE_IP:0.")
	}
	return net.JoinHostPort(addresses[choice-1].IP, "0"), nil
}

func parseByteBudget(value string) (int64, error) {
	multiplier := int64(1)
	for _, suffix := range []struct {
		name string
		size int64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40}} {
		if strings.HasSuffix(value, suffix.name) {
			value = strings.TrimSuffix(value, suffix.name)
			multiplier = suffix.size
			break
		}
	}
	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil || count <= 0 || count > (1<<40)/multiplier {
		return 0, usage("Use a finite integer --max-bytes value in bytes, KiB, MiB, GiB or TiB, between 1MiB and 1TiB.")
	}
	return count * multiplier, nil
}
