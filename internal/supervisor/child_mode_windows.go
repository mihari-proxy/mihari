//go:build windows

package supervisor

import (
	"errors"
	"os/exec"
)

func prepareChildMode(_ *exec.Cmd, shared bool) error {
	if shared {
		return errors.New("shared service process group is unavailable")
	}
	return nil
}
