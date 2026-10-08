package system

import (
	"net"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// TestSystemPortStatus_UnelevatedLinuxInUseShowsNeedsAdmin fails if an in-use
// port with no visible occupant is still labeled Unknown, is filed as a probe
// error, or can no longer be edited.
func TestSystemPortStatus_UnelevatedLinuxInUseShowsNeedsAdmin(t *testing.T) {
	model := portsModel(t)
	addr := boundLoopback(t)
	model.onboarding.MixedAddr = addr
	model.onboarding.ControllerAddr = ""
	model.onboarding.WebAddr = ""
	model.revealsListener = func() bool { return true }
	model.isElevated = func() bool { return false }
	model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{}, false
	}

	msg, model := probeHolds(t, model)
	if errs := diagnosticErrors(msg); len(errs) != 0 {
		t.Fatalf("diagnostics = %v", errs)
	}
	hold := model.portHolds[rowMixed]
	if hold.Kind != ui.PortHoldNeedsAdmin {
		t.Fatalf("kind = %v, want Needs admin", hold.Kind)
	}
	if got := ui.FormatPortHoldLabel(hold); got != "Needs admin" {
		t.Fatalf("label = %q", got)
	}
	if ui.PortHoldTone(hold.Kind) != ui.ToneNeutral {
		t.Fatalf("tone = %v", ui.PortHoldTone(hold.Kind))
	}
	row := model.portRow(rowMixed, ui.MixedLabel, addr, model.core.PID)
	if strings.Contains(row.value, "(Needs admin)") || !strings.Contains(row.value, "Needs admin") {
		t.Fatalf("value = %q", row.value)
	}
	wantDetail := addr + "\nAdministrator privileges are required; re-run Mihari from an elevated shell."
	if row.detail != wantDetail {
		t.Fatalf("detail = %q", row.detail)
	}
	model.focusID = rowMixed
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if model.editID != rowMixed || cmd == nil {
		t.Fatalf("editID=%q cmd nil=%v", model.editID, cmd == nil)
	}
}

// TestSystemPortStatus_NeedsAdminSurvivesReconcile fails if a later owner
// snapshot rewrites the privilege hint before a new probe runs.
func TestSystemPortStatus_NeedsAdminReprobesWhenOwnerChanges(t *testing.T) {
	model := portsModel(t)
	addr := boundLoopback(t)
	model.onboarding.MixedAddr = addr
	model.onboarding.ControllerAddr = ""
	model.onboarding.WebAddr = ""
	model.revealsListener = func() bool { return true }
	model.isElevated = func() bool { return false }
	model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{}, false
	}
	_, model = probeHolds(t, model)
	model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{PID: 30772, Process: "mihomo"}, true
	}
	core := model.core
	core.PID = 30772
	updated, cmd := model.Update(ui.CoreObservedMsg{Core: core})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("owner change did not reprobe")
	}
	result, ok := cmd().(ui.PageResultMsg)
	if !ok {
		t.Fatalf("reprobe message %T", cmd())
	}
	updated, _ = model.Update(result.Result)
	model = updated.(*Model)
	hold := model.portHolds[rowMixed]
	if hold.Kind != ui.PortHoldOwned || hold.PID != 30772 {
		t.Fatalf("hold = %+v", hold)
	}
}

func TestSystemPortStatus_NeedsAdminSurvivesReconcile(t *testing.T) {
	model := portsModel(t)
	addr := boundLoopback(t)
	model.onboarding.MixedAddr = addr
	model.onboarding.ControllerAddr = ""
	model.onboarding.WebAddr = ""
	model.revealsListener = func() bool { return true }
	model.isElevated = func() bool { return false }
	model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{}, false
	}
	_, model = probeHolds(t, model)
	model.SetSnapshot(model.status, model.core)
	if hold := model.portHolds[rowMixed]; hold.Kind != ui.PortHoldNeedsAdmin {
		t.Fatalf("after reconcile kind = %v", hold.Kind)
	}
}

