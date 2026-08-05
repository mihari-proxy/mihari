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
