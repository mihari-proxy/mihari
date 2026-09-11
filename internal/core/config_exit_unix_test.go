//go:build linux || darwin

package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestValidationSignaledExitHelper(t *testing.T) {
	if os.Getenv("MIHARI_TEST_VALIDATION_SIGNAL") != "1" {
		return
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	// The parent owns the process and has a deadline even if signal delivery fails.
	<-time.After(time.Minute)
}

func TestValidateVerifiedConfig_PreservesSignaledExecution(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-test.run=^TestValidationSignaledExitHelper$")
	command.Env = append(os.Environ(), "MIHARI_TEST_VALIDATION_SIGNAL=1")
	err = command.Run()
	var signaled *exec.ExitError
	if !errors.As(err, &signaled) || signaled.Exited() || ctx.Err() != nil {
		t.Fatalf("signal termination fixture failed: %v", err)
	}
	signaled.Stderr = []byte("controller-secret-value")
	s, installer, _ := trustedFixture(t)
	seedInstalledReceipt(t, s, "trusted binary")
	verified, err := OpenInstalledCore(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = verified.Close() })
	configuration, err := installer.GeneratedConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configuration.Close() })
	err = ValidateVerifiedConfig(ctx, verified, configuration, configExecutorFunc(func(context.Context, CoreCommand) ([]byte, error) {
		return []byte("controller-secret-value"), signaled
	}))
	var got *exec.ExitError
	if !errors.As(err, &got) || got != signaled {
		t.Fatalf("signal termination was misclassified: %v", err)
	}
	if strings.Contains(err.Error(), "controller-secret-value") {
		t.Fatal("signaled execution exposed validator output")
	}
}
