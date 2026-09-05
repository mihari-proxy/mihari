package system

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestRows_LoggingSectionFollowsPortsAndIsUnavailableOffline(t *testing.T) {
	model := New(nil, func() string { return "op" })
	rows := model.rows()
	ids := make([]string, len(rows))
	for index, item := range rows {
		ids[index] = item.id
	}

	portsEnd := slices.Index(ids, "port-web")
	daemon := slices.Index(ids, "daemon")
	if portsEnd < 0 || daemon < 0 {
		t.Fatalf("missing boundary rows: %v", ids)
	}
	want := []string{"log-level", "log-max-size", "log-max-files", "log-directory", "log-export"}
	if got := ids[portsEnd+1 : daemon]; !slices.Equal(got, want) {
		t.Fatalf("logging rows between ports and daemon = %v, want %v", got, want)
	}
	for _, item := range rows[portsEnd+1 : daemon-1] {
		if item.section != "Logging" || item.value != ui.UnavailableTitle {
			t.Fatalf("offline logging row = %#v", item)
		}
	}

	view := model.View()
	if !strings.Contains(view, "Logging") {
		t.Fatalf("missing Logging section:\n%s", view)
	}
	if !strings.Contains(view, ui.ExportLogsLabel) {
		t.Fatalf("missing export entry point:\n%s", view)
	}
}

func TestView_ShortHeightNavigationReachesVisibleExportLogs(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 10)
	model.FocusFirst()
	for model.focusID != rowLogExport {
		page, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		model = page.(*Model)
	}
	view := model.View()
	if !strings.Contains(view, ui.ExportLogsLabel) || !strings.Contains(view, "›") {
		t.Fatalf("focused export row is not visible:\n%s", view)
	}
	_, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("offline export row did not open")
	}
	if _, ok := cmd().(ui.OpenExportLogsMsg); !ok {
		t.Fatalf("message=%T", cmd())
	}
}

func TestView_SectionGroups(t *testing.T) {
	model := New(nil, func() string { return "op" })
	model.SetSize(100, 40)
	model.SetSnapshot(
		protocol.Status{DaemonVersion: "v0.4.0", Health: "ok", Capabilities: []string{protocol.CapabilityCore}},
		protocol.CoreStatus{Status: "running", Version: "v1.19.0"},
	)
	// This test asserts section structure and the constant border palette; take
	// it online so the daemon/core status dots use their positive (green) tone
	// instead of the caution-yellow stale tone, which would otherwise bleed
	// warning color into the palette assertion below.
	model.SetMutationsEnabled(true)
	view := model.View()
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Fatalf("missing section borders:\n%s", view)
	}
	// Daemon now absorbs the endpoint and Run Setup rows; the standalone
	// Maintenance and Local endpoints cards are gone.
	for _, title := range []string{ui.DaemonSectionTitle, ui.CoreSectionTitle, ui.SystemServiceSectionTitle} {
		if !strings.Contains(view, title) {
			t.Fatalf("missing section title %q:\n%s", title, view)
		}
	}
	if strings.Contains(view, ui.MaintenanceSectionTitle) {
		t.Fatalf("Maintenance section should be merged into Daemon:\n%s", view)
	}
	if !strings.Contains(view, ui.RunSetupLabel) || !strings.Contains(view, ui.PortsConfigSectionTitle) {
		t.Fatalf("ports/daemon rows missing labels:\n%s", view)
	}
	if !strings.Contains(view, "v0.4.0") || !strings.Contains(view, "v1.19.0") {
		t.Fatalf("status values missing:\n%s", view)
	}
	if !strings.Contains(view, ui.CoreChannelLabel) {
		t.Fatalf("missing core channel row in Core section:\n%s", view)
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
