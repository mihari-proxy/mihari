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
	Subscription string
	Connections  int
	UploadRate   int64
	DownloadRate int64
	MemoryInUse  int64
	// RightStatus is shown in the top-right corner (e.g. Stale, Reconnecting,
	// Service not installed). Empty means live / no badge.
	RightStatus string
}

const (
	statusSep           = "  ·  "
	statusCoreRunning   = "●"
	statusCoreOffline   = "○"
	statusCoreReconnect = "◌"
)

// RenderStatusBar builds the top status-shell bar (Full or Compact).
// Metrics stay left-aligned; RightStatus is pinned to the top-right when width > 0.
// Secrets must not appear in data.
func RenderStatusBar(theme Theme, data StatusBarData, width int, compact bool) string {
	parts := make([]string, 0, 7)
	parts = append(parts, theme.Title.Render(AppName))
	parts = append(parts, renderStatusCore(theme, data.CoreStatus, data.CoreVersion, compact))

	if !compact {
		if sub := strings.TrimSpace(data.Subscription); sub != "" {
			parts = append(parts, sub)
		}
	}

	if compact {
		parts = append(parts, fmt.Sprintf("%dc", data.Connections))
		parts = append(parts, fmt.Sprintf("↑%s ↓%s",
			formatCompactIEC(data.UploadRate),
			formatCompactIEC(data.DownloadRate),
		))
		parts = append(parts, formatCompactIEC(data.MemoryInUse))
	} else {
		parts = append(parts, fmt.Sprintf("%d conn", data.Connections))
		parts = append(parts, fmt.Sprintf("↑%s  ↓%s",
			FormatRate(data.UploadRate),
			FormatRate(data.DownloadRate),
		))
		parts = append(parts, FormatBytes(data.MemoryInUse))
	}

	left := strings.Join(parts, statusSep)
	rightLabel := strings.TrimSpace(data.RightStatus)
	var right string
	if rightLabel != "" {
		right = renderRightStatus(theme, rightLabel)
	}

	if width <= 0 {
		if right == "" {
			return lipgloss.NewStyle().Padding(0, 1).Render(left)
		}
		return lipgloss.NewStyle().Padding(0, 1).Render(left + statusSep + right)
	}

	// Inner width accounts for horizontal padding (1 cell each side).
	inner := max(1, width-2)
	if right == "" {
		return lipgloss.NewStyle().Padding(0, 1).MaxWidth(width).Width(width).Render(
			lipgloss.NewStyle().MaxWidth(inner).Render(left),
		)
	}

	rightWidth := lipgloss.Width(right)
	// At least one space between left metrics and the right badge.
	leftBudget := inner - rightWidth - 1
	if leftBudget < 8 {
		// Prefer keeping the right badge; shrink left aggressively.
		leftBudget = max(1, inner-rightWidth)
	}
	leftClamped := lipgloss.NewStyle().MaxWidth(leftBudget).Render(left)
	gap := inner - lipgloss.Width(leftClamped) - rightWidth
	if gap < 1 {
		gap = 1
		// Re-clamp left so left+gap+right fits.
		leftBudget = max(1, inner-rightWidth-gap)
		leftClamped = lipgloss.NewStyle().MaxWidth(leftBudget).Render(left)
		gap = max(1, inner-lipgloss.Width(leftClamped)-rightWidth)
	}
	line := leftClamped + strings.Repeat(" ", gap) + right
	return lipgloss.NewStyle().Padding(0, 1).MaxWidth(width).Width(width).Render(line)
}

func renderRightStatus(theme Theme, label string) string {
	// Color decision routes through the shared status-tone classifier so the bar
	// agrees with every page (e.g. "not installed" → Neutral, "offline" → Danger,
	// "Reconnecting" → Caution, "running · Connected" → Positive).
	return ToneStyle(theme, ClassifyStatusTone(label)).Render(label)
}

func renderStatusCore(theme Theme, status, version string, compact bool) string {
	symbol, style := coreStatusGlyph(theme, status)
	text := strings.TrimSpace(status)
	var b strings.Builder
	b.WriteString(style.Render(symbol))
	if text != "" {
		b.WriteByte(' ')
		b.WriteString(text)
	}
	if !compact {
		if ver := strings.TrimSpace(version); ver != "" {
			if !strings.HasPrefix(strings.ToLower(ver), "v") {
				ver = "v" + ver
			}
			b.WriteByte(' ')
			b.WriteString(ver)
		}
	}
	return b.String()
}

func coreStatusGlyph(theme Theme, status string) (string, lipgloss.Style) {
	// Glyphs stay (disconnect→○, reconnect→◌, else ●); only the color now flows
	// from the shared tone classifier instead of hand-written substring matches.
	lower := strings.ToLower(status)
	glyph := statusCoreRunning
	switch {
	case strings.Contains(lower, "disconnect"):
		glyph = statusCoreOffline
	case strings.Contains(lower, "reconnect"):
		glyph = statusCoreReconnect
	}
	return glyph, ToneStyle(theme, ClassifyStatusTone(status))
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
