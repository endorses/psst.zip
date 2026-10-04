package api

import "net/http"

// Password hashing must never spawn an unbounded queue of CPU-intensive work.
// The guard covers comparison and replacement, and is released even on panic.
func (s *Server) limitPasswordWork(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.passwordWork <- struct{}{}:
			defer func() { <-s.passwordWork }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "5")
			writeError(w, http.StatusTooManyRequests, "authentication is busy; try again shortly")
		}
	})
}
