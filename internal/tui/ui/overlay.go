package ui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// CenterOverlay centers a pre-sized opaque dialog over a dimmed page. The
// background is clipped to the viewport and loses its focus highlight; callers
// constrain the dialog's dimensions and handle scrolling inside it.
func CenterOverlay(theme Theme, background, dialog string, width, height int) string {
	return PlaceOverlay(theme, background, dialog, width, height, max(0, (width-lipgloss.Width(dialog))/2))
}

// PlaceOverlay pins a pre-sized opaque dialog at x over a dimmed page.
func PlaceOverlay(theme Theme, background, dialog string, width, height, x int) string {
	width, height = max(1, width), max(1, height)
	lines := strings.Split(ansi.Strip(background), "\n")
	lines = SliceLines(lines, 0, height)
	for i, line := range lines {
		lines[i] = theme.Muted.Render(PadCell(line, width, AlignLeft))
	}
	dialogWidth := lipgloss.Width(dialog)
	if dialogWidth >= width {
		x = 0
	} else {
		x = min(max(0, x), width-dialogWidth)
	}
	base := lipgloss.NewLayer(strings.Join(lines, "\n"))
	front := lipgloss.NewLayer(dialog).X(x).Y(max(0, (height-lipgloss.Height(dialog))/2)).Z(1)
	return lipgloss.NewCompositor(base, front).Render()
}
