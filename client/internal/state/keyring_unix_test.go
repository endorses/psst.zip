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

type fixtureSecretBus struct {
	t                *testing.T
	locked           bool
	duplicates       bool
	existing         bool
	collectionLocked bool
	created          bool
	methods          []string
}

func (b *fixtureSecretBus) call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	b.t.Helper()
	b.methods = append(b.methods, method)
	if strings.Contains(method, "Unlock") || strings.Contains(method, "Prompt") {
		b.t.Fatal("credential adapter attempted a UI prompt")
	}
	if _, ok := ctx.Deadline(); !ok {
		b.t.Fatal("credential RPC had no deadline")
	}
	result := &dbus.Call{}
	switch method {
	case secretService + ".SearchItems":
		unlocked, locked := []dbus.ObjectPath{}, []dbus.ObjectPath{}
		if b.locked {
			locked = append(locked, "/fixture/item")
		}
		if b.duplicates {
			unlocked = append(unlocked, "/fixture/item_one", "/fixture/item_two")
		}
		if b.existing {
			unlocked = append(unlocked, "/fixture/item")
		}
		result.Body = []any{unlocked, locked}
	case secretService + ".ReadAlias":
		result.Body = []any{dbus.ObjectPath("/fixture/collection")}
	case "org.freedesktop.DBus.Properties.Get":
		result.Body = []any{dbus.MakeVariant(b.collectionLocked)}
	case secretService + ".OpenSession":
		result.Body = []any{dbus.MakeVariant(""), dbus.ObjectPath("/fixture/session")}
	case secretItem + ".GetSecret":
		result.Body = []any{secretValue{Session: "/fixture/session", Parameters: []byte{}, Value: []byte("fixture master identity"), ContentType: "text/plain"}}
	case secretCollection + ".CreateItem":
		if path != "/fixture/collection" || len(args) != 3 || args[2] != false {
			b.t.Fatal("master creation may replace an existing credential")
		}
		b.created = true
		result.Body = []any{dbus.ObjectPath("/fixture/item"), dbus.ObjectPath("/")}
	case "org.freedesktop.Secret.Session.Close":
	default:
		b.t.Fatalf("unexpected credential RPC: %s", method)
	}
	return result
}

func TestUnlockedSecretCanBeReadWithoutAnyUnlockOrPrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	bus := &fixtureSecretBus{t: t, existing: true}
	value, err := secretGet(ctx, bus, "fixture-service", "fixture-account")
	if err != nil || value != "fixture master identity" {
		t.Fatal("unlocked noninteractive read failed")
	}
	if bus.methods[len(bus.methods)-1] != "org.freedesktop.Secret.Session.Close" {
		t.Fatal("credential session was not closed")
	}
	if err = secretSet(ctx, bus, "fixture-service", "fixture-account", "different fixture identity"); !errors.Is(err, ErrUnavailable) || bus.created {
		t.Fatal("an existing master could be overwritten")
	}
}

func TestLockedOrAmbiguousCredentialsNeverPromptOrBecomeMissing(t *testing.T) {
	for _, locked := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		bus := &fixtureSecretBus{t: t, locked: locked, duplicates: !locked}
		_, err := secretGet(ctx, bus, "fixture-service", "fixture-account")
		cancel()
		if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotFound) || len(bus.methods) != 1 {
			t.Fatal("locked/ambiguous credential could be recreated or unlocked")
		}
	}
}

func TestSecretCreationNeverUnlocksOrReplacesExistingMaster(t *testing.T) {
	for _, locked := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		bus := &fixtureSecretBus{t: t, collectionLocked: locked}
		err := secretSet(ctx, bus, "fixture-service", "fixture-account", "private-fixture")
		cancel()
		if locked && (!errors.Is(err, ErrUnavailable) || bus.created) {
			t.Fatal("locked collection accepted a new master")
		}
		if !locked && (err != nil || !bus.created) {
			t.Fatal("unlocked collection rejected create-only master")
		}
	}
}

type fixtureInteractiveSecretBus struct {
	*fixtureSecretBus
	requested       []dbus.ObjectPath
	prompted        bool
	dismiss         bool
	unrelatedUnlock bool
}

