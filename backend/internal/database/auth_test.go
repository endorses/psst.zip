package database

import (
	"crypto/sha256"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestReceiveBudgetReservationConcurrentAndCumulative(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "quota.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := NewQueries(db)
	if err := q.CreateSlot("slot", time.Now().Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if err := q.CreateSlotTransfer("slot", id, time.Now().Add(time.Hour), 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); results <- q.CreateFileWithQuota(id+"-file", id, 8, 10) }(id)
	}
	wg.Wait()
	close(results)
	success, limited := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrSlotQuota) {
			limited++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || limited != 1 {
		t.Fatalf("reservations success%d limited%d", success, limited)
	}
	if _, err := db.Exec(`DELETE FROM transfers`); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateSlotTransfer("slot", "three", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFileWithQuota("three-file", "three", 3, 10); !errors.Is(err, ErrSlotQuota) {
		t.Fatalf("deletion refunded budget: %v", err)
	}
}
func TestAuthenticationMigrationPreservesLegacyOwnership(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := NewQueries(db)
	if err := q.CreateTransfer("legacy", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	owner, err := q.Owner("transfer", "legacy")
	if err != nil || owner != "" {
		t.Fatalf("invented legacy ownership %q %v", owner, err)
	}
	u := User{ID: "alice", Username: "Alice", Role: "admin", PasswordHash: []byte("hash")}
	if err := q.CreateUser(u, true); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateUser(User{ID: "second", Username: "second", Role: "admin", PasswordHash: []byte("changed")}, true); err != nil {
		t.Fatal(err)
	}
	count, err := q.UserCount()
	if err != nil || count != 1 {
		t.Fatalf("bootstrap added account %d %v", count, err)
	}
	found, err := q.UserByName("alice")
	if err != nil || found.ID != u.ID {
		t.Fatalf("case insensitive lookup: %v", err)
	}
	hash := sha256.Sum256([]byte("token"))
	s := Session{ID: "session", UserID: u.ID, DeviceName: "Browser", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := q.CreateSession(s, hash[:], u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	pairing := sha256.Sum256([]byte("code"))
	if err := q.CreatePairing(pairing[:], u.ID, s.ID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteSession(s.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pairings`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("session revocation retained pairing %d %v", n, err)
	}
}

// Cancellation must invalidate a grant throughout its precise lifetime, including
// the fractional portion of its final second. Both replacement and DELETE use it.
func TestPairingCancellationAndReplacementAtFractionalExpiry(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "fractional-pairing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := NewQueries(db)
	user := User{ID: "issuer", Username: "issuer", Role: "user", PasswordHash: []byte("hash")}
	if err := q.CreateUser(user, false); err != nil {
		t.Fatal(err)
	}
	session := Session{ID: "browser", UserID: user.ID, DeviceName: "Browser", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := q.CreateSession(session, []byte("browser-secret"), user.PasswordHash); err != nil {
		t.Fatal(err)
	}
	for _, replace := range []bool{false, true} {
		name := "cancel"
		if replace {
			name = "replace"
		}
		t.Run(name, func(t *testing.T) {
			// Choose the end of this second; wait for the next second if less than half
			// remains. The UPDATE must cancel, even when the rounded expiry is past.
			now := time.Now().UTC()
			expiry := now.Truncate(time.Second).Add(990 * time.Millisecond)
			if time.Until(expiry) < 500*time.Millisecond {
				time.Sleep(time.Until(now.Truncate(time.Second).Add(time.Second + 10*time.Millisecond)))
				expiry = time.Now().UTC().Truncate(time.Second).Add(990 * time.Millisecond)
			}
			id := "grant-" + name
			hash := []byte(id + "-hash")
			if err := q.CreateTrackedPairing(id, hash, user.ID, session.ID, expiry, ""); err != nil {
				t.Fatal(err)
			}
			if replace {
				err = q.CreateTrackedPairing("replacement", []byte("new-hash"), user.ID, session.ID, time.Now().Add(time.Minute), id)
			} else {
				err = q.CancelPairing(id, user.ID, session.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			status, err := q.PairingStatus(id, user.ID, session.ID)
			if err != nil || status.Status != "canceled" {
				t.Fatalf("successful cancellation left grant active: %+v %v", status, err)
			}
			phone := Session{ID: "phone-" + name, DeviceName: "Phone", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
			if _, err := q.RedeemPairing(hash, []byte("device-hash"), phone); err == nil {
				t.Fatal("canceled code signed in a phone")
			}
		})
	}
}
