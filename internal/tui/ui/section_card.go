package ui

import (
	"fmt"
	"image/color"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

// FullSectionInner returns the content-box width for a full-width bordered
// section inside a page of pageWidth columns. Matches Overview fullCardInner:
// leave room for root Content horizontal padding (2) and the border (2).
func FullSectionInner(pageWidth int) int {
	return max(20, pageWidth-4)
}

// HalfSectionInner returns the content-box width for one of two side-by-side
// bordered sections. Matches Overview halfCardInner.
func HalfSectionInner(pageWidth int) int {
	return max(10, (pageWidth-6)/2)
}

// SectionTextWidth is the printable body width inside a section of contentWidth
// (contentWidth includes 1-col horizontal padding on each side).
func SectionTextWidth(contentWidth int) int {
	if contentWidth < 8 {
		contentWidth = 8
	}
	return max(1, contentWidth-2)
}

// FormatProxyGroupTitle builds "Name · TYPE · N" for Proxies group sections.
func FormatProxyGroupTitle(name, typ string, n int) string {
	display := DisplayProxyName(name)
	return fmt.Sprintf("%s · %s · %d", display, strings.ToUpper(strings.TrimSpace(typ)), n)
}

// FormatConnectionsTitle builds "Connections · N active|closed".
func FormatConnectionsTitle(active bool, n int) string {
	if active {
		return fmt.Sprintf("Connections · %d active", n)
	}
	return fmt.Sprintf("Connections · %d closed", n)
}

// FormatRulesTitle builds "Rules · N" or "Providers · N".
func FormatRulesTitle(rulesView bool, n int) string {
	if rulesView {
		return fmt.Sprintf("Rules · %d", n)
	}
	return fmt.Sprintf("Providers · %d", n)
}

// FormatSubscriptionsTitle builds "Subscriptions · N".
func FormatSubscriptionsTitle(n int) string {
	return fmt.Sprintf("Subscriptions · %d", n)
}

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
	return renderBorderedSectionEx(theme, title, body, contentWidth, theme.ColorSurfaceBorder, theme.ColorAccent)
}

// RenderBorderedSectionWithBorder is RenderBorderedSection with an explicit border color
// (e.g. ColorAccent for a focused Web GUI panel). The title keeps the default
// accent foreground.
func RenderBorderedSectionWithBorder(theme Theme, title, body string, contentWidth int, borderColor color.Color) string {
	return renderBorderedSectionEx(theme, title, body, contentWidth, borderColor, theme.ColorAccent)
}

// RenderBorderedSectionColored draws a bordered section whose border and title
// share one semantic color, so the card reads as a single cohesive unit. Use it
// to partition a page into color-coded regions (e.g. the System page: info,
// core, OS service, network). Pass the same color for both to tint the card.
func RenderBorderedSectionColored(theme Theme, title, body string, contentWidth int, borderColor, titleColor color.Color) string {
	return renderBorderedSectionEx(theme, title, body, contentWidth, borderColor, titleColor)
}

// renderBorderedSectionEx is the shared painter behind the RenderBorderedSection
// family. borderColor tints the box frame; titleColor tints the embedded title.
func renderBorderedSectionEx(theme Theme, title, body string, contentWidth int, borderColor, titleColor color.Color) string {
	if contentWidth < 8 {
		contentWidth = 8
	}
	outer := contentWidth + 2
	pad := 1
	textWidth := max(1, contentWidth-pad*2)

	borderFG := lipgloss.NewStyle().Foreground(borderColor)
	titleStyled := lipgloss.NewStyle().Bold(true).Foreground(titleColor).Render(strings.TrimSpace(title))

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
