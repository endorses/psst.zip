package interop

import "os/exec"

func configureChild(command *exec.Cmd)         {}
func signalChild(command *exec.Cmd, kill bool) { _ = command.Process.Kill() }
