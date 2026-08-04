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
	Stale        bool
}

const (
	statusSep           = "  ·  "
	statusCoreRunning   = "●"
	statusCoreOffline   = "○"
	statusCoreReconnect = "◌"
	statusStalePrefix   = "STALE"
)

// RenderStatusBar builds the top status-shell bar (Full or Compact).
// When width > 0 the line is clamped with MaxWidth; secrets must not appear in data.
func RenderStatusBar(theme Theme, data StatusBarData, width int, compact bool) string {
	parts := make([]string, 0, 7)
	if data.Stale {
		parts = append(parts, theme.Warning.Render(statusStalePrefix))
	}
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

	line := strings.Join(parts, statusSep)
	style := lipgloss.NewStyle().Padding(0, 1)
	if width > 0 {
		style = style.MaxWidth(width)
	}
	return style.Render(line)
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
	lower := strings.ToLower(status)
	switch {
	case strings.Contains(lower, "disconnect"):
		return statusCoreOffline, theme.Danger
	case strings.Contains(lower, "reconnect"):
		return statusCoreReconnect, theme.Warning
	default:
		return statusCoreRunning, theme.Success
	}
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
