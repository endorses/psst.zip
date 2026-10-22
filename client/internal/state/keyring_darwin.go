//go:build darwin

package state

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os/exec"
	"strings"
	"time"
)

func platformKeyringGet(ctx context.Context, nonInteractive bool, service, account string) (string, error) {
	// security(1) has no reliable per-command suppression of Keychain UI.
	// Headless/JSON commands must explicitly select the passphrase vault on macOS.
	if nonInteractive {
		return "", ErrUnavailable
	}
	command := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", service, "-a", account, "-w")
	command.WaitDelay = time.Second
	var stderr bytes.Buffer
	command.Stderr = &stderr
	value, err := command.Output()
	if err != nil {
		if ctx.Err() == nil && strings.Contains(stderr.String(), "could not be found") {
			return "", ErrNotFound
		}
		return "", ErrUnavailable
	}
	secret := strings.TrimSpace(string(value))
	// Preserve previously stored go-keyring master identities.
	if encoded, ok := strings.CutPrefix(secret, "go-keyring-base64:"); ok {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", ErrUnlock
		}
		return string(decoded), nil
	}
	if encoded, ok := strings.CutPrefix(secret, "go-keyring-encoded:"); ok {
		decoded, err := hex.DecodeString(encoded)
		if err != nil {
			return "", ErrUnlock
		}
		return string(decoded), nil
	}
	return secret, nil
}

func platformKeyringSet(ctx context.Context, nonInteractive bool, service, account, secret string) error {
	if nonInteractive {
		return ErrUnavailable
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	// No -U: adding a duplicate may fail but must never overwrite another master.
	// The credential travels over stdin, not through the process argument list.
	input := "add-generic-password -s " + quote(service) + " -a " + quote(account) + " -w " + quote("go-keyring-base64:"+base64.StdEncoding.EncodeToString([]byte(secret))) + "\n"
	if len(input) > 4096 {
		return ErrUnavailable
	}
	command := exec.CommandContext(ctx, "/usr/bin/security", "-i")
	command.WaitDelay = time.Second
	command.Stdin = strings.NewReader(input)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Run(); err != nil || ctx.Err() != nil {
		return ErrUnavailable
	}
	return nil
}
