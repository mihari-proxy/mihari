package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestRenderControlStrip_HighlightsActiveWhenContentFocused(t *testing.T) {
	theme := DefaultTheme()
	parts := []string{"[A]", "[B]", "[C]"}

	unfocused := RenderControlStrip(theme, parts, 1, false, "  ")
	if unfocused != "[A]  [B]  [C]" {
		t.Fatalf("rail-focused strip should be plain: %q", unfocused)
	}
	if strings.Contains(unfocused, "\x1b[") {
		t.Fatalf("rail-focused strip should not emit ANSI: %q", unfocused)
	}

	focused := RenderControlStrip(theme, parts, 1, true, "  ")
	if focused == unfocused {
		t.Fatal("content-focused strip should style the active chip")
	}
	if !strings.Contains(focused, "\x1b[") {
		t.Fatalf("content-focused strip should emit ANSI: %q", focused)
	}
	// Active control focus is a literal light surface: ordinary text becomes
	// black while the focused cell gets a white background.
	wantActive := lipgloss.NewStyle().
		Foreground(lipgloss.Color("0")).
		Background(lipgloss.Color("15")).
		Render("[B]")
	if !strings.Contains(focused, wantActive) {
		t.Fatalf("active chip missing black-on-white focus style\ngot=%q\nwant substring=%q", focused, wantActive)
	}
	// Inactive chips are muted.
	wantMuted := theme.Muted.Render("[A]")
	if !strings.Contains(focused, wantMuted) {
		t.Fatalf("inactive chip missing Muted style\ngot=%q\nwant substring=%q", focused, wantMuted)
	}
}

func TestRenderControlStrip_ActiveChipPreservesSemanticForeground(t *testing.T) {
	theme := DefaultTheme()
	semantic := theme.Warning.Render("WARN")
	part := "Level: " + semantic

	got := RenderControlStrip(theme, []string{part}, 0, true, "  ")
	if !strings.Contains(got, semantic) {
		t.Fatalf("active chip changed semantic foreground\ngot=%q\nwant semantic span=%q", got, semantic)
	}
	want := lipgloss.NewStyle().
		Foreground(lipgloss.Color("0")).
		Background(lipgloss.Color("15")).
		Render(part)
	if got != want {
		t.Fatalf("active chip should put the complete mixed-color value on the focus surface\ngot=%q\nwant=%q", got, want)
	}
}

func TestRenderHeaderCell_FocusStates(t *testing.T) {
	theme := DefaultTheme()
	plain := RenderHeaderCell(theme, "Host", false, true)
	if plain != "Host" {
		t.Fatalf("unfocused header=%q", plain)
	}
	rail := RenderHeaderCell(theme, "Host", true, false)
	if !strings.HasPrefix(rail, FocusMarker) || strings.Contains(rail, "\x1b[") {
		t.Fatalf("rail-focused header should be marker-only: %q", rail)
	}
	content := RenderHeaderCell(theme, "Host", true, true)
	if content != controlActiveChip(theme).Render("Host") {
		t.Fatalf("content-focused header=%q", content)
	}
}
