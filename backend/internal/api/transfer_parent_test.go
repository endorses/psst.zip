package api_test

import (
	"bytes"
	"testing"
)

func TestAmbiguousTransferParentDeniesContentAndUploadCapability(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	other := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	id, capability := child["id"].(string), child["delete_token"].(string)
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", capability, fixtureReceiveEnvelope(), 204)
	// Both inboxes deliberately have the same owner. Ambiguity must still fail
	// closed: selecting either one changes quota and cancellation attribution.
	if err := env.queries.LinkSlotTransfer(other, id); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method, suffix, token string
	}{
		{"GET", "", ""},
		{"GET", "", env.userToken},
		{"GET", "/manifest", env.userToken},
		{"GET", "/upload-status", capability},
		{"GET", "/traffic-status", capability},
		{"POST", "/manifest", capability},
		{"POST", "/complete", capability},
		{"POST", "/downloaded", env.userToken},
	} {
		status := 500
		if test.suffix == "/traffic-status" {
			status = 503
		}
		data := rawAuthorized(t, env, test.method, "/transfers/"+id+test.suffix, test.token, nil, status)
		for _, secret := range []string{slot, other, id, capability, "PSSTRCV2"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatalf("denial leaked resource details: %s", data)
			}
		}
	}
	transfer, err := env.queries.GetTransfer(id)
	if err != nil || transfer.Status != "pending" || transfer.DownloadedAt.Valid {
		t.Fatalf("denied operations changed transfer: %+v %v", transfer, err)
	}
}
