package app

import (
	"fmt"
	"testing"

	"github.com/mihari-proxy/mihari/internal/service"
)

func securityNativeProcessIdentity(boot string, pid int, seconds int64, micros uint32) service.ProcessIdentity {
	return service.ProcessIdentity{
		PID:       pid,
		BootID:    boot,
		StartUnix: seconds,
		StartUsec: micros,
		Group:     fmt.Sprintf("darwin-pgid-v1:%s:%d:%d:%d", boot, pid, seconds, micros),
	}
}

func TestSecurityNativeProcessIdentity_BindsCanonicalDarwinGroup(t *testing.T) {
	id := securityNativeProcessIdentity("fixture-boot", 123, 100, 42)
	want := "darwin-pgid-v1:fixture-boot:123:100:42"
	if id.PID != 123 || id.BootID != "fixture-boot" || id.StartUnix != 100 || id.StartUsec != 42 || id.Group != want {
		t.Fatalf("identity=%+v want group %q", id, want)
	}
}
