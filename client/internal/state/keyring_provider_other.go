//go:build !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package state

import (
	"context"
	"runtime"
)

func nativeKeyringProvider() string {
	switch runtime.GOOS {
	case "darwin":
		return "keychain"
	case "windows":
		return "credential-manager"
	default:
		return ""
	}
}

func platformKeyringGetProvider(ctx context.Context, nonInteractive bool, provider, service, account string) (string, string, error) {
	selected := nativeKeyringProvider()
	if selected == "" || provider != "" && provider != selected {
		return "", "", ErrUnavailable
	}
	value, err := platformKeyringGet(ctx, nonInteractive, service, account)
	return value, selected, err
}

func platformKeyringSetProvider(ctx context.Context, nonInteractive bool, provider, service, account, value string) (string, error) {
	selected := nativeKeyringProvider()
	if selected == "" || provider != "" && provider != selected {
		return "", ErrUnavailable
	}
	return selected, platformKeyringSet(ctx, nonInteractive, service, account, value)
}
