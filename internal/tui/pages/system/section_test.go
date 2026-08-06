package system

import (
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestView_SectionGroups(t *testing.T) {
	model := New(nil, func() string { return "op" })
	model.SetSize(100, 40)
	model.SetSnapshot(
		protocol.Status{DaemonVersion: "v0.4.0", Health: "ok", Capabilities: []string{protocol.CapabilityCore}},
		protocol.CoreStatus{Status: "running", Version: "v1.19.0"},
	)
	view := model.View()
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Fatalf("missing section borders:\n%s", view)
	}
	// Daemon now absorbs the endpoints and Run Setup rows; the standalone
	// Maintenance and Local endpoints cards are gone.
	for _, title := range []string{ui.DaemonSectionTitle, ui.CoreSectionTitle, ui.SystemServiceSectionTitle} {
		if !strings.Contains(view, title) {
			t.Fatalf("missing section title %q:\n%s", title, view)
		}
	}
	if strings.Contains(view, ui.MaintenanceSectionTitle) {
		t.Fatalf("Maintenance section should be merged into Daemon:\n%s", view)
	}
	if !strings.Contains(view, ui.RunSetupLabel) || !strings.Contains(view, ui.LocalEndpointsLabel) {
		t.Fatalf("merged Daemon rows missing labels:\n%s", view)
	}
	if !strings.Contains(view, "v0.4.0") || !strings.Contains(view, "v1.19.0") {
		t.Fatalf("status values missing:\n%s", view)
	}
	// Borders are globally constant: every section uses the surface border
	// (240) with an accent (63) title. The old per-section palette
	// (info 75 / warning 214) must be gone — color now means status, not region.
	for _, want := range []string{"38;5;240", "38;5;63"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing constant section color %q:\n%s", want, view)
		}
	}
	for _, gone := range []string{"38;5;75", "38;5;214"} {
		if strings.Contains(view, gone) {
			t.Fatalf("section color %q should be gone (constant borders):\n%s", gone, view)
		}
	}
}
