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

// VerifiedExecutor consumes only a complete capability-generated command.
// Injected executors in tests record calls without starting an actual core.
type VerifiedExecutor interface {
	Execute(context.Context, CoreCommand) ([]byte, error)
}

// OSVerifiedExecutor explicitly assigns the allowlisted environment.
type OSVerifiedExecutor struct{}

func (OSVerifiedExecutor) Execute(ctx context.Context, c CoreCommand) ([]byte, error) {
	command := exec.CommandContext(ctx, c.Binary, c.Args...)
	command.Env = append([]string(nil), c.Env...)
	command.Dir = c.Home
	return command.CombinedOutput()
}
func executeVerified(ctx context.Context, v *VerifiedCore, p CorePurpose, c *ConfigCapability, x VerifiedExecutor) ([]byte, error) {
	if v == nil || v.store == nil {
		return nil, dataFailure("verified core unavailable")
	}
	release, e := v.store.coreStore().execution().acquire(ctx)
	if e != nil {
		return nil, e
	}
	defer release()
	return executeVerifiedOwned(ctx, v, p, c, x)
}

// executeVerifiedOwned requires the caller to retain the store execution gate.
func executeVerifiedOwned(ctx context.Context, v *VerifiedCore, p CorePurpose, c *ConfigCapability, x VerifiedExecutor) ([]byte, error) {
	command, e := v.Command(ctx, p, c)
	if e != nil {
		return nil, e
	}
	if x == nil {
		x = OSVerifiedExecutor{}
	}
	return x.Execute(ctx, command)
}
