package state

import (
	"context"
	"time"
)

// OSKeyring uses Keychain on macOS, Credential Manager on Windows and Secret
// Service on supported Linux desktops. Availability errors are never downgraded.
type OSKeyring struct{ NonInteractive bool }

func (k OSKeyring) Get(service, account string) (string, error) {
	return k.GetContext(context.Background(), service, account)
}

func (k OSKeyring) Set(service, account, secret string) error {
	return k.SetContext(context.Background(), service, account, secret)
}

func (k OSKeyring) GetContext(ctx context.Context, service, account string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return "", ErrUnavailable
	}
	return platformKeyringGet(ctx, k.NonInteractive, service, account)
}

func (k OSKeyring) SetContext(ctx context.Context, service, account, secret string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	return platformKeyringSet(ctx, k.NonInteractive, service, account, secret)
}
