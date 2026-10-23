package lan

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
)

// handlerTransport keeps owner authorization entirely inside this process. Pipes
// stream payloads with backpressure rather than buffering ciphertext in memory.
type handlerTransport struct{ handler http.Handler }

func (t handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(request.Context())
	request = request.Clone(ctx)
	request.RequestURI = request.URL.RequestURI()
	request.RemoteAddr = "127.0.0.1:0"
	reader, writer := io.Pipe()
	stop := context.AfterFunc(ctx, func() { _ = reader.CloseWithError(ctx.Err()); _ = writer.CloseWithError(ctx.Err()) })
	body := &ownerBody{PipeReader: reader, cancel: cancel, stop: stop}
	response := make(chan *http.Response, 1)
	w := &handlerWriter{header: make(http.Header), writer: writer, response: response, request: request, body: body}
	go func() {
		defer func() {
			if request.Body != nil {
				_ = request.Body.Close()
			}
			if recover() != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = writer.CloseWithError(errors.New("private owner handler failed"))
			} else {
				_ = writer.Close()
			}
		}()
		t.handler.ServeHTTP(w, request)
		w.WriteHeader(http.StatusOK)
	}()
	select {
	case value := <-response:
		return value, nil
	case <-ctx.Done():
		_ = body.Close()
		return nil, ctx.Err()
	}
}

type ownerBody struct {
	*io.PipeReader
	cancel context.CancelFunc
	stop   func() bool
}

func (b *ownerBody) Close() error { b.stop(); b.cancel(); return b.PipeReader.Close() }

type handlerWriter struct {
	header   http.Header
	writer   *io.PipeWriter
	response chan *http.Response
	request  *http.Request
	once     sync.Once
	body     io.ReadCloser
}

func (w *handlerWriter) Header() http.Header { return w.header }
func (w *handlerWriter) WriteHeader(status int) {
	w.once.Do(func() {
		w.response <- &http.Response{StatusCode: status, Header: w.header.Clone(), Body: w.body, ContentLength: -1, Request: w.request}
	})
}
func (w *handlerWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.writer.Write(b)
}
func (w *handlerWriter) Flush() { w.WriteHeader(http.StatusOK) }
