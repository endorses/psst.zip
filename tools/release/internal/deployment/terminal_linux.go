//go:build linux

package deployment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

func termios(f *os.File) (syscall.Termios, error) {
	var term syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&term)))
	if errno != 0 {
		return term, fmt.Errorf("private terminal unavailable")
	}
	return term, nil
}
func isTerminal(f *os.File) bool { _, err := termios(f); return err == nil }
func readSecret(ctx context.Context, f *os.File) (string, error) {
	old, err := termios(f)
	if err != nil {
		return "", err
	}
	next := old
	next.Lflag &^= syscall.ECHO
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&next)))
	if errno != 0 {
		return "", fmt.Errorf("cannot disable private terminal echo")
	}
	defer func() {
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&old)))
	}()
	return readLine(ctx, f)
}

// Poll the canonical terminal so a signal cancellation always restores echo;
// bufio.Reader would otherwise remain blocked after signal.NotifyContext fires.
func readLine(ctx context.Context, f *os.File) (string, error) {
	fd := int(f.Fd())
	var ready syscall.FdSet
	if fd < 0 || fd >= len(ready.Bits)*64 {
		return "", fmt.Errorf("private terminal descriptor unsupported")
	}
	var data []byte
	var buffer [4096]byte
	for {
		if ctx.Err() != nil {
			return "", fmt.Errorf("verification input cancelled")
		}
		ready = syscall.FdSet{}
		ready.Bits[fd/64] |= 1 << uint(fd%64)
		timeout := syscall.Timeval{Usec: 100000}
		n, err := syscall.Select(fd+1, &ready, nil, nil, &timeout)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("private terminal input failed")
		}
		if n == 0 {
			continue
		}
		n, err = syscall.Read(fd, buffer[:])
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || n == 0 {
			return "", fmt.Errorf("private terminal input ended")
		}
		data = append(data, buffer[:n]...)
		if len(data) > 65536 {
			return "", fmt.Errorf("private terminal input exceeds bounds")
		}
		if strings.ContainsRune(string(buffer[:n]), '\n') {
			return strings.TrimRight(string(data), "\r\n"), nil
		}
	}
}
