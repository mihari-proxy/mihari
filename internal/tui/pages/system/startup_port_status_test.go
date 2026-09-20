package system

import (
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// TestSystemPortStatus_StartupWaitsForCoreIdentity covers the startup interval
// where network application deliberately defers the first core snapshot.
func TestSystemPortStatus_StartupWaitsForCoreIdentity(t *testing.T) {
	m := portsModel(t)
	status := m.status
	status.StartupNetwork = &protocol.StartupNetworkStatus{TunApplying: true}
	m.SetSnapshot(status, protocol.CoreStatus{})
	m.listenFree = func(string) bool { return false }
	m.lookupOccupant = func(addr string) (platform.TCPOccupant, bool) {
		if addr == m.onboarding.WebAddr {
			return platform.TCPOccupant{PID: status.PID, Process: "mihari.exe"}, true
		}
		return platform.TCPOccupant{PID: 40616, Process: "mihomo.exe"}, true
	}
	m.Update(m.probePortHolds()())
	assertChecking := func() {
		t.Helper()
		for _, id := range []string{rowMixed, rowController} {
			hold := m.portHolds[id]
			if ui.FormatPortHoldLabel(hold) != "Checking owner…" || ui.PortHoldTone(hold.Kind) != ui.ToneNeutral {
				t.Errorf("%s without core identity = %+v; want neutral pending ownership", id, hold)
			}
			if hold.PID != 40616 || hold.Process != "mihomo.exe" {
				t.Errorf("lost socket observation: %+v", hold)
			}
		}
		if m.portHolds[rowWeb].Kind != ui.PortHoldOwned {
			t.Error("known daemon owner should remain Owned")
		}
		if strings.Contains(m.View(), "Occupied by mihomo") {
			t.Error("startup view claims an unverified foreign core")
		}
	}
	assertChecking()
	// Status polling reports startup completion before the first Core event.
	status.StartupNetwork = nil
	m.SetSnapshot(status, protocol.CoreStatus{})
	assertChecking()
	m.SetSnapshot(status, protocol.CoreStatus{Status: "running", PID: 40616})
	for _, id := range []string{rowMixed, rowController} {
		if hold := m.portHolds[id]; hold.Kind != ui.PortHoldOwned {
			t.Errorf("%s did not become Owned after the core snapshot: %+v", id, hold)
		}
	}
}

// TestSystemPortStatus_PendingIdentityDoesNotHideKnownConflict distinguishes an
// unobserved owner from a confirmed stopped core or a known foreign process.
func TestSystemPortStatus_PendingIdentityDoesNotHideKnownConflict(t *testing.T) {
	for _, tc := range []struct {
		name     string
		core     protocol.CoreStatus
		free     bool
		occupant int
		label    string
	}{
		{"missing snapshot", protocol.CoreStatus{}, false, 99, "Checking owner…"},
		{"starting without PID", protocol.CoreStatus{Status: "starting"}, false, 99, "Checking owner…"},
		{"stopped", protocol.CoreStatus{Status: "stopped"}, false, 99, "Occupied by mihomo.exe (99)"},
		{"foreign", protocol.CoreStatus{Status: "running", PID: 42}, false, 99, "Occupied by mihomo.exe (99)"},
		{"owned", protocol.CoreStatus{Status: "running", PID: 99}, false, 99, "Owned"},
		{"available", protocol.CoreStatus{}, true, 0, "Available"},
		{"unidentified occupant", protocol.CoreStatus{}, false, 0, "Unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := portsModel(t)
			status := m.status
			status.StartupNetwork = &protocol.StartupNetworkStatus{TunApplying: true}
			m.SetSnapshot(status, tc.core)
			m.listenFree = func(string) bool { return tc.free }
			m.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
				return platform.TCPOccupant{PID: tc.occupant, Process: "mihomo.exe"}, tc.occupant > 0
			}
			m.Update(m.probePortHolds()())
			if got := ui.FormatPortHoldLabel(m.portHolds[rowMixed]); got != tc.label {
				t.Fatalf("port label = %q, want %q", got, tc.label)
			}
		})
	}
}
