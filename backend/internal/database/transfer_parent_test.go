package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTransferParentProbeRejectsAmbiguityWithoutPartialResult(t *testing.T) {
	q, _ := resourceFixture(t)
	policy := policyForTest(t, q)
	policy.AccountSlots = 200
	if err := q.SetResourcePolicy(policy); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if err := q.CreateTransfer("child", until, 0, nil); err != nil {
		t.Fatal(err)
	}
	parents, err := q.TransferSlotIDs("child")
	if err != nil || len(parents) != 0 {
		t.Fatalf("standalone parents=%v error=%v", parents, err)
	}
	for i := 0; i < 128; i++ {
		id := fmt.Sprintf("inbox-%03d", i)
		if err := q.CreateReceiveSlot(id, until, nil, "", 2, "key", 0); err != nil {
			t.Fatal(err)
		}
		// Reproduce historical/damaged memberships, not a supported creation path.
		if err := q.LinkSlotTransfer(id, "child"); err != nil {
			t.Fatal(err)
		}
		parents, err = q.TransferSlotIDs("child")
		if i == 0 {
			if err != nil || len(parents) != 1 || parents[0] != id {
				t.Fatalf("single parent=%v error=%v", parents, err)
			}
		} else if !errors.Is(err, ErrAmbiguousTransferParent) || parents != nil {
			t.Fatalf("%d parents returned partial=%v error=%v", i+1, parents, err)
		}
	}
	// An unrelated send remains standalone even with many legacy memberships.
	parents, err = q.TransferSlotIDs("unrelated")
	if err != nil || len(parents) != 0 {
		t.Fatalf("unrelated parents=%v error=%v", parents, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	parents, err = q.TransferSlotIDsContext(ctx, "child")
	if !errors.Is(err, context.Canceled) || parents != nil {
		t.Fatalf("cancelled parents=%v error=%v", parents, err)
	}
}

func TestTransferParentProbeUsesCoveringReverseIndex(t *testing.T) {
	q, _ := resourceFixture(t)
	rows, err := q.db.Query(`EXPLAIN QUERY PLAN SELECT slot_id FROM slot_transfers WHERE transfer_id=? ORDER BY slot_id LIMIT 2`, "child")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "SEARCH slot_transfers USING COVERING INDEX slot_transfers_transfer (transfer_id=?)") || strings.Contains(plan.String(), "TEMP B-TREE") {
		t.Fatalf("parent lookup must seek without sorting: %s", plan.String())
	}
}
