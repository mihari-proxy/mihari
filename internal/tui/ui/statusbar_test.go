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
	if strings.Contains(got, StatusDaemonOffline) || strings.Contains(got, StatusDaemonReconnecting) {
		t.Fatalf("full status bar should not show right badge when RightStatus empty:\n%s", got)
	}
}

func TestStatusBar_RightStatusAligned(t *testing.T) {
	theme := DefaultTheme()
	data := sampleStatusBarData()
	data.CoreStatus = "disconnected"
	data.CoreVersion = ""
	data.RightStatus = StatusServiceStopped + StatusRightJoin + StatusDaemonOffline

	const width = 110
	got := RenderStatusBar(theme, data, width, false)
	plain := stripANSI(got)
	if strings.Contains(plain, "STALE") && strings.HasPrefix(strings.TrimLeft(plain, " "), "STALE") {
		t.Fatalf("stale must not be a left prefix:\n%s", plain)
	}
	if !strings.Contains(plain, StatusServiceStopped) || !strings.Contains(plain, StatusDaemonOffline) {
		t.Fatalf("stale bar missing dual right status:\n%s", plain)
	}
	if !strings.Contains(plain, "○") {
		t.Fatalf("disconnected should use ○:\n%s", plain)
	}
	if !strings.Contains(plain, "12 conn") {
		t.Fatalf("stale bar should keep connection count:\n%s", plain)
	}
	// Right status should sit near the end of the padded line.
	trimmed := strings.TrimRight(plain, " ")
	if !strings.HasSuffix(trimmed, StatusDaemonOffline) {
		t.Fatalf("right status should be right-aligned, got:\n%q", plain)
	}
	if w := lipgloss.Width(got); w != width {
		t.Fatalf("status bar width=%d want %d", w, width)
	}
}

func TestStatusBar_RightStatusServiceAndDaemonLabels(t *testing.T) {
	theme := DefaultTheme()
	for _, label := range []string{
		StatusServiceNotInstalled,
		StatusServiceStopped + StatusRightJoin + StatusDaemonOffline,
		StatusServiceRunning + StatusRightJoin + StatusDaemonReconnecting,
	} {
		data := sampleStatusBarData()
		data.RightStatus = label
		plain := stripANSI(RenderStatusBar(theme, data, 120, false))
		if !strings.Contains(plain, label) {
			t.Fatalf("missing %q:\n%s", label, plain)
		}
		if !strings.HasSuffix(strings.TrimRight(plain, " "), strings.TrimSpace(label[strings.LastIndex(label, "·")+1:])) &&
			!strings.HasSuffix(strings.TrimRight(plain, " "), label) {
			t.Fatalf("%q should be right-aligned:\n%q", label, plain)
		}
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
	// Compact shows the subscription with compact usage (decision 5) but no
	// IEC GiB labels, and never the version (Full only).
	if !strings.Contains(compact, "Main") {
		t.Fatalf("compact should show subscription:\n%s", compact)
	}
	if strings.Contains(compact, "GiB") {
		t.Fatalf("compact subscription should use compact usage:\n%s", compact)
	}
	if strings.Contains(compact, "v1.19.0") {
		t.Fatalf("compact should omit core version:\n%s", compact)
	}
	if len(compact) >= len(full) {
		t.Fatalf("compact should be shorter than full:\nfull=%q\ncompact=%q", full, compact)
	}
}

// TestStatusBar_SegmentDropTiers pins the priority-drop behavior of the
// status bar (design §2.1): segments are dropped in priority order
// 版本(1) → 内存(2) → 订阅(3) → conn/速率(5) → Core(5) → Title(6), with the
// right badge (27 cols, "Service running · Connected") always kept.
// Budget = width−2−27−1 with badge, width−2 without.
func TestStatusBar_SegmentDropTiers(t *testing.T) {
	theme := DefaultTheme()
	data := StatusBarData{
		CoreStatus:          "running",
		CoreVersion:         "v1.19.0",
		Subscription:        "Main · 9.0 GiB/100.0 GiB",
		SubscriptionCompact: "Main · 9G/100G",
		Connections:         3,
		UploadRate:          3 * 1024 * 1024,
		DownloadRate:        12 * 1024 * 1024,
		MemoryInUse:         256 * 1024 * 1024,
		RightStatus:         "Service running · Connected",
	}
	cases := []struct {
		name        string
		width       int
		compact     bool
		rightStatus string
		want        string // plain segment sequence in positional order
	}{
		// Budget 70: version/memory/subscription dropped, padded rates kept.
		{"full-100-badge", 100, false, "Service running · Connected", "Mihari  ·  ● running  ·  3 conn  ·  ↑  3.0 MiB/s  ↓ 12.0 MiB/s"},
		// Budget 98: only version and memory dropped.
		{"full-100-nobadge", 100, false, "", "Mihari  ·  ● running  ·  Main · 9.0 GiB/100.0 GiB  ·  3 conn  ·  ↑  3.0 MiB/s  ↓ 12.0 MiB/s"},
		// Budget 64: padded compact rates leave no room for subscription.
		{"compact-94-badge", 94, true, "Service running · Connected", "Mihari  ·  ● running  ·  3c  ·  ↑    3M/s ↓   12M/s"},
		// Budget 51: subscription dropped, padded rates kept.
		{"compact-81-badge", 81, true, "Service running · Connected", "Mihari  ·  ● running  ·  3c  ·  ↑    3M/s ↓   12M/s"},
		// Budget 42: rates dropped too.
		{"compact-72-badge", 72, true, "Service running · Connected", "Mihari  ·  ● running  ·  3c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data.RightStatus = tc.rightStatus
			plain := stripANSI(RenderStatusBar(theme, data, tc.width, tc.compact))
			// Peel padding and the pinned right badge, leaving the left segments.
			left := strings.TrimSpace(plain)
			left = strings.TrimSuffix(left, tc.rightStatus)
			left = strings.TrimSpace(left)
			if left != tc.want {
				t.Fatalf("width=%d compact=%v got:\n%q\nwant:\n%q", tc.width, tc.compact, left, tc.want)
			}
		})
	}
}

