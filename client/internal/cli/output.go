package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// Exit codes are part of the versioned scripting interface.
const (
	ExitOK          = 0
	ExitUsage       = 2
	ExitAuth        = 3
	ExitUnavailable = 4
	ExitTransport   = 5
	ExitStorage     = 6
	ExitCrypto      = 7
)

type Failure struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	ServerCode        string `json:"server_code,omitempty"`
	RetryAfterSeconds int64  `json:"retry_after_seconds,omitempty"`
	OperationID       string `json:"operation_id,omitempty"`
	exit              int
}

func (f *Failure) Error() string { return f.Message }

type result struct {
	SchemaVersion int      `json:"schema_version"`
	OK            bool     `json:"ok"`
	Data          any      `json:"data,omitempty"`
	Error         *Failure `json:"error,omitempty"`
}

func writeJSON(w io.Writer, data any, failure *Failure) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result{SchemaVersion: 1, OK: failure == nil, Data: data, Error: failure})
}

func usage(message string) *Failure {
	return &Failure{Code: "invalid_input", Message: message, exit: ExitUsage}
}
func storageFailure(message string) *Failure {
	return &Failure{Code: "local_storage", Message: message, exit: ExitStorage}
}

// Terminal text is never allowed to execute control sequences supplied by files,
// titles, server errors, or local paths. Deliberate QR ANSI output is separate.
func safeText(s string) string {
	var out strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069' {
			out.WriteString(strconv.QuoteRuneToASCII(r))
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func humanResult(w io.Writer, data any) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	for i := range lines {
		lines[i] = safeText(lines[i])
	}
	_, err = fmt.Fprintln(w, strings.Join(lines, "\n"))
	return err
}
