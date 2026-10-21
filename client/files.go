package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	clientcrypto "github.com/endorses/psst.zip/client/internal/crypto"
	"github.com/endorses/psst.zip/client/protocol"
)

// UploadResource identifies one exact, origin-bound tus resource.
type UploadResource struct {
	URL    string
	FileID string
	Size   int64
}

func (c *Client) uploadPath(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", ErrProtocol
	}
	if u.IsAbs() {
		o, e := protocol.NormalizeOrigin(u.Scheme + "://" + u.Host)
		if e != nil || o != c.origin {
			return "", errors.New("upload location left configured server origin")
		}
	} else if u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		return "", ErrProtocol
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 7 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "transfers" || parts[5] != "files" || !protocol.ValidID(parts[4]) || !protocol.ValidID(parts[6]) {
		return "", ErrProtocol
	}
	return u.Path, nil
}
func (c *Client) CreateUpload(ctx context.Context, transfer string, size int64) (UploadResource, error) {
	var v UploadResource
	p, e := idPath("transfers", transfer)
	if e != nil {
		return v, e
	}
	if size < 0 || size > protocol.MaxFileSize+32*1024*1024 {
		return v, errors.New("invalid encrypted size")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	h := http.Header{"Tus-Resumable": {"1.0.0"}, "Upload-Length": {strconv.FormatInt(size, 10)}}
	resp, e := c.request(ctx, "POST", p+"/files", nil, h)
	if e != nil {
		return v, e
	}
	if resp.StatusCode != 201 {
		return v, responseError(resp)
	}
	defer resp.Body.Close()
	path, e := c.uploadPath(resp.Header.Get("Location"))
	if e != nil || !strings.HasPrefix(path, p+"/files/") {
		return v, ErrProtocol
	}
	_, e = readBounded(resp.Body, 4096)
	if e != nil {
		return v, e
	}
	return UploadResource{URL: c.origin + path, FileID: path[strings.LastIndex(path, "/")+1:], Size: size}, nil
}
func (c *Client) UploadOffset(ctx context.Context, u UploadResource) (int64, error) {
	p, e := c.uploadPath(u.URL)
	if e != nil {
		return 0, e
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, e := c.request(ctx, "HEAD", p, nil, http.Header{"Tus-Resumable": {"1.0.0"}})
	if e != nil {
		return 0, e
	}
	if resp.StatusCode != 200 {
		return 0, responseError(resp)
	}
	defer resp.Body.Close()
	offset, e := strconv.ParseInt(resp.Header.Get("Upload-Offset"), 10, 64)
	if e != nil || offset < 0 || offset > u.Size {
		return 0, ErrProtocol
	}
	return offset, nil
}

// PatchUpload sends at most one encrypted frame. The caller may resume only from
// the same durable ciphertext; a freshly encrypted stream has different nonces.
func (c *Client) PatchUpload(ctx context.Context, u UploadResource, offset int64, data []byte) error {
	if offset < 0 || len(data) < 1 || len(data) > protocol.ChunkSize+protocol.FrameOverhead || int64(len(data)) > u.Size-offset {
		return errors.New("invalid upload frame")
	}
	p, e := c.uploadPath(u.URL)
	if e != nil {
		return e
	}
	h := http.Header{"Tus-Resumable": {"1.0.0"}, "Upload-Offset": {strconv.FormatInt(offset, 10)}, "Content-Type": {"application/offset+octet-stream"}}
	resp, e := c.request(ctx, "PATCH", p, bytes.NewReader(data), h)
	if e != nil {
		return e
	}
	if resp.StatusCode != 204 {
		return responseError(resp)
	}
	defer resp.Body.Close()
	got, e := strconv.ParseInt(resp.Header.Get("Upload-Offset"), 10, 64)
	if e != nil || got != offset+int64(len(data)) {
		return ErrProtocol
	}
	_, e = readBounded(resp.Body, 4096)
	return e
}
func (c *Client) PutManifest(ctx context.Context, id string, b []byte) error {
	if len(b) < 28 || len(b) > protocol.MaxManifestBytes {
		return errors.New("invalid encrypted manifest size")
	}
	p, e := idPath("transfers", id)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, e := c.request(ctx, "POST", p+"/manifest", bytes.NewReader(b), http.Header{"Content-Type": {"application/octet-stream"}})
	if e != nil {
		return e
	}
	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		return responseError(resp)
	}
	defer resp.Body.Close()
	_, e = readBounded(resp.Body, 4096)
	return e
}
func (c *Client) GetManifest(ctx context.Context, id string) ([]byte, error) {
	p, e := idPath("transfers", id)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, e := c.request(ctx, "GET", p+"/manifest", nil, nil)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode != 200 {
		return nil, responseError(resp)
	}
	defer resp.Body.Close()
	return readBounded(resp.Body, protocol.MaxManifestBytes)
}

// OpenFile returns a streaming ciphertext response; callers must close it and
// enforce the manifest's exact length and authenticated framing before publishing.
func (c *Client) OpenFile(ctx context.Context, transfer, file string, wireSize int64) (io.ReadCloser, error) {
	p, e := filePath(transfer, file)
	if e != nil {
		return nil, e
	}
	resp, e := c.request(ctx, "GET", p, nil, nil)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode != 200 {
		return nil, responseError(resp)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != wireSize {
		resp.Body.Close()
		return nil, ErrProtocol
	}
	return resp.Body, nil
}

// DownloadFileTo streams and authenticates one file from a previously verified
// manifest. destination must be unpublished (for example, a temporary file or
// transactional writer): an authenticated early frame does not prove that the
// complete stream is valid. The caller owns syncing and committing the result
// after this method succeeds and must separately call Acknowledge only after all
// required files have been durably saved. This method never sends a receipt.
func (c *Client) DownloadFileTo(ctx context.Context, transferID string, file protocol.ManifestFile, key []byte, destination io.Writer) error {
	if destination == nil {
		return errors.New("an unpublished destination writer is required")
	}
	if len(key) != 32 {
		return clientcrypto.ErrInvalidData
	}
	if err := clientcrypto.ValidateManifest(protocol.Manifest{Files: []protocol.ManifestFile{file}}); err != nil {
		return err
	}
	wire, err := clientcrypto.WireSize(file.Size)
	if err != nil {
		return err
	}
	body, err := c.OpenFile(ctx, transferID, file.BlobID, wire)
	if err != nil {
		return err
	}
	defer body.Close()
	return clientcrypto.DecryptFile(ctx, destination, body, key, file)
}

// ReadManifest authenticates a public Send link's manifest and checks its blob
// identities and sizes against the server metadata. It never looks up account
// credentials, even when the link belongs to another server. Low-level callers
// can create an anonymous Client at link.Origin, use DownloadFileTo for each
// returned file and acknowledge only after committing every destination.
func (c *Client) ReadManifest(ctx context.Context, link protocol.Link) (protocol.Manifest, error) {
	var manifest protocol.Manifest
	if link.Kind != "send" || len(link.Key) != 32 || !protocol.ValidID(link.ID) {
		return manifest, protocol.ErrInvalidLink
	}
	guest, err := New(link.Origin, c.http, nil)
	if err != nil {
		return manifest, err
	}
	metadata, err := guest.GetTransfer(ctx, link.ID)
	if err != nil {
		return manifest, err
	}
	if metadata.Status != "complete" || !metadata.HasManifest {
		return manifest, &PolicyError{Code: "transfer_incomplete", Message: "The transfer is not complete."}
	}
	encrypted, err := guest.GetManifest(ctx, link.ID)
	if err != nil {
		return manifest, err
	}
	manifest, err = clientcrypto.DecryptManifest(link.Key, encrypted)
	if err != nil {
		return protocol.Manifest{}, err
	}
	if err = validateDownloadedManifest(manifest, metadata); err != nil {
		return protocol.Manifest{}, err
	}
	return manifest, nil
}
