package ui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

// warningCalloutStyle is orange-on-white notice chrome. The fill matches the
// existing light surface (ANSI 15 / controlFocusSurface); the foreground stays
// ColorWarning so the callout reads as a warning, not a focus chip.
func warningCalloutStyle(theme Theme) lipgloss.Style {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.ColorWarning).
		Background(lipgloss.Color("15"))
}

// RenderWarningCallout emphasizes notice copy with orange text on a white
// surface. width is the wrap budget in terminal cells; values < 1 skip wrapping.
func RenderWarningCallout(theme Theme, text string, width int) string {
	style := warningCalloutStyle(theme)
	if width < 1 || lipgloss.Width(text) <= width {
		return style.Render(text)
	}
	var lines []string
	for _, line := range strings.Split(lipgloss.NewStyle().Width(width).Render(text), "\n") {
		lines = append(lines, style.Render(line))
	}
	return strings.Join(lines, "\n")
}
