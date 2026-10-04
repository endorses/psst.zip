package api

import (
	"errors"
	"github.com/endorses/psst.zip/backend/internal/database"
	"io"
	"net/http"
	"time"
)

func resourceFailure(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, database.ErrResourceLimit):
		policyError(w, 403, "resource_limit", "server or account resource allowance exhausted")
	case errors.Is(err, database.ErrDiskCapacity):
		policyError(w, 507, "disk_capacity", "storage safety reserve reached; retry after capacity is available")
	case errors.Is(err, database.ErrRetentionLimit):
		policyError(w, 400, "retention_limit", err.Error())
	default:
		return false
	}
	return true
}
func (s *Server) resourceState(w http.ResponseWriter, owner string) {
	p, err := s.queries.ResourcePolicy()
	if err != nil {
		writeError(w, 500, "could not read resource policy")
		return
	}
	u, err := s.queries.ResourceUsage(owner)
	if err != nil {
		writeError(w, 500, "could not read resource usage")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"policy": p, "usage": u})
}
func (s *Server) getResourcePolicy(w http.ResponseWriter, r *http.Request) { s.resourceState(w, "") }
func (s *Server) accountUsage(w http.ResponseWriter, r *http.Request) {
	s.resourceState(w, identity(r).user.ID)
}
func (s *Server) updateResourcePolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.queries.ResourcePolicy()
	if err != nil {
		writeError(w, 500, "could not read resource policy")
		return
	}
	// Decode over the current values for partial PATCH semantics. Explicit nulls
	// and unknown/duplicate fields are rejected by the bounded strict decoder.
	if !decodeCreation(w, r, &p) {
		return
	}
	if err := p.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.queries.SetResourcePolicy(p); err != nil {
		writeError(w, 500, "could not save resource policy")
		return
	}
	s.resourceState(w, "")
}
func (s *Server) effectiveRetention(w http.ResponseWriter, requested int, fallback time.Duration) (time.Duration, bool) {
	p, err := s.queries.ResourcePolicy()
	if err != nil {
		writeError(w, 500, "could not read resource policy")
		return 0, false
	}
	maximum := time.Duration(p.MaxRetentionSeconds) * time.Second
	if requested > 0 {
		if int64(requested) > p.MaxRetentionSeconds {
			resourceFailure(w, database.ErrRetentionLimit)
			return 0, false
		}
		return time.Duration(requested) * time.Second, true
	}
	if fallback <= 0 {
		fallback = 24 * time.Hour
	}
	return min(fallback, maximum), true
}

// Recheck the real volume reserve throughout a long PATCH, including requests
// whose bytes arrive slowly while another process consumes disk space.
type capacityBody struct {
	io.ReadCloser
	queries   *database.Queries
	remaining int
}

func (b *capacityBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		if err := b.queries.CheckWriteCapacity(); err != nil {
			return 0, err
		}
		b.remaining = 1024 * 1024
	}
	if len(p) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= n
	return n, err
}