// TestSystemPortStatus_ElevatedInUseStaysUnknown fails if root is told to
// elevate again, or if the unexplained bind failure is dropped.
func TestSystemPortStatus_ElevatedInUseStaysUnknown(t *testing.T) {
	model := portsModel(t)
	addr := boundLoopback(t)
	model.onboarding.MixedAddr = addr
	model.onboarding.ControllerAddr = ""
	model.onboarding.WebAddr = ""
	model.revealsListener = func() bool { return true }
	model.isElevated = func() bool { return true }
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

// TestSystemPortStatus_HiddenHolderOnOtherOSStaysUnknown fails if Needs admin
// appears where elevation cannot reveal the listener.
func TestSystemPortStatus_HiddenHolderOnOtherOSStaysUnknown(t *testing.T) {
	model := portsModel(t)
	addr := boundLoopback(t)
	model.onboarding.MixedAddr = addr
	model.onboarding.ControllerAddr = ""
	model.onboarding.WebAddr = ""
	model.revealsListener = func() bool { return false }
	model.isElevated = func() bool { return false }
	model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{}, false
	}
	msg, model := probeHolds(t, model)
	if model.portHolds[rowMixed].Kind != ui.PortHoldUnknown {
		t.Fatalf("kind = %v", model.portHolds[rowMixed].Kind)
	}
	if errs := diagnosticErrors(msg); len(errs) != 1 {
		t.Fatalf("diagnostics = %v", errs)
	}
}

// TestSystemPortStatus_VisibleOccupantIgnoresElevation fails if a holder we
// can already name is replaced with Needs admin.
func TestSystemPortStatus_VisibleOccupantIgnoresElevation(t *testing.T) {
	model := portsModel(t)
	addr := boundLoopback(t)
	model.onboarding.MixedAddr = addr
	model.onboarding.ControllerAddr = ""
	model.onboarding.WebAddr = ""
	model.revealsListener = func() bool { return true }
	model.isElevated = func() bool { return false }
	model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{PID: 9736, Process: "mihomo"}, true
	}
	_, model = probeHolds(t, model)
	hold := model.portHolds[rowMixed]
	if hold.Kind != ui.PortHoldOccupied || hold.PID != 9736 {
		t.Fatalf("hold = %+v", hold)
	}
}

// TestSystemPortStatus_EmptyAddressStaysUnknown fails if a missing address is
// reported as Needs admin.
func TestSystemPortStatus_EmptyAddressStaysUnknown(t *testing.T) {
	model := portsModel(t)
	model.onboarding.MixedAddr = ""
	model.onboarding.ControllerAddr = ""
	model.onboarding.WebAddr = ""
	model.revealsListener = func() bool { return true }
	model.isElevated = func() bool { return false }
	msg, model := probeHolds(t, model)
	if model.portHolds[rowMixed].Kind != ui.PortHoldUnknown {
		t.Fatalf("kind = %v", model.portHolds[rowMixed].Kind)
	}
	if errs := diagnosticErrors(msg); len(errs) != 0 {
		t.Fatalf("diagnostics = %v", errs)
	}
}

// TestSystemPortStatus_InvalidEndpointStaysUnknown fails if a bad address is
// relabeled Needs admin or its probe error is discarded.
func TestSystemPortStatus_InvalidEndpointStaysUnknown(t *testing.T) {
	model := portsModel(t)
	model.onboarding.MixedAddr = "token=fixture-invalid-endpoint"
	model.onboarding.ControllerAddr = ""
	model.onboarding.WebAddr = ""
	model.revealsListener = func() bool { return true }
	model.isElevated = func() bool { return false }
	msg, model := probeHolds(t, model)
	if model.portHolds[rowMixed].Kind != ui.PortHoldUnknown {
		t.Fatalf("kind = %v", model.portHolds[rowMixed].Kind)
	}
	errs := diagnosticErrors(msg)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "token=fixture-invalid-endpoint") {
		t.Fatalf("diagnostics = %v", errs)
	}
}

// TestSystemPortStatus_ListenMissWithoutAddrInUseStaysUnknown fails if every
// failed bind, including an injected miss with no error, becomes Needs admin.
func TestSystemPortStatus_ListenMissWithoutAddrInUseStaysUnknown(t *testing.T) {
	model := portsModel(t)
	model.revealsListener = func() bool { return true }
	model.isElevated = func() bool { return false }
	model.listenFree = func(string) bool { return false }
	model.lookupOccupant = func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{}, false
	}
	_, model = probeHolds(t, model)
	if model.portHolds[rowMixed].Kind != ui.PortHoldUnknown {
		t.Fatalf("kind = %v", model.portHolds[rowMixed].Kind)
	}
}

func boundLoopback(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

func probeHolds(t *testing.T, model *Model) (tea.Msg, *Model) {
	t.Helper()
	msg := model.probePortHolds()()
	updated, _ := model.Update(msg)
	next, ok := updated.(*Model)
	if !ok {
		t.Fatalf("page update type %T", updated)
	}
	return msg, next
}

func diagnosticErrors(msg tea.Msg) []error {
	source, ok := msg.(interface{ DiagnosticErrors() []error })
	if !ok {
		return nil
	}
	return source.DiagnosticErrors()
}
