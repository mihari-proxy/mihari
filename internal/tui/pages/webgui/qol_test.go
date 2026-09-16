package webgui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestWebGUI_ResponsiveCardsAndProminentRefresh(t *testing.T) {
	for _, width := range []int{58, 100, 160} {
		m := New(nil, []string{protocol.CapabilityWebGUI})
		m.SetStatus(sampleStatus())
		m.SetSize(width, 28)
		view := m.View()
		plain := ansi.Strip(view)
		together := false
		for _, line := range strings.Split(plain, "\n") {
			if strings.Contains(line, "Zashboard") && strings.Contains(line, "MetaCubeXD") {
				together = true
			}
		}
		if together != (width >= 90) {
			t.Fatalf("width %d card layout: %s", width, plain)
		}
		if lipgloss.Width(view) > width || lipgloss.Height(view) > 28 {
			t.Fatalf("width %d overflow", width)
		}
		if lipgloss.Width(view) > width-2 {
			t.Fatalf("width %d leaves no room for shell padding: rendered %d", width, lipgloss.Width(view))
		}
		if !strings.Contains(view, "38;5;214") || !strings.Contains(plain, "Ctrl+Shift+R") || strings.Contains(plain, ui.GatewaySafeguardsTitle) {
			t.Fatalf("refresh emphasis / safeguards: %s", view)
		}
	}
}

func TestWebGUI_PrimaryActionAndManageMenu(t *testing.T) {
	f := &fakeClient{status: sampleStatus()}
	m := New(f, []string{protocol.CapabilityWebGUI})
	m.SetStatus(f.status)
	m.SetSize(100, 28)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || cmd().(ui.ActionIntentMsg).Action != ui.ActionOpenWebGUI {
		t.Fatal("Enter must open installed panel")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(ansi.Strip(m.View()), "Manage Zashboard") {
		t.Fatal("Manage menu did not open")
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || cmd().(ui.ActionIntentMsg).Action != ui.ActionUpdatePanel {
		t.Fatal("first management item must update via intent")
	}
	status := sampleStatus()
	status.Panels[0].InstalledBuild = ""
	status.Panels[0].Active = false
	m.SetStatus(status)
	m.FocusFirst()
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || cmd().(ui.ActionIntentMsg).Action != ui.ActionInstallPanel {
		t.Fatal("Enter must install missing panel")
	}
}

func TestWebGUI_InstalledOnlyShortcuts(t *testing.T) {
	for _, key := range []string{"space", "o"} {
		t.Run(key, func(t *testing.T) {
			f := &fakeClient{status: sampleStatus()}
			m := New(f, []string{protocol.CapabilityWebGUI})
			m.SetStatus(f.status)
			cmd := m.handleKey(key)
			if cmd == nil {
				t.Fatal("installed panel shortcut must remain available")
			}
			want := ui.ActionOpenWebGUI
			if key == "space" {
				want = ui.ActionActivatePanel
			}
			if cmd().(ui.ActionIntentMsg).Action != want {
				t.Fatal("shortcut emitted the wrong action")
			}
			f.status.Panels[0].InstalledBuild = ""
			f.status.Panels[0].Active = false
			m.SetStatus(f.status)
			if m.handleKey(key) != nil {
				t.Fatal("missing panel must not offer an installed-only action")
			}
		})
	}
}

func TestWebGUI_MenuDisabledItemsAndDangerousIntents(t *testing.T) {
	f := &fakeClient{status: sampleStatus()}
	m := New(f, []string{protocol.CapabilityWebGUI})
	m.SetStatus(f.status)
	m.SetSize(100, 28)
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || !m.menuOpen {
		t.Fatal("already-default entry must be disabled")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || cmd().(ui.ActionIntentMsg).Action != ui.ActionReinstallPanel || f.reinstalled != 0 {
		t.Fatal("reinstall must only emit its confirmation intent")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for i := 0; i < 3; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if _, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || !m.menuOpen {
		t.Fatal("missing rollback must be disabled")
	}
	if !strings.Contains(ansi.Strip(m.View()), "Unavailable") {
		t.Fatal("missing disabled explanation")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || cmd().(ui.ActionIntentMsg).Action != ui.ActionUninstallPanel || f.uninstalled != 0 {
		t.Fatal("uninstall bypassed intent")
	}
}

func TestWebGUI_ShortWindowKeepsRefreshAndSelectedActions(t *testing.T) {
	m := New(nil, []string{protocol.CapabilityWebGUI})
	m.SetStatus(sampleStatus())
	m.SetSize(58, 20)
	m.SetContentFocused(true)
	for i := 0; i < 4; i++ {
		view := m.View()
		plain := ansi.Strip(view)
		if lipgloss.Width(view) > 58 || lipgloss.Height(view) > 20 {
			t.Fatalf("viewport overflow: %s", view)
		}
		if !strings.Contains(plain, "Ctrl+Shift+R") || !strings.Contains(plain, "› [") {
			t.Fatalf("hint or focused action clipped: %s", plain)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
}

func TestWebGUI_ReorderedStatusPreservesSelectedPanel(t *testing.T) {
	m := New(nil, []string{protocol.CapabilityWebGUI})
	status := sampleStatus()
	m.SetStatus(status)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	status.Panels[0], status.Panels[1] = status.Panels[1], status.Panels[0]
	m.SetStatus(status)
	if !strings.Contains(ansi.Strip(m.View()), "Manage MetaCubeXD") {
		t.Fatal("refresh changed menu target")
	}
	status.Panels = status.Panels[1:]
	m.SetStatus(status)
	if m.menuOpen {
		t.Fatal("removed menu target still active")
	}
}

func TestWebGUI_OddCardsLongNamesAndRightFocus(t *testing.T) {
	m := New(nil, []string{protocol.CapabilityWebGUI})
	status := sampleStatus()
	status.Panels[0].InstalledBuild = strings.Repeat("long-version-", 8)
	status.Panels = append(status.Panels, protocol.PanelStatus{ID: "third", Name: "Third", Health: "missing"})
	m.SetStatus(status)
	m.SetSize(100, 40)
	m.SetContentFocused(true)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	view := m.View()
	if lipgloss.Width(view) > 100 || lipgloss.Height(view) > 40 {
		t.Fatal("long card overflow")
	}
	if !strings.Contains(ansi.Strip(view), "Third") {
		t.Fatal("odd trailing card missing")
	}
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, "› [ Open ]") && strings.Index(line, "› [ Open ]") < 45 {
			t.Fatalf("focus not in right card: %s", line)
		}
	}
	if !strings.Contains(view, m.theme.Success.Render("● Installed")) {
		t.Fatal("installed semantic color lost")
	}
}