func statusBarRateSegment(plain string) string {
	start := strings.Index(plain, "↑")
	if start < 0 {
		return ""
	}
	rest := plain[start:]
	if i := strings.Index(rest, "  ·  "); i >= 0 {
		return rest[:i]
	}
	return strings.TrimRight(rest, " ")
}

func statusBarFieldSet(plain string, compact bool) string {
	var parts []string
	if strings.Contains(plain, AppName) {
		parts = append(parts, "title")
	}
	if strings.Contains(plain, "running") {
		parts = append(parts, "core")
	}
	if strings.Contains(plain, "v1.19.0") {
		parts = append(parts, "version")
	}
	if strings.Contains(plain, "Main") {
		parts = append(parts, "sub")
	}
	if compact {
		if strings.Contains(plain, "3c") {
			parts = append(parts, "conn")
		}
		if strings.Contains(plain, "256M") {
			parts = append(parts, "mem")
		}
	} else {
		if strings.Contains(plain, "3 conn") {
			parts = append(parts, "conn")
		}
		if strings.Contains(plain, "256.0 MiB") {
			parts = append(parts, "mem")
		}
	}
	if strings.Contains(plain, "↑") {
		parts = append(parts, "rate")
	}
	return strings.Join(parts, ",")
}

func TestStatusBar_RateSegmentWidthStable(t *testing.T) {
	theme := DefaultTheme()
	base := StatusBarData{
		CoreStatus:          "running",
		CoreVersion:         "v1.19.0",
		Subscription:        "Main · 9.0 GiB/100.0 GiB",
		SubscriptionCompact: "Main · 9G/100G",
		Connections:         3,
		MemoryInUse:         256 * 1024 * 1024,
	}
	const width = 160
	idle := base
	busy := base
	busy.UploadRate = 12*1024*1024 + 300*1024
	busy.DownloadRate = 12*1024*1024 + 300*1024
	for _, compact := range []bool{false, true} {
		idleSeg := statusBarRateSegment(stripANSI(RenderStatusBar(theme, idle, width, compact)))
		busySeg := statusBarRateSegment(stripANSI(RenderStatusBar(theme, busy, width, compact)))
		if idleSeg == "" || busySeg == "" {
			t.Fatalf("compact=%v missing rate segment idle=%q busy=%q", compact, idleSeg, busySeg)
		}
		if got, want := lipgloss.Width(idleSeg), lipgloss.Width(busySeg); got != want {
			t.Fatalf("compact=%v rate width idle=%d (%q) busy=%d (%q)", compact, got, idleSeg, want, busySeg)
		}
	}
}

func TestStatusBar_RateChangeDoesNotDropFields(t *testing.T) {
	theme := DefaultTheme()
	data := StatusBarData{
		CoreStatus:          "running",
		CoreVersion:         "v1.19.0",
		Subscription:        "Main · 9.0 GiB/100.0 GiB",
		SubscriptionCompact: "Main · 9G/100G",
		Connections:         3,
		MemoryInUse:         256 * 1024 * 1024,
	}
	cases := []struct {
		name    string
		width   int
		compact bool
		badge   string
	}{
		// Full no-badge budget 86: idle keeps subscription, busy 12 MiB/s drops it.
		{"full-88-nobadge", 88, false, ""},
		// Compact with badge budget 63: idle keeps subscription, busy 12 MiB/s drops it.
		{"compact-93-badge", 93, true, "Service running · Connected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idle := data
			idle.RightStatus = tc.badge
			busy := idle
			busy.UploadRate = 12 * 1024 * 1024
			busy.DownloadRate = 12 * 1024 * 1024
			idleSet := statusBarFieldSet(stripANSI(RenderStatusBar(theme, idle, tc.width, tc.compact)), tc.compact)
			busySet := statusBarFieldSet(stripANSI(RenderStatusBar(theme, busy, tc.width, tc.compact)), tc.compact)
			if idleSet != busySet {
				t.Fatalf("field set changed with rate: idle=%s busy=%s", idleSet, busySet)
			}
		})
	}
}

func TestStatusBar_StaleCoreDotDegrades(t *testing.T) {
	theme := DefaultTheme()
	data := StatusBarData{CoreStatus: "running", Stale: true}
	got := RenderStatusBar(theme, data, 80, true)
	if !strings.Contains(got, "\x1b[38;5;214m●") {
		t.Fatalf("stale core dot should degrade to caution yellow:\n%q", got)
	}
	data.Stale = false
	if strings.Contains(stripANSI(RenderStatusBar(theme, data, 80, true)), "●") == false {
		t.Fatal("live dot missing")
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
