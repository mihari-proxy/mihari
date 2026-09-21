//go:build !windows

package supervisor

import (
	"fmt"
	"os/exec"
)

func launchCommand(command *exec.Cmd, shared bool) (Child, error) {
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start mihomo: %w", err)
	}
	if err := trackChild(command); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, fmt.Errorf("track mihomo child: %w", err)
	}
	return startedChild(command, shared), nil
}
