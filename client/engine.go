package client

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	clientcrypto "github.com/endorses/psst.zip/client/internal/crypto"
	"github.com/endorses/psst.zip/client/protocol"
)

// Store is a caller-selected protected, account-scoped persistence provider. It
// must serialize concurrent commands and atomically save each record. Its secrets
// must be encrypted at rest or held by an OS credential store, never plaintext.
type Store interface {
	Get(string, any) error
	Put(string, any) error
	Delete(string) error
	Keys(string) ([]string, error)
}
type InputFile struct {
	Name     string
	Size     int64
	MIMEType string
	Open     func() (io.ReadCloser, error)
}
type SendOptions struct {
	Title            *string
	ExpiresInSeconds int
	MaxDownloads     int
	SpoolDir         string
	OperationID      string
	Progress         Progress
}
type ReceiveOptions struct {
	Title            *string
	ExpiresInSeconds int
	MaxFiles         int
}
type Share struct {
	Kind         string    `json:"kind"`
	ID           string    `json:"id"`
	Origin       string    `json:"origin"`
	Link         string    `json:"link,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	KeyAvailable bool      `json:"key_available"`
	OperationID  string    `json:"operation_id,omitempty"`
}
type UploadResult struct {
	ID          string `json:"id"`
	Origin      string `json:"origin"`
	Status      string `json:"status"`
	OperationID string `json:"operation_id,omitempty"`
}
type DownloadResult struct {
	ID             string   `json:"id"`
	Files          []string `json:"files"`
	ReceiptPending bool     `json:"receipt_pending"`
}
type OperationError struct {
	OperationID string
	Cause       error
}

func (e *OperationError) Error() string {
	return "operation " + e.OperationID + " interrupted; resume using its operation ID"
}
func (e *OperationError) Unwrap() error { return e.Cause }

type resourceRecord struct {
	RequestedExpiresInSeconds int       `json:"requested_expires_in_seconds"`
	RequestedMaxFiles         int       `json:"requested_max_files"`
	CreationServerTime        time.Time `json:"creation_server_time"`
	RequestedMaxDownloads     int       `json:"requested_max_downloads"`
	Origin                    string    `json:"origin"`
	ID                        string    `json:"id"`
	Kind                      string    `json:"kind"`
	Key                       []byte    `json:"key"`
	PublicKey                 []byte    `json:"public_key,omitempty"`
	DeleteToken               string    `json:"delete_token,omitempty"`
	ExpiresAt                 time.Time `json:"expires_at"`
	OperationID               string    `json:"operation_id,omitempty"`
}
type uploadFileRecord struct {
	Manifest protocol.ManifestFile `json:"manifest"`
	Spool    string                `json:"spool"`
	Resource UploadResource        `json:"resource"`
}
type uploadJournal struct {
	RequestedMaxDownloads int                `json:"requested_max_downloads"`
	Origin                string             `json:"origin"`
	ID                    string             `json:"id"`
	Kind                  string             `json:"kind"`
	OperationID           string             `json:"operation_id"`
	Key                   []byte             `json:"key"`
	PublicKey             []byte             `json:"public_key,omitempty"`
	SlotID                string             `json:"slot_id,omitempty"`
	Capability            string             `json:"capability,omitempty"`
	ExpiresAt             time.Time          `json:"expires_at"`
	Files                 []uploadFileRecord `json:"files"`
	Manifest              []byte             `json:"manifest,omitempty"`
	Complete              bool               `json:"complete"`
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func resourceKey(kind, id string) string { return "resource/" + kind + "/" + id }
func journalKey(id string) string        { return "operation/upload/" + id }
func validFilename(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, "/\\:\x00") || filepath.Base(name) != name || strings.IndexFunc(name, unicode.IsControl) >= 0 || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	// Windows device names remain reserved even with an extension. Reject them on
	// every platform so a saved manifest has predictable portable destinations.
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}

// FileInputs opens regular local files lazily and rejects directories and links.
func FileInputs(paths []string) ([]InputFile, error) {
	out := make([]InputFile, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		path := path
		info, e := os.Lstat(path)
		if e != nil {
			return nil, e
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("input must be a regular file")
		}
		name := filepath.Base(path)
		if !validFilename(name) || seen[name] {
			return nil, errors.New("input filenames must be safe and unique")
		}
		seen[name] = true
		size := info.Size()
		out = append(out, InputFile{Name: name, Size: size, MIMEType: mime.TypeByExtension(filepath.Ext(name)), Open: func() (io.ReadCloser, error) {
			f, e := os.Open(path)
			if e != nil {
				return nil, e
			}
			stat, e := f.Stat()
			if e != nil || !stat.Mode().IsRegular() || stat.Size() != size || !os.SameFile(info, stat) {
				f.Close()
				return nil, errors.New("input changed before encryption")
			}
			return f, nil
		}})
	}
	return out, nil
}
func validateInputs(files []InputFile) error {
	if len(files) < 1 || len(files) > protocol.MaxFiles {
		return errors.New("select 1 to 100 files")
	}
	seen := map[string]bool{}
	var total int64
	for _, f := range files {
		if !validFilename(f.Name) || seen[f.Name] || f.Size < 0 || f.Size > protocol.MaxFileSize || f.Open == nil {
			return errors.New("invalid input file")
		}
		if f.Size > protocol.MaxFileSize-total {
			return errors.New("combined transfer size exceeds the supported limit")
		}
		total += f.Size
		seen[f.Name] = true
	}
	return nil
}
func (c *Client) preflightFiles(ctx context.Context, files []InputFile) error {
	cfg, e := c.Config(ctx)
	if e != nil {
		return e
	}
	if cfg.MaxFileSize < 1 || cfg.MaxFileSize > protocol.MaxFileSize {
		return ErrProtocol
	}
	if cfg.PublicTransfersPaused {
		return &PolicyError{Code: "public_transfers_paused", Message: "The server has paused public transfers."}
	}
	for _, f := range files {
		if f.Size > cfg.MaxFileSize {
			return &PolicyError{Code: "file_size_limit", Message: "An input file exceeds the server’s configured file size limit."}
		}
	}
	return nil
}

func requireStore(s Store) error {
	if s == nil {
		return errors.New("protected persistence is required")
	}
	return nil
}
func (c *Client) share(r resourceRecord) (Share, error) {
	out := Share{Kind: r.Kind, ID: r.ID, Origin: c.origin, ExpiresAt: r.ExpiresAt, KeyAvailable: len(r.Key) == 32, OperationID: r.OperationID}
	key := r.Key
	if r.Kind == "receive" {
		key = r.PublicKey
		out.KeyAvailable = len(r.Key) == 32 && len(key) == 32
	}
	if out.KeyAvailable {
		link, e := protocol.BuildLink(c.origin, r.Kind, r.ID, key)
		if e != nil {
			return out, e
		}
		out.Link = link
	}
	return out, nil
}
func (c *Client) Show(ctx context.Context, kind, id string, s Store) (Share, error) {
	if kind != "send" && kind != "receive" {
		return Share{}, errors.New("invalid resource kind")
	}
	if e := requireStore(s); e != nil {
		return Share{}, e
	}
	var r resourceRecord
	e := s.Get(resourceKey(kind, id), &r)
	if e != nil && !errors.Is(e, ErrNotFound) {
		return Share{}, e
	}
	if e == nil && (r.Origin != c.origin || r.ID != id || r.Kind != kind) {
		return Share{}, errors.New("local resource identity mismatch")
	}
	if e != nil {
		r = resourceRecord{Origin: c.origin, ID: id, Kind: kind}
	}
	if len(r.Key) == 32 {
		if e := acceptedExpiry(protocol.CreatedTransfer{ExpiresAt: r.ExpiresAt, ServerTime: r.CreationServerTime}, r.RequestedExpiresInSeconds); e != nil {
			return Share{}, e
		}
	}
	if kind == "send" {
		v, e := c.GetTransfer(ctx, id)
		if e != nil {
			return Share{}, e
		}
		if len(r.Key) == 32 {
			if v.Status != "complete" || !v.HasManifest {
				return Share{}, &PolicyError{Code: "transfer_incomplete", Message: "The transfer is not complete; resume its upload before sharing."}
			}
			if v.MaxDownloads != r.RequestedMaxDownloads || !v.ExpiresAt.Equal(r.ExpiresAt) {
				return Share{}, policyMismatch()
			}
		}
		r.ExpiresAt = v.ExpiresAt
	} else {
		v, e := c.GetInboxPage(ctx, id, "", 1)
		if e != nil {
			return Share{}, e
		}
		if len(r.PublicKey) > 0 && (v.ReceiveProtocol != 2 || !keyMatches(v.RecipientPublicKey, r.PublicKey) || v.MaxFiles != r.RequestedMaxFiles || !v.ExpiresAt.Equal(r.ExpiresAt)) {
			return Share{}, policyMismatch()
		}
		r.ExpiresAt = v.ExpiresAt
	}
	return c.share(r)
}

// CreateReceive persists the private key before allocation and never exposes an
// invitation unless the authoritative inbox metadata confirms its public key.
func (c *Client) CreateReceive(ctx context.Context, opt ReceiveOptions, s Store) (Share, error) {
	if e := requireStore(s); e != nil {
		return Share{}, e
	}
	private, public, e := clientcrypto.GenerateReceiveKey()
	if e != nil {
		return Share{}, e
	}
	defer clear(private)
	op, e := randomID()
	if e != nil {
		return Share{}, e
	}
	r := resourceRecord{Origin: c.origin, Kind: "receive", Key: private, PublicKey: public, OperationID: op, RequestedExpiresInSeconds: opt.ExpiresInSeconds, RequestedMaxFiles: opt.MaxFiles}
	pending := "operation/receive/" + op
	if e = s.Put(pending, r); e != nil {
		return Share{}, e
	}
	created, e := c.CreateSlot(ctx, protocol.CreateReceiveRequest{Title: opt.Title, ExpiresInSeconds: opt.ExpiresInSeconds, MaxFiles: opt.MaxFiles, ReceiveProtocol: 2, RecipientPublicKey: base64.RawURLEncoding.EncodeToString(public)})
	if e != nil {
		return Share{}, e
	}
	r.ID = created.ID
	r.DeleteToken = created.DeleteToken
	r.ExpiresAt = created.ExpiresAt
	r.CreationServerTime = created.ServerTime
	if e = s.Put(resourceKey("receive", r.ID), r); e != nil {
		_ = c.WithToken(created.DeleteToken).RevokeReceive(context.WithoutCancel(ctx), r.ID)
		return Share{}, e
	}
	if e = acceptedExpiry(created, opt.ExpiresInSeconds); e != nil {
		_ = c.WithToken(created.DeleteToken).RevokeReceive(context.WithoutCancel(ctx), r.ID)
		return Share{}, e
	}
	if e = s.Delete(pending); e != nil {
		return Share{}, e
	}
	page, e := c.GetInboxPage(ctx, r.ID, "", 1)
	if e != nil {
		return Share{}, e
	}
	if page.ReceiveProtocol != 2 || !keyMatches(page.RecipientPublicKey, public) || page.MaxFiles != opt.MaxFiles || !page.ExpiresAt.Equal(created.ExpiresAt) {
		_ = c.WithToken(created.DeleteToken).RevokeReceive(context.WithoutCancel(ctx), r.ID)
		return Share{}, errors.New("server did not accept receive protocol and policy")
	}
	return c.share(r)
}
func spoolDirectory(dir string) (string, error) {
	if dir == "" {
		base, e := os.UserCacheDir()
		if e != nil {
			return "", e
		}
		dir = filepath.Join(base, "psst.zip", "ciphertext")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	st, e := os.Lstat(dir)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid ciphertext spool directory")
	}
	if e = os.Chmod(dir, 0700); e != nil {
		return "", e
	}
	return filepath.Abs(dir)
}
func prepareCiphertext(ctx context.Context, files []InputFile, key []byte, dir string) ([]uploadFileRecord, error) {
	dir, e := spoolDirectory(dir)
	if e != nil {
		return nil, e
	}
	records := make([]uploadFileRecord, 0, len(files))
	cleanup := func() {
		for _, r := range records {
			_ = os.Remove(r.Spool)
		}
	}
	for _, f := range files {
		id, e := clientcrypto.NewEncryptionID()
		if e != nil {
			cleanup()
			return nil, e
		}
		mf := protocol.ManifestFile{Name: f.Name, Size: f.Size, MIMEType: f.MIMEType, Encoding: "chunked-v1", ChunkSize: protocol.ChunkSize, EncryptionID: id}
		if mf.MIMEType == "" {
			mf.MIMEType = "application/octet-stream"
		}
		dst, e := os.CreateTemp(dir, "ciphertext-")
		if e != nil {
			cleanup()
			return nil, e
		}
		src, e := f.Open()
		if e == nil {
			e = clientcrypto.EncryptFile(ctx, dst, src, key, mf)
			src.Close()
		}
		if e == nil {
			e = dst.Sync()
		}
		ce := dst.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			os.Remove(dst.Name())
			cleanup()
			return nil, e
		}
		records = append(records, uploadFileRecord{Manifest: mf, Spool: dst.Name()})
	}
	return records, nil
}
func (c *Client) Send(ctx context.Context, files []InputFile, opt SendOptions, s Store) (Share, error) {
	if opt.OperationID != "" {
		return c.ResumeSend(ctx, opt.OperationID, s, opt.Progress)
	}
	if e := requireStore(s); e != nil {
		return Share{}, e
	}
	if e := validateInputs(files); e != nil {
		return Share{}, e
	}
	if e := c.preflightFiles(ctx, files); e != nil {
		return Share{}, e
	}
	key, e := clientcrypto.NewKey()
	if e != nil {
		return Share{}, e
	}
	defer clear(key)
	op, e := randomID()
	if e != nil {
		return Share{}, e
	}
	j := uploadJournal{Origin: c.origin, OperationID: op, Kind: "send", Key: key, RequestedMaxDownloads: opt.MaxDownloads}
	if e = s.Put(journalKey(op), j); e != nil {
		return Share{}, e
	}
	created, e := c.CreateTransfer(ctx, protocol.CreateTransferRequest{Title: opt.Title, ExpiresInSeconds: opt.ExpiresInSeconds, MaxDownloads: opt.MaxDownloads})
	if e != nil {
		return Share{}, e
	}
	j.ID = created.ID
	j.ExpiresAt = created.ExpiresAt
	j.Capability = created.DeleteToken
	if e = s.Put(journalKey(op), j); e != nil {
		_ = c.WithToken(created.DeleteToken).RevokeTransfer(context.WithoutCancel(ctx), j.ID)
		return Share{}, e
	}
	r := resourceRecord{Origin: c.origin, ID: j.ID, Kind: "send", Key: key, DeleteToken: j.Capability, ExpiresAt: j.ExpiresAt, OperationID: op, RequestedMaxDownloads: opt.MaxDownloads, RequestedExpiresInSeconds: opt.ExpiresInSeconds, CreationServerTime: created.ServerTime}
	if e = s.Put(resourceKey("send", j.ID), r); e != nil {
		_ = c.WithToken(created.DeleteToken).RevokeTransfer(context.WithoutCancel(ctx), j.ID)
		return Share{}, e
	}
	// Confirm requested policy while the allocation is still empty. A version-1
	// server that ignores a field must not turn a limited Send into an unlimited
	// capability; the persisted record retains cleanup authority on uncertainty.
	if e = acceptedExpiry(created, opt.ExpiresInSeconds); e == nil {
		var metadata protocol.Transfer
		metadata, e = c.GetTransfer(ctx, j.ID)
		if e == nil {
			e = verifyUploadPolicy(j, metadata)
		}
	}
	if e != nil {
		_ = c.WithToken(j.Capability).RevokeTransfer(context.WithoutCancel(ctx), j.ID)
		return Share{}, e
	}
	j.Files, e = prepareCiphertext(ctx, files, key, opt.SpoolDir)
	if e != nil {
		_ = c.WithToken(j.Capability).RevokeTransfer(context.WithoutCancel(ctx), j.ID)
		return Share{}, e
	}
	if e = s.Put(journalKey(op), j); e != nil {
		for _, f := range j.Files {
			_ = os.Remove(f.Spool)
		}
		_ = c.WithToken(j.Capability).RevokeTransfer(context.WithoutCancel(ctx), j.ID)
		return Share{}, e
	}
	if e = c.finishUpload(ctx, &j, s, opt.Progress); e != nil {
		var policy *PolicyError
		if errors.As(e, &policy) {
			return Share{}, e
		}
		return Share{}, &OperationError{op, e}
	}
	return c.share(r)
}
func (c *Client) ResumeSend(ctx context.Context, op string, s Store, progress Progress) (Share, error) {
	if e := requireStore(s); e != nil {
		return Share{}, e
	}
	if !protocol.ValidID(op) {
		return Share{}, errors.New("invalid operation ID")
	}
	var j uploadJournal
	if e := s.Get(journalKey(op), &j); e != nil {
		return Share{}, e
	}
	if j.Origin != c.origin || j.Kind != "send" || j.OperationID != op || !protocol.ValidID(j.ID) || len(j.Files) == 0 {
		return Share{}, errors.New("upload journal is incomplete or belongs to another origin")
	}
	if e := c.finishUpload(ctx, &j, s, progress); e != nil {
		var policy *PolicyError
		if errors.As(e, &policy) {
			return Share{}, e
		}
		return Share{}, &OperationError{op, e}
	}
	var r resourceRecord
	if e := s.Get(resourceKey("send", j.ID), &r); e != nil {
		return Share{}, e
	}
	return c.share(r)
}
func (c *Client) finishUpload(ctx context.Context, j *uploadJournal, s Store, progress Progress) error {
	uploader := c
	if j.Kind == "receive-upload" {
		uploader = c.Anonymous().WithToken(j.Capability)
	}
	if !j.Complete {
		// The completion response or the following local write can be lost.
		// Reconcile before mutable requests: completed transfers reject manifest
		// writes, so replaying completion blindly would strand a valid upload.
		var current protocol.Transfer
		var statusErr error
		if j.Kind == "receive-upload" {
			current, statusErr = uploader.UploadStatus(ctx, j.ID)
		} else {
			current, statusErr = c.GetTransfer(ctx, j.ID)
		}
		if statusErr != nil {
			return statusErr
		}
		if j.Kind == "send" {
			if e := verifyUploadPolicy(*j, current); e != nil {
				_ = c.WithToken(j.Capability).RevokeTransfer(context.WithoutCancel(ctx), j.ID)
				return e
			}
		}
		if current.Status == "complete" {
			if len(j.Manifest) == 0 {
				return errors.New("server completed an upload absent from its durable journal")
			}
			j.Complete = true
			if e := s.Put(journalKey(j.OperationID), j); e != nil {
				return e
			}
		}
	}
	if !j.Complete {
		buffer := make([]byte, protocol.ChunkSize+protocol.FrameOverhead)
		for i := range j.Files {
			f := &j.Files[i]
			wire, e := clientcrypto.WireSize(f.Manifest.Size)
			if e != nil {
				return e
			}
			if f.Resource.URL == "" {
				f.Resource, e = uploader.CreateUpload(ctx, j.ID, wire)
				if e != nil {
					return e
				}
				f.Manifest.BlobID = f.Resource.FileID
				if e = s.Put(journalKey(j.OperationID), j); e != nil {
					return e
				}
			}
			offset, e := uploader.UploadOffset(ctx, f.Resource)
			if e != nil {
				return e
			}
			src, e := os.Open(f.Spool)
			if e != nil {
				return e
			}
			info, e := src.Stat()
			if e != nil || !info.Mode().IsRegular() || info.Size() != wire {
				src.Close()
				return errors.New("persisted ciphertext is missing or changed")
			}
			if _, e = src.Seek(offset, io.SeekStart); e != nil {
				src.Close()
				return e
			}
			if progress != nil {
				progress(offset, wire)
			}
			for offset < wire {
				if e = ctx.Err(); e != nil {
					src.Close()
					return e
				}
				size := min(int64(len(buffer)), wire-offset)
				n, re := io.ReadFull(src, buffer[:size])
				if re != nil {
					src.Close()
					return re
				}
				e = patchWithRetry(ctx, uploader, f.Resource, offset, buffer[:n])
				if e != nil {
					src.Close()
					return e
				}
				offset += int64(n)
				if progress != nil {
					progress(offset, wire)
				}
			}
			src.Close()
		}
		if len(j.Manifest) == 0 {
			m := protocol.Manifest{Files: make([]protocol.ManifestFile, 0, len(j.Files))}
			for _, f := range j.Files {
				m.Files = append(m.Files, f.Manifest)
			}
			var e error
			if j.Kind == "receive-upload" {
				j.Manifest, e = clientcrypto.SealReceiveManifest(j.SlotID, j.ID, j.PublicKey, j.Key, m)
			} else {
				j.Manifest, e = clientcrypto.EncryptManifest(j.Key, m)
			}
			if e != nil {
				return e
			}
			if e = s.Put(journalKey(j.OperationID), j); e != nil {
				return e
			}
		}
		if e := uploader.PutManifest(ctx, j.ID, j.Manifest); e != nil {
			return e
		}
		if e := uploader.Complete(ctx, j.ID); e != nil {
			return e
		}
		j.Complete = true
		if e := s.Put(journalKey(j.OperationID), j); e != nil {
			return e
		}
	}
	var status protocol.Transfer
	var e error
	if j.Kind == "receive-upload" {
		status, e = uploader.UploadStatus(ctx, j.ID)
	} else {
		status, e = c.GetTransfer(ctx, j.ID)
	}
	if e != nil {
		return e
	}
	if j.Kind == "send" {
		if e := verifyUploadPolicy(*j, status); e != nil {
			_ = c.WithToken(j.Capability).RevokeTransfer(context.WithoutCancel(ctx), j.ID)
			return e
		}
	}
	if status.Status != "complete" || (j.Kind != "receive-upload" && (!status.HasManifest || len(status.Files) != len(j.Files))) {
		return errors.New("server did not confirm completed transfer")
	}
	for _, f := range j.Files {
		if j.Kind == "receive-upload" {
			continue
		}
		matched := false
		for _, remote := range status.Files {
			if remote.ID == f.Manifest.BlobID && remote.Size == f.Resource.Size && remote.UploadComplete {
				matched = true
			}
		}
		if !matched {
			return errors.New("server completion files do not match upload")
		}
	}
	for _, f := range j.Files {
		if e = os.Remove(f.Spool); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	return nil
}
func patchWithRetry(ctx context.Context, c *Client, u UploadResource, offset int64, b []byte) error {
	for attempt := 0; attempt < 3; attempt++ {
		e := c.PatchUpload(ctx, u, offset, b)
		if e == nil {
			return nil
		}
		var api *APIError
		if errors.As(e, &api) && api.Status < 500 && api.Status != 429 {
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt == 2 {
			return e
		}
		delay := time.Duration(100*(1<<attempt)) * time.Millisecond
		if errors.As(e, &api) && api.RetryAfter > delay {
			delay = api.RetryAfter
		}
		if delay > 2*time.Second {
			return e
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		got, he := c.UploadOffset(ctx, u)
		if he != nil {
			return he
		}
		if got == offset+int64(len(b)) {
			return nil
		}
		if got != offset {
			return errors.New("server offset does not match persisted ciphertext")
		}
	}
	return ErrProtocol
}
func (c *Client) Upload(ctx context.Context, l protocol.Link, files []InputFile, s Store) (UploadResult, error) {
	return c.UploadWithOptions(ctx, l, files, SendOptions{}, s)
}

func (c *Client) UploadWithOptions(ctx context.Context, l protocol.Link, files []InputFile, opt SendOptions, s Store) (UploadResult, error) {
	if opt.OperationID != "" {
		return c.ResumeUpload(ctx, opt.OperationID, s, opt.Progress)
	}
	if l.Kind != "receive" || l.Version != 2 {
		return UploadResult{}, errors.New("Receive-v2 invitation required")
	}
	if e := requireStore(s); e != nil {
		return UploadResult{}, e
	}
	if e := validateInputs(files); e != nil {
		return UploadResult{}, e
	}
	guest, e := New(l.Origin, c.http, nil)
	if e != nil {
		return UploadResult{}, e
	}
	if e := guest.preflightFiles(ctx, files); e != nil {
		return UploadResult{}, e
	}
	v, e := guest.Availability(ctx, l.ID)
	if e != nil {
		return UploadResult{}, e
	}
	if e = validatePublicSlot(v, l); e != nil {
		return UploadResult{}, e
	}
	if v.RemainingFiles != nil && *v.RemainingFiles < int64(len(files)) {
		return UploadResult{}, &PolicyError{Code: "receive_file_limit", Message: "The invitation cannot accept this many files."}
	}
	key, e := clientcrypto.NewKey()
	if e != nil {
		return UploadResult{}, e
	}
	defer clear(key)
	op, e := randomID()
	if e != nil {
		return UploadResult{}, e
	}
	j := uploadJournal{Origin: l.Origin, Kind: "receive-upload", OperationID: op, Key: key, PublicKey: l.Key, SlotID: l.ID}
	if e = s.Put(journalKey(op), j); e != nil {
		return UploadResult{}, e
	}
	created, e := guest.CreateGuestTransfer(ctx, l.ID)
	if e != nil {
		return UploadResult{}, e
	}
	j.ID = created.ID
	j.ExpiresAt = created.ExpiresAt
	j.Capability = created.DeleteToken
	if e = s.Put(journalKey(op), j); e != nil {
		_ = guest.WithToken(j.Capability).RevokeTransfer(context.WithoutCancel(ctx), j.ID)
		return UploadResult{}, e
	}
	j.Files, e = prepareCiphertext(ctx, files, key, opt.SpoolDir)
	if e != nil {
		_ = guest.WithToken(j.Capability).RevokeTransfer(context.WithoutCancel(ctx), j.ID)
		return UploadResult{}, e
	}
	if e = s.Put(journalKey(op), j); e != nil {
		for _, f := range j.Files {
			_ = os.Remove(f.Spool)
		}
		_ = guest.WithToken(j.Capability).RevokeTransfer(context.WithoutCancel(ctx), j.ID)
		return UploadResult{}, e
	}
	if e = guest.finishUpload(ctx, &j, s, opt.Progress); e != nil {
		return UploadResult{}, &OperationError{op, e}
	}
	return UploadResult{ID: j.ID, Origin: l.Origin, Status: "complete", OperationID: op}, nil
}

// ReceivePublicKey recovers the public invitation key without exposing private
// key material. Store implementations can use this for encrypted key backups.
func ReceivePublicKey(private []byte) ([]byte, error) {
	k, e := ecdh.X25519().NewPrivateKey(private)
	if e != nil {
		return nil, errors.New("invalid receive private key")
	}
	return k.PublicKey().Bytes(), nil
}
func checkpointID(origin, id, dir string) string {
	sum := sha256.Sum256([]byte(origin + "\x00" + id + "\x00" + dir))
	return "operation/download/" + hex.EncodeToString(sum[:])
}

func policyMismatch() *PolicyError {
	return &PolicyError{Code: "requested_policy_unsupported", Message: "The server did not preserve the requested expiration or download policy; the link was not published."}
}
func acceptedExpiry(created protocol.CreatedTransfer, requestedSeconds int) error {
	if created.ExpiresAt.IsZero() || created.ServerTime.IsZero() || !created.ExpiresAt.After(created.ServerTime) {
		return policyMismatch()
	}
	// HTTP Date has whole-second precision. Processing/transport latency may only
	// shorten the remaining duration; two seconds accommodate date rounding.
	if requestedSeconds > 0 && created.ExpiresAt.After(created.ServerTime.Add(time.Duration(requestedSeconds)*time.Second+2*time.Second)) {
		return policyMismatch()
	}
	return nil
}
func verifyUploadPolicy(j uploadJournal, metadata protocol.Transfer) error {
	if metadata.MaxDownloads != j.RequestedMaxDownloads || j.ExpiresAt.IsZero() || !metadata.ExpiresAt.Equal(j.ExpiresAt) {
		return policyMismatch()
	}
	return nil
}
