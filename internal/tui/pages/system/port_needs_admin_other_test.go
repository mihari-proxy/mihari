//go:build !linux

package system

import (
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// TestSystemPortStatus_DefaultPlatformKeepsUnknown fails if Windows or Darwin
// labels a hidden in-use port as Needs admin.
func TestSystemPortStatus_DefaultPlatformKeepsUnknown(t *testing.T) {
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
	if model.portHolds[rowMixed].Kind != ui.PortHoldUnknown {
		t.Fatalf("kind = %v", model.portHolds[rowMixed].Kind)
	}
	errs := diagnosticErrors(msg)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), addr) {
		t.Fatalf("diagnostics = %v", errs)
	}
}
