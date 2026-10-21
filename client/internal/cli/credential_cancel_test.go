package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/client/internal/state"
)

func TestWalletCancellationRemainsCommandCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	app := App{In: strings.NewReader(""), Out: &output, Err: new(bytes.Buffer), OpenState: func(config state.Config) (*state.Store, error) {
		cancel()
		<-config.Context.Done()
		return nil, state.ErrUnavailable
	}}
	if exit := app.Run(ctx, []string{"auth", "status", "--json", "--state-dir", t.TempDir()}); exit != ExitTransport {
		t.Fatalf("wallet cancellation became a storage failure: exit %d", exit)
	}
	var envelope result
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil || envelope.Error == nil || envelope.Error.Code != "interrupted" {
		t.Fatal("cancellation did not retain its structured error")
	}
}
