package lan

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/endorses/psst.zip/backend/standalone"
	"github.com/endorses/psst.zip/client"
	clientcrypto "github.com/endorses/psst.zip/client/internal/crypto"
	"github.com/endorses/psst.zip/client/protocol"
)

// Config contains only explicit session settings; no account or vault is read.
type Config struct {
	Kind, Listen, OutputDir, TLSCert, TLSKey string
	Files                                    []client.InputFile
	Expires                                  time.Duration
	MaxBytes                                 int64
	MaxFiles, MaxDownloads, MaxStreams       int
	Progress                                 client.Progress
	Received                                 func(client.DownloadResult)
}

// Hosting identifies the ephemeral listener and its finite resource policy.
type Hosting struct {
	Mode         string `json:"mode"`
	Listen       string `json:"listen"`
	TLS          bool   `json:"tls"`
	MaxBytes     int64  `json:"max_bytes"`
	MaxFiles     int    `json:"max_files"`
	MaxDownloads int    `json:"max_downloads"`
	MaxStreams   int    `json:"max_streams"`
}

// Startup is emitted once, after the encrypted snapshot and listener are ready.
// Its embedded Share preserves the existing saved-JSON QR contract.
type Startup struct {
	client.Share
	Hosting Hosting `json:"hosting"`
}

// Host owns one listener, one backend session and private in-memory keys.
// Close must be called after Wait; successful received plaintext is retained.
type Host struct {
	Startup     Startup
	config      Config
	server      *http.Server
	listener    net.Listener
	backend     *standalone.Session
	owner       *client.Client
	store       *memoryStore
	staging     *staging
	ctx         context.Context
	cancel      context.CancelFunc
	serveDone   chan error
	closeOnce   sync.Once
	closeErr    error
	collected   map[string]bool
	completions int
	downloads   int
}

var ErrExpired = errors.New("LAN session expired without a completed transfer")

// ErrTLS reports unusable operator-supplied certificate inputs without leaking paths.
var ErrTLS = errors.New("the supplied TLS certificate or private key is invalid for this LAN listener")

// ErrInsufficientSpace reports a bounded-storage preflight failure.
var ErrInsufficientSpace = errors.New("insufficient free disk space for bounded encrypted staging and plaintext output")

// Validate rejects unbounded or incompatible settings before opening a listener.
func Validate(config Config) error {
	if config.Kind != "send" && config.Kind != "receive" {
		return errors.New("use serve send or serve receive")
	}
	if err := ValidateListen(config.Listen); err != nil {
		return err
	}
	if config.Expires < time.Second || config.Expires%time.Second != 0 || config.Expires > 7*24*time.Hour {
		return errors.New("--expires must be a whole-second lifetime between 1s and 168h")
	}
	if config.MaxBytes < 1<<20 || config.MaxBytes > protocol.MaxFileSize {
		return errors.New("--max-bytes must be between 1MiB and 1TiB")
	}
	if config.MaxFiles < 1 || config.MaxFiles > protocol.MaxFiles || config.MaxDownloads < 1 || config.MaxDownloads > 1000000 || config.MaxStreams < 1 || config.MaxStreams > 32 {
		return errors.New("use 1–100 files, 1–1000000 downloads and 1–32 concurrent streams")
	}
	if (config.TLSCert == "") != (config.TLSKey == "") {
		return errors.New("supply both --tls-cert and --tls-key")
	}
	if config.Kind == "receive" && (config.OutputDir == "" || len(config.Files) > 0) || config.Kind == "send" && (len(config.Files) < 1 || len(config.Files) > config.MaxFiles) {
		return errors.New("send requires explicit files; receive requires --output-dir and no input files")
	}
	var total int64
	for _, f := range config.Files {
		wire, err := clientcrypto.WireSize(f.Size)
		if err != nil || wire > config.MaxBytes-total {
			return errors.New("selected encrypted files exceed --max-bytes")
		}
		total += wire
	}
	return nil
}

