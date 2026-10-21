package client

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/endorses/psst.zip/client/protocol"
)

var cursorPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,512}$`)

func validCursor(s string) bool { return s == "" || cursorPattern.MatchString(s) }
func pageQuery(after string, limit int) (string, error) {
	if limit < 1 || limit > 100 || !validCursor(after) {
		return "", errors.New("invalid page limit or cursor")
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if after != "" {
		q.Set("after", after)
	}
	return q.Encode(), nil
}
func idPath(kind, id string) (string, error) {
	if !protocol.ValidID(id) {
		return "", errors.New("invalid resource ID")
	}
	return "/api/v1/" + kind + "/" + strings.ToLower(id), nil
}
func (c *Client) CreateTransfer(ctx context.Context, in protocol.CreateTransferRequest) (protocol.CreatedTransfer, error) {
	var v protocol.CreatedTransfer
	if in.ExpiresInSeconds < 0 || in.ExpiresInSeconds > math.MaxInt32 || in.MaxDownloads < 0 || in.MaxDownloads > math.MaxInt32 {
		return v, errors.New("invalid transfer policy")
	}
	var headers http.Header
	e := c.controlHeaders(ctx, "POST", "/api/v1/transfers", in, &v, &headers, 201)
	v.ServerTime = responseTime(headers)
	if e == nil && (!protocol.ValidID(v.ID) || !protocol.ValidToken(v.DeleteToken)) {
		e = ErrProtocol
	}
	return v, e
}
func (c *Client) GetTransfer(ctx context.Context, id string) (protocol.Transfer, error) {
	var v protocol.Transfer
	p, e := idPath("transfers", id)
	if e != nil {
		return v, e
	}
	e = c.control(ctx, "GET", p, nil, &v, 200)
	if e == nil {
		e = validateTransfer(v, id)
	}
	return v, e
}
func (c *Client) UploadStatus(ctx context.Context, id string) (protocol.Transfer, error) {
	var v protocol.Transfer
	p, e := idPath("transfers", id)
	if e != nil {
		return v, e
	}
	e = c.control(ctx, "GET", p+"/upload-status", nil, &v, 200)
	if e == nil {
		e = validateTransfer(v, id)
	}
	return v, e
}
func validateTransfer(v protocol.Transfer, id string) error {
	if v.ID != strings.ToLower(id) || len(v.Files) > protocol.MaxFiles || v.FileCount < 0 || v.TotalSize < 0 {
		return ErrProtocol
	}
	seen := map[string]bool{}
	for _, f := range v.Files {
		if !protocol.ValidID(f.ID) || seen[f.ID] || f.Size < 0 || f.Size > protocol.MaxFileSize+32*1024*1024 || f.UploadOffset < 0 || f.UploadOffset > f.Size {
			return ErrProtocol
		}
		seen[f.ID] = true
	}
	return nil
}
func (c *Client) RevokeTransfer(ctx context.Context, id string) error {
	p, e := idPath("transfers", id)
	if e != nil {
		return e
	}
	return c.control(ctx, "DELETE", p, nil, nil, 204, 404, 410)
}
func (c *Client) RevokeReceive(ctx context.Context, id string) error {
	p, e := idPath("slots", id)
	if e != nil {
		return e
	}
	return c.control(ctx, "DELETE", p, nil, nil, 204, 404, 410)
}
func (c *Client) ListResources(ctx context.Context, kind, after string, limit int) (protocol.HistoryPage, error) {
	var v protocol.HistoryPage
	q, e := pageQuery(after, limit)
	if e != nil {
		return v, e
	}
	if kind != "" && kind != "transfer" && kind != "slot" {
		return v, errors.New("invalid resource kind")
	}
	if kind != "" {
		q += "&kind=" + kind
	}
	e = c.control(ctx, "GET", "/api/v1/auth/resources?"+q, nil, &v, 200)
	if e == nil {
		if !v.Paginated || len(v.Transfers)+len(v.Slots) > limit || (v.NextCursor != nil && (!validCursor(*v.NextCursor) || *v.NextCursor == after || *v.NextCursor == "")) {
			e = ErrProtocol
		}
		seen := map[string]bool{}
		for _, t := range v.Transfers {
			if !protocol.ValidID(t.ID) || seen[t.ID] || kind == "slot" {
				e = ErrProtocol
			}
			seen[t.ID] = true
		}
		for _, s := range v.Slots {
			if !protocol.ValidID(s.ID) || seen[s.ID] || kind == "transfer" {
				e = ErrProtocol
			}
			seen[s.ID] = true
		}
	}
	return v, e
}
func (c *Client) CreateSlot(ctx context.Context, in protocol.CreateReceiveRequest) (protocol.CreatedTransfer, error) {
	var v protocol.CreatedTransfer
	pub, e := base64.RawURLEncoding.Strict().DecodeString(in.RecipientPublicKey)
	if e != nil || len(pub) != 32 || in.ReceiveProtocol != 2 || in.MaxFiles < 0 || in.MaxFiles > math.MaxInt32 || in.ExpiresInSeconds < 0 || in.ExpiresInSeconds > math.MaxInt32 {
		return v, errors.New("invalid receive policy")
	}
	var headers http.Header
	e = c.controlHeaders(ctx, "POST", "/api/v1/slots", in, &v, &headers, 201)
	v.ServerTime = responseTime(headers)
	if e == nil && (!protocol.ValidID(v.ID) || !protocol.ValidToken(v.DeleteToken)) {
		e = ErrProtocol
	}
	return v, e
}
func (c *Client) GetInboxPage(ctx context.Context, id, after string, limit int) (protocol.InboxPage, error) {
	var v protocol.InboxPage
	p, e := idPath("slots", id)
	if e != nil {
		return v, e
	}
	q, e := pageQuery(after, limit)
	if e != nil {
		return v, e
	}
	e = c.control(ctx, "GET", p+"/inbox?"+q, nil, &v, 200)
	if e == nil {
		if v.ID != strings.ToLower(id) || !v.Paginated || len(v.Transfers) > limit || (v.NextCursor != nil && (!validCursor(*v.NextCursor) || *v.NextCursor == after || *v.NextCursor == "")) {
			e = ErrProtocol
		}
		seen := map[string]bool{}
		for _, t := range v.Transfers {
			if !protocol.ValidID(t.TransferID) || seen[t.TransferID] {
				e = ErrProtocol
			}
			seen[t.TransferID] = true
		}
	}
	return v, e
}
func (c *Client) Membership(ctx context.Context, slot, transfer string) (protocol.Membership, error) {
	var v protocol.Membership
	p, e := idPath("slots", slot)
	if e != nil || !protocol.ValidID(transfer) {
		return v, errors.New("invalid resource ID")
	}
	e = c.control(ctx, "GET", p+"/transfers/"+strings.ToLower(transfer)+"/membership", nil, &v, 200)
	if e == nil && (v.SlotID != strings.ToLower(slot) || v.TransferID != strings.ToLower(transfer)) {
		e = ErrProtocol
	}
	return v, e
}
func (c *Client) Availability(ctx context.Context, id string) (protocol.Availability, error) {
	var v protocol.Availability
	p, e := idPath("slots", id)
	if e != nil {
		return v, e
	}
	var headers http.Header
	e = c.Anonymous().controlHeaders(ctx, "GET", p+"/availability", nil, &v, &headers, 200)
	v.ServerTime = responseTime(headers)
	if e == nil && v.ID != strings.ToLower(id) {
		e = ErrProtocol
	}
	return v, e
}

// CreateGuestTransfer deliberately drops any account bearer before reserving a
// Receive child. Only the returned child capability authorizes subsequent writes.
func (c *Client) CreateGuestTransfer(ctx context.Context, slot string) (protocol.CreatedTransfer, error) {
	var v protocol.CreatedTransfer
	p, e := idPath("slots", slot)
	if e != nil {
		return v, e
	}
	e = c.Anonymous().control(ctx, "POST", p+"/transfers", struct{}{}, &v, 201)
	if e == nil && (!protocol.ValidID(v.ID) || !protocol.ValidToken(v.DeleteToken)) {
		e = ErrProtocol
	}
	return v, e
}
func (c *Client) Complete(ctx context.Context, id string) error {
	p, e := idPath("transfers", id)
	if e != nil {
		return e
	}
	return c.control(ctx, "POST", p+"/complete", nil, nil, 204, 200)
}
func (c *Client) Acknowledge(ctx context.Context, id string) error {
	p, e := idPath("transfers", id)
	if e != nil {
		return e
	}
	return c.control(ctx, "POST", p+"/downloaded", nil, nil, 204)
}
func keyMatches(value string, key []byte) bool {
	return value == base64.RawURLEncoding.EncodeToString(key)
}
func validatePublicSlot(v protocol.Availability, l protocol.Link) error {
	if v.ID != l.ID || v.ReceiveProtocol != 2 || !keyMatches(v.RecipientPublicKey, l.Key) {
		return errors.New("receive invitation public key does not match server policy")
	}
	if v.ExpiresAt.IsZero() {
		return ErrProtocol
	}
	if v.ServerTime.IsZero() {
		return ErrProtocol
	}
	if !v.ExpiresAt.After(v.ServerTime) {
		return &PolicyError{Code: "link_expired", Message: "The Receive invitation has expired."}
	}
	if !v.Available {
		return &PolicyError{Code: "receive_unavailable", Message: "The Receive invitation is unavailable."}
	}
	return nil
}
func filePath(transfer, file string) (string, error) {
	p, e := idPath("transfers", transfer)
	if e != nil || !protocol.ValidID(file) {
		return "", fmt.Errorf("invalid file identity")
	}
	return p + "/files/" + strings.ToLower(file), nil
}

// Date is server-clock evidence for expiry checks, avoiding an assumption that
// client/server clocks agree. Servers omitting Date fall back to local receipt
// time; such servers require normally synchronized clocks for safe publication.
func responseTime(headers http.Header) time.Time {
	if t, e := http.ParseTime(headers.Get("Date")); e == nil {
		return t
	}
	return time.Now()
}
