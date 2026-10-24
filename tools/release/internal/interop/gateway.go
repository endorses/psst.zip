package interop

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

type gateway struct {
	server         *http.Server
	listener       net.Listener
	origin         string
	mutex          sync.Mutex
	receiptFailure string
	gets           map[string]int
	transport      *http.Transport
}

func newGateway(listener net.Listener, backend string, template []byte) *gateway {
	target := &url.URL{Scheme: "http", Host: backend}
	proxy := httputil.NewSingleHostReverseProxy(target)
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 5 * time.Second, MaxIdleConnsPerHost: 4}
	proxy.Transport = transport
	proxy.ErrorLog = quietLogger()
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(502)
		_, _ = io.WriteString(w, "{}")
	}
	fixture := &gateway{listener: listener, origin: "http://" + listener.Addr().String(), gets: map[string]int{}, transport: transport}
	fixture.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && (r.URL.Path == "/d/_connection_check" || r.URL.Path == "/u/_connection_check") {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(template)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			w.WriteHeader(404)
			return
		}
		fixture.mutex.Lock()
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/files/") {
			fixture.gets[r.URL.Path]++
		}
		if r.Method == http.MethodPost && r.URL.Path == fixture.receiptFailure {
			fixture.receiptFailure = ""
			fixture.mutex.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Psst-Error-Code", "fixture_receipt_retry")
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"code":"fixture_receipt_retry","error":"disposable receipt interruption"}`)
			return
		}
		fixture.mutex.Unlock()
		r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
		proxy.ServeHTTP(w, r)
	})}
	return fixture
}
func (g *gateway) serve() chan error {
	done := make(chan error, 1)
	go func() {
		err := g.server.Serve(g.listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
		close(done)
	}()
	return done
}
func (g *gateway) close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = g.server.Shutdown(ctx)
	_ = g.server.Close()
	g.transport.CloseIdleConnections()
}
func (g *gateway) failReceipt(path string) { g.mutex.Lock(); g.receiptFailure = path; g.mutex.Unlock() }
func (g *gateway) counts() map[string]int {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	counts := map[string]int{}
	for path, count := range g.gets {
		counts[path] = count
	}
	return counts
}
