//go:build linux || freebsd || openbsd || netbsd || dragonfly

package state

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

type liveSecretBus struct{ connection *dbus.Conn }

func (b liveSecretBus) call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	return b.connection.Object(secretName, path).CallWithContext(ctx, method, dbus.FlagNoAutoStart, args...)
}

// Connect only to an existing local session bus. Unlike SessionBus(), this never
// launches dbus-launch, and both connecting and authenticating share the deadline.
func openSecretBus(ctx context.Context) (*dbus.Conn, error) {
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if address == "" {
		runtime := os.Getenv("XDG_RUNTIME_DIR")
		if !filepath.IsAbs(runtime) {
			return nil, ErrUnavailable
		}
		address = "unix:path=" + filepath.Join(runtime, "bus")
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
				return nil, ErrUnavailable
			}
			value, err := url.PathUnescape(parts[1])
			if err != nil {
				return nil, ErrUnavailable
			}
			switch parts[0] {
			case "path":
				path = value
			case "abstract":
				abstract = value
			case "guid":
			default:
				return nil, ErrUnavailable
			}
		}
		if filepath.IsAbs(path) && abstract == "" {
			socket = path
			break
		}
		if abstract != "" && path == "" && !strings.ContainsRune(abstract, '\x00') {
			socket = "@" + abstract
			break
		}
	}
	if socket == "" {
		return nil, ErrUnavailable
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, ErrUnavailable
	}
	bus, err := dbus.NewConn(connection, dbus.WithContext(ctx))
	if err != nil {
		_ = connection.Close()
		return nil, ErrUnavailable
	}
	if err = bus.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Geteuid()))}); err != nil {
		_ = bus.Close()
		return nil, ErrUnavailable
	}
	if err = bus.Hello(); err != nil {
		_ = bus.Close()
		return nil, ErrUnavailable
	}
	return bus, nil
}

func searchSecret(ctx context.Context, bus secretBus, service, account string) (dbus.ObjectPath, error) {
	var unlocked, locked []dbus.ObjectPath
	err := bus.call(ctx, secretRoot, secretService+".SearchItems", map[string]string{"service": service, "username": account}).Store(&unlocked, &locked)
	if err != nil || len(locked) > 0 || len(unlocked) > 1 {
		return "", ErrUnavailable
	}
	if len(unlocked) == 0 {
		return "", ErrNotFound
	}
	return unlocked[0], nil
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
	item, err := searchSecret(ctx, bus, service, account)
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
	// Never replace an existing master, including a request completed late after
	// cancellation. Duplicate items are rejected when reading, rather than guessed.
	if _, err := searchSecret(ctx, bus, service, account); err != ErrNotFound {
		return ErrUnavailable
	}
	var collection dbus.ObjectPath
	if err := bus.call(ctx, secretRoot, secretService+".ReadAlias", "default").Store(&collection); err != nil || collection == "/" || !collection.IsValid() {
		return ErrUnavailable
	}
	var locked dbus.Variant
	if err := bus.call(ctx, collection, "org.freedesktop.DBus.Properties.Get", secretCollection, "Locked").Store(&locked); err != nil || locked.Value() != false {
		return ErrUnavailable
	}
	session, err := secretSession(ctx, bus)
	if err != nil {
		return err
	}
	defer bus.call(ctx, session, "org.freedesktop.Secret.Session.Close")
	properties := map[string]dbus.Variant{
		secretItem + ".Label":      dbus.MakeVariant("psst.zip CLI encrypted vault"),
		secretItem + ".Attributes": dbus.MakeVariant(map[string]string{"service": service, "username": account}),
	}
	var item, prompt dbus.ObjectPath
	err = bus.call(ctx, collection, secretCollection+".CreateItem", properties, secretValue{Session: session, Parameters: []byte{}, Value: []byte(value), ContentType: "text/plain; charset=utf-8"}, false).Store(&item, &prompt)
	if err != nil || prompt != "/" || item == "/" || !item.IsValid() {
		return ErrUnavailable
	}
	return nil
}

func platformKeyringGet(ctx context.Context, _ bool, service, account string) (string, error) {
	bus, err := openSecretBus(ctx)
	if err != nil {
		return "", err
	}
	defer bus.Close()
	return secretGet(ctx, liveSecretBus{bus}, service, account)
}
func platformKeyringSet(ctx context.Context, _ bool, service, account, value string) error {
	bus, err := openSecretBus(ctx)
	if err != nil {
		return err
	}
	defer bus.Close()
	return secretSet(ctx, liveSecretBus{bus}, service, account, value)
}
