package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Stable presentation codes accompany diagnostics; clients never classify English prose.
func writeError(w http.ResponseWriter, status int, msg string, codes ...string) {
	code := defaultErrorCode(status)
	if len(codes) > 0 {
		code = codes[0]
	}
	w.Header().Set("X-Psst-Error-Code", code)
	writeJSON(w, status, ErrorResponse{Error: msg, Code: code})
}

func defaultErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "authentication_required"
	case http.StatusForbidden:
		return "permission_denied"
	case http.StatusNotFound:
		return "resource_not_found"
	case http.StatusConflict:
		return "request_conflict"
	case http.StatusGone:
		return "resource_revoked"
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "request_timeout"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	default:
		return "internal_error"
	}
}

// Empty bodies remain supported for legacy creation clients. Every nonempty
// body must be one strict JSON object; malformed input never creates defaults.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	defer func() { _ = r.Body.Close() }()
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, controlBodyLimit))
	if err != nil {
		return err
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	if data[0] != '{' {
		return errors.New("request must be a JSON object")
	}
	// Reject duplicate fields and null values instead of silently selecting a
	// default or the last occurrence of an allowance.
	fields := json.NewDecoder(bytes.NewReader(data))
	if _, err := fields.Token(); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for fields.More() {
		token, err := fields.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok || seen[name] {
			return errors.New("duplicate or invalid field")
		}
		seen[name] = true
		var value json.RawMessage
		if err := fields.Decode(&value); err != nil {
			return err
		}
		allowsTitleNull := false
		switch v.(type) {
		case *CreateTransferRequest, *CreateSlotRequest, *RenameTitleRequest:
			allowsTitleNull = name == "title"
		}
		if allowsTitleNull && !validEncodedLinkTitle(value) {
			return errors.New("invalid Unicode title")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) && !allowsTitleNull {
			return errors.New("null fields are not supported")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
func decodeCreation(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := decodeJSON(w, r, v); err != nil {
		var sizeError *http.MaxBytesError
		var timeout net.Error
		if errors.As(err, &sizeError) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds 8 KiB")
		} else if errors.As(err, &timeout) && timeout.Timeout() {
			writeError(w, http.StatusRequestTimeout, "request body timed out")
		} else {
			switch v.(type) {
			case *CreateTransferRequest, *CreateSlotRequest:
				policyError(w, http.StatusBadRequest, "invalid_link_policy", "invalid creation request: use supported fields and integer limits")
			default:
				writeError(w, http.StatusBadRequest, "invalid request body")
			}
		}
		return false
	}
	return true
}
