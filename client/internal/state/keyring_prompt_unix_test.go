//go:build linux || freebsd || openbsd || netbsd || dragonfly

package state

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// Every connection in this fixture uses an isolated test daemon/socket. The
// user's session bus, keyrings, wallets and actual credentials are never used.
func privatePromptBus(t *testing.T) (string, *dbus.Conn) {
	t.Helper()
	binary, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon is required for isolated Secret Service prompt mocks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cmd := exec.CommandContext(ctx, binary, "--session", "--nofork", "--nopidfile", "--print-address=1", "--address=unix:path="+filepath.Join(t.TempDir(), "bus"))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatal("private DBus fixture did not publish its socket")
	}
	address := scanner.Text()
	service, err := dbus.Connect(address, dbus.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if result, err := service.RequestName(secretName, dbus.NameFlagDoNotQueue); err != nil || result != dbus.RequestNameReplyPrimaryOwner {
		t.Fatal("private Secret Service fixture name failed", err)
	}
	return address, service
}

type fixturePromptService struct {
	connection *dbus.Conn
	cancel     context.CancelFunc
	dismissed  bool
	dismiss    chan struct{}
	once       sync.Once
}

func (p *fixturePromptService) Prompt(_ string) *dbus.Error {
	if p.cancel != nil {
		p.cancel()
		return nil
	}
	if err := p.connection.Emit("/fixture/prompt", "org.freedesktop.Secret.Prompt.Completed", p.dismissed, dbus.MakeVariant([]dbus.ObjectPath{"/fixture/item"})); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}
func (p *fixturePromptService) Dismiss() *dbus.Error {
	p.once.Do(func() { close(p.dismiss) })
	return nil
}

func TestSecretPromptSubscribesBeforeReplyAndDismissesCancellation(t *testing.T) {
	address, service := privatePromptBus(t)
	for _, mode := range []string{"synchronous completion", "user dismissal", "context cancellation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			fixture := &fixturePromptService{connection: service, dismissed: mode == "user dismissal", dismiss: make(chan struct{})}
			if mode == "context cancellation" {
				fixture.cancel = cancel
			}
			if err := service.Export(fixture, "/fixture/prompt", "org.freedesktop.Secret.Prompt"); err != nil {
				t.Fatal(err)
			}
			t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
			connection, err := openSecretBus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			owner, err := desktopOwner(ctx, liveDesktopSecretsBus{connection}, secretName)
			if err != nil || !strings.HasPrefix(owner, ":") {
				t.Fatal("private service owner lookup failed", err)
			}
			result, err := (liveSecretBus{connection: connection, owner: owner}).prompt(ctx, "/fixture/prompt")
			if mode == "synchronous completion" {
				paths, ok := result.Value().([]dbus.ObjectPath)
				if err != nil || !ok || len(paths) != 1 || paths[0] != "/fixture/item" {
					t.Fatal("lost Completed signal emitted before Prompt reply", err)
				}
			} else if !errors.Is(err, ErrUnavailable) {
				t.Fatal("dismissal/cancellation was not rejected", err)
			}
			if mode == "context cancellation" {
				select {
				case <-fixture.dismiss:
				case <-time.After(time.Second):
					t.Fatal("canceled operation did not dismiss its owned prompt")
				}
			}
		})
	}
}

func TestSecretPromptKeepsUniqueOwnerAfterNameReplacement(t *testing.T) {
	address, original := privatePromptBus(t)
	originalPrompt := &fixturePromptService{connection: original, dismiss: make(chan struct{})}
	if err := original.Export(originalPrompt, "/fixture/prompt", "org.freedesktop.Secret.Prompt"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	connection, err := dbus.Connect(address, dbus.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	owner, err := desktopOwner(ctx, liveDesktopSecretsBus{connection}, secretName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = original.ReleaseName(secretName); err != nil {
		t.Fatal(err)
	}
	replacement, err := dbus.Connect(address, dbus.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if result, err := replacement.RequestName(secretName, dbus.NameFlagDoNotQueue); err != nil || result != dbus.RequestNameReplyPrimaryOwner {
		t.Fatal("could not replace fixture service owner", err)
	}
	// The replacement intentionally exports no prompt. Calling the well-known
	// name after lookup would now fail; the pinned legitimate owner still works.
	result, err := (liveSecretBus{connection: connection, owner: owner}).prompt(ctx, "/fixture/prompt")
	if paths, ok := result.Value().([]dbus.ObjectPath); err != nil || !ok || len(paths) != 1 || paths[0] != "/fixture/item" {
		t.Fatal("prompt changed owners between lookup and use", err)
	}
}
