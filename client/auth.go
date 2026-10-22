package client

import (
	"context"
	"errors"
	"io"

	"github.com/endorses/psst.zip/client/protocol"
)

func ParsePairing(r io.Reader) (protocol.Pairing, error) {
	var p protocol.Pairing
	b, e := readBounded(r, 4096)
	if e != nil {
		return p, e
	}
	if decode(b, &p) != nil || p.Type != "psst-pairing" || p.Version != 1 || !protocol.ValidToken(p.Code) {
		return p, errors.New("invalid pairing document")
	}
	o, e := protocol.NormalizeOrigin(p.ServerURL)
	if e != nil {
		return p, errors.New("invalid pairing server")
	}
	p.ServerURL = o
	return p, nil
}
func validSession(s protocol.Session) bool {
	return protocol.ValidToken(s.Token) && protocol.ValidID(s.User.ID) && protocol.ValidID(s.SessionID) && !s.ExpiresAt.IsZero() && s.User.Role == "user" && !s.User.Disabled
}
func (c *Client) Login(ctx context.Context, username, password, deviceName string) (protocol.Session, error) {
	var s protocol.Session
	if e := c.ValidateServer(ctx); e != nil {
		return s, e
	}
	e := c.Anonymous().control(ctx, "POST", "/api/v1/auth/login", map[string]string{"username": username, "password": password, "device_name": deviceName, "session_type": "device"}, &s, 200)
	if e == nil && !validSession(s) {
		e = errors.New("server returned invalid regular-user device session")
	}
	return s, e
}
func (c *Client) Pair(ctx context.Context, p protocol.Pairing, deviceName string) (protocol.Session, error) {
	var s protocol.Session
	if p.Type != "psst-pairing" || p.Version != 1 || p.ServerURL != c.origin || !protocol.ValidToken(p.Code) {
		return s, errors.New("pairing belongs to a different server or is invalid")
	}
	if e := c.ValidateServer(ctx); e != nil {
		return s, e
	}
	e := c.Anonymous().control(ctx, "POST", "/api/v1/auth/pairings/redeem", map[string]string{"code": p.Code, "device_name": deviceName}, &s, 200)
	if e == nil && !validSession(s) {
		e = errors.New("server returned invalid regular-user device session")
	}
	return s, e
}
func (c *Client) Me(ctx context.Context) (protocol.User, error) {
	var v struct {
		User protocol.User `json:"user"`
	}
	e := c.control(ctx, "GET", "/api/v1/auth/me", nil, &v, 200)
	if e == nil && (!protocol.ValidID(v.User.ID) || v.User.Disabled || v.User.Role != "user") {
		e = errors.New("regular-user account required")
	}
	return v.User, e
}
func (c *Client) Logout(ctx context.Context) error {
	return c.control(ctx, "POST", "/api/v1/auth/logout", nil, nil, 204, 401)
}
