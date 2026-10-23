//go:build !windows

package interop

import (
	"os/exec"
	"syscall"
)

func configureChild(command *exec.Cmd) { command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func signalChild(command *exec.Cmd, kill bool) {
	signal := syscall.SIGTERM
	if kill {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-command.Process.Pid, signal)
}
