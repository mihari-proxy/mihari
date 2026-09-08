//go:build !darwin

package supervisor

import (
	"errors"
	"os/exec"
)

func prepareChildMode(command *exec.Cmd, shared bool) error {
	if shared {
		return errors.New("shared service process group is unavailable")
	}
	prepareChild(command)
	return nil
}

func startedChild(command *exec.Cmd, _ bool) Child {
	return &processChild{command: command}
}
