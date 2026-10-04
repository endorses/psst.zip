package api_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func TestAuthenticationAuditHTTPLogoutAndSessionRevocation(t *testing.T) {
	env := setupAuthFixture(t, false)
	user, token := addAccount(t, env, "alice-audit", "user")
	extraToken := sha256.Sum256([]byte("never-audit-this-secret"))
	extra := database.Session{ID: "alice-extra", UserID: user.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := env.queries.CreateSession(extra, extraToken[:], user.PasswordHash); err != nil {
		t.Fatal(err)
	}
	authRequest(t, env, "DELETE", "/auth/sessions/not-present", token, nil, 204)
	authRequest(t, env, "DELETE", "/auth/sessions/fixture-session", token, nil, 204)
	authRequest(t, env, "DELETE", "/auth/sessions/"+extra.ID, token, nil, 204)
	authRequest(t, env, "DELETE", "/auth/sessions/"+extra.ID, token, nil, 204)
	authRequest(t, env, "POST", "/auth/logout", token, nil, 204)
	authRequest(t, env, "POST", "/auth/logout", env.authToken, nil, 204)
	page, err := env.queries.SecurityEvents(0, 100, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var account, administrator int
	for _, event := range page.Events {
		if event.Kind != "session.revoked" {
			continue
		}
		switch event.Origin {
		case "account":
			account++
			if event.ActorID != user.ID {
				t.Fatal("wrong owner", event)
			}
		case "administrator":
			administrator++
			if event.ActorID != "fixture-admin" {
				t.Fatal("wrong administrator", event)
			}
		default:
			t.Fatal("wrong audit origin", event)
		}
	}
	if account != 2 || administrator != 1 {
		t.Fatalf("actual revocations: account=%d administrator=%d", account, administrator)
	}
}

func TestAuthenticationAuditPairingStorageFailureIsRetryable(t *testing.T) {
	env := setupAuthFixture(t, false)
	pairing := authRequest(t, env, "POST", "/auth/pairings", env.userToken, nil, 201)
	code := pairing["code"].(string)
	if _, err := env.db.Exec(`CREATE TRIGGER reject_pairing_audit BEFORE INSERT ON security_events WHEN NEW.kind='pairing.redeemed' BEGIN SELECT RAISE(FAIL,'unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	failure := authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]string{"code": code}, 503)
	if failure["code"] != "authentication_unavailable" {
		t.Fatal(failure)
	}
	status := authRequest(t, env, "GET", "/auth/pairings/"+pairing["id"].(string), env.userToken, nil, 200)
	if status["status"] != "pending" {
		t.Fatal("failed audit consumed pairing", status)
	}
	if _, err := env.db.Exec(`DROP TRIGGER reject_pairing_audit`); err != nil {
		t.Fatal(err)
	}
	authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]string{"code": code}, 200)
	authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]string{"code": code}, 401)
}
