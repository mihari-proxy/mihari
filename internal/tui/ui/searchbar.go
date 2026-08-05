package ui

import (
	"unicode/utf8"

	lipgloss "charm.land/lipgloss/v2"
)

// RenderSearchBar draws the independent search strip used between a control strip
// and a table header (Connections, Rules, Logs).
// Empty query shows "/ " + placeholder; non-empty shows "/ " + query.
// When focused, a reverse-video cursor is drawn at cursor (rune offset in query;
// empty query places the cursor at the start of the placeholder).
// Focused styling matches ControlActive (bold accent); unfocused uses Muted.
// When width > 0 the line is clamped with MaxWidth.
func RenderSearchBar(theme Theme, query, placeholder string, focused bool, cursor, width int) string {
	text := query
	bodyCursor := cursor
	if text == "" {
		text = placeholder
		bodyCursor = 0
	} else {
		bodyCursor = ClampSearchCursor(query, cursor)
	}

	var styled string
	if focused {
		// Compose bold-accent segments so reverse cursor works without
		// re-styling an already-colored string. Match ControlActive padding.
		active := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorAccent)
		inner := active.Render("/ ") + renderCursorText(text, bodyCursor, active)
		styled = lipgloss.NewStyle().Padding(0, 1).Render(inner)
	} else {
		styled = theme.Muted.Render("/ " + text)
	}

	if width <= 0 {
		return styled
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(styled)
}

// renderCursorText inserts a reverse-video cursor into text at the given rune offset.
// When cursor is at the end, a reverse space is appended. Non-cursor segments use active.
func renderCursorText(text string, cursor int, active lipgloss.Style) string {
	runes := []rune(text)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	rev := active.Reverse(true)
	if cursor < len(runes) {
		return active.Render(string(runes[:cursor])) + rev.Render(string(runes[cursor])) + active.Render(string(runes[cursor+1:]))
	}
	return active.Render(text) + rev.Render(" ")
}

// ClampSearchCursor returns cursor clamped to the query rune length.
func ClampSearchCursor(query string, cursor int) int {
	n := utf8.RuneCountInString(query)
	if cursor < 0 {
		return 0
	}
	if cursor > n {
		return n
	}
	return cursor
}
