package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func sampleStatusBarData() StatusBarData {
	return StatusBarData{
		CoreStatus:   "core",
		CoreVersion:  "1.19.0",
		Subscription: "Main",
		Connections:  12,
		// ~1.2 MiB/s and ~4.1 MiB/s for FormatRate assertions.
		UploadRate:   12 * 1024 * 1024 / 10,
		DownloadRate: 41 * 1024 * 1024 / 10,
		MemoryInUse:  84 * 1024 * 1024,
	}
}

func TestStatusBar_FullIncludesRates(t *testing.T) {
	theme := DefaultTheme()
	data := sampleStatusBarData()
	got := stripANSI(RenderStatusBar(theme, data, 120, false))

	for _, want := range []string{
		AppName,
		"●",
		"core",
		"v1.19.0",
		"Main",
		"12 conn",
		"↑",
		"↓",
		"MiB/s",
		"MiB",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("full status bar missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "STALE") {
		t.Fatalf("full status bar should not be STALE when Stale=false:\n%s", got)
	}
}

func TestStatusBar_StalePrefix(t *testing.T) {
	theme := DefaultTheme()
	data := sampleStatusBarData()
	data.Stale = true
	data.CoreStatus = "disconnected"
	data.CoreVersion = ""

	got := stripANSI(RenderStatusBar(theme, data, 120, false))
	if !strings.HasPrefix(strings.TrimLeft(got, " "), "STALE") {
		t.Fatalf("stale bar should prefix STALE:\n%s", got)
	}
	if !strings.Contains(got, "○") {
		t.Fatalf("disconnected should use ○:\n%s", got)
	}
	// Keep last numeric snapshot even when stale.
	if !strings.Contains(got, "12 conn") {
		t.Fatalf("stale bar should keep connection count:\n%s", got)
	}
}

func TestStatusBar_CompactShorterThanFull(t *testing.T) {
	theme := DefaultTheme()
	data := sampleStatusBarData()
	data.CoreStatus = "running"

	full := stripANSI(RenderStatusBar(theme, data, 120, false))
	compact := stripANSI(RenderStatusBar(theme, data, 120, true))

	if !strings.Contains(compact, AppName) {
		t.Fatalf("compact missing brand:\n%s", compact)
	}
	if !strings.Contains(compact, "●") || !strings.Contains(compact, "running") {
		t.Fatalf("compact missing core status:\n%s", compact)
	}
	if !strings.Contains(compact, "12c") {
		t.Fatalf("compact should use Nc form:\n%s", compact)
	}
	if strings.Contains(compact, "12 conn") {
		t.Fatalf("compact should not use full conn label:\n%s", compact)
	}
	// Compact omits subscription and version to save width.
	if strings.Contains(compact, "Main") {
		t.Fatalf("compact should omit subscription:\n%s", compact)
	}
	if strings.Contains(compact, "v1.19.0") {
		t.Fatalf("compact should omit core version:\n%s", compact)
	}
	if len(compact) >= len(full) {
		t.Fatalf("compact should be shorter than full:\nfull=%q\ncompact=%q", full, compact)
	}
}

func TestStatusBar_CoreSymbols(t *testing.T) {
	theme := DefaultTheme()
	cases := []struct {
		status string
		symbol string
	}{
		{"running", "●"},
		{"ok", "●"},
		{"disconnected", "○"},
		{"Daemon disconnected", "○"},
		{"reconnecting", "◌"},
		{"reconnect", "◌"},
	}
	for _, tc := range cases {
		got := stripANSI(RenderStatusBar(theme, StatusBarData{CoreStatus: tc.status}, 80, true))
		if !strings.Contains(got, tc.symbol) {
			t.Fatalf("status %q: want symbol %q in %q", tc.status, tc.symbol, got)
		}
	}
}

func TestStatusBar_TruncatesToWidth(t *testing.T) {
	theme := DefaultTheme()
	data := sampleStatusBarData()
	const width = 40
	got := RenderStatusBar(theme, data, width, false)
	if w := lipgloss.Width(got); w > width {
		t.Fatalf("width=%d exceeds max %d: %q", w, width, stripANSI(got))
	}
}
