package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/endorses/psst.zip/client/protocol"
	qr "github.com/piglig/go-qr/v2"
)

const maxSavedJSON = 64 * 1024

type savedShare struct {
	Kind         string     `json:"kind"`
	ID           string     `json:"id"`
	Origin       string     `json:"origin"`
	Link         string     `json:"link"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	KeyAvailable *bool      `json:"key_available,omitempty"`
	OperationID  string     `json:"operation_id,omitempty"`
}

// linkFromJSON accepts only individual version-1 CLI share results. In
// particular, it cannot accidentally render a pairing credential or a list.
func linkFromJSON(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxSavedJSON+1))
	if err != nil || len(b) > maxSavedJSON {
		return "", usage("Cannot read a bounded CLI JSON result (maximum 64 KiB).")
	}
	if err = uniqueJSON(b); err != nil {
		return "", usage("Expected one unambiguous JSON document.")
	}
	var envelope struct {
		SchemaVersion int             `json:"schema_version"`
		OK            bool            `json:"ok"`
		Data          savedShare      `json:"data"`
		Error         json.RawMessage `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || envelope.SchemaVersion != 1 || !envelope.OK || len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return "", usage("Expected a successful schema_version 1 CLI Send or Receive result.")
	}
	d := envelope.Data
	link, err := protocol.ParseLink(d.Link)
	if err != nil || d.Kind != link.Kind || d.ID != link.ID || d.Origin != link.Origin {
		return "", usage("The JSON result needs one complete Send or Receive link with matching kind, id and origin.")
	}
	canonical, err := link.URL()
	if err != nil || canonical != d.Link {
		return "", usage("The JSON result contains a noncanonical share link.")
	}
	return d.Link, nil
}

// Reject duplicate object fields and excessive nesting before decoding a typed
// result; encoding/json otherwise accepts the last duplicate silently.
func uniqueJSON(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 32 {
			return errors.New("too deeply nested")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				key, ok := k.(string)
				key = strings.ToLower(key)
				if !ok || seen[key] {
					return errors.New("duplicate field")
				}
				seen[key] = true
				if e = value(depth + 1); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("unexpected delimiter")
		}
		end, err := d.Token()
		if err != nil {
			return err
		}
		if delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
			return errors.New("unclosed JSON")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

// qrArt keeps all modules and a four-module quiet zone. Colored half-block mode
// resets inherited styles and requests RGB black on white on every line, rather
// than using the terminal theme's configurable ANSI palette slots.
func qrArt(link string, columns int, plain bool) (string, error) {
	if _, err := protocol.ParseLink(link); err != nil {
		return "", usage("A complete psst.zip Send or Receive link is required.")
	}
	code, err := qr.Encode(link, qr.WithECC(qr.ECCMedium))
	if err != nil {
		return "", usage("The share link exceeds single QR-code capacity.")
	}
	side := code.Size() + 8
	width := side
	if plain {
		width *= 2
	}
	if columns > 0 && columns < width {
		return "", usage(fmt.Sprintf("The QR code needs %d terminal columns; widen the terminal or render to a file.", width))
	}
	module := func(x, y int) bool { return x >= 4 && y >= 4 && x < side-4 && y < side-4 && code.Module(x-4, y-4) }
	var out strings.Builder
	step := 2
	if plain {
		step = 1
	}
	for y := 0; y < side; y += step {
		if !plain {
			out.WriteString("\x1b[0;38;2;0;0;0;48;2;255;255;255m")
		}
		for x := 0; x < side; x++ {
			if plain {
				if module(x, y) {
					out.WriteString("██")
				} else {
					out.WriteString("  ")
				}
				continue
			}
			top, bottom := module(x, y), module(x, y+1)
			switch {
			case top && bottom:
				out.WriteRune('█')
			case top:
				out.WriteRune('▀')
			case bottom:
				out.WriteRune('▄')
			default:
				out.WriteByte(' ')
			}
		}
		if !plain {
			out.WriteString("\x1b[0m")
		}
		out.WriteByte('\n')
	}
	return out.String(), nil
}
