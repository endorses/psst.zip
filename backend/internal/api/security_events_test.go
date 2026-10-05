package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func TestSecurityEventsAccessPaginationAndRedaction(t *testing.T) {
	env := setupAuthFixture(t, false)
	for i := 0; i < 3; i++ {
		tx, err := env.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		err = env.queries.AppendSecurityEvent(tx, database.SecurityEvent{Kind: "settings.file_size_changed", Origin: "administrator", ActorID: "fixture-admin", TargetType: "server", Outcome: "succeeded", Count: 1})
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	authRequest(t, env, "GET", "/admin/security-events", "", nil, 401)
	authRequest(t, env, "GET", "/admin/security-events", env.userToken, nil, 403)
	first := authRequest(t, env, "GET", "/admin/security-events?limit=2", env.authToken, nil, 200)
	rows := first["events"].([]any)
	if len(rows) != 2 || first["retention_days"] != float64(90) || first["max_events"] != float64(10000) {
		t.Fatal(first)
	}
	cursor := int64(first["next_before"].(float64))
	second := authRequest(t, env, "GET", "/admin/security-events?limit=2&before="+strconv.FormatInt(cursor, 10), env.authToken, nil, 200)
	for _, row := range second["events"].([]any) {
		if row.(map[string]any)["id"].(float64) >= float64(cursor) {
			t.Fatal(second)
		}
	}
	for _, query := range []string{"limit=0", "limit=101", "before=-1", "before=9223372036854775808", "limit=2&limit=3", "secret=DO_NOT_ECHO", "before=DO_NOT_ECHO"} {
		out := authRequest(t, env, "GET", "/admin/security-events?"+query, env.authToken, nil, 400)
		data, _ := json.Marshal(out)
		if strings.Contains(string(data), "DO_NOT_ECHO") {
			t.Fatal(string(data))
		}
	}
	env.queries.MarkSecurityAuditDegraded()
	req, _ := http.NewRequest("GET", env.url("/api/v1/admin/security-events"), nil)
	req.Header.Set("Authorization", "Bearer "+env.authToken)
	resp, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.Header.Get("Cache-Control") != "no-store" || !strings.Contains(string(data), `"degraded":true`) {
		t.Fatalf("%v %s", resp.Header, data)
	}
	if strings.Contains(string(data), env.authToken) || strings.Contains(string(data), "password_hash") {
		t.Fatal("sensitive authentication data in audit response")
	}
}

func TestDeletionAuditsProvenAuthorityAndSurvivesResourceRemoval(t *testing.T) {
	env := setupAuthFixture(t, false)
	ownerLink := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)
	capLink := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)
	ownerID, capID, slotID := ownerLink["id"].(string), capLink["id"].(string), slot["id"].(string)
	authRequest(t, env, "DELETE", "/transfers/"+ownerID, env.userToken, nil, 204)
	authRequest(t, env, "DELETE", "/transfers/"+capID, capLink["delete_token"].(string), nil, 204)
	authRequest(t, env, "DELETE", "/slots/"+slotID, env.authToken, nil, 204)
	// A failed/repeated delete is not another successful revocation.
	authRequest(t, env, "DELETE", "/transfers/"+ownerID, env.userToken, nil, 404)
	page, err := env.queries.SecurityEvents(0, 100, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{ownerID: "account", capID: "capability", slotID: "administrator"}
	for _, event := range page.Events {
		origin, ok := want[event.TargetID]
		if !ok {
			continue
		}
		if event.Origin != origin {
			t.Fatalf("wrong authority: %+v", event)
		}
		if origin == "capability" && event.ActorID != "" {
			t.Fatalf("capability attributed to account: %+v", event)
		}
		delete(want, event.TargetID)
	}
	if len(want) != 0 {
		t.Fatalf("missing revocations: %v", want)
	}
}
