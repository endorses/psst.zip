package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// SSEHub manages SSE subscriptions by key (e.g., slot ID).
type SSEHub struct {
	mu      sync.RWMutex
	clients map[string]map[chan string]struct{}
}

// NewSSEHub creates an SSEHub.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[string]map[chan string]struct{}),
	}
}

// Subscribe returns a channel that receives events for the given key.
func (h *SSEHub) Subscribe(key string) chan string {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan string, 16)
	if h.clients[key] == nil {
		h.clients[key] = make(map[chan string]struct{})
	}
	h.clients[key][ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber.
func (h *SSEHub) Unsubscribe(key string, ch chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if subs, ok := h.clients[key]; ok {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(h.clients, key)
		}
	}
	close(ch)
}

// Send broadcasts a message to all subscribers of the given key.
func (h *SSEHub) Send(key string, msg string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for ch := range h.clients[key] {
		select {
		case ch <- msg:
		default:
			// Drop if subscriber is too slow.
		}
	}
}

func (s *Server) slotEvents(w http.ResponseWriter, r *http.Request) {
	slotID := chi.URLParam(r, "slotID")

	if !isValidUUID(slotID) {
		writeError(w, http.StatusBadRequest, "invalid slot ID")
		return
	}
	ch := s.sseHub.Subscribe(slotID)
	defer s.sseHub.Unsubscribe(slotID, ch)

	slot, err := s.queries.GetSlot(slotID)
	if err != nil {
		writeError(w, http.StatusNotFound, "slot not found")
		return
	}

	if slot.Status == "revoked" || !time.Now().Before(slot.ExpiresAt) {
		writeError(w, http.StatusGone, "slot expired or revoked")
		return
	}
	expires := time.NewTimer(time.Until(slot.ExpiresAt))
	defer expires.Stop()
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Send initial connected event.
	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", slotID)
	flusher.Flush()

	check := time.NewTicker(time.Second)
	defer check.Stop()
	ctx := r.Context()
	for {
		select {
		case <-check.C:
			current, err := s.queries.GetSlot(slotID)
			if err == nil && current.Status != "revoked" && time.Now().Before(current.ExpiresAt) {
				continue
			}
			if err == nil || err == sql.ErrNoRows {
				return
			}
		case <-expires.C:
			return
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
			if msg == slotDeletedEvent {
				return
			}
		}
	}
}
