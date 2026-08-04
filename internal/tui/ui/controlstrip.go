package ui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

// RenderControlStrip joins navigable control chips with sep.
// When contentFocused is true, activeIndex uses the ControlActive look (bold accent,
// no padding so multi-chip layout stays stable); other chips use Muted.
// When contentFocused is false, chips stay plain so the rail can own focus chrome.
func RenderControlStrip(theme Theme, parts []string, activeIndex int, contentFocused bool, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	styled := make([]string, len(parts))
	for index, part := range parts {
		switch {
		case contentFocused && index == activeIndex:
			styled[index] = controlActiveChip(theme).Render(part)
		case contentFocused:
			styled[index] = theme.Muted.Render(part)
		default:
			styled[index] = part
		}
	}
	return strings.Join(styled, sep)
}

// controlActiveChip matches ControlActive colors without horizontal padding so
// chips can be joined with an explicit separator.
func controlActiveChip(theme Theme) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(theme.ColorAccent)
}

// RenderHeaderCell styles a table header cell under keyboard focus.
// Unfocused headers stay plain so table width clipping remains predictable.
func RenderHeaderCell(theme Theme, label string, focused, contentFocused bool) string {
	switch {
	case focused && contentFocused:
		return controlActiveChip(theme).Render(label)
	case focused:
		return FocusMarker + label
	default:
		return label
	}
}
