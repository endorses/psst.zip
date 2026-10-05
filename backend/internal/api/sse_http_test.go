package api_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

type inboxEventResponse struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	ctx     context.Context
}

func openInboxEvents(t *testing.T, env *testEnv, slot, token string) *inboxEventResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	t.Cleanup(cancel)
	request, _ := http.NewRequestWithContext(ctx, "GET", env.url("/api/v1/slots/"+slot+"/events"), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := env.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("SSE handshake: %d / %s", response.StatusCode, response.Header.Get("Cache-Control"))
	}
	scanner := bufio.NewScanner(response.Body)
	connected := false
	for scanner.Scan() {
		if scanner.Text() == "event: connected" {
			connected = true
		}
		if scanner.Text() == "" {
			break
		}
	}
	if !connected {
		t.Fatalf("missing connected event: %v", scanner.Err())
	}
	return &inboxEventResponse{response.Body, scanner, ctx}
}
func expectInboxClosed(t *testing.T, event *inboxEventResponse) {
	t.Helper()
	for event.scanner.Scan() {
		if strings.HasPrefix(event.scanner.Text(), "data:") {
			t.Fatalf("unexpected disclosure on revoked stream: %s", event.scanner.Text())
		}
	}
	if event.ctx.Err() != nil {
		t.Fatalf("stream did not close before timeout: %v", event.ctx.Err())
	}
}
func TestInboxEventsCloseWhenReadAuthorityChanges(t *testing.T) {
	cases := []struct {
		name, statement string
		args            func(string) []any
	}{
		{"session revoked", `DELETE FROM sessions WHERE id='fixture-session'`, func(string) []any { return nil }},
		{"session expired", `UPDATE sessions SET expires_at=? WHERE id='fixture-session'`, func(string) []any { return []any{time.Now().Add(-time.Hour)} }},
		{"owner changed", `UPDATE slots SET owner_id='fixture-admin' WHERE id=?`, func(slot string) []any { return []any{slot} }},
		{"slot expired", `UPDATE slots SET expires_at=? WHERE id=?`, func(slot string) []any { return []any{time.Now().Add(-time.Hour), slot} }},
		{"slot revoked", `UPDATE slots SET status='revoked' WHERE id=?`, func(slot string) []any { return []any{slot} }},
		{"account disabled", `UPDATE users SET disabled=1 WHERE id='fixture-user'`, func(string) []any { return nil }},
		{"role changed", `UPDATE users SET role='admin' WHERE id='fixture-user'`, func(string) []any { return nil }},
		{"password change required", `UPDATE users SET must_change_password=1 WHERE id='fixture-user'`, func(string) []any { return nil }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			env := setupAuthFixture(t, false)
			slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
			event := openInboxEvents(t, env, slot, env.userToken)
			if _, err := env.db.Exec(test.statement, test.args(slot)...); err != nil {
				t.Fatal(err)
			}
			expectInboxClosed(t, event)
		})
	}
}
func TestInboxEventsOnlyCloseRevokedSessionViewers(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	user, err := env.queries.UserByID("fixture-user")
	if err != nil {
		t.Fatal(err)
	}
	otherToken := "another-session-for-same-inbox-owner"
	hash := sha256.Sum256([]byte(otherToken))
	if err := env.queries.CreateSession(database.Session{ID: "another-event-session", UserID: user.ID, DeviceName: "Other", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, hash[:], user.PasswordHash); err != nil {
		t.Fatal(err)
	}
	first := openInboxEvents(t, env, slot, env.userToken)
	second := openInboxEvents(t, env, slot, env.userToken)
	live := openInboxEvents(t, env, slot, otherToken)
	if _, err := env.db.Exec(`DELETE FROM sessions WHERE id='fixture-session'`); err != nil {
		t.Fatal(err)
	}
	expectInboxClosed(t, first)
	expectInboxClosed(t, second)
	submission := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	child := submission["id"].(string)
	capability := submission["delete_token"].(string)
	rawAuthorized(t, env, "POST", "/transfers/"+child+"/manifest", capability, fixtureReceiveEnvelope(), 204)
	authRequest(t, env, "POST", "/transfers/"+child+"/complete", capability, nil, 204)
	found := false
	for live.scanner.Scan() {
		line := live.scanner.Text()
		if strings.Contains(line, `"event":"transfer_complete"`) && strings.Contains(line, child) {
			found = true
			break
		}
	}
	if !found || live.ctx.Err() != nil {
		t.Fatalf("valid session did not receive events: %v", live.scanner.Err())
	}
}

func TestInboxEventsExpiryInterruptsIdleStream(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	// Expiry precedes the one-second lifecycle poll, exercising the request timer.
	if _, err := env.db.Exec(`UPDATE slots SET expires_at=? WHERE id=?`, time.Now().Add(350*time.Millisecond), slot); err != nil {
		t.Fatal(err)
	}
	event := openInboxEvents(t, env, slot, env.userToken)
	expectInboxClosed(t, event)
}
