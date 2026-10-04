package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPasswordAdmissionRejectsWithoutQueueAndRecovers(t *testing.T) {
	s := &Server{passwordWork: make(chan struct{}, 1)}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	guarded := s.limitPasswordWork(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(204) }))
	go func() {
		defer close(done)
		guarded.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/auth/login", nil))
	}()
	<-entered
	denied := httptest.NewRecorder()
	guarded.ServeHTTP(denied, httptest.NewRequest("POST", "/auth/login", nil))
	if denied.Code != 429 || denied.Header().Get("Retry-After") == "" {
		t.Fatalf("missing bounded rejection: %d", denied.Code)
	}
	close(release)
	<-done
	recovered := httptest.NewRecorder()
	s.limitPasswordWork(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(recovered, httptest.NewRequest("POST", "/auth/login", nil))
	if recovered.Code != 204 {
		t.Fatal("capacity leaked")
	}
}
