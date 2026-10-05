package database

import (
	"io"
	"testing"
)

func closeFixture(t *testing.T, closer io.Closer) {
	t.Helper()
	if err := closer.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
}
