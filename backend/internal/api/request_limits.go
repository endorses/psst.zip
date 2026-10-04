package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/endorses/psst.zip/backend/internal/store"
)

const controlBodyLimit = 8 * 1024
const controlBodyTimeout = 10 * time.Second
const streamIdleTimeout = 30 * time.Second
const resourceLockTimeout = 5 * time.Second

// Refresh deadlines at IO boundaries, not once for the lifetime of a large
// transfer. ResponseController traverses Unwrap wrappers, including traffic
// accounting, without bypassing their Read/Write methods.
func requestLimits(next http.Handler) http.Handler {
	return withRequestDeadlines(next, controlBodyTimeout, streamIdleTimeout)
}
func withRequestDeadlines(next http.Handler, controlTimeout, idleTimeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controller := http.NewResponseController(w)
		state := &requestDeadlineState{controller: controller}
		r = r.WithContext(context.WithValue(r.Context(), requestDeadlineKey{}, state))
		body := &deadlineBody{ReadCloser: r.Body, state: state, idle: idleTimeout}
		streaming := r.Method == http.MethodPatch || (r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/manifest"))
		if !streaming {
			body.expires = time.Now().Add(controlTimeout)
		}
		// Arm the connection even if authorization rejects the request before a
		// handler reads its body: net/http may drain unread bytes while flushing.
		deadline := time.Now().Add(idleTimeout)
		if !body.expires.IsZero() {
			deadline = body.expires
		}
		if r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 {
			state.read(deadline)
		}
		r.Body = body
		writer := &deadlineWriter{ResponseWriter: w, state: state, idle: idleTimeout}
		defer func() {
			// Flush net/http's final buffered bytes under the same deadline. Clearing
			// deadlines then avoids carrying them into a subsequent keep-alive request.
			_ = writer.FlushError()
			state.clear()
		}()
		next.ServeHTTP(writer, r)
	})
}

type deadlineBody struct {
	io.ReadCloser
	state   *requestDeadlineState
	idle    time.Duration
	expires time.Time
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	deadline := time.Now().Add(b.idle)
	if !b.expires.IsZero() && b.expires.Before(deadline) {
		deadline = b.expires
	}
	b.state.read(deadline)
	return b.ReadCloser.Read(p)
}

type deadlineWriter struct {
	http.ResponseWriter
	state *requestDeadlineState
	idle  time.Duration
}

func (w *deadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *deadlineWriter) WriteHeader(status int) {
	w.state.write(time.Now().Add(w.idle))
	w.ResponseWriter.WriteHeader(status)
}
func (w *deadlineWriter) Write(p []byte) (int, error) {
	w.state.write(time.Now().Add(w.idle))
	return w.ResponseWriter.Write(p)
}
func (w *deadlineWriter) FlushError() error {
	w.state.write(time.Now().Add(w.idle))
	err := w.state.controller.Flush()
	// An SSE stream can legitimately have no events for longer than the idle
	// write timeout. Only blocked writes, not time between events, are timed out.
	w.state.write(time.Time{})
	return err
}
func (w *deadlineWriter) Flush() { _ = w.FlushError() }

func acquireResource(w http.ResponseWriter, r *http.Request, id string, slot bool) (func(), bool) {
	ctx, cancel := context.WithTimeout(r.Context(), resourceLockTimeout)
	defer cancel()
	var unlock func()
	var err error
	if slot {
		unlock, err = store.AcquireSlot(ctx, id)
	} else {
		unlock, err = store.AcquireTransfer(ctx, id)
	}
	if err != nil {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "resource is busy; retry later")
		return nil, false
	}
	return unlock, true
}

// Serialize deadline updates with cancellation so an IO operation cannot
// accidentally extend the deadline after revocation/expiry has interrupted it.
type requestDeadlineKey struct{}
type requestDeadlineState struct {
	mu         sync.Mutex
	controller *http.ResponseController
	canceled   bool
}

func (s *requestDeadlineState) read(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.canceled {
		at = time.Now()
	}
	_ = s.controller.SetReadDeadline(at)
}
func (s *requestDeadlineState) write(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.canceled {
		at = time.Now()
	}
	_ = s.controller.SetWriteDeadline(at)
}
func (s *requestDeadlineState) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.canceled {
		return
	}
	_ = s.controller.SetReadDeadline(time.Time{})
	_ = s.controller.SetWriteDeadline(time.Time{})
}
func cancelRequestIO(r *http.Request) {
	state, ok := r.Context().Value(requestDeadlineKey{}).(*requestDeadlineState)
	if !ok {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.canceled = true
	_ = state.controller.SetReadDeadline(time.Now())
	_ = state.controller.SetWriteDeadline(time.Now())
}
