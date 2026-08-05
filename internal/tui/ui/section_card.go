package ui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

// RenderBorderedSection draws a rounded card whose section title is embedded in
// the top border line (not as body text):
//
//	╭───General──────────────────────╮
//	│ Service    stopped             │
//	╰────────────────────────────────╯
//
// contentWidth is the total width of the content box (including horizontal
// padding of 1), matching the previous lipgloss Width() for these cards.
// Outer visual width is contentWidth+2 (left/right border runes).
func RenderBorderedSection(theme Theme, title, body string, contentWidth int) string {
	if contentWidth < 8 {
		contentWidth = 8
	}
	outer := contentWidth + 2
	pad := 1
	textWidth := max(1, contentWidth-pad*2)

	borderFG := lipgloss.NewStyle().Foreground(theme.ColorSurfaceBorder)
	titleStyled := theme.Title.Render(strings.TrimSpace(title))

	top := renderTopBorder(borderFG, titleStyled, outer)
	bottom := borderFG.Render("╰" + strings.Repeat("─", max(0, outer-2)) + "╯")

	var lines []string
	lines = append(lines, top)
	for _, raw := range strings.Split(body, "\n") {
		// Truncate by display width, then pad to textWidth.
		clipped := lipgloss.NewStyle().MaxWidth(textWidth).Render(raw)
		// Pad plain trailing spaces so side borders align (use visible width).
		vis := lipgloss.Width(clipped)
		if vis < textWidth {
			clipped += strings.Repeat(" ", textWidth-vis)
		}
		left := borderFG.Render("│") + strings.Repeat(" ", pad)
		right := strings.Repeat(" ", pad) + borderFG.Render("│")
		lines = append(lines, left+clipped+right)
	}
	if body == "" {
		empty := strings.Repeat(" ", textWidth)
		lines = append(lines, borderFG.Render("│")+strings.Repeat(" ", pad)+empty+strings.Repeat(" ", pad)+borderFG.Render("│"))
	}
	lines = append(lines, bottom)
	return strings.Join(lines, "\n")
}

// renderTopBorder builds ╭───Title────────╮ with title styled, border muted.
// No spaces around the title (tight ───Name─── embedding).
func renderTopBorder(borderFG lipgloss.Style, titleStyled string, outer int) string {
	const leftCap = "╭"
	const rightCap = "╮"
	const dash = "─"

	titleW := lipgloss.Width(titleStyled)
	budget := outer - 2 // inside the corners
	if titleW > budget {
		titleStyled = lipgloss.NewStyle().MaxWidth(max(1, budget)).Render(titleStyled)
		titleW = lipgloss.Width(titleStyled)
	}
	remain := budget - titleW
	if remain < 0 {
		remain = 0
	}
	// Prefer 3-dash lead when space allows: ╭───Name──…──╮
	leftDashes := 0
	switch {
	case remain == 0:
		leftDashes = 0
	case remain == 1:
		leftDashes = 1
	default:
		leftDashes = min(3, remain-1)
		if leftDashes < 1 {
			leftDashes = 1
		}
	}
	rightDashes := remain - leftDashes
	if rightDashes < 0 {
		rightDashes = 0
		leftDashes = remain
	}

	var b strings.Builder
	b.WriteString(borderFG.Render(leftCap + strings.Repeat(dash, leftDashes)))
	b.WriteString(titleStyled)
	b.WriteString(borderFG.Render(strings.Repeat(dash, rightDashes) + rightCap))
	return b.String()
}
