package api

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// rateLimiter implements a per-IP token bucket rate limiter.
type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	rate      float64 // tokens per second
	burst     int     // max tokens
	nextSweep time.Time
}

type bucket struct {
	tokens   float64
	lastTime time.Time
}

func newRateLimiter(rate float64, burst int) *rateLimiter {
	rl := &rateLimiter{
		buckets: make(map[string]*bucket),
		rate:    rate,
		burst:   burst,
	}

	return rl
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	if !now.Before(rl.nextSweep) {
		cutoff := now.Add(-10 * time.Minute)
		for key, item := range rl.buckets {
			if item.lastTime.Before(cutoff) {
				delete(rl.buckets, key)
			}
		}
		rl.nextSweep = now.Add(time.Minute)
	}
	b, ok := rl.buckets[ip]
	if !ok {
		if len(rl.buckets) >= 8192 {
			return false
		}
		rl.buckets[ip] = &bucket{
			tokens:   float64(rl.burst) - 1,
			lastTime: now,
		}
		return true
	}

	// Add tokens based on elapsed time.
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.lastTime = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// parseTrustedProxies accepts explicit CIDRs only. Empty means trust no proxy.
// Reject universal ranges so configuration cannot trust Internet-supplied headers.
func parseTrustedProxies(value string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	if strings.TrimSpace(value) == "" {
		return prefixes, nil
	}
	for _, part := range strings.Split(value, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil || prefix.Bits() == 0 || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("TRUSTED_PROXIES must contain explicit non-universal IPv4/IPv6 CIDRs")
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func resolveClientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown-peer"
	}
	peer = peer.Unmap()
	isTrusted := func(ip netip.Addr) bool {
		for _, prefix := range trusted {
			if prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !isTrusted(peer) {
		return peer.String()
	}
	// Traverse from the socket towards the client, stopping at the first
	// untrusted hop. Never use the caller-controlled leftmost entry blindly.
	values := r.Header.Values("X-Forwarded-For")
	chain := strings.Join(values, ",")
	if len(chain) == 0 || len(chain) > 4096 {
		return peer.String()
	}
	hops := strings.Split(chain, ",")
	if len(hops) > 32 {
		return peer.String()
	}
	current := peer
	for i := len(hops) - 1; i >= 0; i-- {
		if !isTrusted(current) {
			break
		}
		next, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil || next.Zone() != "" {
			return peer.String()
		}
		current = next.Unmap()
	}
	return current.String()
}

func (s *Server) clientIP(r *http.Request) string {
	trusted, err := parseTrustedProxies(s.cfg.TrustedProxies)
	if err != nil {
		trusted = nil
	} // Bootstrap rejects invalid settings; fail closed in fixtures.
	return resolveClientIP(r, trusted)
}

// rateLimitMiddleware returns an http middleware that rate-limits by client IP.
func rateLimitMiddleware(rl *rateLimiter, clientIP func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !rl.allow(ip) {
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
