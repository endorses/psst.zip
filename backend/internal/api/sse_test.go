package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/endorses/psst.zip/backend/internal/database"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testEventClock struct {
	ticks   chan time.Time
	stopped chan struct{}
}

func controlledEventHub(check inboxEventCheck) (*SSEHub, <-chan *testEventClock) {
	clocks := make(chan *testEventClock, maxEventStreams)
	hub := newSSEHub(check, time.Hour)
	hub.ticker = func() (<-chan time.Time, func()) {
		clock := &testEventClock{make(chan time.Time, 1), make(chan struct{})}
		clocks <- clock
		return clock.ticks, func() { close(clock.stopped) }
	}
	return hub, clocks
}
func eventWait[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event worker")
		var zero T
		return zero
	}
}
func eventCanceled(t *testing.T, ctx context.Context) { t.Helper(); eventWait(t, ctx.Done()) }
func TestInboxEventHubSharesPollingAndJoinsLastWorker(t *testing.T) {
	calls := make(chan []string, 4)
	finish := make(chan struct{})
	hub, clocks := controlledEventHub(func(ctx context.Context, key, owner string, sessions []string) (map[string]bool, error) {
		if key != "inbox" || owner != "owner" {
			return nil, errors.New("wrong group")
		}
		calls <- sessions
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-finish:
			return map[string]bool{"same": true, "second": true}, nil
		}
	})
	first, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	second, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()
	a := hub.subscribe("inbox", "owner", "same", firstCancel)
	b := hub.subscribe("inbox", "owner", "same", secondCancel)
	c := hub.subscribe("inbox", "owner", "second", func() {})
	clock := eventWait(t, clocks)
	clock.ticks <- time.Now()
	sessions := eventWait(t, calls)
	if len(sessions) != 2 {
		t.Fatalf("session checks were not deduplicated: %v", sessions)
	}
	select {
	case <-clocks:
		t.Fatal("one inbox created multiple workers")
	default:
	}
	hub.unsubscribe(a)
	eventCanceled(t, first)
	if second.Err() != nil {
		t.Fatal("first unsubscribe canceled another reader")
	}
	hub.unsubscribe(b)
	// The final unsubscribe must cancel a blocked poll and wait for worker exit.
	hub.unsubscribe(c)
	eventWait(t, clock.stopped)
	hub.mu.Lock()
	total, groups := hub.total, len(hub.groups)
	hub.mu.Unlock()
	if total != 0 || groups != 0 {
		t.Fatalf("hub retained readers: %d / %d", total, groups)
	}
	hub.unsubscribe(c) // idempotent, including after worker shutdown
}
func TestInboxEventHubRecreationCannotBeRemovedByOldPoll(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var call atomic.Int32
	hub, clocks := controlledEventHub(func(ctx context.Context, _, _ string, sessions []string) (map[string]bool, error) {
		if call.Add(1) == 1 {
			entered <- struct{}{}
			<-ctx.Done()
			<-release
			return nil, ctx.Err()
		}
		active := map[string]bool{}
		for _, session := range sessions {
			active[session] = true
		}
		return active, nil
	})
	old := hub.subscribe("same", "old-owner", "old-session", func() {})
	oldClock := eventWait(t, clocks)
	oldClock.ticks <- time.Now()
	eventWait(t, entered)
	removed := make(chan struct{})
	go func() { hub.unsubscribe(old); close(removed) }()
	eventWait(t, old.group.ctx.Done())
	replacement := hub.subscribe("same", "new-owner", "new-session", func() {})
	if replacement == nil || replacement.group == old.group {
		t.Fatal("same-key replacement retained old group")
	}
	newClock := eventWait(t, clocks)
	close(release)
	eventWait(t, removed)
	eventWait(t, oldClock.stopped)
	hub.Send("same", "replacement event")
	if got := eventWait(t, replacement.events); got != "replacement event" {
		t.Fatal(got)
	}
	hub.unsubscribe(replacement)
	eventWait(t, newClock.stopped)
}
func TestInboxEventHubDisconnectsSlowReaderAndPreservesHealthyReader(t *testing.T) {
	hub, clocks := controlledEventHub(func(context.Context, string, string, []string) (map[string]bool, error) { return nil, nil })
	slowCtx, slowCancel := context.WithCancel(context.Background())
	defer slowCancel()
	healthyCtx, healthyCancel := context.WithCancel(context.Background())
	defer healthyCancel()
	slow := hub.subscribe("inbox", "owner", "slow", slowCancel)
	healthy := hub.subscribe("inbox", "owner", "healthy", healthyCancel)
	clock := eventWait(t, clocks)
	for i := 0; i <= eventQueueSize; i++ {
		message := fmt.Sprint(i)
		hub.Send("inbox", message)
		if got := eventWait(t, healthy.events); got != message {
			t.Fatal(got)
		}
	}
	eventCanceled(t, slowCtx)
	if healthyCtx.Err() != nil {
		t.Fatal("slow reader canceled healthy reader")
	}
	if len(slow.events) != eventQueueSize {
		t.Fatal("queue bound changed")
	}
	hub.unsubscribe(slow)
	hub.unsubscribe(healthy)
	eventWait(t, clock.stopped)
}
func TestInboxEventHubSelectiveRevocationAndDatabaseFailure(t *testing.T) {
	var fail atomic.Bool
	polled := make(chan struct{}, 2)
	hub, clocks := controlledEventHub(func(context.Context, string, string, []string) (map[string]bool, error) {
		polled <- struct{}{}
		if fail.Load() {
			return nil, errors.New("database unavailable")
		}
		return map[string]bool{"live": true}, nil
	})
	staleCtx, staleCancel := context.WithCancel(context.Background())
	defer staleCancel()
	liveCtx, liveCancel := context.WithCancel(context.Background())
	defer liveCancel()
	stale := hub.subscribe("inbox", "owner", "stale", staleCancel)
	live := hub.subscribe("inbox", "owner", "live", liveCancel)
	clock := eventWait(t, clocks)
	clock.ticks <- time.Now()
	eventWait(t, polled)
	eventCanceled(t, staleCtx)
	if liveCtx.Err() != nil {
		t.Fatal("revocation affected valid session")
	}
	fail.Store(true)
	clock.ticks <- time.Now()
	eventWait(t, polled)
	eventCanceled(t, liveCtx)
	hub.unsubscribe(stale)
	hub.unsubscribe(live)
	eventWait(t, clock.stopped)
}
func TestInboxEventHubLimitsAndConcurrentRelease(t *testing.T) {
	hub, _ := controlledEventHub(func(context.Context, string, string, []string) (map[string]bool, error) { return nil, nil })
	readers := make([]*eventSubscriber, 0, maxEventStreams)
	for i := 0; i < maxEventStreams; i++ {
		reader := hub.subscribe(fmt.Sprint(i/maxInboxEventStreams), "owner", "session", func() {})
		if reader == nil {
			t.Fatalf("early admission failure at %d", i)
		}
		readers = append(readers, reader)
	}
	if hub.subscribe("0", "owner", "session", func() {}) != nil || hub.subscribe("overflow", "owner", "session", func() {}) != nil {
		t.Fatal("hub cap bypassed")
	}
	var wait sync.WaitGroup
	for _, reader := range readers {
		wait.Add(1)
		go func(reader *eventSubscriber) {
			defer wait.Done()
			hub.Send(reader.group.key, "event")
			hub.unsubscribe(reader)
			hub.unsubscribe(reader)
		}(reader)
	}
	wait.Wait()
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.total != 0 || len(hub.groups) != 0 {
		t.Fatal("concurrent unsubscribe leaked hub state")
	}
}

