package ui

import (
	lipgloss "charm.land/lipgloss/v2"
)

// RenderSearchBar draws the independent search strip used between a control strip
// and a table header (Connections, Rules, Logs).
// Empty query shows "/ " + placeholder; non-empty shows "/ " + query.
// Focused styling uses ControlActive; unfocused uses Muted. When width > 0 the
// line is clamped with MaxWidth.
func RenderSearchBar(theme Theme, query, placeholder string, focused bool, width int) string {
	text := query
	if text == "" {
		text = placeholder
	}
	line := "/ " + text

	var styled string
	if focused {
		styled = theme.ControlActive.Render(line)
	} else {
		styled = theme.Muted.Render(line)
	}

	if width <= 0 {
		return styled
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(styled)
}
