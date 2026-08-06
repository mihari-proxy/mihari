package ui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

// ApplyFocusStyle wraps every plain-text segment of s in the focus style,
// leaving inline ANSI sequences verbatim. lipgloss's Render styles a whole
// string with one SGR prefix and one reset at the end, so a single
// RowFocus.Render(line) around a line that already contains colored spans is
// broken by each span's own reset (\x1b[m clears reverse video). Wrapping each
// plain segment separately keeps the reverse highlight running between spans.
func ApplyFocusStyle(s string, focus lipgloss.Style) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	plainStart := 0
	for i := 0; i < len(s); {
		if s[i] != '\x1b' {
			i++
			continue
		}
		// Plain segment before this escape sequence.
		if seg := s[plainStart:i]; seg != "" {
			b.WriteString(focus.Render(seg))
		}
		// Copy the escape sequence verbatim (CSI consumes up to the final byte
		// in 0x40..0x7E; anything else is a single-char escape).
		start := i
		i++
		if i < len(s) && s[i] == '[' {
			i++
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7E) {
				i++
			}
			if i < len(s) {
				i++
			}
		} else if i < len(s) {
			i++
		}
		b.WriteString(s[start:i])
		plainStart = i
	}
	if plainStart < len(s) {
		b.WriteString(focus.Render(s[plainStart:]))
	}
	return b.String()
}
