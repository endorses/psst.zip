package database

import (
	"database/sql"
	"testing"
)

type fixtureFileWriter interface {
	Exec(string, ...any) (sql.Result, error)
}

// seedTransferFileRows creates fixture metadata in one statement while retaining
// every production insert trigger. Allocation and query behavior remain exercised
// separately through Queries; repeated SQL preparation is not part of that setup.
func seedTransferFileRows(t *testing.T, q *Queries, transfer, idFormat string, count int, size, offset int64, complete bool, downloads int) {
	t.Helper()
	seedTransferFileRowsUsing(t, q.db, transfer, idFormat, count, size, offset, complete, downloads)
}

// Historical listing exercises restored metadata, not allocation admission.
// Avoid rescanning account resource usage for every historical row. All derived
// totals/history/inbox triggers stay active, and the exact budget trigger is
// restored in the same transaction before the fixture becomes visible.
func seedHistoricalTransferFileRows(t *testing.T, q *Queries, transfer, idFormat string, count int, size, offset int64, complete bool, downloads int) {
	t.Helper()
	tx, err := q.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var budgetTrigger string
	if err := tx.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='budget_files'`).Scan(&budgetTrigger); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DROP TRIGGER budget_files`); err != nil {
		t.Fatal(err)
	}
	seedTransferFileRowsUsing(t, tx, transfer, idFormat, count, size, offset, complete, downloads)
	if _, err := tx.Exec(budgetTrigger); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var restored string
	if err := q.db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='budget_files'`).Scan(&restored); err != nil || restored != budgetTrigger {
		t.Fatal("historical fixture did not restore allocation admission", err)
	}
}

func seedTransferFileRowsUsing(t *testing.T, writer fixtureFileWriter, transfer, idFormat string, count int, size, offset int64, complete bool, downloads int) {
	t.Helper()
	result, err := writer.Exec(`WITH RECURSIVE fixture_files(n) AS (SELECT 0 WHERE ?>0 UNION ALL SELECT n+1 FROM fixture_files WHERE n+1<?)
 INSERT INTO files(id,transfer_id,size,upload_offset,upload_complete,download_count)
 SELECT printf(?,n),?,?,?,?,? FROM fixture_files`, count, count, idFormat, transfer, size, offset, complete, downloads)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != int64(count) {
		t.Fatal("file fixture cardinality changed", rows, count, err)
	}
}
