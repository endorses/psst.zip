package database

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestIncidentPausePersistsAndGuardsAllocationRaces(t *testing.T) {
	q, path := resourceFixture(t)
	if err := q.CreateTransfer("upload", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveManifest("upload", []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := q.SetTransfersPaused(true); err != nil {
		t.Fatal(err)
	}
	actions := []func() error{
		func() error { return q.CreateTransfer("blocked", time.Now().Add(time.Hour), 0, nil) },
		func() error { return q.CreateSlot("slot", time.Now().Add(time.Hour), nil) },
		func() error { return q.CreateFile("file", "upload", 1) },
		func() error { return q.SaveManifest("upload", []byte("replacement")) },
		func() error { return q.CompleteTransfer("upload") },
	}
	for _, action := range actions {
		if err := IncidentError(action()); !errors.Is(err, ErrTransfersPaused) {
			t.Fatalf("pause bypass: %v", err)
		}
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	state, err := NewQueries(reopened).IncidentState()
	if err != nil || !state.PublicTransfersPaused {
		t.Fatalf("lost pause on restart %+v %v", state, err)
	}
	if err := q.RevokeTransfer("upload"); err != nil {
		t.Fatal(err)
	}
	if err := q.SetTransfersPaused(false); err != nil {
		t.Fatal(err)
	}
	old, err := q.GetTransfer("upload")
	if err != nil || old.Status != "revoked" {
		t.Fatalf("resume resurrected %+v %v", old, err)
	}
	if err := IncidentError(q.CreateFile("new", "upload", 0)); !errors.Is(err, ErrResourceRevoked) {
		t.Fatalf("revoked mutation %v", err)
	}
	if err := q.CreateTransfer("resumed", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
}
func TestIncidentAccountShutdownIsAtomicWithConcurrentCreation(t *testing.T) {
	q, _ := resourceFixture(t)
	if err := q.CreateUser(User{ID: "owner", Username: "owner", Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateSession(Session{ID: "session", UserID: "owner", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, []byte("session-hash"), []byte("hash")); err != nil {
		t.Fatal(err)
	}
	if err := q.CreatePairing([]byte("pairing-hash"), "owner", "session", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateReceiveSlot("inbox", time.Now().Add(time.Hour), nil, "owner", 2, "key", 0); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateSlotTransfer("inbox", "child", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := IncidentError(q.CreateTransfer(fmt.Sprint("racing-", i), time.Now().Add(time.Hour), 0, nil, "owner"))
			if err != nil && !errors.Is(err, ErrAccountDisabled) {
				t.Errorf("unexpected allocation error %v", err)
			}
		}(i)
	}
	result, err := q.ShutdownAccount("owner")
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if !result.User.Disabled || result.RevokedSessions != 1 || result.RevokedPairings != 1 || result.RevokedSlots != 1 || result.RevokedTransfers < 1 {
		t.Fatalf("incomplete shutdown %+v", result)
	}
	var live, sessions, pairings int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM transfers WHERE owner_id='owner' AND status!='revoked'`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	q.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id='owner'`).Scan(&sessions)
	q.db.QueryRow(`SELECT COUNT(*) FROM pairings WHERE user_id='owner'`).Scan(&pairings)
	if live != 0 || sessions != 0 || pairings != 0 {
		t.Fatalf("shutdown survivors links=%d sessions=%d pairings=%d", live, sessions, pairings)
	}
	enabled := false
	if err := q.UpdateUser("owner", &enabled, nil); err != nil {
		t.Fatal(err)
	}
	child, err := q.GetTransfer("child")
	if err != nil || child.Status != "revoked" {
		t.Fatalf("reenabling restored child %+v %v", child, err)
	}
	if _, err := q.RedeemPairing([]byte("pairing-hash"), []byte("new-hash"), Session{ID: "new", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}); err == nil {
		t.Fatal("pairing survived shutdown")
	}
}
func TestIncidentShutdownProtectsLastAdministrator(t *testing.T) {
	q, _ := resourceFixture(t)
	if err := q.CreateUser(User{ID: "admin", Username: "admin", Role: "admin", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ShutdownAccount("admin"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last admin shutdown %v", err)
	}
	user, err := q.UserByID("admin")
	if err != nil || user.Disabled {
		t.Fatalf("failed shutdown modified admin %+v %v", user, err)
	}
}

func TestIncidentPauseFailsIfControlSingletonIsMissing(t *testing.T) {
	q, _ := resourceFixture(t)
	if _, err := q.db.Exec(`DELETE FROM incident_state`); err != nil {
		t.Fatal(err)
	}
	if err := q.SetTransfersPaused(true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing singleton reported success: %v", err)
	}
	if err := q.ValidateIncidentSchema(); err == nil {
		t.Fatal("invalid singleton accepted")
	}
}
