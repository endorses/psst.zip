// Package protocol contains the database-independent psst.zip wire contract.
package protocol

import (
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const MaxLinkBytes = 4096

var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var labelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)
var ErrInvalidLink = errors.New("invalid psst.zip share link")
var ErrInvalidOrigin = errors.New("invalid server origin")

func ValidID(s string) bool    { return idPattern.MatchString(s) }
func ValidToken(s string) bool { return tokenPattern.MatchString(s) }
func NormalizeOrigin(raw string) (string, error) {
	if len(raw) > 2048 || strings.ContainsAny(raw, `\?#@`) || strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", ErrInvalidOrigin
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", ErrInvalidOrigin
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", ErrInvalidOrigin
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return "", ErrInvalidOrigin
	}
	if strings.Contains(host, ":") {
		ip := net.ParseIP(host)
		if ip == nil || strings.Contains(host, "%") {
			return "", ErrInvalidOrigin
		}
		host = "[" + ip.String() + "]"
	} else {
		if len(host) > 253 {
			return "", ErrInvalidOrigin
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) > 63 || !labelPattern.MatchString(label) {
				return "", ErrInvalidOrigin
			}
		}
		if strings.Trim(host, "0123456789.") == "" {
			ip := net.ParseIP(host)
			if ip == nil || ip.To4() == nil || ip.String() != host {
				return "", ErrInvalidOrigin
			}
		}
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return "", ErrInvalidOrigin
	}
	if port != "" {
		p, e := strconv.Atoi(port)
		if e != nil || p < 1 || p > 65535 {
			return "", ErrInvalidOrigin
		}
		if scheme == "https" && p == 443 || scheme == "http" && p == 80 {
			port = ""
		} else {
			port = strconv.Itoa(p)
		}
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, nil
}

// Link is a parsed capability. String deliberately redacts its key.
type Link struct {
	Origin  string
	Kind    string
	ID      string
	Key     []byte
	Version int
}

func (l Link) String() string       { return "psst.zip " + l.Kind + " " + l.ID + " [redacted]" }
func (l Link) URL() (string, error) { return BuildLink(l.Origin, l.Kind, l.ID, l.Key) }
func BuildLink(origin, kind, id string, key []byte) (string, error) {
	o, err := NormalizeOrigin(origin)
	if err != nil || !ValidID(id) || len(key) != 32 {
		return "", ErrInvalidLink
	}
	path, fragment := "d", base64.RawURLEncoding.EncodeToString(key)
	if kind == "receive" {
		path = "u"
		fragment = "v2." + fragment
	} else if kind != "send" {
		return "", ErrInvalidLink
	}
	return o + "/" + path + "/" + strings.ToLower(id) + "#" + fragment, nil
}
func ParseLink(raw string) (Link, error) {
	var l Link
	if len(raw) > MaxLinkBytes || strings.IndexFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return l, ErrInvalidLink
	}
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" || u.User != nil || u.RawPath != "" {
		return l, ErrInvalidLink
	}
	o, err := NormalizeOrigin(u.Scheme + "://" + u.Host)
	if err != nil {
		return l, ErrInvalidLink
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 3 || !ValidID(parts[2]) {
		return l, ErrInvalidLink
	}
	kind := "send"
	f := u.Fragment
	version := 1
	if parts[1] == "u" && strings.HasPrefix(f, "v2.") {
		kind = "receive"
		f = strings.TrimPrefix(f, "v2.")
		version = 2
	} else if parts[1] != "d" {
		return l, ErrInvalidLink
	}
	f = strings.TrimSuffix(f, "=")
	key, err := base64.RawURLEncoding.Strict().DecodeString(f)
	if err != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != f {
		return l, ErrInvalidLink
	}
	return Link{Origin: o, Kind: kind, ID: strings.ToLower(parts[2]), Key: key, Version: version}, nil
}
