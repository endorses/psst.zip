//go:build !linux && !darwin && !windows

package lan

import (
	"errors"
	"os"
)

func protectStaging(string) error {
	return errors.New("protected LAN staging is unsupported on this platform")
}
func lockStaging(*os.File) (bool, error) {
	return false, errors.New("LAN staging locking is unsupported on this platform")
}
func checkSpace(string, int64) error {
	return errors.New("LAN disk preflight is unsupported on this platform")
}
