package interop

import (
	"context"
	"encoding/base32"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTOTPIndependentRFCVector(t *testing.T) {
	// RFC 6238's SHA-1 vector is 94287082 at Unix 59; the production API
	// requires its six-digit suffix. This must not reuse backend TOTP code.
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	code, err := totp(secret, time.Unix(59, 0))
	if err != nil || code != "287082" {
		t.Fatalf("independent TOTP vector: %q %v", code, err)
	}
	if _, err := totp("invalid!", time.Unix(59, 0)); err == nil {
		t.Fatal("invalid fixture secret accepted")
	}
}

func TestGatewayInterruptionIsOneShotAndCountsOnlyFileGETs(t *testing.T) {
	var posts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			posts.Add(1)
		}
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gateway := newGateway(listener, target.Host, []byte("production identity fixture"))
	done := gateway.serve()
	defer func() { gateway.close(); <-done }()
	client := newAPI(context.Background(), gateway.origin)
	defer client.client.CloseIdleConnections()
	path := "/api/v1/transfers/disposable/downloaded"
	gateway.failReceipt(path)
	for _, status := range []int{503, 204} {
		if _, err := client.request("POST", "/transfers/disposable/downloaded", map[string]any{}, "", status); err != nil {
			t.Fatal(err)
		}
	}
	if posts.Load() != 1 {
		t.Fatal("receipt fault did not interrupt exactly one upstream request")
	}
	if _, err := client.request("GET", "/transfers/disposable/files/blob", nil, "", 204); err != nil {
		t.Fatal(err)
	}
	if _, err := client.request("GET", "/health", nil, "", 204); err != nil {
		t.Fatal(err)
	}
	counts := gateway.counts()
	counts["/api/v1/transfers/disposable/files/blob"] = 99
	if fresh := gateway.counts(); len(fresh) != 1 || fresh["/api/v1/transfers/disposable/files/blob"] != 1 {
		t.Fatal("file counters included unrelated traffic or exposed mutable state")
	}
}

func TestPrivateChildCancellationReapsAndWithholdsOutput(t *testing.T) {
	if os.Getenv("PSST_INTEROP_TEST_CHILD") == "wait" {
		_, _ = io.WriteString(os.Stdout, "private fixture output must stay private")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestPrivateChildCancellationReapsAndWithholdsOutput$")
	command.Env = environment(map[string]string{"PSST_INTEROP_TEST_CHILD": "wait"})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	data, code, err := runChild(ctx, command)
	if err == nil || len(data) != 0 || code != -1 || strings.Contains(err.Error(), "private fixture output") {
		t.Fatal("cancelled private child leaked output or succeeded")
	}
	if command.ProcessState == nil {
		t.Fatal("cancelled child was not reaped")
	}
}
