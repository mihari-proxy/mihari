package ui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

// RenderControlStrip joins navigable control chips with sep.
// When contentFocused is true, activeIndex uses a black-on-white focus surface
// (no padding so multi-chip layout stays stable); other chips use Muted.
// When contentFocused is false, chips stay plain so the rail can own focus chrome.
func RenderControlStrip(theme Theme, parts []string, activeIndex int, contentFocused bool, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	styled := make([]string, len(parts))
	for index, part := range parts {
		switch {
		case contentFocused && index == activeIndex:
			styled[index] = controlFocusSurface(theme).Render(part)
		case contentFocused:
			styled[index] = theme.Muted.Render(part)
		default:
			styled[index] = part
		}
	}
	return strings.Join(styled, sep)
}

// controlFocusSurface is the explicit light keyboard-focus surface used by
// controls and search. Applying it outside an already-colored span lets that
// span keep its semantic foreground while inheriting the white background.
func controlFocusSurface(theme Theme) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(theme.ColorOnSolid).Background(lipgloss.Color("15"))
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
