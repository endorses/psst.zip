package api_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/cleanup"
	"github.com/endorses/psst.zip/backend/internal/database"
)

// The first body read occurs after authentication and the recent-auth middleware.
// Holding it models a request admitted before an operator revokes its session.
type heldAdminBody struct {
	io.Reader
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (b *heldAdminBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered); <-b.release })
	return b.Reader.Read(p)
}
func (*heldAdminBody) Close() error { return nil }

func TestAdminControlWritesRecheckSessionAfterBodyAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, snapshot string
		status                             int
	}{
		{"create administrator", "POST", "/admin/users", `{"username":"new-administrator","password":"new administrator password","role":"admin"}`, `SELECT COUNT(*) FROM users WHERE role='admin'`, 201},
		{"reset user password", "PATCH", "/admin/users/fixture-user", `{"password":"replacement user password"}`, `SELECT hex(password_hash) FROM users WHERE id='fixture-user'`, 200},
		{"pause", "PATCH", "/admin/incident-state", `{"public_transfers_paused":true}`, `SELECT public_transfers_paused FROM incident_state`, 200},
		{"global traffic budget", "PATCH", "/admin/traffic-policy", `{"enforcement_enabled":true}`, `SELECT policy FROM traffic_policy`, 200},
		{"account traffic budget", "PATCH", "/admin/users/fixture-user/traffic-policy", `{"account_budget_bytes":1048576}`, `SELECT COUNT(*) FROM traffic_account_policy`, 200},
		{"resource policy", "PATCH", "/admin/resource-policy", `{"server_files":54321}`, `SELECT server_files FROM resource_policy`, 200},
		{"file size", "PATCH", "/admin/settings", `{"max_file_size":1048576}`, `SELECT COALESCE((SELECT max_file_size FROM server_settings WHERE id=1),0)`, 200},
		{"traffic chart settings", "PATCH", "/admin/traffic/settings", `{"allowance_bytes":1048576,"cycle_start_day":2,"basis":"combined"}`, `SELECT cycle_start_day FROM traffic_state`, 200},
		{"shutdown", "POST", "/admin/users/fixture-user/shutdown", `{}`, `SELECT disabled FROM users WHERE id='fixture-user'`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := setupAuthFixture(t, false)
			snapshot := func() string {
				t.Helper()
				var v string
				if err := env.db.QueryRow(tc.snapshot).Scan(&v); err != nil {
					t.Fatal(err)
				}
				return v
			}
			before := snapshot()
			body := &heldAdminBody{Reader: strings.NewReader(tc.body), entered: make(chan struct{}), release: make(chan struct{})}
			req := httptest.NewRequest(tc.method, env.url("/api/v1"+tc.path), body)
			req.Header.Set("Authorization", "Bearer "+env.authToken)
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); env.server.Config.Handler.ServeHTTP(response, req) }()
			select {
			case <-body.entered:
			case <-done:
				t.Fatalf("request rejected before body: %d %s", response.Code, response.Body.String())
			case <-time.After(5 * time.Second):
				t.Fatal("request did not reach body")
			}
			// Reset changes the security revision and revokes all sessions without
			// changing the password or any policy/target being mutated.
			resetErr := env.queries.ResetAdminFactor("administrator")
			close(body.release)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not finish")
			}
			if resetErr != nil {
				t.Fatal(resetErr)
			}
			if response.Code != 401 || !strings.Contains(response.Body.String(), `"code":"administrator_authentication_changed"`) {
				t.Fatalf("stale write accepted/wrong error: %d %s", response.Code, response.Body.String())
			}
			if after := snapshot(); after != before {
				t.Fatalf("revoked actor changed target: %q -> %q", before, after)
			}
			fresh := adminWebLoginToken(t, env, "administrator", "administrator test password")
			authRequest(t, env, tc.method, tc.path, fresh, []byte(tc.body), tc.status)
			if after := snapshot(); after == before {
				t.Fatal("fresh authorized write did not persist")
			}
		})
	}
}

func TestAdminResourceRevocationKeepsGuardAcrossCleanup(t *testing.T) {
	for _, kind := range []string{"transfers", "slots"} {
		t.Run(kind, func(t *testing.T) {
			env := setupAuthFixture(t, false)
			var body any
			if kind == "slots" {
				body = fixtureSlotPolicy()
			}
			resource := authRequest(t, env, "POST", "/"+kind, env.userToken, body, 201)
			id := resource["id"].(string)
			sum := sha256.Sum256([]byte(env.authToken))
			session, user, err := env.queries.SessionByHash(sum[:])
			if err != nil {
				t.Fatal(err)
			}
			stale := &database.AdminActor{UserID: user.ID, SessionID: session.ID}
			if err = env.queries.ResetAdminFactor("administrator"); err != nil {
				t.Fatal(err)
			}
			if kind == "slots" {
				err = cleanup.RemoveSlotContext(context.Background(), env.queries, fixtureStore(t, env), id, stale)
			} else {
				err = cleanup.RemoveTransferContext(context.Background(), env.queries, fixtureStore(t, env), id, stale)
			}
			if !errors.Is(err, database.ErrAdminAuthenticationChanged) {
				t.Fatalf("stale actor reached cleanup: %v", err)
			}
			// A rejected revocation must leave the live resource intact.
			if kind == "slots" {
				row, e := env.queries.GetSlot(id)
				if e != nil || row.Status == "revoked" {
					t.Fatalf("slot changed: %v %v", row, e)
				}
			} else {
				row, e := env.queries.GetTransfer(id)
				if e != nil || row.Status == "revoked" {
					t.Fatalf("transfer changed: %v %v", row, e)
				}
			}
			fresh := adminWebLoginToken(t, env, "administrator", "administrator test password")
			authRequest(t, env, "DELETE", "/"+kind+"/"+id, fresh, nil, 204)
		})
	}
}

func TestPublicDeletionCapabilityDoesNotInheritAdministratorCookie(t *testing.T) {
	env := setupAuthFixture(t, false)
	resource := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)
	id, capability := resource["id"].(string), resource["delete_token"].(string)
	// Expire the cookie's recent proof; an explicit capability still stands alone.
	if _, err := env.db.Exec(`UPDATE sessions SET recent_until=0 WHERE user_id='fixture-admin'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token  string
		status int
	}{{"wrong-capability", 403}, {capability, 204}} {
		req := httptest.NewRequest("DELETE", env.url("/api/v1/transfers/"+id), nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		req.AddCookie(&http.Cookie{Name: "psst_session", Value: env.authToken})
		response := httptest.NewRecorder()
		env.server.Config.Handler.ServeHTTP(response, req)
		if response.Code != tc.status {
			t.Fatalf("capability/cookie scope confused: want %d got %d %s", tc.status, response.Code, response.Body.String())
		}
	}
}
