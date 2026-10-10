package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type OSCommandRunner struct{}

func (OSCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = FileReferenceEnvironment()
	return command.CombinedOutput()
}

// VerifiedExecutor consumes only a complete capability-generated command.
// Injected executors in tests record calls without starting an actual core.
type VerifiedExecutor interface {
	Execute(context.Context, CoreCommand) ([]byte, error)
}

// OSVerifiedExecutor explicitly assigns the allowlisted environment.
type OSVerifiedExecutor struct{}

// verifiedExecutionError retains the operating-system error class without
// exposing executor output, paths, or configuration values in its message.
type verifiedExecutionError struct{ cause error }

func (e verifiedExecutionError) Error() string { return "execute verified mihomo command failed" }
func (e verifiedExecutionError) Unwrap() error { return e.cause }

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
	output, err := x.Execute(ctx, command)
	if err != nil {
		return output, verifiedExecutionError{cause: errors.Join(err, commandOutputCause("verified mihomo command", output))}
	}
	return output, nil
}

// FileReferenceEnvironment permits approved native mihomo file references.
// Replace inherited policy rather than relying on duplicate environment keys.
func FileReferenceEnvironment() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(entry), "SKIP_SAFE_PATH_CHECK=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "SKIP_SAFE_PATH_CHECK=true")
}
