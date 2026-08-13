package ui

import (
	"fmt"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

// StatusBarData is the read-only snapshot rendered by RenderStatusBar.
// Callers must never put secrets, controller addresses, full subscription URLs,
// or auth tokens into these fields.
type StatusBarData struct {
	CoreStatus   string // running / disconnected / reconnecting / …
	CoreVersion  string
	Subscription string // full format, e.g. "Main · 9.0 GiB/100.0 GiB"
	// SubscriptionCompact is the compact format for compact bars
	// (name truncated to 16 columns + compact usage, e.g. "Main · 9G/100G").
	SubscriptionCompact string
	Connections         int
	UploadRate          int64
	DownloadRate        int64
	MemoryInUse         int64
	// Stale marks last-known values when the daemon stream is stale; the core
	// dot degrades to caution yellow (glyph unchanged).
	Stale bool
	// RightStatus is shown in the top-right corner (e.g. Stale, Reconnecting,
	// Service not installed). Empty means live / no badge.
	RightStatus string
}

const (
	statusSep              = "  ·  "
	statusCoreRunning      = "●"
	statusCoreOffline      = "○"
	statusCoreReconnect    = "◌"
	statusRateWidth        = 11 // "999.9 MiB/s"
	statusCompactRateWidth = 6  // "999.9M"
)

// RenderStatusBar builds the top status-shell bar (Full or Compact).
// Left segments are priority-rendered: when width runs out they are dropped
// in priority order 版本(1) → 内存(2) → 订阅(3) → conn/速率(5) → Core(5) →
// Title(6). RightStatus is pinned to the top-right and never dropped.
// Secrets must not appear in data.
func RenderStatusBar(theme Theme, data StatusBarData, width int, compact bool) string {
	segments := []PrioritySegment{
		{Priority: 6, Render: func() string { return theme.Title.Render(AppName) }},
		{Priority: 5, Render: func() string { return renderStatusCore(theme, data.CoreStatus, data.Stale) }},
	}
	if !compact && strings.TrimSpace(data.CoreVersion) != "" {
		segments = append(segments, PrioritySegment{Priority: 1, Render: func() string {
			return renderVersion(theme, data.CoreVersion)
		}})
	}
	sub := strings.TrimSpace(data.Subscription)
	if compact {
		// Compact bars use the compact usage format (e.g. "Main · 9G/100G").
		if c := strings.TrimSpace(data.SubscriptionCompact); c != "" {
			sub = c
		}
	}
	if sub != "" {
		segments = append(segments, PrioritySegment{Priority: 3, Render: func() string { return sub }})
	}
	if compact {
		segments = append(segments,
			PrioritySegment{Priority: 5, Render: func() string { return fmt.Sprintf("%dc", data.Connections) }},
			PrioritySegment{Priority: 5, Render: func() string {
				return fmt.Sprintf("↑%s/s ↓%s/s", statusRateLabel(data.UploadRate, true), statusRateLabel(data.DownloadRate, true))
			}},
			PrioritySegment{Priority: 2, Render: func() string { return formatCompactIEC(data.MemoryInUse) }},
		)
	} else {
		segments = append(segments,
			PrioritySegment{Priority: 5, Render: func() string { return fmt.Sprintf("%d conn", data.Connections) }},
			PrioritySegment{Priority: 5, Render: func() string {
				return fmt.Sprintf("↑%s  ↓%s", statusRateLabel(data.UploadRate, false), statusRateLabel(data.DownloadRate, false))
			}},
			PrioritySegment{Priority: 2, Render: func() string { return FormatBytes(data.MemoryInUse) }},
		)
	}

	rightLabel := strings.TrimSpace(data.RightStatus)
	var right string
	if rightLabel != "" {
		right = renderRightStatus(theme, rightLabel)
	}

	if width <= 0 {
		left := PriorityBar(1<<20, segments, statusSep)
		if right == "" {
			return lipgloss.NewStyle().Padding(0, 1).Render(left)
		}
		return lipgloss.NewStyle().Padding(0, 1).Render(left + statusSep + right)
	}

	// Inner width accounts for horizontal padding (1 cell each side); the
	// right badge reserves its width plus one separating space.
	inner := max(1, width-2)
	leftBudget := inner
	if right != "" {
		rightWidth := lipgloss.Width(right)
		leftBudget = inner - rightWidth - 1
		if leftBudget < 8 {
			// Prefer keeping the right badge; shrink left aggressively.
			leftBudget = max(1, inner-rightWidth)
		}
	}
	left := PriorityBar(leftBudget, segments, statusSep)

	if right == "" {
		return lipgloss.NewStyle().Padding(0, 1).MaxWidth(width).Width(width).Render(left)
	}
	gap := max(1, inner-lipgloss.Width(left)-lipgloss.Width(right))
	line := left + strings.Repeat(" ", gap) + right
	return lipgloss.NewStyle().Padding(0, 1).MaxWidth(width).Width(width).Render(line)
}

func renderRightStatus(theme Theme, label string) string {
	// Color decision routes through the shared status-tone classifier so the bar
	// agrees with every page (e.g. "not installed" → Neutral, "offline" → Danger,
	// "Reconnecting" → Caution, "running · Connected" → Positive).
	return ToneStyle(theme, ClassifyStatusTone(label)).Render(label)
}

func renderStatusCore(theme Theme, status string, stale bool) string {
	symbol, style := coreStatusGlyph(theme, status, stale)
	text := strings.TrimSpace(status)
	var b strings.Builder
	b.WriteString(style.Render(symbol))
	if text != "" {
		b.WriteByte(' ')
		b.WriteString(text)
	}
	return b.String()
}

// renderVersion renders the version segment (Full bars only): muted, so the
// least important segment reads quietly. Empty version renders nothing.
func renderVersion(theme Theme, version string) string {
	ver := strings.TrimSpace(version)
	if ver == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(ver), "v") {
		ver = "v" + ver
	}
	return theme.Muted.Render(ver)
}

func coreStatusGlyph(theme Theme, status string, stale bool) (string, lipgloss.Style) {
	// Glyphs stay (disconnect→○, reconnect→◌, else ●); only the color now flows
	// from the shared tone classifier instead of hand-written substring matches.
	// A stale stream degrades the dot to caution yellow (glyph unchanged).
	lower := strings.ToLower(status)
	glyph := statusCoreRunning
	switch {
	case strings.Contains(lower, "disconnect"):
		glyph = statusCoreOffline
	case strings.Contains(lower, "reconnect"):
		glyph = statusCoreReconnect
	}
	style := ToneStyle(theme, ClassifyStatusTone(status))
	if stale {
		style = ToneStyle(theme, ToneCaution)
	}
	return glyph, style
}

func statusRateLabel(value int64, compact bool) string {
	if compact {
		return PadCell(formatCompactIEC(value), statusCompactRateWidth, AlignRight)
	}
	return PadCell(FormatRate(value), statusRateWidth, AlignRight)
}

// formatCompactIEC renders a short IEC magnitude for compact status (e.g. 1.2M, 84M).
func formatCompactIEC(value int64) string {
	if value < 0 {
		value = 0
	}
	units := []string{"", "K", "M", "G", "T"}
	amount := float64(value)
	unit := 0
	for amount >= 1024 && unit < len(units)-1 {
		amount /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d", value)
	}
	if amount == float64(int64(amount)) {
		return fmt.Sprintf("%.0f%s", amount, units[unit])
	}
	return fmt.Sprintf("%.1f%s", amount, units[unit])
}
