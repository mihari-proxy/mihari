package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestRenderWarningCallout_OrangeOnWhite(t *testing.T) {
	theme := DefaultTheme()
	got := RenderWarningCallout(theme, WebGUICacheRefreshHint, 0)
	want := lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.ColorWarning).
		Background(lipgloss.Color("15")).
		Render(WebGUICacheRefreshHint)
	if got != want {
		t.Fatalf("callout style\ngot =%q\nwant=%q", got, want)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("width 0 should not wrap: %q", got)
	}
}

func TestRenderWarningCallout_WrapsAtNarrowWidth(t *testing.T) {
	theme := DefaultTheme()
	const width = 20
	if lipgloss.Width(WebGUICacheRefreshHint) <= width {
		t.Fatalf("fixture too short to wrap: cells=%d", lipgloss.Width(WebGUICacheRefreshHint))
	}
	got := RenderWarningCallout(theme, WebGUICacheRefreshHint, width)
	if !strings.Contains(got, "\n") {
		t.Fatalf("narrow callout should wrap: %q", got)
	}
	for _, want := range []string{"Ctrl+Shift+R", "白屏", "强制刷新"} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrapped callout dropped %q:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if lipgloss.Width(line) > width {
			t.Fatalf("wrapped line exceeds width %d: cells=%d line=%q", width, lipgloss.Width(line), line)
		}
	}
}
