package ui

import (
	"strings"
	"testing"
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
	// Active chip should match ControlActive colors (bold + accent).
	wantActive := controlActiveChip(theme).Render("[B]")
	if !strings.Contains(focused, wantActive) {
		t.Fatalf("active chip missing ControlActive style\ngot=%q\nwant substring=%q", focused, wantActive)
	}
	// Inactive chips are muted.
	wantMuted := theme.Muted.Render("[A]")
	if !strings.Contains(focused, wantMuted) {
		t.Fatalf("inactive chip missing Muted style\ngot=%q\nwant substring=%q", focused, wantMuted)
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
