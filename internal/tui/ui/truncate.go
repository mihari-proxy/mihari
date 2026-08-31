package ui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

// TruncateDisplay shortens value so its lipgloss display width is at most max.
// Double-width runes (CJK) count as two columns. max <= 0 returns "".
func TruncateDisplay(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= max {
		return value
	}
	const ellipsis = "…"
	ew := lipgloss.Width(ellipsis)
	if max < ew {
		return ""
	}
	budget := max - ew
	var b strings.Builder
	for _, r := range value {
		next := b.String() + string(r)
		if lipgloss.Width(next) > budget {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + ellipsis
}
