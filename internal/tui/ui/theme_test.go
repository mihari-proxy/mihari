package ui

import (
	"image/color"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestDefaultTheme_HasSemanticRoles(t *testing.T) {
	theme := DefaultTheme()

	// Token colors match the status-shell palette (§3 Theme Tokens).
	assertColor(t, "ColorAccent", theme.ColorAccent, lipgloss.Color("63"))
	assertColor(t, "ColorSuccess", theme.ColorSuccess, lipgloss.Color("78"))
	assertColor(t, "ColorWarning", theme.ColorWarning, lipgloss.Color("214"))
	assertColor(t, "ColorDanger", theme.ColorDanger, lipgloss.Color("203"))
	assertColor(t, "ColorInfo", theme.ColorInfo, lipgloss.Color("75"))
	assertColor(t, "ColorSurfaceBorder", theme.ColorSurfaceBorder, lipgloss.Color("240"))
	assertColor(t, "ColorMuted", theme.ColorMuted, lipgloss.Color("245"))
	assertColor(t, "ColorOnSolid", theme.ColorOnSolid, lipgloss.Color("0"))

	// Semantic styles are wired to their tokens.
	assertStyleFG(t, "Success", theme.Success, theme.ColorSuccess)
	assertStyleFG(t, "Warning", theme.Warning, theme.ColorWarning)
	assertStyleFG(t, "Danger", theme.Danger, theme.ColorDanger)
	assertStyleFG(t, "Info", theme.Info, theme.ColorInfo)
	assertStyleFG(t, "DelayGood", theme.DelayGood, theme.ColorSuccess)
	assertStyleFG(t, "DelayMid", theme.DelayMid, theme.ColorWarning)
	assertStyleFG(t, "DelayBad", theme.DelayBad, theme.ColorDanger)
	assertStyleFG(t, "SurfaceBorder", theme.SurfaceBorder, theme.ColorSurfaceBorder)
	assertColor(t, "Dialog.BorderForeground", theme.Dialog.GetBorderTopForeground(), theme.ColorSurfaceBorder)

	// Control / table chrome present.
	assertStyleFG(t, "Control", theme.Control, theme.ColorMuted)
	assertStyleFG(t, "ControlActive", theme.ControlActive, theme.ColorAccent)
	if !theme.ControlActive.GetBold() {
		t.Fatal("ControlActive should be bold")
	}
	if !theme.TableHeader.GetBold() {
		t.Fatal("TableHeader should be bold")
	}
	if isNoColor(theme.TableHeader.GetForeground()) {
		t.Fatal("TableHeader should set a foreground color")
	}

	// Existing accent wiring preserved.
	assertStyleFG(t, "RailSelected", theme.RailSelected, theme.ColorAccent)
	assertStyleFG(t, "Title", theme.Title, theme.ColorAccent)
	assertStyleFG(t, "RowSelected", theme.RowSelected, theme.ColorAccent)

	// RailCurrent is muted (no underline requirement).
	assertStyleFG(t, "RailCurrent", theme.RailCurrent, theme.ColorMuted)
	if theme.RailCurrent.GetUnderline() {
		t.Fatal("RailCurrent should not use underline; muted foreground is enough")
	}

	// RowFocus is distinct from business RowSelected.
	if theme.RowFocus.GetReverse() == theme.RowSelected.GetReverse() &&
		colorsEqual(theme.RowFocus.GetForeground(), theme.RowSelected.GetForeground()) &&
		theme.RowFocus.GetBold() == theme.RowSelected.GetBold() {
		t.Fatal("RowFocus must be visually distinct from RowSelected")
	}
	if !theme.RowFocus.GetReverse() && isNoColor(theme.RowFocus.GetForeground()) && !theme.RowFocus.GetBold() {
		t.Fatal("RowFocus should apply reverse, foreground, or bold")
	}
}

func assertColor(t *testing.T, name string, got, want color.Color) {
	t.Helper()
	if !colorsEqual(got, want) {
		t.Fatalf("%s: got %#v want %#v", name, got, want)
	}
}

func assertStyleFG(t *testing.T, name string, style lipgloss.Style, want color.Color) {
	t.Helper()
	got := style.GetForeground()
	if !colorsEqual(got, want) {
		t.Fatalf("%s foreground: got %#v want %#v", name, got, want)
	}
}

func colorsEqual(a, b color.Color) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if _, ok := a.(lipgloss.NoColor); ok {
		_, okb := b.(lipgloss.NoColor)
		return okb
	}
	if _, ok := b.(lipgloss.NoColor); ok {
		return false
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func isNoColor(c color.Color) bool {
	if c == nil {
		return true
	}
	_, ok := c.(lipgloss.NoColor)
	return ok
}
