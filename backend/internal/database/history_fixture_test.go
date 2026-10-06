package database

import "testing"

// seedHistoryEvents populates a legitimate, contiguous pre-retention journal.
// Only repetitive insert bookkeeping is bypassed during this isolated fixture
// transaction. Restore the exact production trigger before any boundary mutation;
// derive counters and resource revisions from the rows, never preset prune floors.
func seedHistoryEvents(t *testing.T, q *Queries, count, accounts int, resource, action string) {
	t.Helper()
	tx, err := q.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var floors int
	if err = tx.QueryRow(`SELECT global_floor+(SELECT COALESCE(SUM(floor),0) FROM history_sync_accounts) FROM history_sync_state`).Scan(&floors); err != nil || floors != 0 {
		t.Fatal("cannot seed an already-pruned journal", floors, err)
	}
	var insertTrigger string
	if err = tx.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='history_sync_event_insert'`).Scan(&insertTrigger); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`DROP TRIGGER history_sync_event_insert`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`WITH RECURSIVE revisions(n) AS (SELECT 1 WHERE ?>0 UNION ALL SELECT n+1 FROM revisions WHERE n<?)
 INSERT INTO history_sync_events(revision,owner_id,kind,resource_id,action)
 SELECT s.revision+n,CASE WHEN n%?=0 THEN 'alice' ELSE 'account-'||(n%?) END,'transfer',?,?
 FROM revisions CROSS JOIN history_sync_state s`, count, count, accounts, accounts, resource, action); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE history_sync_state SET event_count=(SELECT COUNT(*) FROM history_sync_events),revision=(SELECT COALESCE(MAX(revision),0) FROM history_sync_events);
 INSERT INTO history_sync_accounts(owner_id,event_count) SELECT owner_id,COUNT(*) FROM history_sync_events GROUP BY owner_id ON CONFLICT(owner_id) DO UPDATE SET event_count=excluded.event_count;
 INSERT INTO history_sync_revisions(kind,resource_id,revision) SELECT kind,resource_id,MAX(revision) FROM history_sync_events GROUP BY kind,resource_id ON CONFLICT(kind,resource_id) DO UPDATE SET revision=excluded.revision`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(insertTrigger); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func assertHistoryFixtureBoundary(t *testing.T, q *Queries, expected int) {
	t.Helper()
	var count, stateCount, revision, globalFloor, accountDrift, accountFloors int
	if err := q.db.QueryRow(`SELECT COUNT(*),(SELECT event_count FROM history_sync_state),(SELECT revision FROM history_sync_state),(SELECT global_floor FROM history_sync_state),
 (SELECT COUNT(*) FROM history_sync_accounts a WHERE a.event_count<>(SELECT COUNT(*) FROM history_sync_events e WHERE e.owner_id=a.owner_id)),
 (SELECT COALESCE(SUM(floor),0) FROM history_sync_accounts) FROM history_sync_events`).Scan(&count, &stateCount, &revision, &globalFloor, &accountDrift, &accountFloors); err != nil {
		t.Fatal(err)
	}
	if count != expected || stateCount != count || revision != count || globalFloor != 0 || accountDrift != 0 || accountFloors != 0 {
		t.Fatalf("invalid pre-boundary journal: rows=%d state=%d revision=%d globalFloor=%d accountDrift=%d accountFloors=%d", count, stateCount, revision, globalFloor, accountDrift, accountFloors)
	}
}
