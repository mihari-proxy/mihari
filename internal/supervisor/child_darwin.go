//go:build darwin

package supervisor

import (
	"os/exec"
	"syscall"
)

func prepareChild(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func trackChild(*exec.Cmd) error { return nil }

func terminateChild(command *exec.Cmd) error {
	return signalProcessGroup(command, syscall.SIGTERM)
}

func killChild(command *exec.Cmd) error {
	return signalProcessGroup(command, syscall.SIGKILL)
}

func signalProcessGroup(command *exec.Cmd, signal syscall.Signal) error {
	if command.Process == nil {
		return nil
	}
	if err := syscall.Kill(-command.Process.Pid, signal); err != nil {
		return command.Process.Signal(signal)
	}
	return nil
}
