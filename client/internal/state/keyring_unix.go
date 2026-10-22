//go:build linux || freebsd || openbsd || netbsd || dragonfly

package state

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const secretName = "org.freedesktop.secrets"
const secretRoot dbus.ObjectPath = "/org/freedesktop/secrets"
const secretService = "org.freedesktop.Secret.Service"
const secretCollection = "org.freedesktop.Secret.Collection"
const secretItem = "org.freedesktop.Secret.Item"

type secretValue struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

type secretBus interface {
	call(context.Context, dbus.ObjectPath, string, ...any) *dbus.Call
}

type liveSecretBus struct {
	connection *dbus.Conn
	owner      string
}

func (b liveSecretBus) call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	return b.connection.Object(b.owner, path).CallWithContext(ctx, method, dbus.FlagNoAutoStart, args...)
}

// Connect only to an existing local session bus. Unlike SessionBus(), this never
// launches dbus-launch, and both connecting and authenticating share the deadline.
func openSecretBus(ctx context.Context) (*dbus.Conn, error) {
	socket, err := secretBusSocket(os.Getenv("DBUS_SESSION_BUS_ADDRESS"), os.Getenv("XDG_RUNTIME_DIR"), os.Geteuid(), runtime.GOOS)
	if err != nil {
		return nil, err
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, ErrUnavailable
	}
	bus, err := dbus.NewConn(connection, dbus.WithContext(context.WithoutCancel(ctx)))
	if err != nil {
		_ = connection.Close()
		return nil, ErrUnavailable
	}
	// Authentication/Hello have no context-taking variants. Cancel their
	// socket promptly, then detach that cancellation once connected so a
	// canceled UI operation can still dismiss its prompt before Close.
	stop := context.AfterFunc(ctx, func() { _ = bus.Close() })
	ready := false
	defer func() {
		stop()
		if !ready {
			_ = bus.Close()
		}
	}()
	if err = bus.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Geteuid()))}); err != nil {
		return nil, ErrUnavailable
	}
	if err = bus.Hello(); err != nil {
		return nil, ErrUnavailable
	}
	if !stop() || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	ready = true
	return bus, nil
}

// Linux's standard per-user bus can outlive environment propagation (for
// example, a shell launched by a desktop agent). This discovers only an existing
// socket. Explicit addresses, including invalid ones, are never bypassed.
func secretBusSocket(address, runtimeDirectory string, uid int, platform string) (string, error) {
	if address == "" {
		runtimeDir := runtimeDirectory
		if runtimeDir == "" && platform == "linux" {
			if uid < 0 {
				return "", ErrUnavailable
			}
			runtimeDir = filepath.Join("/run/user", strconv.Itoa(uid))
		}
		if !filepath.IsAbs(runtimeDir) {
			return "", ErrUnavailable
		}
		address = "unix:path=" + filepath.Join(runtimeDir, "bus")
	}
	var socket string
	for _, candidate := range strings.Split(address, ";") {
		if !strings.HasPrefix(candidate, "unix:") {
			continue
		}
		var path, abstract string
		for _, pair := range strings.Split(strings.TrimPrefix(candidate, "unix:"), ",") {
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) != 2 {
				return "", ErrUnavailable
			}
			value, err := url.PathUnescape(parts[1])
			if err != nil {
				return "", ErrUnavailable
			}
			switch parts[0] {
			case "path":
				path = value
			case "abstract":
				abstract = value
			case "guid":
			default:
				return "", ErrUnavailable
			}
		}
		if filepath.IsAbs(path) && abstract == "" && !strings.ContainsRune(path, '\x00') {
			socket = path
			break
		}
		if abstract != "" && path == "" && !strings.ContainsRune(abstract, '\x00') {
			socket = "@" + abstract
			break
		}
	}
	if socket == "" {
		return "", ErrUnavailable
	}
	return socket, nil

}

// Prompt support is optional for injected test buses. Noninteractive paths never
// assert this interface or issue Unlock/Prompt calls.
type secretPrompter interface {
	prompt(context.Context, dbus.ObjectPath) (dbus.Variant, error)
}

