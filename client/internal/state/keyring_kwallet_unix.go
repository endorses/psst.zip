//go:build linux || freebsd || openbsd || netbsd || dragonfly

package state

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const secretProvider = "secret-service"
const desktopBusName = "org.freedesktop.DBus"
const desktopBusPath dbus.ObjectPath = "/org/freedesktop/DBus"

type desktopSecretsBus interface {
	call(context.Context, string, dbus.ObjectPath, string, ...any) *dbus.Call
	waitOwner(context.Context, string) (string, error)
}

type liveDesktopSecretsBus struct{ connection *dbus.Conn }

func (b liveDesktopSecretsBus) call(ctx context.Context, destination string, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	return b.connection.Object(destination, path).CallWithContext(ctx, method, dbus.FlagNoAutoStart, args...)
}

func desktopOwner(ctx context.Context, bus desktopSecretsBus, name string) (string, error) {
	var owner string
	err := bus.call(ctx, desktopBusName, desktopBusPath, desktopBusName+".GetNameOwner", name).Store(&owner)
	if err != nil {
		var missing dbus.Error
		var missingPointer *dbus.Error
		if errors.As(err, &missing) && missing.Name == "org.freedesktop.DBus.Error.NameHasNoOwner" || errors.As(err, &missingPointer) && missingPointer.Name == "org.freedesktop.DBus.Error.NameHasNoOwner" {
			return "", ErrNotFound
		}
		return "", ErrUnavailable
	}
	if !strings.HasPrefix(owner, ":") || len(owner) > 255 {
		return "", ErrUnavailable
	}
	return owner, nil
}

func (b liveDesktopSecretsBus) waitOwner(ctx context.Context, name string) (string, error) {
	if owner, err := desktopOwner(ctx, b, name); err == nil || err != ErrNotFound {
		return owner, err
	}
	signals := make(chan *dbus.Signal, 4)
	b.connection.Signal(signals)
	defer b.connection.RemoveSignal(signals)
	match := []dbus.MatchOption{dbus.WithMatchSender(desktopBusName), dbus.WithMatchObjectPath(desktopBusPath), dbus.WithMatchInterface(desktopBusName), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, name)}
	if err := b.connection.AddMatchSignalContext(ctx, match...); err != nil {
		return "", ErrUnavailable
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = b.connection.RemoveMatchSignalContext(cleanup, match...)
	}()
	// Close the subscribe/check race before waiting for registration.
	if owner, err := desktopOwner(ctx, b, name); err == nil || err != ErrNotFound {
		return owner, err
	}
	for {
		select {
		case <-ctx.Done():
			return "", ErrUnavailable
		case signal, ok := <-signals:
			if !ok {
				return "", ErrUnavailable
			}
			if signal.Sender != desktopBusName || signal.Name != desktopBusName+".NameOwnerChanged" || len(signal.Body) != 3 || signal.Body[0] != name {
				continue
			}
			if owner, err := desktopOwner(ctx, b, name); err == nil {
				return owner, nil
			}
		}
	}
}

// KWallet 6 can activate its standards-based Secret Service facade. Only the
// facade stores this CLI's master: native KWallet open/writePassword has neither
// a no-prompt open nor an atomic replace=false write. We do not call those APIs.
func secretServiceOwner(ctx context.Context, bus desktopSecretsBus, nonInteractive bool) (string, error) {
	owner, err := desktopOwner(ctx, bus, secretName)
	if err == nil || err != ErrNotFound {
		return owner, err
	}
	if nonInteractive {
		return "", ErrUnavailable
	}
	var names []string
	if err := bus.call(ctx, desktopBusName, desktopBusPath, desktopBusName+".ListActivatableNames").Store(&names); err != nil || len(names) > 4096 {
		return "", ErrUnavailable
	}
	available := make(map[string]bool, len(names))
	for _, name := range names {
		available[name] = true
	}
	for _, candidate := range []string{secretName, "org.kde.kwalletd6", "org.kde.kwalletd5"} {
		if !available[candidate] {
			continue
		}
		if ctx.Err() != nil {
			return "", ErrUnavailable
		}
		var started uint32
		if err := bus.call(ctx, desktopBusName, desktopBusPath, desktopBusName+".StartServiceByName", candidate, uint32(0)).Store(&started); err != nil || (started != 1 && started != 2) {
			continue
		}
		// KWallet's facade may register just after the native service name.
		// Wait on owner-change signals, not polling or a prompt-capable native
		// open. Unsupported older daemons remain unavailable after this bound.
		registration, cancel := context.WithTimeout(ctx, 3*time.Second)
		owner, err = bus.waitOwner(registration, secretName)
		cancel()
		if err == nil {
			return owner, nil
		}
	}
	return "", ErrUnavailable
}

func platformKeyringGetProvider(ctx context.Context, nonInteractive bool, provider, service, account string) (string, string, error) {
	if provider != "" && provider != secretProvider {
		return "", "", ErrUnavailable
	}
	bus, err := openSecretBus(ctx)
	if err != nil {
		return "", "", err
	}
	defer bus.Close()
	owner, err := secretServiceOwner(ctx, liveDesktopSecretsBus{bus}, nonInteractive)
	if err != nil {
		return "", "", err
	}
	value, err := secretGetMode(ctx, liveSecretBus{connection: bus, owner: owner}, nonInteractive, service, account)
	return value, secretProvider, err
}

func platformKeyringSetProvider(ctx context.Context, nonInteractive bool, provider, service, account, value string) (string, error) {
	if provider != "" && provider != secretProvider {
		return "", ErrUnavailable
	}
	bus, err := openSecretBus(ctx)
	if err != nil {
		return "", err
	}
	defer bus.Close()
	owner, err := secretServiceOwner(ctx, liveDesktopSecretsBus{bus}, nonInteractive)
	if err != nil {
		return "", err
	}
	return secretProvider, secretSetMode(ctx, liveSecretBus{connection: bus, owner: owner}, nonInteractive, service, account, value)
}
