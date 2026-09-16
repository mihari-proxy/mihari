package webgui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type menuItem struct {
	label, reason string
	run           func() tea.Cmd
}

// HelpMode exposes the management menu's keyboard context to the shell.
func (m *Model) HelpMode() string {
	if m.menuOpen {
		return ui.ModePanelMenu
	}
	return ""
}

// HelpContent keeps gateway safeguards available without crowding the page.
func (m *Model) HelpContent() string {
	s := m.status.Safeguards
	return ui.RenderHelp(m.ID(), m.HelpMode()) + "\n\n" + ui.GatewaySafeguardsTitle + ":\n" +
		strings.Join([]string{boolState("Loopback binding", s.LoopbackBound), boolState("Browser authentication", s.BrowserAuthenticated), boolState("Controller isolation", s.ControllerIsolated), boolState("Mutation coordinator", s.MutationsCoordinated)}, "\n") +
		"\nAuthentication, isolation and coordination describe gateway protections, not live health checks."
}

func (m *Model) moveAction(delta int) {
	panel, ok := m.selectedPanel()
	if !ok {
		return
	}
	count := 1
	if panel.InstalledBuild != "" {
		count = 2
	}
	m.action += delta
	if m.action >= count {
		m.selected = (m.selected + 1) % len(m.status.Panels)
		m.action = 0
	}
	if m.action < 0 {
		m.selected = (m.selected + len(m.status.Panels) - 1) % len(m.status.Panels)
		m.action = 0
		if m.status.Panels[m.selected].InstalledBuild != "" {
			m.action = 1
		}
	}
}

func (m *Model) menuItems() []menuItem {
	panel, ok := m.selectedPanel()
	if !ok {
		return nil
	}
	items := []menuItem{{label: "Update", run: m.updateSelected}, {label: "Set as default", run: m.activateSelected}, {label: "Reinstall", run: m.reinstallSelected}, {label: "Rollback", run: m.rollbackSelected}, {label: "Uninstall", run: m.uninstallSelected}}
	if panel.Active || panel.ID == m.status.ActivePanel {
		items[1].reason = "Already default"
	}
	if panel.RollbackBuild == "" {
		items[3].reason = "Unavailable"
	}
	return items
}

func (m *Model) handleMenuKey(key string) tea.Cmd {
	items := m.menuItems()
	if len(items) == 0 {
		m.menuOpen = false
		return nil
	}
	switch key {
	case "esc":
		m.menuOpen = false
	case "up", "k", "shift+tab":
		m.menuIndex = max(0, m.menuIndex-1)
	case "down", "j", "tab":
		m.menuIndex = min(len(items)-1, m.menuIndex+1)
	case "enter":
		item := items[m.menuIndex]
		if item.reason == "" {
			m.menuOpen = false
			return item.run()
		}
	}
	return nil
}

func (m *Model) renderMenu(background string, height int) string {
	panel, ok := m.selectedPanel()
	if !ok {
		return background
	}
	inner := min(48, m.layoutWidth()-8)
	var lines []string
	for i, item := range m.menuItems() {
		text := item.label
		if item.reason != "" {
			text = m.theme.Muted.Render(text + " · " + item.reason)
		} else if item.label == "Uninstall" {
			text = m.theme.Danger.Render(text)
		} else if item.label == "Reinstall" {
			text = m.theme.Warning.Render(text)
		}
		line := ui.FocusPrefix(i == m.menuIndex) + text
		if i == m.menuIndex {
			line = ui.ApplyFocusStyle(line, m.theme.RowFocus)
		}
		lines = append(lines, line)
	}
	footer := m.theme.Muted.Render("↑/↓ choose · Enter apply · Esc close")
	dialog := ui.RenderBorderedSectionWithBorder(m.theme, "Manage "+valueOr(panel.Name, panel.ID), strings.Join(lines, "\n")+"\n\n"+footer, inner, m.theme.ColorAccent)
	return ui.CenterOverlay(m.theme, background, dialog, m.layoutWidth(), height)
}
