package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestCenterOverlay_OpaqueAndDimmed(t *testing.T) {
	theme := DefaultTheme()
	background := strings.Repeat(strings.Repeat("X", 80)+"\n", 24)
	dialog := RenderBorderedSectionWithBorder(theme, "Details", "\nBody\n", 30, theme.ColorAccent)
	view := CenterOverlay(theme, background, dialog, 80, 24)
	if lipgloss.Width(view) > 80 || lipgloss.Height(view) > 24 {
		t.Fatal("overlay exceeded viewport")
	}
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		left, right := strings.Index(line, "│"), strings.LastIndex(line, "│")
		if left >= 0 && right > left && strings.Contains(line[left:right], "X") {
			t.Fatal("background leaked through dialog")
		}
	}
	if !strings.Contains(view, "38;5;245") || !strings.Contains(view, "38;5;63") {
		t.Fatal("dim background / accent dialog colors missing")
	}
}
