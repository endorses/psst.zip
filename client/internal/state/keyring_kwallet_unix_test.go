//go:build linux || freebsd || openbsd || netbsd || dragonfly

package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type fixtureDesktopBus struct {
	t           *testing.T
	owners      map[string]string
	activatable []string
	facade      bool
	started     []string
	calls       []string
}

func (b *fixtureDesktopBus) call(ctx context.Context, destination string, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	b.t.Helper()
	b.calls = append(b.calls, method)
	if _, ok := ctx.Deadline(); !ok {
		b.t.Fatal("desktop RPC has no deadline")
	}
	if ctx.Err() != nil {
		return &dbus.Call{Err: ctx.Err()}
	}
	if destination != desktopBusName || path != desktopBusPath || !strings.HasPrefix(method, desktopBusName+".") {
		b.t.Fatal("provider discovery attempted a wallet/content operation")
	}
	switch method {
	case desktopBusName + ".GetNameOwner":
		owner := b.owners[args[0].(string)]
		if owner == "" {
			return &dbus.Call{Err: dbus.Error{Name: "org.freedesktop.DBus.Error.NameHasNoOwner"}}
		}
		return &dbus.Call{Body: []any{owner}}
	case desktopBusName + ".ListActivatableNames":
		return &dbus.Call{Body: []any{b.activatable}}
	case desktopBusName + ".StartServiceByName":
		name := args[0].(string)
		if len(args) != 2 || args[1] != uint32(0) {
			b.t.Fatal("invalid activation arguments")
		}
		b.started = append(b.started, name)
		if b.facade {
			b.owners[secretName] = ":1.fixture"
		}
		return &dbus.Call{Body: []any{uint32(1)}}
	default:
		b.t.Fatalf("unexpected discovery RPC: %s", method)
		return nil
	}
}
func (b *fixtureDesktopBus) waitOwner(ctx context.Context, name string) (string, error) {
	return desktopOwner(ctx, b, name)
}
func TestKWalletActivationUsesOnlySecretServiceFacade(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		running, noninteractive, facade bool
		available                       []string
		wantStart                       string
		wantOK                          bool
	}{
		{"running Secret Service wins without activation", true, true, false, []string{"org.kde.kwalletd6"}, "", true},
		{"JSON never activates installed KWallet", false, true, true, []string{"org.kde.kwalletd6"}, "", false},
		{"interactive KWallet6 facade", false, false, true, []string{"org.kde.kwalletd6"}, "org.kde.kwalletd6", true},
		{"interactive KWallet5 facade", false, false, true, []string{"org.kde.kwalletd5"}, "org.kde.kwalletd5", true},
		{"native-only KWallet is unavailable", false, false, false, []string{"org.kde.kwalletd6"}, "org.kde.kwalletd6", false},
		{"direct Secret Service activation preferred", false, false, true, []string{secretName, "org.kde.kwalletd6"}, secretName, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			bus := &fixtureDesktopBus{t: t, owners: map[string]string{}, activatable: tc.available, facade: tc.facade}
			if tc.running {
				bus.owners[secretName] = ":1.fixture"
			}
			owner, err := secretServiceOwner(ctx, bus, tc.noninteractive)
			if tc.wantOK {
				if err != nil || owner != ":1.fixture" {
					t.Fatalf("facade discovery failed: %v", err)
				}
			} else if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("unsafe/unavailable provider accepted: %v", err)
			}
			if tc.wantStart == "" && len(bus.started) != 0 || tc.wantStart != "" && (len(bus.started) != 1 || bus.started[0] != tc.wantStart) {
				t.Fatalf("unexpected activations: %v", bus.started)
			}
			if tc.noninteractive && !tc.running && len(bus.calls) != 1 {
				t.Fatal("noninteractive discovery enumerated or activated services")
			}
		})
	}
}
