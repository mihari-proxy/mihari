package core

import (
	"context"
	"os/exec"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type OSCommandRunner struct{}

func (OSCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
