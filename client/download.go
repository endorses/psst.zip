package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	clientcrypto "github.com/endorses/psst.zip/client/internal/crypto"
	"github.com/endorses/psst.zip/client/protocol"
)

type completedFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type pendingFile struct {
	Temp           string `json:"temp"`
	BaseName       string `json:"base_name"`
	ReadyToPublish bool   `json:"ready_to_publish"`
	completedFile
}

type downloadJournal struct {
	Origin      string                   `json:"origin"`
	ID          string                   `json:"id"`
	Directory   string                   `json:"directory"`
	Files       map[string]completedFile `json:"files"`
	Pending     map[string]pendingFile   `json:"pending"`
	Manifest    protocol.Manifest        `json:"manifest"`
	KeyDigest   string                   `json:"key_digest"`
	AllSaved    bool                     `json:"all_saved"`
	ReceiptSent bool                     `json:"receipt_sent"`
}
type ReceiptError struct{ Cause error }

func (e *ReceiptError) Error() string {
	return "files were saved; completion receipt is pending, repeat fetch to retry without downloading again"
}
func (e *ReceiptError) Unwrap() error { return e.Cause }
func (c *Client) ShowTransfer(ctx context.Context, id string, s Store) (Share, error) {
	return c.Show(ctx, "send", id, s)
}
func (c *Client) ShowReceive(ctx context.Context, id string, s Store) (Share, error) {
	return c.Show(ctx, "receive", id, s)
}

