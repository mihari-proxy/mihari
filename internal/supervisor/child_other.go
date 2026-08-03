//go:build !windows && !linux && !darwin

package supervisor

import "os/exec"

func prepareChild(*exec.Cmd) {}

func trackChild(*exec.Cmd) error { return nil }

func terminateChild(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}

func killChild(command *exec.Cmd) error { return terminateChild(command) }