func (b *fixtureInteractiveSecretBus) call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	if ctx.Err() != nil {
		return &dbus.Call{Err: ctx.Err()}
	}
	if method == secretService+".Unlock" {
		b.requested = args[0].([]dbus.ObjectPath)
		if len(b.requested) != 1 || (b.requested[0] != "/fixture/item" && b.requested[0] != "/fixture/collection") {
			b.t.Fatal("unlock requested unrelated secrets")
		}
		unlocked := []dbus.ObjectPath{}
		if b.unrelatedUnlock {
			unlocked = append(unlocked, "/other/app")
		}
		return &dbus.Call{Body: []any{unlocked, dbus.ObjectPath("/fixture/prompt")}}
	}
	if method == secretService+".SearchItems" {
		attrs, ok := args[0].(map[string]string)
		if !ok || len(attrs) != 2 || attrs["service"] != "fixture-service" || attrs["username"] != "fixture-account" {
			b.t.Fatal("credential lookup broadened its owned selector")
		}
	}
	return b.fixtureSecretBus.call(ctx, path, method, args...)
}
func (b *fixtureInteractiveSecretBus) prompt(ctx context.Context, path dbus.ObjectPath) (dbus.Variant, error) {
	b.prompted = true
	if path != "/fixture/prompt" || ctx.Err() != nil || b.dismiss {
		return dbus.Variant{}, ErrUnavailable
	}
	if b.locked {
		b.locked = false
		b.existing = true
	}
	b.collectionLocked = false
	return dbus.MakeVariant(b.requested), nil
}
func TestInteractiveSecretUnlockIsTargetedAndCreateOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	item := &fixtureInteractiveSecretBus{fixtureSecretBus: &fixtureSecretBus{t: t, locked: true}}
	value, err := secretGetMode(ctx, item, false, "fixture-service", "fixture-account")
	if err != nil || value != "fixture master identity" || !item.prompted || len(item.requested) != 1 || item.requested[0] != "/fixture/item" {
		t.Fatal("interactive read did not unlock only its matched credential", err)
	}
	collection := &fixtureInteractiveSecretBus{fixtureSecretBus: &fixtureSecretBus{t: t, collectionLocked: true}}
	if err = secretSetMode(ctx, collection, false, "fixture-service", "fixture-account", "private-fixture"); err != nil || !collection.created || !collection.prompted || collection.requested[0] != "/fixture/collection" {
		t.Fatal("default-collection initialization failed", err)
	}
	existing := &fixtureInteractiveSecretBus{fixtureSecretBus: &fixtureSecretBus{t: t, locked: true}}
	if err = secretSetMode(ctx, existing, false, "fixture-service", "fixture-account", "replacement-fixture"); !errors.Is(err, ErrUnavailable) || existing.created {
		t.Fatal("unlock authorized replacement of an existing master")
	}
	for _, dismiss := range []bool{false, true} {
		bus := &fixtureInteractiveSecretBus{fixtureSecretBus: &fixtureSecretBus{t: t, collectionLocked: true}, dismiss: dismiss, unrelatedUnlock: !dismiss}
		if err = secretSetMode(ctx, bus, false, "fixture-service", "fixture-account", "private-fixture"); !errors.Is(err, ErrUnavailable) || bus.created {
			t.Fatal("dismissed or unrelated unlock created a master")
		}
	}
}

func TestExistingSessionBusDiscoveryWithoutEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, address, runtime, platform, want string
		uid                                    int
	}{
		{name: "sanitized Linux environment", platform: "linux", uid: 1000, want: "/run/user/1000/bus"},
		{name: "explicit runtime directory", runtime: "/fixture/runtime", platform: "linux", uid: 1000, want: "/fixture/runtime/bus"},
		{name: "explicit socket takes precedence", address: "unix:path=/fixture/bus", runtime: "/other/runtime", platform: "linux", uid: 1000, want: "/fixture/bus"},
		{name: "abstract Unix address", address: "unix:abstract=fixture", platform: "linux", uid: 1000, want: "@fixture"},
		{name: "malformed explicit address has no fallback", address: "unix:path=%zz", runtime: "/fixture/runtime", platform: "linux", uid: 1000},
		{name: "unsupported explicit address has no fallback", address: "tcp:host=localhost,port=1", runtime: "/fixture/runtime", platform: "linux", uid: 1000},
		{name: "relative runtime does not silently fallback", runtime: "relative", platform: "linux", uid: 1000},
		{name: "BSD requires existing environment", platform: "freebsd", uid: 1000},
		{name: "invalid uid", platform: "linux", uid: -1},
		{name: "nul socket rejected", address: "unix:path=/fixture/%00bus", platform: "linux", uid: 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := secretBusSocket(tc.address, tc.runtime, tc.uid, tc.platform)
			if tc.want == "" {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatal("malformed or unavailable discovery bypassed", err)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("socket got%q err%v want%q", got, err, tc.want)
			}
		})
	}
}
