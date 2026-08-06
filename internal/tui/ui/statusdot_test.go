package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestToneStyle_MapsToExistingPalette(t *testing.T) {
	theme := DefaultTheme()
	cases := []struct {
		tone StatusTone
		want lipgloss.Style
		name string
	}{
		{ToneNeutral, theme.Muted, "neutral"},
		{TonePositive, theme.Success, "positive"},
		{ToneCaution, theme.Warning, "caution"},
		{ToneNegative, theme.Danger, "negative"},
	}
	for _, tc := range cases {
		got := ToneStyle(theme, tc.tone)
		if got.GetForeground() != tc.want.GetForeground() {
			t.Fatalf("%s: foreground mismatch got=%v want=%v", tc.name, got.GetForeground(), tc.want.GetForeground())
		}
	}
}

func TestStatusDot_RendersGlyphAndLabel(t *testing.T) {
	theme := DefaultTheme()
	// Label preserved verbatim; glyph is the solid circle.
	got := StatusDot(theme, TonePositive, "running")
	if !strings.Contains(got, "●") || !strings.Contains(got, "running") {
		t.Fatalf("StatusDot should contain glyph + label: %q", got)
	}
	// No case change injected.
	if strings.Contains(got, "Running") {
		t.Fatalf("label should be preserved lower-case: %q", got)
	}
	// Empty label renders just the glyph.
	dot := StatusDot(theme, ToneNeutral, "")
	plain := stripANSI(dot)
	if plain != "●" {
		t.Fatalf("empty label should render only glyph, got %q", plain)
	}
}

func TestStatusDot_ToneColor(t *testing.T) {
	theme := DefaultTheme()
	if StatusDot(theme, TonePositive, "on") != theme.Success.Render("● on") {
		t.Fatal("positive dot should use Success color")
	}
	if StatusDot(theme, ToneNegative, "failed") != theme.Danger.Render("● failed") {
		t.Fatal("negative dot should use Danger color")
	}
	if StatusDot(theme, ToneCaution, "stale") != theme.Warning.Render("● stale") {
		t.Fatal("caution dot should use Warning color")
	}
	if StatusDot(theme, ToneNeutral, "off") != theme.Muted.Render("● off") {
		t.Fatal("neutral dot should use Muted color")
	}
}

func TestClassifyStatusTone(t *testing.T) {
	cases := []struct {
		value string
		tone  StatusTone
	}{
		// Positive
		{"running", TonePositive},
		{"On", TonePositive},
		{"HEALTHY", TonePositive},
		{"connected", TonePositive},
		{"installed", TonePositive},
		// Neutral
		{"off", ToneNeutral},
		{"stopped", ToneNeutral},
		{"unknown", ToneNeutral},
		{"unmanaged", ToneNeutral},
		{"disabled", ToneNeutral},
		// Caution
		{"stale", ToneCaution},
		{"missing", ToneCaution},
		{"cached", ToneCaution},
		{"reconnecting", ToneCaution},
		{"Cache missing", ToneCaution},
		{"Retry pending", ToneCaution},
		// Negative
		{"failed", ToneNegative},
		{"error", ToneNegative},
		{"offline", ToneNegative},
		{"disconnected", ToneNegative},
		{"degraded", ToneNegative},
		// "not installed" must stay Neutral despite "installed" substring.
		{"not installed", ToneNeutral},
		{"not_installed", ToneNeutral},
		// Compound right-status badges.
		{"Service running · Connected", TonePositive},
		{"Service running · Reconnecting", ToneCaution},
		{"Service stopped · Offline", ToneNegative},
		// Unknown / empty fallback.
		{"", ToneNeutral},
		{"core", ToneNeutral},
	}
	for _, tc := range cases {
		if got := ClassifyStatusTone(tc.value); got != tc.tone {
			t.Fatalf("ClassifyStatusTone(%q)=%v want %v", tc.value, got, tc.tone)
		}
	}
}
