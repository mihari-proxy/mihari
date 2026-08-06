package overview

import "strings"

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

// renderBanner returns the dos_rebel wordmark for the top of the Overview
// page, unstyled so it renders in the terminal's default (brightest)
// foreground and the block glyphs stay crisp. Returns "" when the window is
// too narrow or too short for the banner to fit cleanly.
func renderBanner(width, height int) string {
	if width < dosRebelBannerMinWidth || height < dosRebelBannerMinHeight {
		return ""
	}
	return strings.Join(dosRebelBanner, "\n")
}
