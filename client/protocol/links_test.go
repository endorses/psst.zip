package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestOriginsAndCanonicalCapabilities(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{{"HTTPS://Files.Example.:443/", "https://files.example"}, {"http://127.0.0.1:80", "http://127.0.0.1"}, {"http://[::1]:8080", "http://[::1]:8080"}} {
		got, e := NormalizeOrigin(tc.raw)
		if e != nil || got != tc.want {
			t.Fatalf("origin got %q %v", got, e)
		}
	}
	for _, bad := range []string{"https://good.example@evil.example", "https://good.example/path", "https://good.example?token=x", "https://good.example#key", "https://0177.0.0.1", "https://127.1", "https://files.example:", "https://files.example:0", "https://files.example\\evil", " https://files.example", "https://files.example\n"} {
		if _, e := NormalizeOrigin(bad); e == nil {
			t.Errorf("accepted invalid origin %q", bad)
		}
	}
	key := bytes.Repeat([]byte{19}, 32)
	id := "12345678-1234-4234-8234-123456789abc"
	for _, kind := range []string{"send", "receive"} {
		url, e := BuildLink("https://files.example", kind, id, key)
		if e != nil {
			t.Fatal(e)
		}
		l, e := ParseLink(url)
		if e != nil || l.Kind != kind || l.ID != id || !bytes.Equal(l.Key, key) {
			t.Fatal("link roundtrip", e)
		}
		if strings.Contains(l.String(), "ExMT") {
			t.Fatal("capability logged")
		}
		if _, e = ParseLink(url + "&extra"); e == nil {
			t.Fatal("accepted extra fragment")
		}
		if _, e = ParseLink(strings.Replace(url, "/"+id, "/"+id+"/extra", 1)); e == nil {
			t.Fatal("accepted extra path")
		}
	}
}