// Fetch downloads a public Send capability anonymously, even when called on an
// account client, and writes authenticated files without overwriting destinations.
func (c *Client) Fetch(ctx context.Context, l protocol.Link, dir string, s Store) (DownloadResult, error) {
	if l.Kind != "send" || len(l.Key) != 32 || !protocol.ValidID(l.ID) {
		return DownloadResult{}, protocol.ErrInvalidLink
	}
	guest, e := New(l.Origin, c.http, nil)
	if e != nil {
		return DownloadResult{}, e
	}
	return guest.fetchTransfer(ctx, l.ID, l.Key, "", nil, dir, s)
}
func (c *Client) fetchTransfer(ctx context.Context, id string, key []byte, slot string, private []byte, dir string, s Store) (DownloadResult, error) {
	result := DownloadResult{ID: id, Files: []string{}}
	if e := requireStore(s); e != nil {
		return result, e
	}
	absolute, e := filepath.Abs(dir)
	if e != nil {
		return result, e
	}
	if e = os.MkdirAll(absolute, 0700); e != nil {
		return result, e
	}
	root, e := os.OpenRoot(absolute)
	if e != nil {
		return result, e
	}
	defer root.Close()
	identityKey := key
	if slot != "" {
		identityKey = private
	}
	identity := sha256.Sum256(identityKey)
	digest := hex.EncodeToString(identity[:])
	jk := checkpointID(c.origin, id, absolute)
	j := downloadJournal{Origin: c.origin, ID: id, Directory: absolute, Files: map[string]completedFile{}, Pending: map[string]pendingFile{}, KeyDigest: digest}
	e = s.Get(jk, &j)
	if e != nil && !errors.Is(e, ErrNotFound) {
		return result, e
	}
	if j.Origin != c.origin || j.ID != id || j.Directory != absolute || j.KeyDigest != digest || j.Files == nil || j.Pending == nil {
		return result, ErrCheckpointInvalid
	}
	for blob, p := range j.Pending {
		if !protocol.ValidID(blob) || !validTemporaryName(p.Temp) {
			return result, ErrCheckpointInvalid
		}
		if p.SHA256 == "" {
			_ = root.Remove(p.Temp)
			delete(j.Pending, blob)
			if e = s.Put(jk, j); e != nil {
				return result, e
			}
			continue
		}
		if _, e = publishPending(ctx, root, &j, blob, jk, s); e != nil {
			return result, e
		}
	}
	for _, saved := range j.Files {
		if !validFilename(saved.Name) || saved.Size < 0 || len(saved.SHA256) != 64 {
			return result, ErrCheckpointInvalid
		}
		if e = verifySaved(ctx, root, saved); e != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, ErrCheckpointInvalid
		}
		result.Files = append(result.Files, filepath.Join(absolute, saved.Name))
	}
	if len(j.Manifest.Files) > 0 && allFilesSaved(j) {
		j.AllSaved = true
		if e = s.Put(jk, j); e != nil {
			return result, e
		}
	}
	if j.AllSaved {
		return c.finishReceipt(ctx, result, &j, jk, s)
	}
	metadata, e := c.GetTransfer(ctx, id)
	if e != nil {
		return result, e
	}
	if metadata.Status != "complete" || !metadata.HasManifest {
		return result, errors.New("transfer is not complete")
	}
	manifest := j.Manifest
	if len(manifest.Files) == 0 {
		ciphertext, e := c.GetManifest(ctx, id)
		if e != nil {
			return result, e
		}
		if slot != "" {
			key, manifest, e = clientcrypto.OpenReceiveManifest(slot, id, private, ciphertext)
			defer clear(key)
		} else {
			manifest, e = clientcrypto.DecryptManifest(key, ciphertext)
		}
		if e != nil {
			return result, e
		}
		j.Manifest = manifest
	} else if slot != "" {
		ciphertext, e := c.GetManifest(ctx, id)
		if e != nil {
			return result, e
		}
		key, _, e = clientcrypto.OpenReceiveManifest(slot, id, private, ciphertext)
		defer clear(key)
		if e != nil {
			return result, e
		}
	}
	if e = validateDownloadedManifest(manifest, metadata); e != nil {
		return result, e
	}
	if e = s.Put(jk, j); e != nil {
		return result, e
	}
	for _, f := range manifest.Files {
		if _, ok := j.Files[f.BlobID]; ok {
			continue
		}
		if e = ctx.Err(); e != nil {
			return result, e
		}
		wire, e := clientcrypto.WireSize(f.Size)
		if e != nil {
			return result, e
		}
		random, e := randomID()
		if e != nil {
			return result, e
		}
		temp := ".psst-" + random + ".partial"
		dst, e := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return result, e
		}
		j.Pending[f.BlobID] = pendingFile{Temp: temp, BaseName: f.Name}
		if e = s.Put(jk, j); e != nil {
			dst.Close()
			root.Remove(temp)
			return result, e
		}
		body, e := c.OpenFile(ctx, id, f.BlobID, wire)
		if e != nil {
			dst.Close()
			root.Remove(temp)
			delete(j.Pending, f.BlobID)
			_ = s.Put(jk, j)
			return result, e
		}
		hash := sha256.New()
		e = clientcrypto.DecryptFile(ctx, io.MultiWriter(dst, hash), body, key, f)
		body.Close()
		if e == nil {
			e = dst.Sync()
		}
		ce := dst.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			root.Remove(temp)
			delete(j.Pending, f.BlobID)
			_ = s.Put(jk, j)
			return result, e
		}
		j.Pending[f.BlobID] = pendingFile{Temp: temp, BaseName: f.Name, completedFile: completedFile{Name: f.Name, Size: f.Size, SHA256: hex.EncodeToString(hash.Sum(nil))}}
		if e = s.Put(jk, j); e != nil {
			return result, e
		}
		name, e := publishPending(ctx, root, &j, f.BlobID, jk, s)
		if e != nil {
			return result, e
		}
		result.Files = append(result.Files, filepath.Join(absolute, name))
	}
	j.AllSaved = true
	if e = s.Put(jk, j); e != nil {
		return result, e
	}
	return c.finishReceipt(ctx, result, &j, jk, s)
}
func allFilesSaved(j downloadJournal) bool {
	if len(j.Files) != len(j.Manifest.Files) {
		return false
	}
	for _, f := range j.Manifest.Files {
		if saved, ok := j.Files[f.BlobID]; !ok || saved.Size != f.Size {
			return false
		}
	}
	return true
}
func validTemporaryName(name string) bool {
	return strings.HasPrefix(name, ".psst-") && strings.HasSuffix(name, ".partial") && protocol.ValidID(strings.TrimSuffix(strings.TrimPrefix(name, ".psst-"), ".partial"))
}

