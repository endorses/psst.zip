package api

import (
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

// Every stream flushes at 1 MiB, once per second, at UTC midnight, and on exit.
// No file bodies are buffered. A failed delta is discarded and permanently marks
// accounting degraded; retrying an uncertain commit could double-count traffic.
type trafficMeter struct {
	mu      sync.Mutex
	server  *Server
	date    time.Time
	pending database.TrafficTotals
	stop    chan struct{}
	done    chan struct{}
}

func (s *Server) newTrafficMeter() *trafficMeter {
	m := &trafficMeter{server: s, date: time.Now().UTC(), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.mu.Lock()
				m.flushLocked()
				m.mu.Unlock()
			case <-m.stop:
				return
			}
		}
	}()
	return m
}
func (m *trafficMeter) flushLocked() {
	if m.pending.UploadedBytes == 0 && m.pending.DownloadedBytes == 0 {
		return
	}
	if err := m.server.queries.AddTraffic(m.date, m.pending); err != nil {
		m.server.trafficDegraded.Store(true)
		if persistErr := m.server.queries.MarkTrafficDegraded(); persistErr != nil {
			log.Printf("traffic accounting unavailable; degradation marker could not persist: %v", persistErr)
		}
		log.Printf("traffic accounting degraded: %v", err)
	}
	m.pending = database.TrafficTotals{}
}
func (m *trafficMeter) add(n int, upload bool) {
	if n <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	if now.Format("2006-01-02") != m.date.Format("2006-01-02") {
		m.flushLocked()
	}
	m.date = now
	if upload {
		m.pending.UploadedBytes += int64(n)
	} else {
		m.pending.DownloadedBytes += int64(n)
	}
	if m.pending.UploadedBytes+m.pending.DownloadedBytes >= 1<<20 {
		m.flushLocked()
	}
}
func (m *trafficMeter) close() {
	close(m.stop)
	<-m.done
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flushLocked()
}

type trafficBody struct {
	io.ReadCloser
	meter *trafficMeter
}

func (b trafficBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.meter.add(n, true)
	return n, err
}

type trafficWriter struct {
	http.ResponseWriter
	meter  *trafficMeter
	status int
}

func (w *trafficWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *trafficWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *trafficWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	if w.status == 200 || w.status == 206 {
		w.meter.add(n, false)
	}
	return n, err
}
func (s *Server) measureUpload(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := s.newTrafficMeter()
		defer m.close()
		r.Body = trafficBody{r.Body, m}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) measureDownload(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := s.newTrafficMeter()
		defer m.close()
		next.ServeHTTP(&trafficWriter{ResponseWriter: w, meter: m}, r)
	})
}

// Track entire handlers, including those waiting for authorization/storage locks,
// so graceful shutdown cannot close the DB before their final accounting flush.
func (s *Server) trackRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		defer s.requests.Done()
		next.ServeHTTP(w, r)
	})
}
func (s *Server) WaitForRequests() { s.requests.Wait() }