func TestInboxEventInitialAuthorizationFailureIsDelivered(t *testing.T) {
	for _, databaseFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(databaseFailure), func(t *testing.T) {
			db, err := database.Open(t.TempDir() + "/events.db")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			slot := "55dca03b-4720-4052-b7c5-28eeb0597497"
			if _, err := db.Exec(`INSERT INTO slots(id,status,expires_at) VALUES(?,'waiting',?)`, slot, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			check := func(context.Context, string, string, []string) (map[string]bool, error) {
				if databaseFailure {
					return nil, errors.New("failed read")
				}
				return map[string]bool{}, nil
			}
			server := &Server{queries: database.NewQueries(db), sseHub: newSSEHub(check, time.Hour)}
			router := chi.NewRouter()
			router.Use(requestLimits)
			router.Get("/{slotID}", func(w http.ResponseWriter, r *http.Request) {
				principal := &authentication{user: &database.User{ID: "original-owner"}, session: &database.Session{ID: "original-session"}}
				server.slotEvents(w, r.WithContext(context.WithValue(r.Context(), authKey{}, principal)))
			})
			host := httptest.NewServer(router)
			defer host.Close()
			response, err := host.Client().Get(host.URL + "/" + slot)
			if err != nil {
				t.Fatalf("authorization response was lost: %v", err)
			}
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := http.StatusForbidden
			if databaseFailure {
				want = http.StatusServiceUnavailable
			}
			if response.StatusCode != want || len(data) == 0 || strings.Contains(string(data), "connected") {
				t.Fatalf("unexpected handshake: %d %s", response.StatusCode, data)
			}
		})
	}
}

