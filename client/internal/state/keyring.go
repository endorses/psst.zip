package state

import (
	"context"
	"errors"
	"sync"
	"time"
)

// OSKeyring uses Keychain on macOS, Credential Manager on Windows and Secret
// Service on supported Linux desktops. Availability errors are never downgraded.
type OSKeyring struct {
	NonInteractive bool
	Provider       string
	mu             sync.Mutex
}

func (k *OSKeyring) Get(service, account string) (string, error) {
	return k.GetContext(context.Background(), service, account)
}

func (k *OSKeyring) Set(service, account, secret string) error {
	return k.SetContext(context.Background(), service, account, secret)
}

func credentialTimeout(nonInteractive bool) time.Duration {
	if nonInteractive {
		return 10 * time.Second
	}
	return 2 * time.Minute
}

func (k *OSKeyring) ProviderName() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.Provider
}

func (k *OSKeyring) GetContext(ctx context.Context, service, account string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, credentialTimeout(k.NonInteractive))
	defer cancel()
	if ctx.Err() != nil {
		return "", ErrUnavailable
	}
	value, selected, err := platformKeyringGetProvider(ctx, k.NonInteractive, k.Provider, service, account)
	if (err == nil || errors.Is(err, ErrNotFound)) && selected != "" {
		k.Provider = selected
	}
	return value, err
}

func (k *OSKeyring) SetContext(ctx context.Context, service, account, secret string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, credentialTimeout(k.NonInteractive))
	defer cancel()
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	selected, err := platformKeyringSetProvider(ctx, k.NonInteractive, k.Provider, service, account, secret)
	if err == nil && selected != "" {
		k.Provider = selected
	}
	return err
}
