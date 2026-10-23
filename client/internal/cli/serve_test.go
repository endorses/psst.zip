package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/client"
	"github.com/endorses/psst.zip/client/internal/lan"
	"github.com/endorses/psst.zip/client/internal/state"
	"github.com/endorses/psst.zip/client/protocol"
)

func TestServeInvalidFlagsNeverOpenAccountStorage(t *testing.T) {
	cases := [][]string{
		{"serve", "receive", "--listen", "0.0.0.0:0", "--output-dir", "unused"},
		{"serve", "receive", "--listen", "127.0.0.1:0", "--output-dir", "unused", "--expires", "0"},
		{"serve", "receive", "--listen", "127.0.0.1:0", "--output-dir", "unused", "--tls-cert", "one-sided"},
		{"serve", "receive", "--listen", "127.0.0.1:0", "--output-dir", "unused", "--max-bytes", "unlimited"},
		{"serve", "receive", "--listen", "127.0.0.1:0", "--output-dir", "unused", "--json", "--qr"},
	}
	for _, args := range cases {
		var out, diagnostics bytes.Buffer
		app := App{In: strings.NewReader(""), Out: &out, Err: &diagnostics, OpenState: func(state.Config) (*state.Store, error) {
			t.Fatal("LAN command opened a credential store")
			return nil, nil
		}}
		if code := app.Run(context.Background(), args); code != ExitUsage {
			t.Fatalf("%v exit %d stderr %s", args, code, diagnostics.String())
		}
	}
}

func TestServeTimeoutAndSavedJSONContract(t *testing.T) {
	for _, value := range []struct {
		args     []string
		explicit bool
	}{{[]string{"serve", "receive"}, false}, {[]string{"serve", "receive", "--timeout", "3m"}, true}} {
		options, args, err := parseCommon(value.args)
		if err != nil {
			t.Fatal(err)
		}
		if options.timeoutExplicit != value.explicit || len(args) != 2 || options.timeout <= 0 {
			t.Fatal("global timeout tracking incorrect")
		}
	}
	id := "12345678-1234-4234-8234-123456789abc"
	origin := "http://192.168.1.2:49152"
	link, err := protocol.BuildLink(origin, "send", id, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	startup := lan.Startup{Share: client.Share{Kind: "send", ID: id, Origin: origin, Link: link, ExpiresAt: time.Now().Add(time.Hour), KeyAvailable: true}, Hosting: lan.Hosting{Mode: "lan", Listen: "192.168.1.2:49152", MaxBytes: 1 << 30, MaxFiles: 100, MaxDownloads: 1, MaxStreams: 2}}
	var output bytes.Buffer
	if err = writeJSON(&output, startup, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := linkFromJSON(&output); err != nil || got != link {
		t.Fatalf("saved startup QR: %q %v", got, err)
	}
}
