//go:build !darwin

package supervisor

import (
	"os/exec"
	"testing"
)

func TestPrepareChildMode_SharedFailsClosed(t *testing.T) {
	command := exec.Command("unused")
	if err := prepareChildMode(command, true); err == nil {
		t.Fatal("shared process-group mode succeeded outside Darwin")
	}
}

func TestPrepareChildMode_OrdinaryModeRemainsAvailable(t *testing.T) {
	command := exec.Command("unused")
	if err := prepareChildMode(command, false); err != nil {
		t.Fatalf("ordinary child mode: %v", err)
	}
}
