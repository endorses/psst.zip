package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/endorses/psst.zip/backend/internal/config"
)

func TestStreamAdmissionScopesAndRelease(t *testing.T) {
	for _, scope := range []string{"owner", "ip", "transfer", "slot"} {
		t.Run(scope, func(t *testing.T) {
			admission := newStreamAdmission(config.Config{MaxActiveStreams: 8, MaxStreamsPerAccount: 1, MaxStreamsPerIP: 1, MaxStreamsPerTransfer: 1, MaxStreamsPerSlot: 1})
			release, ok := admission.acquire("one", "one", "one", "one")
			if !ok {
				t.Fatal("initial admission denied")
			}
			owner, ip, transfer, slot := "two", "two", "two", "two"
			switch scope {
			case "owner":
				owner = "one"
			case "ip":
				ip = "one"
			case "transfer":
				transfer = "one"
			case "slot":
				slot = "one"
			}
			if done, ok := admission.acquire(owner, ip, transfer, slot); ok {
				done()
				t.Fatalf("%s cap bypassed", scope)
			}
			release()
			release()
			done, ok := admission.acquire(owner, ip, transfer, slot)
			if !ok {
				t.Fatal("release did not restore admission")
			}
			done()
			if admission.active != 0 || len(admission.counts) != 0 {
				t.Fatal("leaked admission state")
			}
		})
	}
}
func TestStreamAdmissionConcurrentGlobalBound(t *testing.T) {
	admission := newStreamAdmission(config.Config{MaxActiveStreams: 4})
	var wait sync.WaitGroup
	releases := make(chan func(), 32)
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			key := fmt.Sprint(i)
			if release, ok := admission.acquire(key, key, key, key); ok {
				releases <- release
			}
		}(i)
	}
	wait.Wait()
	close(releases)
	if len(releases) != 4 {
		t.Fatalf("admitted %d, want4", len(releases))
	}
	for release := range releases {
		release()
	}
}
func TestRecoveryRequestLaneSurvivesPayloadSaturation(t *testing.T) {
	server := &Server{applicationRequests: make(chan struct{}, 1), recoveryRequests: make(chan struct{}, 1)}
	server.applicationRequests <- struct{}{}
	calls := 0
	handler := server.admitRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }))
	busy := httptest.NewRecorder()
	handler.ServeHTTP(busy, httptest.NewRequest("GET", "/api/v1/transfers/id/files/file", nil))
	if busy.Code != 503 || calls != 0 {
		t.Fatal("saturated application request reached handler")
	}
	recovery := httptest.NewRecorder()
	handler.ServeHTTP(recovery, httptest.NewRequest("GET", "/api/v1/admin/overview", nil))
	if recovery.Code != 204 || calls != 1 {
		t.Fatal("application saturation blocked recovery")
	}
	if len(server.recoveryRequests) != 0 {
		t.Fatal("recovery slot leaked")
	}
}
func TestBodylessGETHasNoControlReadDeadline(t *testing.T) {
	writer := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	requestLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(writer, httptest.NewRequest("GET", "/events", nil))
	for _, deadline := range writer.reads {
		if !deadline.IsZero() {
			t.Fatalf("bodyless stream inherited read deadline: %v", deadline)
		}
	}
}
func TestCancellationCannotExtendIODeadline(t *testing.T) {
	writer := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	requestLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancelRequestIO(r)
		_, _ = w.Write([]byte("x"))
		state := r.Context().Value(requestDeadlineKey{}).(*requestDeadlineState)
		if !state.canceled {
			t.Fatal("cancellation not retained")
		}
	})).ServeHTTP(writer, httptest.NewRequest("GET", "/files/id", nil).WithContext(context.Background()))
	if len(writer.writes) == 0 || writer.writes[len(writer.writes)-1].IsZero() {
		t.Fatal("cancellation deadline cleared")
	}
}

func TestRecoveryRateBucketSurvivesPayloadRateSaturation(t *testing.T) {
	server := &Server{}
	handler := server.limitRequestRates(newRateLimiter(0, 1), newRateLimiter(0, 1))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, test := range []struct {
		path   string
		status int
	}{{"/api/v1/transfers/id/manifest", 204}, {"/api/v1/transfers/id/manifest", 429}, {"/api/v1/admin/overview", 204}} {
		request := httptest.NewRequest("GET", test.path, nil)
		request.RemoteAddr = "192.0.2.1:80"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s expected%d got%d", test.path, test.status, response.Code)
		}
	}
}
