package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBoundedCreationJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"empty", "", 0}, {"object", "{}", 0}, {"valid", `{"max_downloads":2}`, 0},
		{"malformed", `{"max_downloads":`, 400}, {"unknown", `{"unused":1}`, 400},
		{"null object", "null", 400}, {"null field", `{"max_downloads":null}`, 400},
		{"duplicate", `{"max_downloads":1,"max_downloads":0}`, 400},
		{"trailing", `{} {}`, 400}, {"fraction", `{"max_downloads":1.5}`, 400},
		{"oversized", `{"unused":"` + strings.Repeat("x", controlBodyLimit) + `"}`, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			writer := httptest.NewRecorder()
			var value CreateTransferRequest
			accepted := decodeCreation(writer, request, &value)
			if tc.status == 0 {
				if !accepted {
					t.Fatal(writer.Body.String())
				}
			} else if accepted || writer.Code != tc.status {
				t.Fatalf("accepted=%v status=%d body=%s", accepted, writer.Code, writer.Body.String())
			}
		})
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	reads, writes []time.Time
}

func (w *deadlineRecorder) SetReadDeadline(at time.Time) error {
	w.reads = append(w.reads, at)
	return nil
}
func (w *deadlineRecorder) SetWriteDeadline(at time.Time) error {
	w.writes = append(w.writes, at)
	return nil
}
func TestStreamingDeadlinesRefreshThroughWrappers(t *testing.T) {
	writer := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler := withRequestDeadlines(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1)
		if _, err := r.Body.Read(chunk); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
		if _, err := r.Body.Read(chunk); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(chunk)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("lost streaming flush")
		}
		flusher.Flush()
	}), time.Second, time.Second)
	handler.ServeHTTP(writer, httptest.NewRequest("PATCH", "/files/id", bytes.NewBufferString("xy")))
	if len(writer.reads) < 3 || !writer.reads[1].After(writer.reads[0]) || !writer.reads[len(writer.reads)-1].IsZero() {
		t.Fatalf("read deadlines: %v", writer.reads)
	}
	if len(writer.writes) < 3 || !writer.writes[len(writer.writes)-1].IsZero() {
		t.Fatalf("write deadlines: %v", writer.writes)
	}
}
func TestControlDeadlineDoesNotExtendWithProgress(t *testing.T) {
	writer := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler := withRequestDeadlines(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := make([]byte, 1)
		_, _ = r.Body.Read(p)
		time.Sleep(time.Millisecond)
		_, _ = r.Body.Read(p)
	}), time.Second, 30*time.Second)
	handler.ServeHTTP(writer, httptest.NewRequest("POST", "/slots", bytes.NewBufferString("xy")))
	if len(writer.reads) < 3 || !writer.reads[0].Equal(writer.reads[1]) {
		t.Fatalf("control deadline extended: %v", writer.reads)
	}
}
func TestStalledBodyDeadlineAndProgressingStream(t *testing.T) {
	server := httptest.NewServer(withRequestDeadlines(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			http.Error(w, "body timed out", http.StatusRequestTimeout)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), 50*time.Millisecond, 100*time.Millisecond))
	defer server.Close()
	for _, tc := range []struct {
		name     string
		method   string
		progress bool
		status   int
	}{
		{"stalled", "PATCH", false, 408}, {"progressing", "PATCH", true, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, writer := io.Pipe()
			defer func() { _ = reader.Close() }()
			defer func() { _ = writer.Close() }()
			go func() {
				defer func() { _ = writer.Close() }()
				_, _ = writer.Write([]byte("a"))
				if tc.progress {
					for i := 0; i < 6; i++ {
						time.Sleep(30 * time.Millisecond)
						if _, err := writer.Write([]byte("a")); err != nil {
							return
						}
					}
				} else {
					time.Sleep(200 * time.Millisecond)
				}
			}()
			request := httptest.NewRequest(tc.method, server.URL, reader)
			request.RequestURI = ""
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != tc.status {
				t.Fatalf("want %d got %d", tc.status, response.StatusCode)
			}
		})
	}
}
func TestCreationReaderCannotReadBeyondBound(t *testing.T) {
	request := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat(" ", controlBodyLimit+1)))
	var value CreateTransferRequest
	var limit *http.MaxBytesError
	if err := decodeJSON(httptest.NewRecorder(), request, &value); !errors.As(err, &limit) {
		t.Fatalf("want body bound, got %v", err)
	}
}

func TestEarlyResponseBoundsUnreadBodyDrain(t *testing.T) {
	server := httptest.NewServer(withRequestDeadlines(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "sign in required", http.StatusUnauthorized)
	}), 50*time.Millisecond, 100*time.Millisecond))
	defer server.Close()
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := server.Client().Post(server.URL, "application/json", reader)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("early response left unread body drain unbounded")
	}
}
