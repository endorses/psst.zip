package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/client/internal/state"
	"github.com/endorses/psst.zip/client/protocol"
	qr "github.com/piglig/go-qr/v2"
)

func shareFixture(t *testing.T, kind string) (string, []byte) {
	t.Helper()
	id := "12345678-1234-1234-1234-123456789012"
	link, e := protocol.BuildLink("https://files.example:8443", kind, id, bytes.Repeat([]byte{3}, 32))
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(result{SchemaVersion: 1, OK: true, Data: savedShare{Kind: kind, ID: id, Origin: "https://files.example:8443", Link: link}})
	if e != nil {
		t.Fatal(e)
	}
	return link, b
}

// Reconstruct the actual terminal output's module pixels and decode them through
// the QR library's separate image decoder. An encoder matrix snapshot would not
// catch an inverted half-block, missing quiet zone, or dropped key fragment.
func decodeTerminal(t *testing.T, art string, plain bool) string {
	t.Helper()
	art = strings.ReplaceAll(strings.ReplaceAll(art, "\x1b[30;47m", ""), "\x1b[0m", "")
	lines := strings.Split(strings.TrimSuffix(art, "\n"), "\n")
	w := len([]rune(lines[0]))
	h := len(lines) * 2
	if plain {
		w /= 2
		h = len(lines)
	}
	const scale = 4
	img := image.NewGray(image.Rect(0, 0, w*scale, h*scale))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	paint := func(x, y int) {
		for dy := 0; dy < scale; dy++ {
			for dx := 0; dx < scale; dx++ {
				img.SetGray(x*scale+dx, y*scale+dy, color.Gray{Y: 0})
			}
		}
	}
	for y, line := range lines {
		runes := []rune(line)
		for x, r := range runes {
			if plain {
				if x%2 == 0 && r == '█' {
					paint(x/2, y)
				}
				continue
			}
			if r == '█' || r == '▀' {
				paint(x, y*2)
			}
			if r == '█' || r == '▄' {
				paint(x, y*2+1)
			}
		}
	}
	d, e := qr.Decode(img, qr.WithFastPathOnly())
	if e != nil {
		t.Fatal(e)
	}
	return d.Text
}

func TestOfflineJSONQRRoundTrip(t *testing.T) {
	for _, kind := range []string{"send", "receive"} {
		for _, plain := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "half", true: "plain"}[plain], func(t *testing.T) {
				link, b := shareFixture(t, kind)
				var out, diagnostics bytes.Buffer
				a := App{In: bytes.NewReader(b), Out: &out, Err: &diagnostics, OpenState: func(state.Config) (*state.Store, error) { t.Fatal("offline QR touched credentials"); return nil, nil }, HTTP: &http.Client{Transport: forbiddenTransport{t}}}
				args := []string{"qr", "--json-file", "-"}
				if plain {
					args = append(args, "--plain")
				}
				if exit := a.Run(context.Background(), args); exit != 0 {
					t.Fatalf("exit%d: %s", exit, diagnostics.String())
				}
				first, art, ok := strings.Cut(out.String(), "\n")
				if !ok || first != link {
					t.Fatal("QR output lost complete link")
				}
				if got := decodeTerminal(t, art, plain); got != link {
					t.Fatal("terminal QR changed link or key fragment")
				}
			})
		}
	}
}

type forbiddenTransport struct{ t *testing.T }

func (f forbiddenTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Fatal("offline QR accessed network")
	return nil, nil
}

