package api_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/store"
)

func TestInvalidCreationDoesNotAllocateResources(t *testing.T) {
	env := setup(t)
	slot := ownedSlot(t, env)
	for _, endpoint := range []string{"/transfers", "/slots", "/slots/" + slot.ID + "/transfers"} {
		for _, tc := range []struct {
			body   string
			status int
		}{{`{"unexpected":1}`, 400}, {`null`, 400}, {`{} {}`, 400}, {`{"x":"` + strings.Repeat("a", 8192) + `"}`, 413}} {
			request(t, env, "POST", env.url("/api/v1"+endpoint), strings.NewReader(tc.body), tc.status)
		}
	}
	var transfers, slots int
	if err := env.db.QueryRow("SELECT count(*) FROM transfers").Scan(&transfers); err != nil {
		t.Fatal(err)
	}
	if err := env.db.QueryRow("SELECT count(*) FROM slots").Scan(&slots); err != nil {
		t.Fatal(err)
	}
	if transfers != 0 || slots != 1 {
		t.Fatalf("invalid input created resources: transfers=%d slots=%d", transfers, slots)
	}
}
func TestSlowSlotCreationBodyDoesNotHoldSlotLock(t *testing.T) {
	env := setup(t)
	slot := ownedSlot(t, env)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", env.url("/api/v1/slots/"+slot.ID+"/transfers"), reader)
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := env.server.Client().Do(req)
		if err == nil {
			response.Body.Close()
		}
	}()
	// Pipe.Write returning means the client started sending the deliberately
	// incomplete JSON body. The parallel bounded creation must remain usable.
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	request(t, env, "POST", env.url("/api/v1/slots/"+slot.ID+"/transfers"), nil, http.StatusCreated)
	unlock, err := store.TryLockSlot(slot.ID)
	if err != nil {
		t.Fatalf("slow body held slot lock: %v", err)
	}
	unlock()
	cancel()
	writer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled request did not stop")
	}
}
