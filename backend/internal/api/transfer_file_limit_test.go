package api_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

const fileLimitTransfer = "4a9c8e0e-af08-41ba-8cfa-a1c8b1d02747"

func TestTransferMetadataRejectsOversizedLegacyListWithoutPartialDisclosure(t *testing.T) {
	for _, count := range []int{0, 100, 101} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			h, q, db, _ := guestCapacityAPI(t)
			if err := q.CreateTransfer(fileLimitTransfer, time.Now().Add(time.Hour), 3, nil, "private-owner"); err != nil {
				t.Fatal(err)
			}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for i := 0; i < count; i++ {
				if _, err = tx.Exec(`INSERT INTO files(id,transfer_id,size,upload_offset,upload_complete,download_count) VALUES(?,?,7,7,1,2)`, fmt.Sprintf("private-file-%04d", i), fileLimitTransfer); err != nil {
					t.Fatal(err)
				}
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/transfers/"+fileLimitTransfer, nil))
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("metadata response cacheable", w.Header())
			}
			var out map[string]any
			if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if count > 100 {
				if w.Code != http.StatusConflict || w.Header().Get("X-Psst-Error-Code") != "transfer_file_limit_exceeded" || out["code"] != "transfer_file_limit_exceeded" || out["error"] != "transfer contains more than the supported 100 files" {
					t.Fatal(w.Code, w.Header(), out)
				}
				if len(out) != 2 || strings.Contains(w.Body.String(), "private-file") || strings.Contains(w.Body.String(), fileLimitTransfer) {
					t.Fatal("partial metadata leaked", w.Body.String())
				}
				return
			}
			if w.Code != http.StatusOK || out["file_count"] != float64(count) || out["total_size"] != float64(count*7) {
				t.Fatal(w.Code, out)
			}
			files, ok := out["files"].([]any)
			if !ok || len(files) != count {
				t.Fatal("complete supported list missing", out)
			}
			for _, value := range files {
				file := value.(map[string]any)
				if file["download_count"] != float64(2) || file["remaining_downloads"] != float64(1) || file["upload_complete"] != true || file["upload_offset"] != float64(7) {
					t.Fatal("file policy changed", file)
				}
			}
		})
	}
}
func TestTransferMetadataSizeGuardDoesNotBypassInboxAuthorization(t *testing.T) {
	h, q, db, _ := guestCapacityAPI(t)
	if err := q.CreateSlotTransfer(guestCapacitySlot, fileLimitTransfer, time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 101; i++ {
		if _, err := db.Exec(`INSERT INTO files(id,transfer_id,size) VALUES(?,?,1)`, fmt.Sprintf("private-child-file-%d", i), fileLimitTransfer); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/transfers/"+fileLimitTransfer, nil))
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatal("metadata limit replaced authorization", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-child-file") || strings.Contains(w.Body.String(), "transfer_file_limit_exceeded") {
		t.Fatal("unauthorized caller learned private metadata", w.Body.String())
	}
}

func TestTransferAllocationAndGuestCapacityShareEffectiveFileLimit(t *testing.T) {
	for _, configured := range []int{-1, 0, 2, 500} {
		t.Run(fmt.Sprint(configured), func(t *testing.T) {
			h, q, db, _ := guestCapacityAPI(t, configured)
			limit := database.EffectiveTransferFileLimit(configured)
			w, response := guestCapacityResponse(t, h)
			capacity := response["upload_capacity"].(map[string]any)
			if w.Code != 200 || capacity["state"] != "ready" || capacity["available_files"] != float64(limit) {
				t.Fatal("advertised file cap differs from allocation", w.Code, response)
			}
			token := "test-guest-upload-capability"
			hash := sha256.Sum256([]byte(token))
			if err := q.CreateSlotTransfer(guestCapacitySlot, fileLimitTransfer, time.Now().Add(time.Hour), 0, hash[:]); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < limit-1; i++ {
				if err := q.CreateFile(fmt.Sprintf("seed-%d", i), fileLimitTransfer, 60); err != nil {
					t.Fatal(err)
				}
			}
			for _, status := range []int{http.StatusCreated, http.StatusBadRequest} {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/transfers/"+fileLimitTransfer+"/files", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Tus-Resumable", "1.0.0")
				req.Header.Set("Upload-Length", "60")
				result := httptest.NewRecorder()
				h.ServeHTTP(result, req)
				if result.Code != status {
					t.Fatal(result.Code, result.Body.String())
				}
				if status == http.StatusBadRequest {
					var out map[string]any
					if err := json.Unmarshal(result.Body.Bytes(), &out); err != nil {
						t.Fatal(err)
					}
					if len(out) != 2 || out["code"] != "transfer_file_limit_exceeded" || out["error"] != "maximum file count reached for this transfer" || result.Header().Get("X-Psst-Error-Code") != "transfer_file_limit_exceeded" {
						t.Fatal("unexpected allocation limit response", out)
					}
				}
			}
			files, err := q.ListFiles(fileLimitTransfer)
			if err != nil || len(files) != limit {
				t.Fatal("new allocation cannot be read by metadata API", len(files), err)
			}
			var bytes, count int64
			if err := db.QueryRow(`SELECT reserved_bytes,reserved_files FROM slots WHERE id=?`, guestCapacitySlot).Scan(&bytes, &count); err != nil || bytes != 60 || count != 1 {
				t.Fatal("rejected allocation changed link allowances", bytes, count, err)
			}
		})
	}
}
