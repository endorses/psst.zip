package reconcile

import (
	"io"
	"os"
	"testing"

	"github.com/endorses/psst.zip/backend/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Run(m)) }

func closeFixture(t *testing.T, closer io.Closer) {
	t.Helper()
	if err := closer.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
}
