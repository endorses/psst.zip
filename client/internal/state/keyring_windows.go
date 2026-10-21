//go:build windows

package state

import (
	"context"
	"errors"

	"github.com/zalando/go-keyring"
)

// Credential Manager's native calls are noninteractive. Windows provides no
// cancellation handle for these synchronous OS primitives: context is checked
// before and after, and no background Set is allowed to outlive the store lock.
// Native syscall cancellation/latency remains a Windows runtime limitation.
func platformKeyringGet(ctx context.Context, _ bool, service, account string) (string, error) {
	if ctx.Err() != nil {
		return "", ErrUnavailable
	}
	value, err := keyring.Get(service, account)
	if ctx.Err() != nil {
		return "", ErrUnavailable
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", ErrUnavailable
	}
	return value, nil
}
func platformKeyringSet(ctx context.Context, _ bool, service, account, secret string) error {
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	if _, err := keyring.Get(service, account); !errors.Is(err, keyring.ErrNotFound) {
		return ErrUnavailable
	}
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	err := keyring.Set(service, account, secret)
	if err != nil || ctx.Err() != nil {
		return ErrUnavailable
	}
	return nil
}