func TestSavedJSONRejectsAmbiguousAndUnsupportedInputs(t *testing.T) {
	_, good := shareFixture(t, "send")
	cases := [][]byte{
		[]byte("[]"), []byte("null"), append(append([]byte{}, good...), good...),
		bytes.Replace(good, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1),
		bytes.Replace(good, []byte(`"ok":true`), []byte(`"ok":false`), 1),
		bytes.Replace(good, []byte(`"ok":true`), []byte(`"ok":false,"ok":true`), 1),
		bytes.Replace(good, []byte(`"ok":true`), []byte(`"ok":false,"OK":true`), 1),
		bytes.Replace(good, []byte(`"ok":true`), []byte(`"ok":true,"resources":[]`), 1),
		bytes.Replace(good, []byte(`"kind":"send"`), []byte(`"kind":"receive"`), 1),
		bytes.Replace(good, []byte(`"link":`), []byte(`"unrecognized_link":`), 1),
		[]byte(strings.Repeat(" ", maxSavedJSON+1)),
		[]byte(`{"type":"psst-pairing","version":1,"code":"not-a-transfer"}`),
	}
	for i, b := range cases {
		if _, e := linkFromJSON(bytes.NewReader(b)); e == nil {
			t.Errorf("case%d accepted", i)
		}
	}
}

func TestQRWidthAndContradictoryFlagsBeforePrivateState(t *testing.T) {
	link, _ := shareFixture(t, "send")
	if _, e := qrArt(link, 10, false); e == nil {
		t.Fatal("wrapped QR would be unreadable")
	}
	for _, args := range [][]string{{"send", "--qr", "--json", "nonexistent"}, {"receive", "create", "--qr", "--json"}, {"qr", "--json-file", "-", "--json"}, {"unknown", "--json"}, {"send", "--resume", "bad", "--json"}} {
		var out, diag bytes.Buffer
		a := App{Out: &out, Err: &diag, In: strings.NewReader(""), OpenState: func(state.Config) (*state.Store, error) {
			t.Fatal("invalid command touched private state")
			return nil, nil
		}}
		if got := a.Run(context.Background(), args); got != ExitUsage {
			t.Fatalf("%v exit%d %s", args, got, diag.String())
		}
		var envelope result
		if e := json.Unmarshal(out.Bytes(), &envelope); e != nil || envelope.OK || envelope.SchemaVersion != 1 || envelope.Error == nil {
			t.Fatalf("failure is not one JSON envelope: %s", out.String())
		}
	}
}

func TestTerminalDiagnosticsEscapeControlText(t *testing.T) {
	raw := "file\x1b[2J\r\n\u202e.exe"
	safe := safeText(raw)
	if strings.ContainsAny(safe, "\x1b\r\n\u202e") {
		t.Fatal("untrusted terminal controls survived")
	}
	var output bytes.Buffer
	if err := humanResult(&output, map[string]string{"title": "unsafe\u009b2J\u202e.txt"}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(output.String(), "\u009b\u202e") {
		t.Fatal("human result bypassed terminal escaping")
	}
}

type observedPipe struct {
	*io.PipeReader
	entered chan struct{}
}

func (p observedPipe) Read(b []byte) (int, error) {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	return p.PipeReader.Read(b)
}

func TestBlockedInputHonorsCommandCancellation(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	entered := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, diag bytes.Buffer
	a := App{In: observedPipe{r, entered}, Out: &out, Err: &diag}
	done := make(chan int, 1)
	go func() { done <- a.Run(ctx, []string{"qr", "--json-file", "-"}) }()
	<-entered
	cancel()
	select {
	case exit := <-done:
		if exit != ExitTransport {
			t.Fatalf("cancelled input returned %d", exit)
		}
	case <-time.After(time.Second):
		t.Fatal("unclosed stdin defeated cancellation")
	}
}

func TestPrivateInputUnicodeEditingAndInterrupt(t *testing.T) {
	b, err := readPrivateLine(context.Background(), strings.NewReader("aä\x7fZ\n"))
	if err != nil || string(b) != "aZ" {
		t.Fatal("private input lost Unicode editing semantics")
	}
	if _, err = readPrivateLine(context.Background(), strings.NewReader("discard\x03")); err != context.Canceled {
		t.Fatal("private input ignored Ctrl-C")
	}
}
