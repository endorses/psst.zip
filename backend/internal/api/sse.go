package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/endorses/psst.zip/backend/internal/database"
)

const (
	maxEventStreams      = 256
	maxInboxEventStreams = 16
	eventQueueSize       = 16
	eventStreamLifetime  = 10 * time.Minute
	eventCheckTimeout    = 2 * time.Second
)

type inboxEventCheck func(context.Context, string, string, []string) (map[string]bool, error)

type eventSubscriber struct {
	group   *inboxEventGroup
	session string
	events  chan string
	cancel  context.CancelFunc
}
type inboxEventGroup struct {
	key, owner string
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	readers    map[*eventSubscriber]struct{}
}

// SSEHub bounds fanout and shares one lifecycle worker per active inbox. It is
// only subscribed after owner authentication and global/scoped stream admission.
type SSEHub struct {
	mu     sync.Mutex
	groups map[string]*inboxEventGroup
	total  int
	check  inboxEventCheck
	ticker func() (<-chan time.Time, func())
}

func newSSEHub(check inboxEventCheck, interval time.Duration) *SSEHub {
	return &SSEHub{groups: make(map[string]*inboxEventGroup), check: check, ticker: func() (<-chan time.Time, func()) {
		ticker := time.NewTicker(interval)
		return ticker.C, ticker.Stop
	}}
}

func (h *SSEHub) subscribe(key, owner, session string, cancel context.CancelFunc) *eventSubscriber {
	h.mu.Lock()
	defer h.mu.Unlock()
	group := h.groups[key]
	if h.total >= maxEventStreams || (group != nil && (group.owner != owner || len(group.readers) >= maxInboxEventStreams)) {
		return nil
	}
	if group == nil {
		ctx, stop := context.WithCancel(context.Background())
		group = &inboxEventGroup{key: key, owner: owner, ctx: ctx, cancel: stop, done: make(chan struct{}), readers: make(map[*eventSubscriber]struct{})}
		h.groups[key] = group
		go h.watch(group)
	}
	reader := &eventSubscriber{group: group, session: session, events: make(chan string, eventQueueSize), cancel: cancel}
	group.readers[reader] = struct{}{}
	h.total++
	return reader
}

// removeLocked is idempotent. Cancel the HTTP context as well as removing the
// queue: a subscriber may already be blocked writing an earlier event.
func (h *SSEHub) removeLocked(reader *eventSubscriber) {
	group := reader.group
	if _, ok := group.readers[reader]; !ok {
		return
	}
	delete(group.readers, reader)
	h.total--
	reader.cancel()
	if len(group.readers) == 0 {
		if h.groups[group.key] == group {
			delete(h.groups, group.key)
		}
		group.cancel()
	}
}

func (h *SSEHub) unsubscribe(reader *eventSubscriber) {
	h.mu.Lock()
	h.removeLocked(reader)
	last := len(reader.group.readers) == 0
	h.mu.Unlock()
	// The last request joins its worker before leaving Server.WaitForRequests, so
	// database shutdown cannot race a lifecycle query from an abandoned inbox.
	if last {
		<-reader.group.done
	}
}

func (h *SSEHub) watch(group *inboxEventGroup) {
	defer close(group.done)
	ticks, stop := h.ticker()
	defer stop()
	for {
		select {
		case <-group.ctx.Done():
			return
		case <-ticks:
			h.mu.Lock()
			readers := make([]*eventSubscriber, 0, len(group.readers))
			sessions := make([]string, 0, len(group.readers))
			seen := make(map[string]bool)
			for reader := range group.readers {
				readers = append(readers, reader)
				if !seen[reader.session] {
					seen[reader.session] = true
					sessions = append(sessions, reader.session)
				}
			}
			h.mu.Unlock()
			if len(readers) == 0 {
				return
			}
			ctx, cancel := context.WithTimeout(group.ctx, eventCheckTimeout)
			active, err := h.check(ctx, group.key, group.owner, sessions)
			cancel()
			h.mu.Lock()
			// Validate only the captured readers; a new session may have subscribed
			// while the query ran. Its initial authorization is checked by the handler.
			for _, reader := range readers {
				if err != nil || !active[reader.session] {
					h.removeLocked(reader)
				}
			}
			h.mu.Unlock()
		}
	}
}

// Send never waits for a subscriber. Overflow terminates that subscriber instead
// of silently losing events and presenting an apparently complete inbox view.
func (h *SSEHub) Send(key, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	group := h.groups[key]
	if group == nil {
		return
	}
	for reader := range group.readers {
		select {
		case reader.events <- msg:
		default:
			h.removeLocked(reader)
		}
	}
}

func (s *Server) slotEvents(w http.ResponseWriter, r *http.Request) {
	slotID := chi.URLParam(r, "slotID")
	if !isValidUUID(slotID) {
		writeError(w, http.StatusBadRequest, "invalid slot ID")
		return
	}
	slot, err := s.queries.GetSlot(slotID)
	if err != nil {
		writeError(w, http.StatusNotFound, "slot not found")
		return
	}
	if slot.Status == "revoked" {
		incidentFailure(w, database.ErrResourceRevoked)
		return
	}
	if !time.Now().Before(slot.ExpiresAt) {
		policyError(w, http.StatusGone, "link_expired", "receive link expired")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), min(time.Until(slot.ExpiresAt), eventStreamLifetime))
	defer cancel()
	// This lifetime includes blocked network writes. Stop and join the callback
	// before outer middleware clears deadlines for a reusable connection.
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(callbackDone); cancelRequestIO(r) })
	var reader *eventSubscriber
	defer func() {
		if !stop() {
			<-callbackDone
		}
		if reader != nil {
			s.sseHub.unsubscribe(reader)
		}
	}()
	principal := identity(r)
	reader = s.sseHub.subscribe(slotID, principal.user.ID, principal.session.ID, cancel)
	if reader == nil {
		w.Header().Set("Retry-After", "5")
		policyError(w, http.StatusTooManyRequests, "stream_limit", "event stream limit reached")
		return
	}
	authorized := func() (bool, error) {
		checkCtx, stop := context.WithTimeout(ctx, eventCheckTimeout)
		defer stop()
		active, err := s.sseHub.check(checkCtx, slotID, principal.user.ID, []string{principal.session.ID})
		return err == nil && active[principal.session.ID] && ctx.Err() == nil, err
	}
	if valid, err := authorized(); !valid {
		if err != nil {
			policyError(w, http.StatusServiceUnavailable, "inbox_events_unavailable", "inbox events are temporarily unavailable")
		} else {
			writeError(w, http.StatusForbidden, "inbox read authorization changed")
		}
		return
	}
	controller := http.NewResponseController(w)
	writeEvent := func(event string) bool {
		if ctx.Err() != nil {
			return false
		}
		if _, err := fmt.Fprint(w, event); err != nil {
			return false
		}
		return controller.Flush() == nil
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	if !writeEvent(fmt.Sprintf("event: connected\ndata: %s\n\n", slotID)) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-reader.events:
			// A queued message must not disclose child IDs after session revocation or
			// ownership change, even before the next shared lifecycle tick.
			if valid, _ := authorized(); !valid || !writeEvent("data: "+msg+"\n\n") {
				return
			}
			if msg == slotDeletedEvent {
				return
			}
		}
	}
}
