//go:build linux

package supervisor

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestPrepareChildMode_OrdinaryLinuxChildIsIsolated(t *testing.T) {
	command := exec.Command("unused")
	if err := prepareChildMode(command, false); err != nil {
		t.Fatal(err)
	}
	if command.SysProcAttr == nil || !command.SysProcAttr.Setpgid || command.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("ordinary Linux child attributes=%+v", command.SysProcAttr)
	}
}
