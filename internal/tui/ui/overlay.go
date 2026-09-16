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
	width, height = max(1, width), max(1, height)
	lines := strings.Split(ansi.Strip(background), "\n")
	lines = SliceLines(lines, 0, height)
	for i, line := range lines {
		lines[i] = theme.Muted.Render(PadCell(line, width, AlignLeft))
	}
	base := lipgloss.NewLayer(strings.Join(lines, "\n"))
	front := lipgloss.NewLayer(dialog).X(max(0, (width-lipgloss.Width(dialog))/2)).Y(max(0, (height-lipgloss.Height(dialog))/2)).Z(1)
	return lipgloss.NewCompositor(base, front).Render()
}
