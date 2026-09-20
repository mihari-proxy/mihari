package webgui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestWebGUI_SummaryUsesAlignedRows(t *testing.T) {
	m := New(nil, []string{protocol.CapabilityWebGUI})
	m.SetStatus(sampleStatus())
	m.SetSize(100, 35)
	view := ansi.Strip(m.View())
	for _, row := range []string{"Gateway           127.0.0.1:9191", "Default panel     zashboard", "Browser sessions  3"} {
		if !strings.Contains(view, row) {
			t.Fatalf("missing summary row %q: %s", row, view)
		}
	}
}

func TestWebGUI_ProgressFollowsActions(t *testing.T) {
	for _, tc := range []struct {
		key, label, button string
		installed          bool
	}{
		{"i", "Installing", "[ Install ]", false},
		{"r", "Reinstalling", "[ Manage ▾ ]", true},
		{"u", "Updating", "[ Manage ▾ ]", true},
	} {
		for _, inner := range []int{36, 80} {
			m := New(&fakeClient{}, []string{protocol.CapabilityWebGUI})
			status := sampleStatus()
			if !tc.installed {
				status.Panels[0].InstalledBuild = ""
				status.Panels[0].Active = false
				status.ActivePanel = ""
			}
			m.SetStatus(status)
			intent := m.handleKey(tc.key)().(ui.ActionIntentMsg)
			m.Update(ui.ActionPendingMsg{Page: intent.Page, Action: intent.Action, Key: intent.Key})
			body := ansi.Strip(m.panelBody(status.Panels[0], 0, inner))
			lines := strings.Split(body, "\n")
			buttonLine, badgeLine := -1, -1
			for i, line := range lines {
				if strings.Contains(line, tc.button) {
					buttonLine = i
				}
				if strings.Contains(line, tc.label) {
					badgeLine = i
					if !strings.Contains(line, ansi.Strip(ui.SpinnerLabel(m.installClock, tc.label))) {
						t.Fatalf("split badge: %s", body)
					}
				}
			}
			if buttonLine < 0 || badgeLine < buttonLine {
				t.Fatalf("progress must follow button: %s", body)
			}
			if inner == 80 && (badgeLine != buttonLine || strings.Index(lines[badgeLine], tc.label) < strings.Index(lines[buttonLine], tc.button)) {
				t.Fatalf("wide progress must follow button inline: %s", body)
			}
			if inner == 36 && tc.installed && badgeLine != buttonLine+1 {
				t.Fatalf("narrow progress must wrap as a whole: %s", body)
			}
			if strings.Contains(lines[0], tc.label) {
				t.Fatalf("progress replaced installed state: %s", body)
			}
		}
	}
}

func TestWebGUI_UpdateAvailabilityStaysOnLatestRow(t *testing.T) {
	c := &checkingClient{fakeClient: fakeClient{status: sampleStatus()}, latest: "v9.0.0"}
	m := New(c, []string{protocol.CapabilityWebGUI})
	runCheckCommands(m, m.Load())
	intent := m.updateSelected()().(ui.ActionIntentMsg)
	m.Update(ui.ActionPendingMsg{Page: intent.Page, Action: intent.Action, Key: intent.Key})
	body := ansi.Strip(m.panelBody(m.status.Panels[0], 0, 80))
	if !strings.Contains(body, "Latest     v9.0.0 · Update available") {
		t.Fatalf("missing latest availability: %s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "Manage") && strings.Contains(line, "available") {
			t.Fatal("availability moved to actions")
		}
	}
	c.status.Panels[0].InstalledBuild = "v9.0.0"
	_, reload := m.Update(intent.Execute())
	runCheckCommands(m, reload)
	body = ansi.Strip(m.panelBody(m.status.Panels[0], 0, 80))
	if strings.Contains(body, "Updating") || strings.Contains(body, "Update available") || !strings.Contains(body, "v9.0.0 · Up to date") {
		t.Fatalf("completion did not settle: %s", body)
	}
}
