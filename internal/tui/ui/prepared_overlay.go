package ui

import (
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

// PreparedOverlay owns the dimmed snapshot behind a modal dialog.
type PreparedOverlay struct {
	lines                   []string
	width, height           int
	frontWidth, frontHeight int
	left, right             []string
}

// PrepareOverlay captures a background for repeated modal renders.
func PrepareOverlay(theme Theme, background string, width, height int) *PreparedOverlay {
	width, height = max(1, width), max(1, height)
	source := strings.Split(ansi.Strip(background), "\n")
	lines := make([]string, height)
	for i := range lines {
		line := ""
		if i < len(source) {
			line = source[i]
		}
		lines[i] = theme.Muted.Render(PadCell(line, width, AlignLeft))
	}
	return &PreparedOverlay{lines: lines, width: width, height: height}
}

// Center places an opaque dialog over the snapshot.
func (p *PreparedOverlay) Center(dialog string) string {
	rows := append([]string{}, p.lines...)
	front := strings.Split(dialog, "\n")
	width := min(p.width, lipgloss.Width(dialog))
	x, y := max(0, (p.width-width)/2), max(0, (p.height-len(front))/2)
	if p.frontWidth != width || p.frontHeight != len(front) {
		p.frontWidth, p.frontHeight = width, len(front)
		p.left, p.right = make([]string, len(front)), make([]string, len(front))
		for i := range front {
			if y+i >= p.height {
				break
			}
			p.left[i] = PadCell(ansi.Cut(p.lines[y+i], 0, x), x, AlignLeft)
			rightStart := x + width
			boundary := rightStart
			for boundary < p.width && ansi.StringWidth(ansi.Cut(p.lines[y+i], 0, boundary)) < boundary {
				boundary++
			}
			p.right[i] = strings.Repeat(" ", boundary-rightStart) + ansi.Cut(p.lines[y+i], boundary, p.width)
		}
	}
	for i, line := range front {
		if y+i >= p.height {
			break
		}
		middle := PadCell(ansi.Cut(line, 0, width), width, AlignLeft)
		rows[y+i] = p.left[i] + middle + p.right[i]
	}
	return strings.Join(rows, "\n")
}
