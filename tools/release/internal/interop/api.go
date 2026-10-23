package interop

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"slices"
	"strings"
	"time"
)

func randomSecret() string {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

type api struct {
	origin string
	client *http.Client
	ctx    context.Context
}

func newAPI(ctx context.Context, origin string) *api {
	jar, _ := cookiejar.New(nil)
	return &api{origin, &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 5 * time.Second}}, ctx}
}
func (a *api) request(method, path string, body any, token string, statuses ...int) (map[string]any, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, errors.New("private API request is invalid")
		}
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, a.origin+"/api/v1"+path, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("disposable API request could not be created")
	}
	request.Header.Set("Origin", a.origin)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := a.client.Do(request)
	if err != nil {
		return nil, errors.New("disposable API connection failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxChildOutput+1))
	if err != nil || len(raw) > maxChildOutput {
		return nil, errors.New("disposable API response exceeds bounds")
	}
	if !slices.Contains(statuses, response.StatusCode) {
		return nil, fmt.Errorf("disposable API %s returned %d, expected %v", method, response.StatusCode, statuses)
	}
	result := map[string]any{}
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &result); err != nil {
			return nil, errors.New("disposable API JSON is invalid")
		}
	}
	return result, nil
}
func totp(secret string, at time.Time) (string, error) {
	secret = strings.ToUpper(strings.TrimRight(secret, "="))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) == 0 {
		return "", errors.New("disposable TOTP secret is invalid")
	}
	counter := make([]byte, 8)
	binary.BigEndian.PutUint64(counter, uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(counter)
	sum := mac.Sum(nil)
	offset := int(sum[len(sum)-1] & 15)
	code := (binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", code), nil
}