// The intent is saved before Link. On restart either the retained authenticated
// temporary file or the exclusively published final file completes this step;
// the same transfer never consumes a new download merely to retry a journal write.
func publishPending(ctx context.Context, root *os.Root, j *downloadJournal, blob, jk string, s Store) (string, error) {
	p := j.Pending[blob]
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if !validFilename(p.Name) || !validFilename(p.BaseName) || len(p.SHA256) != 64 {
		return "", ErrCheckpointInvalid
	}
	if p.ReadyToPublish && verifySaved(ctx, root, p.completedFile) == nil {
		_ = root.Remove(p.Temp)
		if e := syncDestinationDirectory(root); e != nil {
			return "", e
		}
		j.Files[blob] = p.completedFile
		delete(j.Pending, blob)
		if e := s.Put(jk, j); e != nil {
			return "", e
		}
		return p.Name, nil
	}
	if e := ctx.Err(); e != nil {
		return "", e
	}
	tempRecord := p.completedFile
	tempRecord.Name = p.Temp
	if e := verifySaved(ctx, root, tempRecord); e != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrCheckpointInvalid
	}
	ext := filepath.Ext(p.BaseName)
	stem := strings.TrimSuffix(p.BaseName, ext)
	for index := 0; index < 10000; index++ {
		candidate := p.Name
		if index > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, index, ext)
		}
		if len(candidate) > 255 {
			return "", errors.New("download filename is too long for collision-safe publication")
		}
		// Never adopt a pre-existing same-content path as a new publication.
		if _, statErr := root.Lstat(candidate); statErr == nil {
			continue
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		p.Name = candidate
		p.ReadyToPublish = true
		j.Pending[blob] = p
		if e := s.Put(jk, j); e != nil {
			return "", e
		}
		e := root.Link(p.Temp, candidate)
		if errors.Is(e, os.ErrExist) {
			continue
		}
		if e != nil {
			return "", e
		}
		if e = root.Remove(p.Temp); e != nil {
			return "", e
		}
		e = syncDestinationDirectory(root)
		if e != nil {
			return "", e
		}
		j.Files[blob] = p.completedFile
		delete(j.Pending, blob)
		if e = s.Put(jk, j); e != nil {
			return "", e
		}
		return candidate, nil
	}
	return "", errors.New("too many filename collisions")
}
func validateDownloadedManifest(m protocol.Manifest, t protocol.Transfer) error {
	if len(m.Files) != len(t.Files) {
		return errors.New("manifest does not match transfer files")
	}
	remote := make(map[string]protocol.FileInfo, len(t.Files))
	for _, f := range t.Files {
		remote[f.ID] = f
	}
	for _, f := range m.Files {
		if !validFilename(f.Name) {
			return errors.New("manifest contains an unsafe filename")
		}
		wire, e := clientcrypto.WireSize(f.Size)
		if e != nil {
			return e
		}
		v, ok := remote[f.BlobID]
		if !ok || v.Size != wire || !v.UploadComplete {
			return errors.New("manifest file does not match transfer metadata")
		}
	}
	return nil
}
func verifySaved(ctx context.Context, root *os.Root, saved completedFile) error {
	lstat, e := root.Lstat(saved.Name)
	if e != nil {
		return e
	}
	if !lstat.Mode().IsRegular() {
		return errors.New("saved output is not a regular file")
	}
	f, e := root.Open(saved.Name)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != saved.Size {
		return errors.New("saved file changed")
	}
	h := sha256.New()
	if _, e = io.Copy(h, io.LimitReader(contextReader{ctx: ctx, r: f}, saved.Size+1)); e != nil {
		return e
	}
	if hex.EncodeToString(h.Sum(nil)) != saved.SHA256 {
		return errors.New("saved file changed")
	}
	return nil
}

