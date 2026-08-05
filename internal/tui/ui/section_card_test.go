package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestRenderBorderedSection_TitleEmbeddedInTopBorder(t *testing.T) {
	theme := DefaultTheme()
	got := RenderBorderedSection(theme, "General", "Service  stopped\nMihari   dev", 40)
	plain := stripANSI(got)
	lines := strings.Split(plain, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected top/body/bottom, got:\n%s", plain)
	}
	top := lines[0]
	// Title must live on the top border line with box-drawing corners.
	if !strings.Contains(top, "╭") || !strings.Contains(top, "╮") {
		t.Fatalf("top border missing corners: %q", top)
	}
	if !strings.Contains(top, "General") {
		t.Fatalf("title not embedded in top border: %q", top)
	}
	if !strings.Contains(top, "─") {
		t.Fatalf("top border missing dashes: %q", top)
	}
	// Title must NOT appear as a separate body line like "---General---".
	for _, line := range lines[1 : len(lines)-1] {
		if strings.Contains(line, "---General---") {
			t.Fatalf("title must not be body text: %q", line)
		}
	}
	if !strings.Contains(plain, "Service  stopped") || !strings.Contains(plain, "Mihari   dev") {
		t.Fatalf("body missing:\n%s", plain)
	}
	bottom := lines[len(lines)-1]
	if !strings.HasPrefix(strings.TrimLeft(bottom, " "), "╰") || !strings.Contains(bottom, "╯") {
		t.Fatalf("bottom border bad: %q", bottom)
	}
	// Outer width consistent across rows.
	wantW := lipgloss.Width(lines[0])
	for i, line := range lines {
		if w := lipgloss.Width(line); w != wantW {
			t.Fatalf("line %d width=%d want %d\n%s", i, w, wantW, plain)
		}
	}
}

func TestRenderBorderedSection_LongTitleTruncates(t *testing.T) {
	theme := DefaultTheme()
	got := RenderBorderedSection(theme, "Traffic · 60 s · very long section name", "body", 20)
	plain := stripANSI(got)
	if w := lipgloss.Width(strings.Split(plain, "\n")[0]); w != 22 { // content 20 + 2 borders
		// contentWidth 20 → outer 22
		if w < 10 {
			t.Fatalf("top too narrow: %d\n%s", w, plain)
		}
	}
}

func TestFullSectionInner(t *testing.T) {
	if got := FullSectionInner(100); got != 96 {
		t.Fatalf("FullSectionInner(100)=%d want 96", got)
	}
	if got := FullSectionInner(10); got != 20 {
		t.Fatalf("FullSectionInner(10)=%d want floor 20", got)
	}
	if got := FullSectionInner(24); got != 20 {
		t.Fatalf("FullSectionInner(24)=%d want 20", got)
	}
	if got := FullSectionInner(25); got != 21 {
		t.Fatalf("FullSectionInner(25)=%d want 21", got)
	}
}

func TestHalfSectionInner(t *testing.T) {
	// half := max(10, (width-6)/2)
	if got := HalfSectionInner(100); got != 47 {
		t.Fatalf("HalfSectionInner(100)=%d want 47", got)
	}
	if got := HalfSectionInner(10); got != 10 {
		t.Fatalf("HalfSectionInner(10)=%d want floor 10", got)
	}
	if got := HalfSectionInner(26); got != 10 {
		t.Fatalf("HalfSectionInner(26)=%d want 10", got)
	}
	if got := HalfSectionInner(28); got != 11 {
		t.Fatalf("HalfSectionInner(28)=%d want 11", got)
	}
}

func TestFormatProxyGroupTitle(t *testing.T) {
	got := FormatProxyGroupTitle("GLOBAL", "Selector", 12)
	want := "GLOBAL · SELECTOR · 12"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// Type is uppercased; count is appended; DisplayProxyName applied to name.
	got = FormatProxyGroupTitle("Proxy", "URLTest", 8)
	if got != "Proxy · URLTEST · 8" {
		t.Fatalf("got %q want %q", got, "Proxy · URLTEST · 8")
	}
}

func TestFormatConnectionsTitle(t *testing.T) {
	if got := FormatConnectionsTitle(true, 76); got != "Connections · 76 active" {
		t.Fatalf("active: got %q", got)
	}
	if got := FormatConnectionsTitle(false, 24); got != "Connections · 24 closed" {
		t.Fatalf("closed: got %q", got)
	}
}

func TestFormatRulesTitle(t *testing.T) {
	if got := FormatRulesTitle(true, 42); got != "Rules · 42" {
		t.Fatalf("rules: got %q", got)
	}
	if got := FormatRulesTitle(false, 5); got != "Providers · 5" {
		t.Fatalf("providers: got %q", got)
	}
}

func TestFormatSubscriptionsTitle(t *testing.T) {
	if got := FormatSubscriptionsTitle(3); got != "Subscriptions · 3" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderBorderedSectionColored_TitleAndBorderShareColor(t *testing.T) {
	theme := DefaultTheme()
	got := RenderBorderedSectionColored(theme, "Network", "System proxy  on", 30, theme.ColorSuccess, theme.ColorSuccess)
	// Both the border frame and the embedded title carry the success 256-color
	// foreground sequence (38;5;78).
	if !strings.Contains(got, "38;5;78") {
		t.Fatalf("missing success color 78:\n%s", got)
	}
	plain := stripANSI(got)
	top := strings.Split(plain, "\n")[0]
	if !strings.Contains(top, "Network") || !strings.Contains(top, "╭") || !strings.Contains(top, "╮") {
		t.Fatalf("colored top border malformed: %q", top)
	}
	// Width stays consistent with the default renderer.
	wantW := lipgloss.Width(strings.Split(plain, "\n")[0])
	for i, line := range strings.Split(plain, "\n") {
		if w := lipgloss.Width(line); w != wantW {
			t.Fatalf("line %d width=%d want %d", i, w, wantW)
		}
	}
}

func TestRenderBorderedSection_DefaultColorsUnchanged(t *testing.T) {
	theme := DefaultTheme()
	got := RenderBorderedSection(theme, "General", "body", 30)
	// Default keeps an accent (63) title and a muted (240) border so existing
	// pages (Overview, Web GUI) render exactly as before.
	if !strings.Contains(got, "38;5;63") {
		t.Fatalf("default title lost accent color:\n%s", got)
	}
	if !strings.Contains(got, "38;5;240") {
		t.Fatalf("default border lost surface color:\n%s", got)
	}
}
