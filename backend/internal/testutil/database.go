// Package testutil contains support imported only by backend tests.
package testutil

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type databaseTemplate struct {
	once sync.Once
	dir  string
	path string
	err  error
}

var emptyDatabase databaseTemplate

// OpenDatabase clones a fully migrated empty SQLite database before invoking the
// production opener. Its caller must always supply the same database opener.
// Existing paths bypass cloning, preserving restart and generation semantics.
// Each test binary using this helper must call Run from its TestMain.
func OpenDatabase(path string, opener func(string) (*sql.DB, error)) (*sql.DB, error) {
	return emptyDatabase.open(path, opener)
}

func (template *databaseTemplate) open(path string, opener func(string) (*sql.DB, error)) (*sql.DB, error) {
	if path == ":memory:" {
		return opener(path)
	}
	if _, err := os.Stat(path); err == nil {
		return opener(path)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	template.once.Do(func() { template.err = template.initialize(opener) })
	if template.err != nil {
		return nil, template.err
	}
	source, err := os.Open(template.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = source.Close() }()
	// O_EXCL prevents overwriting a database created by another connection.
	destination, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		return opener(path)
	}
	if err != nil {
		return nil, err
	}
	_, copyErr := io.Copy(destination, source)
	if err = errors.Join(copyErr, destination.Close()); err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	return opener(path)
}

func (template *databaseTemplate) initialize(opener func(string) (*sql.DB, error)) (err error) {
	template.dir, err = os.MkdirTemp("", "psst-test-database-")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, template.close())
		}
	}()
	template.path = filepath.Join(template.dir, "empty.db")
	db, err := opener(template.path)
	if err != nil {
		return err
	}
	// No connection remains alive while clones are made. Checkpoint explicitly
	// so copying only the main file includes every migration and initial row.
	var busy, logFrames, checkpointedFrames int
	err = db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointedFrames)
	if err == nil && busy != 0 {
		err = fmt.Errorf("empty database checkpoint is busy")
	}
	if err = errors.Join(err, db.Close()); err != nil {
		return err
	}
	return os.Chmod(template.path, 0600)
}

func (template *databaseTemplate) close() error {
	if template.dir == "" {
		return nil
	}
	return os.RemoveAll(template.dir)
}

// Run releases the process-owned template after all tests and their cleanup
// callbacks have finished. The caller exits with the returned status.
func Run(m *testing.M) (code int) {
	// Release the template after normal completion, including failed tests.
	defer func() {
		if err := emptyDatabase.close(); err != nil {
			fmt.Fprintf(os.Stderr, "remove test database template: %v\n", err)
			code = 1
		}
	}()
	return m.Run()
}
