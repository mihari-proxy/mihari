package overview

import (
	"strings"

	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

// dosRebelBanner is the "Mihari" wordmark rendered in the dos_rebel figlet
// font (59 columns wide, 8 rows). The blank rows the font emits at the end
// are trimmed.
const (
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
// page, uniformly in the muted foreground so it reads as a quiet brand
// mark above the KPI cards. Returns "" when the window is too narrow or too
// short for the banner to fit cleanly.
func renderBanner(theme ui.Theme, width, height int) string {
	if width < dosRebelBannerMinWidth || height < dosRebelBannerMinHeight {
		return ""
	}
	lines := make([]string, len(dosRebelBanner))
	for index, line := range dosRebelBanner {
		lines[index] = theme.Muted.Render(line)
	}
	return strings.Join(lines, "\n")
}
