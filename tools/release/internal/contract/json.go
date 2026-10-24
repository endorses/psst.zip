// Package contract contains the small, dependency-free release input contract.
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Decode rejects duplicate keys, trailing values and unknown struct fields.
// Required-field and semantic validation belongs to the concrete contract type.
func Decode(data []byte, limit int64, target any) error {
	if int64(len(data)) > limit {
		return fmt.Errorf("JSON exceeds its size limit")
	}
	check := json.NewDecoder(bytes.NewReader(data))
	check.UseNumber()
	if err := value(check, 0); err != nil {
		return err
	}
	if _, err := check.Token(); err != io.EOF {
		return fmt.Errorf("JSON contains trailing data")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON contract: %w", err)
	}
	return nil
}

func value(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds limit")
	}
	t, err := d.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	switch t {
	case json.Delim('{'):
		keys := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return fmt.Errorf("duplicate or invalid JSON object key")
			}
			keys[name] = true
			if err := value(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("invalid JSON object")
		}
	case json.Delim('['):
		for d.More() {
			if err := value(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid JSON array")
		}
	default:
		if _, ok := t.(json.Delim); ok {
			return fmt.Errorf("unexpected JSON delimiter")
		}
	}
	return nil
}

func JSON(v any) ([]byte, error) {
	var out bytes.Buffer
	e := json.NewEncoder(&out)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
