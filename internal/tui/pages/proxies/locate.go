package proxies

import (
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// locateTarget uses retained candidates too: locating never authorizes a mutation.
func locateTarget(group protocol.ProxyGroup) string {
	if group.Now == "" || nodeIndex(group.Nodes, group.Now) < 0 {
		return ""
	}
	return group.Now
}

// locateCurrent resolves the selected card at activation time without submitting a selection.
func (m *Model) locateCurrent() {
	index := m.groupIndex(m.focus.Group)
	if index < 0 {
		return
	}
	target := locateTarget(m.groups[index])
	if target == "" {
		return
	}
	m.expanded[m.focus.Group] = true
	m.focus = FocusID{Group: m.focus.Group, Node: target}
	m.ensureFocusVisible()
}

// renderGroupHeader reserves space for Locate before styling or truncating selection text.
func (m *Model) renderGroupHeader(group protocol.ProxyGroup, width int, focused bool) string {
	marker := "▸"
	if m.expanded[group.Name] {
		marker = "▾"
	}
	focus := "  "
	headerFocused := focused && !m.focus.Locate && m.contentFocused
	buttonFocused := focused && m.focus.Locate && m.contentFocused
	if headerFocused {
		focus = ui.FocusMarker
	}
	prefix := focus + marker + "  "
	label := "Now: "
	if m.loadError != "" {
		label = "Last selected: "
	}
	button := "Locate Selected"
	// Reserve the action before truncating names, including in retained snapshots.
	available := max(0, width-lipgloss.Width(prefix)-2-lipgloss.Width(button))
	label = ui.TruncateVisible(label, max(0, available-1))
	name := ui.DisplayProxyName(group.Now)
	if name == "" {
		name = ui.MissingValue
	}
	name = ui.TruncateVisible(name, max(0, available-lipgloss.Width(label)))
	if group.Now != "" {
		name = m.theme.Success.Render(name)
	}
	header := prefix + label + name
	if headerFocused {
		header = ui.ApplyFocusStyle(header, m.theme.RowFocus)
	}
	if locateTarget(group) == "" {
		button = m.theme.Muted.Render(button)
	}
	if buttonFocused {
		button = ui.ApplyFocusStyle(button, m.theme.RowFocus)
	}
	return header + "  " + button
}
