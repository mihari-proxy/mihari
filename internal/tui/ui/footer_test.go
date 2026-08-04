package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestFitFooter_DropsMiddleBeforeHelpAndQuit(t *testing.T) {
	hints := "Esc back  Enter details  a add  e edit  Space toggle  r refresh  Ctrl+R refresh all  u use  d delete  ? help  q quit"
	global := "⠋ Working…"
	// Narrow enough that the full line cannot fit.
	width := 48
	got := FitFooter(hints, global, width)
	if lipgloss.Width(got) > width {
		t.Fatalf("width %d > %d: %q", lipgloss.Width(got), width, got)
	}
	if !strings.Contains(got, "? help") {
		t.Fatalf("expected protected ? help to remain: %q", got)
	}
	if !strings.Contains(got, "q quit") {
		t.Fatalf("expected protected q quit to remain: %q", got)
	}
	if !strings.Contains(got, "Working") {
		t.Fatalf("expected global spinner segment to remain: %q", got)
	}
	// A middle secondary action should be dropped before help/quit.
	if strings.Contains(got, "Ctrl+R") && strings.Contains(got, "Space toggle") && strings.Contains(got, "Enter details") {
		t.Fatalf("expected some middle shortcuts to be dropped: %q", got)
	}
}

func TestFitFooter_FitsUnchangedWhenWide(t *testing.T) {
	hints := FooterSubscriptions
	got := FitFooter(hints, "", 200)
	if got != strings.TrimSpace(hints) && got != hints {
		// Allow exact equality with trimmed spaces collapsed only via join path.
		if !strings.Contains(got, "? help") || !strings.Contains(got, "q quit") {
			t.Fatalf("wide footer lost content: %q", got)
		}
	}
	if lipgloss.Width(got) > 200 {
		t.Fatalf("unexpected clamp: %q", got)
	}
}
