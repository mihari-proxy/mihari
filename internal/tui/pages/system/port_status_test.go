package system

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// TestSystemPortStatus_ReconcilesOwnerAfterRestart verifies that owner updates
// correct cached occupancy regardless of probe delivery order or core update source.
func TestSystemPortStatus_ReconcilesOwnerAfterRestart(t *testing.T) {
	setSnapshot := func(m *Model, status protocol.Status, core protocol.CoreStatus) { m.SetSnapshot(status, core) }
	for _, tc := range []struct {
		name          string
		snapshotFirst bool
		update        func(*Model, protocol.Status, protocol.CoreStatus)
	}{
		{"probe-first", false, setSnapshot},
		{"snapshot-first", true, setSnapshot},
		{"core-observed", false, func(m *Model, status protocol.Status, core protocol.CoreStatus) {
			m.SetSnapshot(status, m.core)
			m.Update(ui.CoreObservedMsg{Core: core})
		}},
		{"core-loaded", false, func(m *Model, status protocol.Status, core protocol.CoreStatus) {
			m.SetSnapshot(status, m.core)
			m.Update(coreLoadResultMsg{core: core})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			if tc.snapshotFirst {
				tc.update(model, status, core)
				model.Update(probe())
			} else {
				model.Update(probe())
				tc.update(model, status, core)
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
	oldPID := model.core.PID
	model.listenFree = func(string) bool { return false }
	model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{PID: oldPID, Process: "mihomo.exe"}, true
	}
	model.Update(model.probePortHolds()())
	model.SetSnapshot(model.status, protocol.CoreStatus{PID: 30772, Status: "running"})
	model.Update(model.SyncPortHolds()().(ui.PageResultMsg).Result)
	if hold := model.portHolds[rowMixed]; hold.Kind != ui.PortHoldOccupied || hold.PID != oldPID {
		t.Fatalf("foreign mihomo after owner change = %+v; want Occupied by PID %d", hold, oldPID)
	}
}

// TestSystemPortStatus_ReprobesWhenCoreBecomesRunning verifies that an early
// starting-state probe does not leave a now-bound endpoint marked Available.
func TestSystemPortStatus_ReprobesWhenCoreBecomesRunning(t *testing.T) {
	model := portsModel(t)
	core := model.core
	core.Status = "starting"
	model.SetSnapshot(model.status, core)
	model.listenFree = func(string) bool { return true }
	model.Update(model.probePortHolds()())
	model.listenFree = func(string) bool { return false }
	model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{PID: core.PID, Process: "mihomo.exe"}, true
	}
	core.Status = "running"
	_, cmd := model.Update(ui.CoreObservedMsg{Core: core})
	if cmd == nil {
		t.Fatal("running transition did not refresh the early probe")
	}
	model.Update(cmd().(ui.PageResultMsg).Result)
	if hold := model.portHolds[rowMixed]; hold.Kind != ui.PortHoldOwned {
		t.Fatalf("running core still has an early observation: %+v", hold)
	}
}

// TestSystemPortStatus_ReprobesAfterOwnerChange covers an old socket observation
// followed by a new core identity while the System page remains open.
func TestSystemPortStatus_ReprobesAfterOwnerChange(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message func(protocol.CoreStatus) tea.Msg
	}{
		{"observed", func(core protocol.CoreStatus) tea.Msg { return ui.CoreObservedMsg{Core: core} }},
		{"loaded", func(core protocol.CoreStatus) tea.Msg { return coreLoadResultMsg{core: core} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := portsModel(t)
			occupant := model.core.PID
			model.listenFree = func(string) bool { return false }
			model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
				return platform.TCPOccupant{PID: occupant, Process: "mihomo.exe"}, true
			}
			model.Update(model.probePortHolds()())
			occupant++
			core := model.core
			core.PID = occupant
			_, cmd := model.Update(tc.message(core))
			if cmd == nil {
				t.Fatal("owner change did not schedule a fresh port probe")
			}
			model.Update(cmd().(ui.PageResultMsg).Result)
			if hold := model.portHolds[rowMixed]; hold.Kind != ui.PortHoldOwned || hold.PID != occupant {
				t.Fatalf("stale socket observation survived owner refresh: %+v", hold)
			}
			if _, cmd := model.Update(tc.message(core)); cmd != nil {
				t.Fatal("unchanged owner triggered another probe")
			}
		})
	}
}
