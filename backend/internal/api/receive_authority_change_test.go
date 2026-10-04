package api_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPrivateInboxReadAuthorityChanges(t *testing.T) {
	cases := []struct {
		name      string
		statement string
		args      func(slot, newOwner string) []any
		status    int
		moveOwner bool
	}{
		{
			name: "session revoked", statement: `DELETE FROM sessions WHERE id='fixture-session'`,
			status: http.StatusUnauthorized,
		},
		{
			name: "session expired", statement: `UPDATE sessions SET expires_at=? WHERE id='fixture-session'`,
			args:   func(string, string) []any { return []any{time.Now().Add(-time.Hour)} },
			status: http.StatusUnauthorized,
		},
		{
			name: "account disabled", statement: `UPDATE users SET disabled=1 WHERE id='fixture-user'`,
			status: http.StatusUnauthorized,
		},
		{
			name: "role changed", statement: `UPDATE users SET role='admin' WHERE id='fixture-user'`,
			status: http.StatusForbidden,
		},
		{
			name: "password change required", statement: `UPDATE users SET must_change_password=1 WHERE id='fixture-user'`,
			status: http.StatusForbidden,
		},
		{
			name: "owner changed", statement: `UPDATE slots SET owner_id=? WHERE id=?`,
			args:   func(slot, newOwner string) []any { return []any{newOwner, slot} },
			status: http.StatusForbidden, moveOwner: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			env := setupAuthFixture(t, false)
			newOwner, newOwnerToken := addAccount(t, env, "next-inbox-owner", "user")
			_, unrelatedToken := addAccount(t, env, "unrelated-inbox-user", "user")
			slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), http.StatusCreated)["id"].(string)
			child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, http.StatusCreated)
			transferID, capability := child["id"].(string), child["delete_token"].(string)
			fileID := uuid.NewString()
			manifest := fixtureReceiveEnvelope()
			const payload = "data"
			rawAuthorized(t, env, "POST", "/transfers/"+transferID+"/manifest", capability, manifest, http.StatusNoContent)
			if err := env.queries.CreateFileWithQuota(fileID, transferID, int64(len(payload)), 1024); err != nil {
				t.Fatal(err)
			}
			if err := fixtureStore(t, env).Save(transferID+"/"+fileID, strings.NewReader(payload)); err != nil {
				t.Fatal(err)
			}
			if err := env.queries.UpdateFileOffset(fileID, int64(len(payload)), true); err != nil {
				t.Fatal(err)
			}
			authRequest(t, env, "POST", "/transfers/"+transferID+"/complete", capability, nil, http.StatusNoContent)
			// Receive submissions inherit their policy server-side. Keep a finite
			// allowance here so an unauthorized file read cannot hide consumption.
			if _, err := env.db.Exec(`UPDATE transfers SET max_downloads=2 WHERE id=?`, transferID); err != nil {
				t.Fatal(err)
			}
			paths := []string{
				"/slots/" + slot,
				"/slots/" + slot + "/inbox",
				"/slots/" + slot + "/transfers/" + transferID + "/membership",
				"/transfers/" + transferID,
				"/transfers/" + transferID + "/manifest",
				"/transfers/" + transferID + "/files/" + fileID,
			}
			assertOwnerReads := func(token string) {
				t.Helper()
				for index, path := range paths {
					body := rawAuthorized(t, env, "GET", path, token, nil, http.StatusOK)
					switch index {
					case 0, 1, 2, 3:
						if !bytes.Contains(body, []byte(transferID)) {
							t.Fatalf("%s did not return the submission: %s", path, body)
						}
					case 4:
						if !bytes.Equal(body, manifest) {
							t.Fatalf("manifest changed: %q", body)
						}
					case 5:
						if string(body) != payload {
							t.Fatalf("file payload changed: %q", body)
						}
					}
				}
			}
			assertDownloadState := func(count int, acknowledged bool) {
				t.Helper()
				file, err := env.queries.GetFile(fileID)
				if err != nil {
					t.Fatal(err)
				}
				transfer, err := env.queries.GetTransfer(transferID)
				if err != nil {
					t.Fatal(err)
				}
				if file.DownloadCount != count || transfer.DownloadCount != count || transfer.DownloadedAt.Valid != acknowledged {
					t.Fatalf("download state: file=%d transfer=%d acknowledged=%v; want %d/%v", file.DownloadCount, transfer.DownloadCount, transfer.DownloadedAt.Valid, count, acknowledged)
				}
			}
			assertDenied := func(token string, status int) {
				t.Helper()
				for _, path := range paths {
					rawAuthorized(t, env, "GET", path, token, nil, status)
				}
				rawAuthorized(t, env, "POST", "/transfers/"+transferID+"/downloaded", token, nil, status)
				assertDownloadState(1, false)
			}
			// Every endpoint has a valid target. This first file read also makes
			// the receipt eligible, so a denied POST must be an authority failure.
			assertOwnerReads(env.userToken)
			assertDownloadState(1, false)
			var args []any
			if test.args != nil {
				args = test.args(slot, newOwner.ID)
			}
			if _, err := env.db.Exec(test.statement, args...); err != nil {
				t.Fatal(err)
			}
			assertDenied(env.userToken, test.status)
			assertDenied(unrelatedToken, http.StatusForbidden)
			assertDenied(env.authToken, http.StatusForbidden)
			assertDenied(capability, http.StatusUnauthorized)
			if !test.moveOwner {
				assertDenied(newOwnerToken, http.StatusForbidden)
				return
			}
			// Changing the canonical slot owner does not rewrite historical
			// transfer ownership, and that history must not confer read access.
			owner, err := env.queries.Owner("transfer", transferID)
			if err != nil || owner != "fixture-user" {
				t.Fatalf("historical transfer owner: %q %v", owner, err)
			}
			assertOwnerReads(newOwnerToken)
			assertDownloadState(2, false)
			rawAuthorized(t, env, "POST", "/transfers/"+transferID+"/downloaded", newOwnerToken, nil, http.StatusNoContent)
			assertDownloadState(2, true)
		})
	}
}