// Start prepares the private encrypted snapshot using the normal SDK, then
// exposes only the backend's restricted guest handler and embedded browser UI.
// The owner handler is invoked through an in-process transport, never a socket.
func Start(ctx context.Context, config Config, assets http.Handler) (host *Host, err error) {
	if err = Validate(config); err != nil {
		return nil, err
	}
	if assets == nil {
		return nil, errors.New("browser assets are unavailable")
	}
	var certificate tls.Certificate
	if config.TLSCert != "" {
		certificate, err = tls.LoadX509KeyPair(config.TLSCert, config.TLSKey)
		if err != nil {
			return nil, ErrTLS
		}
	}
	host = &Host{config: config, store: newMemoryStore(), serveDone: make(chan error, 1), collected: make(map[string]bool)}
	expires := time.Now().Add(config.Expires)
	host.ctx, host.cancel = context.WithDeadline(ctx, expires)
	prepared := host
	defer func() {
		if err != nil {
			_ = prepared.Close()
			host = nil
		}
	}()
	host.staging, err = newStaging()
	if err != nil {
		return nil, err
	}
	var wire int64
	for _, f := range config.Files {
		size, _ := clientcrypto.WireSize(f.Size)
		wire += size
	}
	needed := 2*wire + (16 << 20)
	if config.Kind == "receive" {
		needed = 2*config.MaxBytes + (16 << 20)
	}
	if err = checkSpace(host.staging.path, needed); err != nil {
		return nil, err
	}
	if config.Kind == "receive" {
		if err = prepareOutput(config.OutputDir, config.MaxBytes); err != nil {
			return nil, err
		}
	}
	host.listener, err = Listen(host.ctx, config.Listen)
	if err != nil {
		return nil, err
	}
	if config.TLSCert != "" {
		if err = validateCertificate(certificate, host.listener.Addr(), time.Now()); err != nil {
			return nil, err
		}
	}
	scheme := "http"
	if config.TLSCert != "" {
		scheme = "https"
	}
	origin := scheme + "://" + host.listener.Addr().String()
	host.backend, err = standalone.New(standalone.Config{Directory: host.staging.path, Origin: origin, ExpiresAt: expires, MaxBytes: config.MaxBytes, MaxFiles: config.MaxFiles, MaxDownloads: config.MaxDownloads, MaxStreams: config.MaxStreams})
	if err != nil {
		return nil, err
	}
	host.owner, err = client.New(origin, &http.Client{Transport: handlerTransport{handler: host.backend.OwnerHandler()}}, nil)
	if err != nil {
		return nil, err
	}
	var share client.Share
	if config.Kind == "send" {
		share, err = host.owner.Send(host.ctx, config.Files, client.SendOptions{ExpiresInSeconds: 0, MaxDownloads: config.MaxDownloads, SpoolDir: filepath.Join(host.staging.path, "snapshot"), Progress: config.Progress}, host.store)
	} else {
		share, err = host.owner.CreateReceive(host.ctx, client.ReceiveOptions{ExpiresInSeconds: 0, MaxFiles: config.MaxFiles}, host.store)
	}
	if err != nil {
		return nil, err
	}
	if err = host.backend.Activate(config.Kind, share.ID); err != nil {
		return nil, err
	}
	share.ExpiresAt = expires
	host.Startup = Startup{Share: share, Hosting: Hosting{Mode: "lan", Listen: host.listener.Addr().String(), TLS: config.TLSCert != "", MaxBytes: config.MaxBytes, MaxFiles: config.MaxFiles, MaxDownloads: config.MaxDownloads, MaxStreams: config.MaxStreams}}
	handler := host.backend.Handler()
	host.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The embedding facade owns Host/Origin validation, including UI requests.
		if r.URL.Path == "/api" || len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			handler.ServeHTTP(w, r)
			return
		}
		if r.Host != host.listener.Addr().String() {
			http.Error(w, "invalid host", http.StatusMisdirectedRequest)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if originHeader := r.Header.Get("Origin"); originHeader != "" && originHeader != origin {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		assets.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10, ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return host.ctx }}
	listener := host.listener
	if config.TLSCert != "" {
		listener = tls.NewListener(listener, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}})
	}
	go func() { host.serveDone <- host.server.Serve(listener) }()
	return host, nil
}

func prepareOutput(path string, maxBytes int64) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = checkSpace(path, maxBytes+(16<<20)); err != nil {
		return err
	}
	probe, err := os.CreateTemp(path, ".psst-write-check-")
	if err != nil {
		return err
	}
	name := filepath.Base(probe.Name())
	closeErr := probe.Close()
	removeErr := root.Remove(name)
	return errors.Join(closeErr, removeErr)
}

// Wait serves until success, expiry, interruption or failure. Receive sessions
// collect each completed submission only once, avoiding repeated hashing of all
// earlier plaintext whenever the inbox is polled.
func (h *Host) Wait() error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	return h.wait(ticker.C)
}

