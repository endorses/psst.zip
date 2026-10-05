package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorPresentationContract(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 410, 413, 429, 503, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeError(recorder, status, "compatibility diagnostic")
			var body ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != status || body.Error != "compatibility diagnostic" || body.Code == "" || body.Code != recorder.Header().Get("X-Psst-Error-Code") {
				t.Fatalf("unstable error contract: %+v", body)
			}
		})
	}
}

func TestSemanticErrorCodeIndependentOfDiagnostics(t *testing.T) {
	for _, diagnostic := range []string{"invalid credentials", "Anmeldedaten ungültig", "<html>untrusted response</html>"} {
		recorder := httptest.NewRecorder()
		writeError(recorder, http.StatusUnauthorized, diagnostic, "invalid_credentials")
		var body ErrorResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Code != "invalid_credentials" || body.Error != diagnostic {
			t.Fatalf("diagnostic changed semantic code: %+v", body)
		}
	}
}
