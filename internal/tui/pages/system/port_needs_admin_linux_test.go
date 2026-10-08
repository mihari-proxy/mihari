//go:build linux

package system

import (
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// TestSystemPortStatus_LinuxDefaultShowsNeedsAdmin fails if the System page
// does not use the Linux privilege rule for a hidden in-use port.
func TestSystemPortStatus_LinuxDefaultShowsNeedsAdmin(t *testing.T) {
	model := portsModel(t)
	addr := boundLoopback(t)
	model.onboarding.MixedAddr = addr
	model.onboarding.ControllerAddr = ""
	model.onboarding.WebAddr = ""
	model.isElevated = func() bool { return false }
	model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{}, false
	}
	msg, model := probeHolds(t, model)
	if errs := diagnosticErrors(msg); len(errs) != 0 {
		t.Fatalf("diagnostics = %v", errs)
	}
	if model.portHolds[rowMixed].Kind != ui.PortHoldNeedsAdmin {
		t.Fatalf("kind = %v", model.portHolds[rowMixed].Kind)
	}
}