func searchSecret(ctx context.Context, bus secretBus, service, account string) (dbus.ObjectPath, error) {
	return searchSecretMode(ctx, bus, true, service, account)
}
func searchSecretMode(ctx context.Context, bus secretBus, nonInteractive bool, service, account string) (dbus.ObjectPath, error) {
	var unlocked, locked []dbus.ObjectPath
	if err := bus.call(ctx, secretRoot, secretService+".SearchItems", map[string]string{"service": service, "username": account}).Store(&unlocked, &locked); err != nil || len(unlocked)+len(locked) > 1 {
		return "", ErrUnavailable
	}
	if len(locked) > 0 {
		if nonInteractive || locked[0] == "/" || !locked[0].IsValid() {
			return "", ErrUnavailable
		}
		if err := unlockSecret(ctx, bus, []dbus.ObjectPath{locked[0]}); err != nil {
			return "", err
		}
		// Re-read only the same service/account selector after authorization. A
		// duplicate or still-locked credential is never treated as a missing master.
		return searchSecretMode(ctx, bus, true, service, account)
	}
	if len(unlocked) == 0 {
		return "", ErrNotFound
	}
	if unlocked[0] == "/" || !unlocked[0].IsValid() {
		return "", ErrUnavailable
	}
	return unlocked[0], nil
}
func unlockSecret(ctx context.Context, bus secretBus, paths []dbus.ObjectPath) error {
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := bus.call(ctx, secretRoot, secretService+".Unlock", paths).Store(&unlocked, &prompt); err != nil {
		return ErrUnavailable
	}
	for _, p := range unlocked {
		found := false
		for _, requested := range paths {
			found = found || p == requested
		}
		if !found {
			return ErrUnavailable
		}
	}
	if prompt == "/" {
		return nil
	}
	if !prompt.IsValid() {
		return ErrUnavailable
	}
	prompter, ok := bus.(secretPrompter)
	if !ok {
		return ErrUnavailable
	}
	_, err := prompter.prompt(ctx, prompt)
	return err
}
func secretSession(ctx context.Context, bus secretBus) (dbus.ObjectPath, error) {
	var output dbus.Variant
	var session dbus.ObjectPath
	if err := bus.call(ctx, secretRoot, secretService+".OpenSession", "plain", dbus.MakeVariant("")).Store(&output, &session); err != nil || session == "/" || !session.IsValid() {
		return "", ErrUnavailable
	}
	return session, nil
}
func secretGet(ctx context.Context, bus secretBus, service, account string) (string, error) {
	return secretGetMode(ctx, bus, true, service, account)
}
func secretGetMode(ctx context.Context, bus secretBus, nonInteractive bool, service, account string) (string, error) {
	item, err := searchSecretMode(ctx, bus, nonInteractive, service, account)
	if err != nil {
		return "", err
	}
	session, err := secretSession(ctx, bus)
	if err != nil {
		return "", err
	}
	defer bus.call(ctx, session, "org.freedesktop.Secret.Session.Close")
	var value secretValue
	if err = bus.call(ctx, item, secretItem+".GetSecret", session).Store(&value); err != nil || value.Session != session || len(value.Parameters) != 0 || len(value.Value) > 4096 {
		return "", ErrUnavailable
	}
	return string(value.Value), nil
}
func secretSet(ctx context.Context, bus secretBus, service, account, value string) error {
	return secretSetMode(ctx, bus, true, service, account, value)
}
func secretSetMode(ctx context.Context, bus secretBus, nonInteractive bool, service, account, value string) error {
	// CreateItem's replace=false remains the only write primitive. In particular,
	// cancellation or desktop-provider startup cannot authorize replacement.
	if _, err := searchSecretMode(ctx, bus, nonInteractive, service, account); err != ErrNotFound {
		return ErrUnavailable
	}
	var collection dbus.ObjectPath
	if err := bus.call(ctx, secretRoot, secretService+".ReadAlias", "default").Store(&collection); err != nil || collection == "/" || !collection.IsValid() {
		return ErrUnavailable
	}
	locked := func() (bool, error) {
		var v dbus.Variant
		if err := bus.call(ctx, collection, "org.freedesktop.DBus.Properties.Get", secretCollection, "Locked").Store(&v); err != nil {
			return false, ErrUnavailable
		}
		b, ok := v.Value().(bool)
		if !ok {
			return false, ErrUnavailable
		}
		return b, nil
	}
	isLocked, err := locked()
	if err != nil {
		return err
	}
	if isLocked {
		if nonInteractive {
			return ErrUnavailable
		}
		if err = unlockSecret(ctx, bus, []dbus.ObjectPath{collection}); err != nil {
			return err
		}
		if isLocked, err = locked(); err != nil || isLocked {
			return ErrUnavailable
		}
	}
	// Unlocking may have exposed an item created by another process or a late
	// previous request. Re-check the exact owned selector before the atomic add.
	if _, err = searchSecret(ctx, bus, service, account); err != ErrNotFound {
		return ErrUnavailable
	}
	session, err := secretSession(ctx, bus)
	if err != nil {
		return err
	}
	defer bus.call(ctx, session, "org.freedesktop.Secret.Session.Close")
	properties := map[string]dbus.Variant{secretItem + ".Label": dbus.MakeVariant("psst.zip CLI encrypted vault"), secretItem + ".Attributes": dbus.MakeVariant(map[string]string{"service": service, "username": account})}
	var item, prompt dbus.ObjectPath
	err = bus.call(ctx, collection, secretCollection+".CreateItem", properties, secretValue{Session: session, Parameters: []byte{}, Value: []byte(value), ContentType: "text/plain; charset=utf-8"}, false).Store(&item, &prompt)
	if err != nil {
		return ErrUnavailable
	}
	if prompt != "/" {
		if nonInteractive || !prompt.IsValid() {
			return ErrUnavailable
		}
		prompter, ok := bus.(secretPrompter)
		if !ok {
			return ErrUnavailable
		}
		result, err := prompter.prompt(ctx, prompt)
		if err != nil {
			return err
		}
		var valid bool
		item, valid = result.Value().(dbus.ObjectPath)
		if !valid {
			return ErrUnavailable
		}
	}
	if item == "/" || !item.IsValid() {
		return ErrUnavailable
	}
	return nil
}
func (b liveSecretBus) prompt(ctx context.Context, path dbus.ObjectPath) (dbus.Variant, error) {
	var empty dbus.Variant
	signals := make(chan *dbus.Signal, 4)
	b.connection.Signal(signals)
	defer b.connection.RemoveSignal(signals)
	match := []dbus.MatchOption{dbus.WithMatchSender(b.owner), dbus.WithMatchObjectPath(path), dbus.WithMatchInterface("org.freedesktop.Secret.Prompt"), dbus.WithMatchMember("Completed")}
	if err := b.connection.AddMatchSignalContext(ctx, match...); err != nil {
		return empty, ErrUnavailable
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = b.connection.RemoveMatchSignalContext(cleanup, match...)
	}()
	completed := false
	defer func() {
		if !completed {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			_ = b.call(cleanup, path, "org.freedesktop.Secret.Prompt.Dismiss").Err
		}
	}()
	// Subscribe before Prompt: Completed is permitted before the method reply.
	if err := b.call(ctx, path, "org.freedesktop.Secret.Prompt.Prompt", "").Err; err != nil {
		return empty, ErrUnavailable
	}
	for {
		select {
		case <-ctx.Done():
			return empty, ErrUnavailable
		case signal, ok := <-signals:
			if !ok {
				return empty, ErrUnavailable
			}
			if signal.Sender != b.owner || signal.Path != path || signal.Name != "org.freedesktop.Secret.Prompt.Completed" {
				continue
			}
			if len(signal.Body) != 2 {
				return empty, ErrUnavailable
			}
			dismissed, ok := signal.Body[0].(bool)
			if !ok {
				return empty, ErrUnavailable
			}
			completed = true
			if dismissed {
				return empty, ErrUnavailable
			}
			result, ok := signal.Body[1].(dbus.Variant)
			if !ok {
				return empty, ErrUnavailable
			}
			return result, nil
		}
	}
}

func platformKeyringGet(ctx context.Context, nonInteractive bool, service, account string) (string, error) {
	value, _, err := platformKeyringGetProvider(ctx, nonInteractive, "", service, account)
	return value, err
}
func platformKeyringSet(ctx context.Context, nonInteractive bool, service, account, value string) error {
	_, err := platformKeyringSetProvider(ctx, nonInteractive, "", service, account, value)
	return err
}
