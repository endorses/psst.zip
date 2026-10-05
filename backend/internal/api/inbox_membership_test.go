package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInboxMembershipExactOwnerOnlyAndMinimal(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, other := addAccount(t, env, "membership-other", "user")
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	id := child["id"].(string)
	path := "/slots/" + slot + "/transfers/" + id + "/membership"
	for _, test := range []struct {
		token  string
		status int
	}{{"", 401}, {child["delete_token"].(string), 401}, {other, 403}, {env.authToken, 403}} {
		authRequest(t, env, "GET", path, test.token, nil, test.status)
	}
	request, _ := http.NewRequest("GET", env.url("/api/v1"+path), nil)
	request.Header.Set("Authorization", "Bearer "+env.userToken)
	response, err := env.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(body) > 4096 || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unbounded or cacheable membership: %d %s %v", response.StatusCode, body, err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got["slot_id"] != slot || got["transfer_id"] != id || got["receive_protocol"] != float64(2) || got["recipient_public_key"] != fixtureRecipientKey {
		t.Fatalf("unexpected membership: %s", body)
	}
	otherSlot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	standalone := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	for _, target := range []string{
		"/slots/" + otherSlot + "/transfers/" + id + "/membership",
		"/slots/" + slot + "/transfers/" + standalone + "/membership",
		"/slots/" + slot + "/transfers/" + uuid.NewString() + "/membership",
		"/slots/" + uuid.NewString() + "/transfers/" + id + "/membership",
	} {
		authRequest(t, env, "GET", target, env.userToken, nil, 404)
	}
	for _, target := range []string{"/slots/bad/transfers/" + id + "/membership", "/slots/" + slot + "/transfers/bad/membership"} {
		authRequest(t, env, "GET", target, env.userToken, nil, 400)
	}
	// Exact membership does not depend on manifests, files, or inbox counters.
	for _, table := range []string{"manifests", "files"} {
		if _, err := env.db.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	authRequest(t, env, "GET", path, env.userToken, nil, 200)
	if err := env.queries.LinkSlotTransfer(otherSlot, id); err != nil {
		t.Fatal(err)
	}
	authRequest(t, env, "GET", path, env.userToken, nil, 503)
}

func TestInboxMembershipRejectsDeadResourcesAndInvalidPolicy(t *testing.T) {
	env := setupAuthFixture(t, false)
	owner, _ := addAccount(t, env, "membership-lifecycle", "user")
	for _, test := range []struct {
		name, update string
		status       int
	}{
		{"live pending", "", 200},
		{"live complete", `UPDATE transfers SET status='complete',pending_expires_at='2000-01-01' WHERE id=?`, 200},
		{"expired pending", `UPDATE transfers SET pending_expires_at='2000-01-01' WHERE id=?`, 410},
		{"expired child", `UPDATE transfers SET expires_at='2000-01-01' WHERE id=?`, 410},
		{"revoked child", `UPDATE transfers SET status='revoked' WHERE id=?`, 410},
		{"expired inbox", `UPDATE slots SET expires_at='2000-01-01' WHERE id=?`, 410},
		{"revoked inbox", `UPDATE slots SET status='revoked' WHERE id=?`, 410},
		{"oversized key", `UPDATE slots SET recipient_public_key=printf('%1000000s','x') WHERE id=?`, 500},
		{"zero key", `UPDATE slots SET recipient_public_key='AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA' WHERE id=?`, 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			slot, child := uuid.NewString(), uuid.NewString()
			until := time.Now().Add(time.Hour)
			if err := env.queries.CreateReceiveSlot(slot, until, nil, owner.ID, 2, fixtureRecipientKey, 0); err != nil {
				t.Fatal(err)
			}
			if err := env.queries.CreateSlotTransfer(slot, child, until, 0, nil); err != nil {
				t.Fatal(err)
			}
			if test.update != "" {
				target := child
				if strings.HasPrefix(test.update, "UPDATE slots") {
					target = slot
				}
				if _, err := env.db.Exec(test.update, target); err != nil {
					t.Fatal(err)
				}
			}
			authRequest(t, env, "GET", "/slots/"+slot+"/transfers/"+child+"/membership", "membership-lifecycle-session", nil, test.status)
		})
	}
}
