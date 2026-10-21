// Package client implements the psst.zip API without terminal, database, or
// server dependencies. Credentials are bound to one normalized server origin.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/endorses/psst.zip/client/protocol"
)

const controlLimit int64 = 1024 * 1024

var ErrNotFound = errors.New("local state not found")
var ErrMissingKey = errors.New("Receive private key is unavailable on this device; restore its encrypted backup")
var ErrCheckpointInvalid = errors.New("download checkpoint or completed output is invalid; choose a fresh output directory")
var ErrProtocol = errors.New("invalid server response")
var ErrRedirect = errors.New("server redirects are not permitted; configure the final origin")
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,95}$`)

// CredentialProvider is invoked only for requests to the configured origin.
type CredentialProvider func(context.Context) (string, error)
type Progress func(completed, total int64)
type Client struct {
	origin      string
	http        *http.Client
	credentials CredentialProvider
}

func New(origin string, h *http.Client, credentials CredentialProvider) (*Client, error) {
	o, e := protocol.NormalizeOrigin(origin)
	if e != nil {
		return nil, e
	}
	if h == nil {
		h = &http.Client{}
	}
	copyHTTP := *h
	copyHTTP.Jar = nil
	copyHTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{origin: o, http: &copyHTTP, credentials: credentials}, nil
}
func (c *Client) Origin() string     { return c.origin }
func (c *Client) Anonymous() *Client { copy := *c; copy.credentials = nil; return &copy }
func (c *Client) WithToken(token string) *Client {
	copy := *c
	copy.credentials = func(context.Context) (string, error) { return token, nil }
	return &copy
}

// PolicyError reports a local preflight decision from authoritative server
// metadata. Its code is a client code, not an invented HTTP/server error.
type PolicyError struct {
	Code    string
	Message string
}

func (e *PolicyError) Error() string { return e.Message }

// APIError preserves stable policy codes and retry timing. Untrusted server text
// is intentionally excluded from Error() and diagnostics.
type APIError struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("server returned HTTP %d (%s)", e.Status, e.Code)
	}
	return fmt.Sprintf("server returned HTTP %d", e.Status)
}

type TransportError struct{ Cause error }

func (e *TransportError) Error() string { return "server connection failed" }
func (e *TransportError) Unwrap() error { return e.Cause }
func retryAfter(raw string) time.Duration {
	if n, e := strconv.ParseInt(raw, 10, 64); e == nil && n >= 0 {
		if n > 86400 {
			n = 86400
		}
		return time.Duration(n) * time.Second
	}
	if t, e := http.ParseTime(raw); e == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		if d > 24*time.Hour {
			return 24 * time.Hour
		}
		return d
	}
	return 0
}
func (c *Client) request(ctx context.Context, method, path string, body io.Reader, headers http.Header) (*http.Response, error) {
	// Only internal absolute paths are accepted, never an authority or fragment.
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "#\r\n\\") {
		return nil, ErrProtocol
	}
	u, e := url.Parse(c.origin + path)
	if e != nil || u.Scheme+"://"+u.Host != c.origin {
		return nil, ErrProtocol
	}
	req, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return nil, ErrProtocol
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "psst.zip-client/1")
	req.Header.Del("Cookie")
	req.Header.Del("Authorization")
	if c.credentials != nil {
		token, e := c.credentials(ctx)
		if e != nil {
			return nil, e
		}
		if len(token) > 4096 || strings.ContainsAny(token, " \t\r\n") || token == "" {
			return nil, errors.New("device credential unavailable")
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return nil, &TransportError{Cause: e}
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, ErrRedirect
	}
	return resp, nil
}
func responseError(resp *http.Response) error {
	defer resp.Body.Close()
	code := resp.Header.Get("X-Psst-Error-Code")
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	var v struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(b, &v) == nil && code == "" {
		code = v.Code
	}
	if !codePattern.MatchString(code) {
		code = ""
	}
	return &APIError{Status: resp.StatusCode, Code: code, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
}
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil {
		return nil, &TransportError{Cause: e}
	}
	if int64(len(b)) > limit {
		return nil, ErrProtocol
	}
	return b, nil
}
func decode(b []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	if e := d.Decode(out); e != nil {
		return ErrProtocol
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrProtocol
	}
	return nil
}
func (c *Client) control(ctx context.Context, method, path string, in, out any, statuses ...int) error {
	return c.controlHeaders(ctx, method, path, in, out, nil, statuses...)
}

func (c *Client) controlHeaders(ctx context.Context, method, path string, in, out any, responseHeaders *http.Header, statuses ...int) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var body io.Reader
	h := make(http.Header)
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		if int64(len(b)) > controlLimit {
			return errors.New("request exceeds control limit")
		}
		body = bytes.NewReader(b)
		h.Set("Content-Type", "application/json")
	}
	resp, e := c.request(ctx, method, path, body, h)
	if e != nil {
		return e
	}
	if responseHeaders != nil {
		*responseHeaders = resp.Header.Clone()
	}
	ok := false
	for _, s := range statuses {
		ok = ok || resp.StatusCode == s
	}
	if !ok {
		return responseError(resp)
	}
	defer resp.Body.Close()
	if out == nil {
		_, e = readBounded(resp.Body, controlLimit)
		return e
	}
	ct, _, e := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if e != nil || ct != "application/json" {
		return ErrProtocol
	}
	b, e := readBounded(resp.Body, controlLimit)
	if e != nil {
		return e
	}
	return decode(b, out)
}

var metaTag = regexp.MustCompile(`(?i)<meta\b[^>]*>`)
var htmlAttribute = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9_-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)

func webMarker(b []byte) bool {
	for _, tag := range metaTag.FindAll(b, -1) {
		attrs := map[string]string{}
		for _, m := range htmlAttribute.FindAllSubmatch(tag, -1) {
			v := m[2]
			if len(v) == 0 {
				v = m[3]
			}
			if len(v) == 0 {
				v = m[4]
			}
			attrs[strings.ToLower(string(m[1]))] = string(v)
		}
		if attrs["name"] == "psst-web" && attrs["content"] == "1" {
			return true
		}
	}
	return false
}

// ValidateServer checks API and both web share routes anonymously before login.
func (c *Client) ValidateServer(ctx context.Context) error {
	a := c.Anonymous()
	var health struct {
		Service    string `json:"service"`
		APIVersion int    `json:"api_version"`
	}
	if e := a.control(ctx, "GET", "/api/v1/health", nil, &health, 200); e != nil {
		return e
	}
	if health.Service != "psst.zip" || health.APIVersion != 1 {
		return errors.New("server does not provide psst.zip API version 1")
	}
	for _, route := range []string{"d", "u"} {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		resp, e := a.request(ctx, "GET", "/"+route+"/_connection_check", nil, nil)
		if e != nil {
			cancel()
			return e
		}
		if resp.StatusCode != 200 {
			e = responseError(resp)
			cancel()
			return e
		}
		ct, _, ce := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		b, re := readBounded(resp.Body, 512*1024)
		resp.Body.Close()
		cancel()
		if ce != nil || ct != "text/html" || re != nil || !webMarker(b) {
			return errors.New("server psst.zip web share routes are missing")
		}
	}
	return nil
}
func (c *Client) Config(ctx context.Context) (protocol.Config, error) {
	var v protocol.Config
	e := c.Anonymous().control(ctx, "GET", "/api/v1/config", nil, &v, 200)
	return v, e
}
