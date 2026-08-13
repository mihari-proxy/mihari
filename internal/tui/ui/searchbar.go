package ui

import (
	"unicode/utf8"

	lipgloss "charm.land/lipgloss/v2"
)

// RenderSearchBar draws the independent search strip used between a control strip
// and a table header (Connections, Rules, Logs).
// Empty query shows "/ " + placeholder; non-empty shows "/ " + query.
// When focused, an underlined cursor is drawn at cursor (rune offset in query;
// empty query places the cursor at the start of the placeholder).
// Focused styling uses the controls' black-on-white surface; unfocused uses Muted.
// When width > 0 the line is clamped with MaxWidth.
func RenderSearchBar(theme Theme, query, placeholder string, focused bool, cursor, width int) string {
	text := query
	var bodyCursor int
	if text == "" {
		text = placeholder
		bodyCursor = 0
	} else {
		bodyCursor = ClampSearchCursor(query, cursor)
	}

	var styled string
	if focused {
		// Render every segment on the same explicit surface. Keeping padding as
		// styled cells avoids black gaps at either end of the focused search bar.
		active := controlFocusSurface(theme)
		inner := active.Render("/ ") + renderCursorText(text, bodyCursor, active)
		styled = active.Render(" ") + inner + active.Render(" ")
	} else {
		styled = theme.Muted.Render("/ " + text)
	}

	if width <= 0 {
		return styled
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(styled)
}

// renderCursorText inserts an underlined cursor into text at the given rune offset.
// When cursor is at the end, an underlined space is appended. Non-cursor segments use active.
func renderCursorText(text string, cursor int, active lipgloss.Style) string {
	runes := []rune(text)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	cursorStyle := active.Underline(true)
	if cursor < len(runes) {
		var before, after string
		if cursor > 0 {
			before = active.Render(string(runes[:cursor]))
		}
		if cursor+1 < len(runes) {
			after = active.Render(string(runes[cursor+1:]))
		}
		return before + cursorStyle.Render(string(runes[cursor])) + after
	}
	return active.Render(text) + cursorStyle.Render(" ")
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