type blockedEventWriter struct {
	entered, interrupted chan struct{}
	once                 sync.Once
}

func (w *blockedEventWriter) Header() http.Header { return make(http.Header) }
func (w *blockedEventWriter) WriteHeader(int)     {}
func (w *blockedEventWriter) Write([]byte) (int, error) {
	close(w.entered)
	<-w.interrupted
	return 0, context.Canceled
}
func (w *blockedEventWriter) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() && !deadline.After(time.Now()) {
		w.once.Do(func() { close(w.interrupted) })
	}
	return nil
}
func TestInboxEventOverflowInterruptsBlockedWrite(t *testing.T) {
	hub, clocks := controlledEventHub(func(context.Context, string, string, []string) (map[string]bool, error) { return nil, nil })
	writer := &blockedEventWriter{entered: make(chan struct{}), interrupted: make(chan struct{})}
	request := httptest.NewRequest("GET", "/events", nil)
	state := &requestDeadlineState{controller: http.NewResponseController(writer)}
	request = request.WithContext(context.WithValue(request.Context(), requestDeadlineKey{}, state))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(callbackDone); cancelRequestIO(request) })
	defer func() {
		if !stop() {
			<-callbackDone
		}
	}()
	reader := hub.subscribe("inbox", "owner", "session", cancel)
	clock := eventWait(t, clocks)
	writeDone := make(chan error, 1)
	go func() { _, err := writer.Write([]byte("already writing")); writeDone <- err }()
	eventWait(t, writer.entered)
	for i := 0; i <= eventQueueSize; i++ {
		hub.Send("inbox", "more events")
	}
	if err := eventWait(t, writeDone); err == nil {
		t.Fatal("blocked writer was not interrupted")
	}
	hub.unsubscribe(reader)
	eventWait(t, clock.stopped)
}

func TestInboxEventQueuedMessageRechecksReadAuthority(t *testing.T) {
	for _, databaseFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(databaseFailure), func(t *testing.T) {
			db, err := database.Open(t.TempDir() + "/events.db")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			slot := "e63a50c0-d65c-4371-b312-34d8f65b9ef7"
			if _, err := db.Exec(`INSERT INTO slots(id,status,expires_at) VALUES(?,'waiting',?)`, slot, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			var revoke atomic.Bool
			var checks atomic.Int32
			check := func(context.Context, string, string, []string) (map[string]bool, error) {
				checks.Add(1)
				if revoke.Load() {
					if databaseFailure {
						return nil, errors.New("query unavailable")
					}
					return nil, nil
				}
				return map[string]bool{"session": true}, nil
			}
			server := &Server{queries: database.NewQueries(db), sseHub: newSSEHub(check, time.Hour)}
			router := chi.NewRouter()
			router.Use(requestLimits)
			router.Get("/{slotID}", func(w http.ResponseWriter, r *http.Request) {
				server.slotEvents(w, r.WithContext(context.WithValue(r.Context(), authKey{}, &authentication{user: &database.User{ID: "owner"}, session: &database.Session{ID: "session"}})))
			})
			host := httptest.NewServer(router)
			defer host.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			request, _ := http.NewRequestWithContext(ctx, "GET", host.URL+"/"+slot, nil)
			response, err := host.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			scan := bufio.NewScanner(response.Body)
			for scan.Scan() {
				if scan.Text() == "" {
					break
				}
			}
			revoke.Store(true)
			server.sseHub.Send(slot, `{"event":"transfer_created","transfer_id":"private-child"}`)
			for scan.Scan() {
				if strings.Contains(scan.Text(), "private-child") {
					t.Fatal("queued event disclosed after authorization changed")
				}
			}
			if ctx.Err() != nil || checks.Load() != 2 {
				t.Fatalf("queued message was not rechecked immediately: %v / %d", ctx.Err(), checks.Load())
			}
		})
	}
}
