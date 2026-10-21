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
