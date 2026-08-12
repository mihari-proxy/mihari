//go:build windows

package platform

import (
	"fmt"
	"os"
	"strings"
)

// Relaunch starts the replacement Mihari binary attached to the current console.
func Relaunch(binary string, args, env []string) error {
	if strings.TrimSpace(binary) == "" || len(args) == 0 {
		return fmt.Errorf("relaunch Mihari: binary and arguments are required")
	}
	process, err := os.StartProcess(binary, args, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		return fmt.Errorf("start updated Mihari: %w", err)
	}
	if err := process.Release(); err != nil {
		return fmt.Errorf("release updated Mihari process: %w", err)
	}
	return nil
}
