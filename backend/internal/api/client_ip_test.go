package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestTrustedClientIP(t *testing.T) {
	trusted, err := parseTrustedProxies("192.0.2.0/24,2001:db8::/32")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, peer, forwarded, expected string }{
		{"untrusted cannot spoof", "198.51.100.7:4000", "203.0.113.3", "198.51.100.7"},
		{"proxy resolves client", "192.0.2.4:4000", "203.0.113.3", "203.0.113.3"},
		{"ignore spoofed left prefix", "192.0.2.4:4000", "198.51.100.7, 203.0.113.3", "203.0.113.3"},
		{"trusted chain", "192.0.2.4:4000", "203.0.113.3, 192.0.2.5", "203.0.113.3"},
		{"malformed nearest hop", "192.0.2.4:4000", "203.0.113.3, garbage", "192.0.2.4"},
		{"IPv6", "[2001:db8::1]:4000", "2001:db9::7", "2001:db9::7"},
		{"mapped IPv4", "[::ffff:192.0.2.4]:4000", "::ffff:203.0.113.3", "203.0.113.3"},
		{"missing header", "192.0.2.4:4000", "", "192.0.2.4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = c.peer
			r.Header.Set("X-Forwarded-For", c.forwarded)
			if got := resolveClientIP(r, trusted); got != c.expected {
				t.Fatalf("got %q want %q", got, c.expected)
			}
		})
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.4:4000"
	r.Header.Set("X-Forwarded-For", "203.0.113.3")
	if got := resolveClientIP(r, nil); got != "192.0.2.4" {
		t.Fatal("default trusted forwarded header:", got)
	}
}

func TestInvalidTrustedProxiesRejected(t *testing.T) {
	for _, value := range []string{"*", "0.0.0.0/0", "::/0", "garbage", "192.0.2.1", "::ffff:192.0.2.1/128"} {
		if _, err := parseTrustedProxies(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestRateLimiterBoundsIdentityTracking(t *testing.T) {
	limiter := newRateLimiter(1, 2)
	now := time.Now()
	for i := 0; i < 8192; i++ {
		limiter.buckets[string(rune(i))] = &bucket{tokens: 1, lastTime: now}
	}
	if limiter.allow("new-identity") {
		t.Fatal("allowed unbounded new identity")
	}
	if !limiter.allow("A") {
		t.Fatal("blocked already tracked client")
	}
	limiter.buckets["A"].lastTime = now.Add(-11 * time.Minute)
	limiter.nextSweep = time.Time{}
	if !limiter.allow("new-identity") {
		t.Fatal("did not reclaim expired identity")
	}
}
