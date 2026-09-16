package webgui

import (
	"fmt"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// View renders the Web GUI page.
func (m *Model) View() string {
	inner := ui.FullSectionInner(m.layoutWidth())
	if !m.available {
		body := m.theme.Muted.Render(ui.UnavailableTitle + ": " + ui.WebGUILifecycleUnavailable)
		if m.lastError != "" {
			body += "\n" + m.lastError
		}
		return ui.RenderBorderedSection(m.theme, ui.WebGUITitle, body, inner)
	}
	textW := ui.SectionTextWidth(inner)
	height := m.height
	if height <= 0 {
		height = 40
	}
	active := valueOr(m.status.ActivePanel, "None")
	summary := m.theme.Info.Render(valueOr(m.status.GatewayAddr, ui.MissingValue)) + "\n" +
		fmt.Sprintf("%s: %s · %s: %d", ui.ActivePanelLabel, active, ui.BrowserSessionsLabel, m.status.BrowserSessions)
	summaryLines := strings.Split(summary, "\n")
	for i, line := range summaryLines {
		summaryLines[i] = ui.TruncateVisible(line, textW)
	}
	summary = strings.Join(summaryLines, "\n")
	header := ui.RenderBorderedSection(m.theme, ui.WebGUITitle, summary, inner)
	hint := strings.ReplaceAll(ui.WebGUICacheRefreshHint, "Ctrl+Shift+R", m.theme.Warning.Bold(true).Render("Ctrl+Shift+R"))
	warning := ui.RenderBorderedSectionColored(m.theme, "! Browser refresh", m.theme.Warning.Render(ansi.Wrap(hint, textW, "")), inner, m.theme.ColorWarning, m.theme.ColorWarning)
	pinned := header + "\n" + warning
	if m.lastError != "" {
		pinned += "\n" + m.theme.Danger.Render(ui.TruncateVisible(strings.Join(strings.Fields(m.lastError), " "), textW))
	}
	if m.toast != "" {
		pinned += "\n" + m.theme.Danger.Render(ui.TruncateVisible(strings.Join(strings.Fields(m.toast), " "), textW))
	}
	if lipgloss.Height(pinned) >= height {
		compact := m.theme.Warning.Bold(true).Render(ui.TruncateVisible("! Ctrl+Shift+R · Refresh Web GUI", textW)) + "\n" +
			m.theme.Warning.Render(ansi.Wrap(hint, textW, "")) + "\n" + m.theme.Muted.Render("Resize terminal to view panels")
		return strings.Join(ui.SliceLines(strings.Split(compact, "\n"), 0, height), "\n")
	}
	lines, start, end := m.panelLines()
	room := max(1, height-lipgloss.Height(pinned)-1)
	offset := ui.EnsureLineVisible(0, room, len(lines), start, end)
	// For a card taller than the remaining viewport, keep its actions visible.
	if end-start > room {
		offset = max(0, end-room)
	}
	view := pinned + "\n" + strings.Join(ui.SliceLines(lines, offset, room), "\n")
	if m.menuOpen {
		view = m.renderMenu(view, height)
	}
	return view
}

func (m *Model) panelLines() (lines []string, start, end int) {
	if len(m.status.Panels) == 0 {
		return strings.Split(ui.RenderBorderedSection(m.theme, "Panels", m.theme.Muted.Render(ui.NoWebGUIPanels), ui.FullSectionInner(m.layoutWidth())), "\n"), 0, 1
	}
	columns := 1
	inner := ui.FullSectionInner(m.layoutWidth())
	if m.layoutWidth() >= 90 {
		columns = 2
		// Reserve the two-cell gap as well as the shell's horizontal padding.
		inner = ui.HalfSectionInner(m.layoutWidth() - 2)
	}
	for i := 0; i < len(m.status.Panels); i += columns {
		body := m.panelBody(m.status.Panels[i], i, inner)
		var block string
		if columns == 2 && i+1 < len(m.status.Panels) {
			right := m.panelBody(m.status.Panels[i+1], i+1, inner)
			body, right = ui.EqualizeLineCount(body, right)
			block = lipgloss.JoinHorizontal(lipgloss.Top, m.panelCard(i, body, inner), "  ", m.panelCard(i+1, right, inner))
		} else {
			block = m.panelCard(i, body, inner)
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		first := len(lines)
		lines = append(lines, strings.Split(block, "\n")...)
		if m.selected >= i && m.selected < min(i+columns, len(m.status.Panels)) {
			start, end = first, len(lines)
		}
	}
	return
}

func (m *Model) panelCard(index int, body string, inner int) string {
	panel := m.status.Panels[index]
	border := m.theme.ColorSurfaceBorder
	if index == m.selected && m.contentFocused {
		border = m.theme.ColorAccent
	}
	return ui.RenderBorderedSectionWithBorder(m.theme, valueOr(panel.Name, panel.ID), body, inner, border)
}

func (m *Model) panelBody(panel protocol.PanelStatus, index, inner int) string {
	installed := panel.InstalledBuild != ""
	state := m.theme.Muted.Render("○ Not installed")
	if installed {
		state = m.theme.Success.Render("● Installed")
	}
	if panel.Health != "" && !strings.EqualFold(panel.Health, "healthy") && !strings.EqualFold(panel.Health, "ok") && panel.Health != "installed" && panel.Health != "missing" {
		state = m.theme.Warning.Render("! " + panel.Health)
	}
	if m.installing["panel:install:"+panel.ID] || m.installing["panel:reinstall:"+panel.ID] {
		state = ui.RenderStatusChip(m.theme, ui.StatusChipPending, ui.SpinnerLabel(m.installClock, "Installing"))
	}
	if panel.Active || panel.ID == m.status.ActivePanel {
		state += "  " + m.theme.Success.Bold(true).Render("DEFAULT")
	}
	primary := "Open"
	if !installed {
		primary = "Install"
	}
	action := func(label string, position int) string {
		text := "[ " + label + " ]"
		if index == m.selected && m.action == position {
			if m.contentFocused {
				return ui.FocusMarker + m.theme.RowFocus.Render(text)
			}
			return ui.FocusMarker + text
		}
		return "  " + text
	}
	actions := action(primary, 0)
	if installed {
		actions += "  " + action("Manage ▾", 1)
	}
	field := func(label, value string) string { return m.theme.Muted.Render(fmt.Sprintf("%-11s", label)) + value }
	body := strings.Join([]string{state, "", field("Installed", valueOr(panel.InstalledBuild, ui.MissingValue)), field("Latest", valueOr(panel.LatestBuild, ui.UnknownLabel)), field("Rollback", valueOr(panel.RollbackBuild, ui.MissingValue)), "", actions}, "\n")
	return ansi.Wrap(body, ui.SectionTextWidth(inner), "")
}
