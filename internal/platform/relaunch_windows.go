//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type replacementProcess interface {
	Wait() (*os.ProcessState, error)
	Release() error
}

// startProcess starts a process and is replaced in tests to avoid spawning binaries.
var startProcess = func(name string, argv []string, attr *os.ProcAttr) (replacementProcess, error) {
	return os.StartProcess(name, argv, attr)
}

// Relaunch starts the replacement Mihari binary attached to the current console.
func Relaunch(binary string, args, env []string) error {
	if strings.TrimSpace(binary) == "" || len(args) == 0 {
		return fmt.Errorf("relaunch Mihari: binary and arguments are required")
	}
	process, err := startProcess(binary, args, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		return fmt.Errorf("start updated Mihari: %w", err)
	}
	if _, err := process.Wait(); err != nil {
		waitErr := fmt.Errorf("wait for updated Mihari: %w", err)
		if releaseErr := process.Release(); releaseErr != nil {
			return errors.Join(waitErr, fmt.Errorf("release updated Mihari after wait failure: %w", releaseErr))
		}
		return waitErr
	}
	return nil
}
