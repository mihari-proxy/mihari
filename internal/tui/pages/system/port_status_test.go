package system

import (
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// TestSystemPortStatus_ReconcilesOwnerAfterRestart verifies that owner updates
// correct cached occupancy regardless of probe delivery order or core update source.
func TestSystemPortStatus_ReconcilesOwnerAfterRestart(t *testing.T) {
	for _, order := range []string{"probe-first", "snapshot-first", "core-observed", "core-loaded"} {
		t.Run(order, func(t *testing.T) {
			model := portsModel(t)
			model.listenFree = func(string) bool { return false }
			model.lookupOccupant = func(addr string) (platform.TCPOccupant, bool) {
				if addr == model.onboarding.WebAddr {
					return platform.TCPOccupant{PID: 200, Process: "mihari.exe"}, true
				}
				return platform.TCPOccupant{PID: 30772, Process: "mihomo.exe"}, true
			}
			// The new processes already listen, but the page still knows the old PIDs.
			probe := model.probePortHolds()
			status := model.status
			status.PID = 200
			core := protocol.CoreStatus{PID: 30772, Status: "running"}
			switch order {
			case "snapshot-first":
				model.SetSnapshot(status, core)
				model.Update(probe())
			case "probe-first":
				model.Update(probe())
				model.SetSnapshot(status, core)
			case "core-observed":
				model.Update(probe())
				model.SetSnapshot(status, model.core)
				model.Update(ui.CoreObservedMsg{Core: core})
			case "core-loaded":
				model.Update(probe())
				model.SetSnapshot(status, model.core)
				model.Update(coreLoadResultMsg{core: core})
			}
			for _, id := range []string{rowMixed, rowController, rowWeb} {
				if hold := model.portHolds[id]; hold.Kind != ui.PortHoldOwned {
					t.Errorf("%s after owner refresh = %+v; want Owned without another probe", id, hold)
				}
			}
		})
	}
}

// TestSystemPortStatus_OlderProbeCannotOverwriteNewerResult verifies that a delayed
// probe cannot replace a newer observation of available endpoints.
func TestSystemPortStatus_OlderProbeCannotOverwriteNewerResult(t *testing.T) {
	model := portsModel(t)
	model.listenFree = func(string) bool { return false }
	model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{PID: 9736, Process: "mihomo.exe"}, true
	}
	older := model.probePortHolds()()
	model.listenFree = func(string) bool { return true }
	model.Update(model.probePortHolds()())
	model.Update(older)
	for _, id := range []string{rowMixed, rowController, rowWeb} {
		if hold := model.portHolds[id]; hold.Kind != ui.PortHoldAvailable {
			t.Errorf("%s overwritten by delayed result: %+v", id, hold)
		}
	}
}

// TestSystemPortStatus_OwnerChangePreservesForeignOccupant verifies that a socket
// held by a different PID remains foreign even when its process is named mihomo.
func TestSystemPortStatus_OwnerChangePreservesForeignOccupant(t *testing.T) {
	model := portsModel(t)
	model.listenFree = func(string) bool { return false }
	model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{PID: 42, Process: "mihomo.exe"}, true
	}
	model.Update(model.probePortHolds()())
	model.SetSnapshot(model.status, protocol.CoreStatus{PID: 30772, Status: "running"})
	if hold := model.portHolds[rowMixed]; hold.Kind != ui.PortHoldOccupied || hold.PID != 42 {
		t.Fatalf("foreign mihomo after owner change = %+v; want Occupied by PID 42", hold)
	}
}
