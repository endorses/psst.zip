//go:build !windows && !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package state

import "context"

func platformKeyringGet(context.Context, bool, string, string) (string, error) {
	return "", ErrUnavailable
}
func platformKeyringSet(context.Context, bool, string, string, string) error { return ErrUnavailable }
