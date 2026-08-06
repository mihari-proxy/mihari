package overview

import (
	"strings"

	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

// dosRebelBanner is the "Mihari" wordmark rendered in the dos_rebel figlet
// font (59 columns wide, 8 rows). The M block occupies the first
// dosRebelMWidth columns; "ihari" begins one column later. The blank rows
// the font emits at the end are trimmed.
const (
	// dosRebelMWidth is the column width of the leading M block.
	dosRebelMWidth = 16
	// dosRebelBannerMinWidth is the content width needed for the banner plus
	// the Content style's 1-cell side padding.
	dosRebelBannerMinWidth = 61
	// dosRebelBannerMinHeight hides the banner in short windows so the KPI
	// cards stay fully visible (the content pane clips instead of scrolling).
	dosRebelBannerMinHeight = 24
)

var dosRebelBanner = []string{
	" ██████   ██████  ███  █████                           ███",
	"░░██████ ██████  ░░░  ░░███                           ░░░",
	" ░███░█████░███  ████  ░███████    ██████   ████████  ████",
	" ░███░░███ ░███ ░░███  ░███░░███  ░░░░░███ ░░███░░███░░███",
	" ░███ ░░░  ░███  ░███  ░███ ░███   ███████  ░███ ░░░  ░███",
	" ░███      ░███  ░███  ░███ ░███  ███░░███  ░███      ░███",
	" █████     █████ █████ ████ █████░░████████ █████     █████",
	"░░░░░     ░░░░░ ░░░░░ ░░░░ ░░░░░  ░░░░░░░░ ░░░░░     ░░░░░",
}

// renderBanner renders the dos_rebel wordmark for the top of the Overview
// page: the M block in the accent color, "ihari" in muted, so the brand
// anchor reads first. Returns "" when the window is too narrow or too short
// for the banner to fit cleanly.
func renderBanner(theme ui.Theme, width, height int) string {
	if width < dosRebelBannerMinWidth || height < dosRebelBannerMinHeight {
		return ""
	}
	lines := make([]string, len(dosRebelBanner))
	for index, line := range dosRebelBanner {
		lines[index] = theme.Title.Render(line[:dosRebelMWidth]) + theme.Muted.Render(line[dosRebelMWidth:])
	}
	return strings.Join(lines, "\n")
}
