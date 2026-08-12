//go:build unix

package platform

import (
	"fmt"
	"strings"
	"syscall"
)

// Relaunch replaces the current process with the updated Mihari binary.
func Relaunch(binary string, args, env []string) error {
	if strings.TrimSpace(binary) == "" || len(args) == 0 {
		return fmt.Errorf("relaunch Mihari: binary and arguments are required")
	}
	if err := syscall.Exec(binary, args, env); err != nil {
		return fmt.Errorf("exec updated Mihari: %w", err)
	}
	return nil
}