// wait accepts tick times so capacity retries can be verified without sleeping.
func (h *Host) wait(ticks <-chan time.Time) error {
	var nextCollect time.Time
	for {
		select {
		case <-h.ctx.Done():
			if errors.Is(h.ctx.Err(), context.DeadlineExceeded) && (h.completions > 0 || h.downloads > 0) {
				return nil
			}
			if errors.Is(h.ctx.Err(), context.DeadlineExceeded) {
				return ErrExpired
			}
			return h.ctx.Err()
		case err := <-h.serveDone:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case now := <-ticks:
			if h.config.Kind == "receive" && !now.Before(nextCollect) {
				if err := h.collect(); err != nil {
					next, retry := collectionRetry(err, now, h.ctx)
					if !retry {
						return err
					}
					nextCollect = next
				} else {
					nextCollect = time.Time{}
				}
			}
			stats, err := h.backend.Stats()
			if err != nil {
				return err
			}
			h.downloads = stats.Downloads
			if h.config.Kind == "send" && stats.Downloads >= h.config.MaxDownloads {
				return nil
			}
			if h.config.Kind == "receive" && h.completions >= h.config.MaxFiles {
				return nil
			}
		}
	}
}

// collectionRetry preserves the SDK download journal while the shared payload
// admission limit is occupied. Permanent protocol, crypto and filesystem errors
// still terminate; Retry-After never schedules work past the session deadline.
func collectionRetry(err error, now time.Time, ctx context.Context) (time.Time, bool) {
	var api *client.APIError
	if !errors.As(err, &api) || (api.Status != http.StatusTooManyRequests && api.Status != http.StatusServiceUnavailable) {
		return time.Time{}, false
	}
	delay := max(250*time.Millisecond, api.RetryAfter)
	next := now.Add(delay)
	if deadline, ok := ctx.Deadline(); ok && next.After(deadline) {
		next = deadline
	}
	return next, true
}

// validateCertificate checks properties that can be established locally before
// a link is printed. Recipient trust roots remain a device configuration choice;
// accepting a self-signed certificate here never bypasses client verification.
func validateCertificate(pair tls.Certificate, address net.Addr, now time.Time) error {
	if len(pair.Certificate) == 0 {
		return ErrTLS
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return ErrTLS
	}
	host, _, err := net.SplitHostPort(address.String())
	if err != nil || leaf.VerifyHostname(host) != nil {
		return ErrTLS
	}
	allowed := len(leaf.ExtKeyUsage) == 0 && len(leaf.UnknownExtKeyUsage) == 0
	for _, usage := range leaf.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
			allowed = true
		}
	}
	if !allowed {
		return ErrTLS
	}
	return nil
}

func (h *Host) collect() error {
	after := ""
	for pages := 0; pages < 100; pages++ {
		page, err := h.owner.GetInboxPage(h.ctx, h.Startup.ID, after, 100)
		if err != nil {
			return err
		}
		for _, transfer := range page.Transfers {
			if transfer.Status != "complete" || h.collected[transfer.TransferID] {
				continue
			}
			result, err := h.owner.FetchSubmission(h.ctx, h.Startup.ID, transfer.TransferID, h.config.OutputDir, h.store)
			if err != nil {
				return err
			}
			h.collected[transfer.TransferID] = true
			h.completions += len(result.Files)
			if h.config.Received != nil {
				h.config.Received(result)
			}
		}
		if page.NextCursor == nil {
			return nil
		}
		after = *page.NextCursor
	}
	return errors.New("Receive inbox exceeded its bounded traversal")
}

// Close bounds network draining and deletes only this invocation's locked
// staging directory. Successfully published received plaintext is never removed.
func (h *Host) Close() error {
	h.closeOnce.Do(func() {
		if h.cancel != nil {
			h.cancel()
		}
		if h.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := h.server.Shutdown(ctx)
			cancel()
			if err != nil {
				_ = h.server.Close()
			}
		} else if h.listener != nil {
			_ = h.listener.Close()
		}
		if h.backend != nil {
			h.closeErr = errors.Join(h.closeErr, h.backend.Close())
		}
		if h.store != nil {
			h.store.cleanupPending(h.config.OutputDir)
			h.store.close()
		}
		if h.staging != nil {
			h.closeErr = errors.Join(h.closeErr, h.staging.close())
		}
	})
	return h.closeErr
}

// Summary contains no capability or private filename and is safe for stderr.
func (h *Host) Summary() string {
	if h.config.Kind == "send" {
		return fmt.Sprintf("LAN session stopped; %d acknowledged download(s).", h.downloads)
	}
	return fmt.Sprintf("LAN session stopped; %d received file(s) retained.", h.completions)
}
