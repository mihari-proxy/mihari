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
	for _, title := range []string{ui.DaemonSectionTitle, ui.CoreSectionTitle, ui.MaintenanceSectionTitle} {
		if !strings.Contains(view, title) {
			t.Fatalf("missing section title %q:\n%s", title, view)
		}
	}
	if !strings.Contains(view, "v0.4.0") || !strings.Contains(view, "v1.19.0") {
		t.Fatalf("status values missing:\n%s", view)
	}
}
