//go:build darwin

package state

import (
	"context"
	"errors"
	"testing"
)

func TestNoninteractiveMacKeychainFailsBeforeExecutingSecurityTool(t *testing.T) {
	if _, err := platformKeyringGet(context.Background(), true, "unused-fixture-service", "unused-account"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("headless macOS keychain read could prompt")
	}
	if err := platformKeyringSet(context.Background(), true, "unused-fixture-service", "unused-account", "unused-fixture"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("headless macOS keychain write could prompt")
	}
}