func (c *Client) finishReceipt(ctx context.Context, result DownloadResult, j *downloadJournal, jk string, s Store) (DownloadResult, error) {
	result.Files = result.Files[:0]
	for _, file := range j.Manifest.Files {
		if saved, ok := j.Files[file.BlobID]; ok {
			result.Files = append(result.Files, filepath.Join(j.Directory, saved.Name))
		}
	}
	if j.ReceiptSent {
		return result, nil
	}
	e := c.Acknowledge(ctx, j.ID)
	if e != nil {
		result.ReceiptPending = true
		return result, &ReceiptError{Cause: e}
	}
	j.ReceiptSent = true
	if e = s.Put(jk, j); e != nil {
		result.ReceiptPending = true
		return result, e
	}
	return result, nil
}

// FetchInbox checks exact authoritative membership for every submission before
// decrypting it. A login cannot recover a private key absent from this Store.
func (c *Client) FetchInbox(ctx context.Context, id, dir string, s Store) ([]DownloadResult, error) {
	if e := requireStore(s); e != nil {
		return nil, e
	}
	var local resourceRecord
	if e := s.Get(resourceKey("receive", id), &local); e != nil {
		if errors.Is(e, ErrNotFound) {
			return nil, ErrMissingKey
		}
		return nil, e
	}
	if local.Origin != c.origin || local.ID != id || len(local.Key) != 32 || len(local.PublicKey) != 32 {
		return nil, errors.New("Receive private key identity mismatch")
	}
	after := ""
	seenCursors := map[string]bool{}
	seenTransfers := map[string]bool{}
	results := []DownloadResult{}
	for pageCount := 0; pageCount < 100; pageCount++ {
		page, e := c.GetInboxPage(ctx, id, after, 100)
		if e != nil {
			return results, e
		}
		if page.ReceiveProtocol != 2 || !keyMatches(page.RecipientPublicKey, local.PublicKey) {
			return results, errors.New("inbox public key does not match local private key")
		}
		for _, item := range page.Transfers {
			if seenTransfers[item.TransferID] {
				return results, ErrProtocol
			}
			seenTransfers[item.TransferID] = true
			if item.Status != "complete" {
				continue
			}
			member, e := c.Membership(ctx, id, item.TransferID)
			if e != nil {
				return results, e
			}
			if member.ReceiveProtocol != 2 || !keyMatches(member.RecipientPublicKey, local.PublicKey) {
				return results, errors.New("submission membership policy mismatch")
			}
			r, e := c.fetchTransfer(ctx, item.TransferID, nil, id, local.Key, filepath.Join(dir, item.TransferID), s)
			results = append(results, r)
			if e != nil {
				return results, e
			}
		}
		if page.NextCursor == nil {
			return results, nil
		}
		after = *page.NextCursor
		if seenCursors[after] {
			return results, ErrProtocol
		}
		seenCursors[after] = true
	}
	return results, errors.New("inbox exceeds bounded traversal; use explicit pages")
}

// ResumeUpload retries a guest upload using its child-only capability and the
// exact encrypted spool retained before interruption; it never uses the account.
func (c *Client) ResumeUpload(ctx context.Context, op string, s Store, progress Progress) (UploadResult, error) {
	if e := requireStore(s); e != nil {
		return UploadResult{}, e
	}
	if !protocol.ValidID(op) {
		return UploadResult{}, errors.New("invalid operation ID")
	}
	var j uploadJournal
	if e := s.Get(journalKey(op), &j); e != nil {
		return UploadResult{}, e
	}
	if j.Origin != c.origin || j.Kind != "receive-upload" || j.OperationID != op || !protocol.ValidID(j.ID) || len(j.Files) == 0 {
		return UploadResult{}, errors.New("invalid guest upload journal")
	}
	guest, e := New(j.Origin, c.http, nil)
	if e != nil {
		return UploadResult{}, e
	}
	if e = guest.finishUpload(ctx, &j, s, progress); e != nil {
		return UploadResult{}, &OperationError{op, e}
	}
	return UploadResult{ID: j.ID, Origin: j.Origin, Status: "complete", OperationID: op}, nil
}

// Go cannot fsync a Windows directory handle. File.Sync and exclusive atomic
// publication are retained there; Unix also flushes the directory entry.
func syncDestinationDirectory(root *os.Root) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, e := root.Open(".")
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}
