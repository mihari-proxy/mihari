package ui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func RenderRail(theme Theme, pages []PageID, selected int, focused bool, width, height int) string {
	// Brand lives only on the status bar; do not repeat AppName here.
	// Item styles reuse the rail colors but drop their own padding: the
	// container already pads 1 cell each side. Double padding plus
	// Width() word-wrap splits "1 Overview" onto two lines at compact 14.
	inner := max(1, width-theme.Rail.GetHorizontalPadding())
	lines := make([]string, 0, len(pages))
	for index, page := range pages {
		label := "  " + railTabLabel(index, page)
		style := theme.Rail.Padding(0, 0)
		if index == selected {
			label = FocusMarker + railTabLabel(index, page)
			if focused {
				// Keyboard focus is on the rail: strong selection highlight.
				style = theme.RailSelected.Padding(0, 0)
			} else {
				// Content owns keyboard focus: keep a quieter "you are here" marker.
				style = theme.RailCurrent.Padding(0, 0)
			}
		}
		lines = append(lines, style.Render(TruncateVisible(label, inner)))
	}
	return theme.Rail.Width(width).MaxWidth(width).Height(height).MaxHeight(height).Render(strings.Join(lines, "\n"))
}

// railTabLabel prefixes a rail entry with its 1-based shortcut digit so the rail
// doubles as the key map for the 1–8 jump shortcuts (see tui.railDigit).
func railTabLabel(index int, page PageID) string {
	return strconv.Itoa(index+1) + " " + PageLabel(page)
}

// ContentFocusable pages have in-page keyboard targets. The rail uses this
// to decide whether Enter may move focus into the page; pages use
// SetContentFocused to suppress selection chrome while the rail owns focus.
type ContentFocusable interface {
	SetContentFocused(bool)
}

// FooterHintProvider supplies contextual footer shortcuts for the focused page.
type FooterHintProvider interface {
	FooterHints() string
}

// HelpModeProvider reports the current overlay/input mode for keyboard help.
type HelpModeProvider interface {
	HelpMode() string
}

// PageFooterHints returns the contextual footer line for a page while content is focused.
func PageFooterHints(id PageID) string {
	return RenderFooter(id, "", FooterOpt{})
}

type UnavailablePage struct {
	id     PageID
	width  int
	height int
}

func NewUnavailablePage(id PageID) *UnavailablePage { return &UnavailablePage{id: id} }

func (p *UnavailablePage) ID() PageID { return p.id }

func (p *UnavailablePage) SetSize(width, height int) { p.width, p.height = width, height }

func (p *UnavailablePage) FocusFirst() {}

func (p *UnavailablePage) Update(message tea.Msg) (Page, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "esc" {
		return p, func() tea.Msg { return FocusRailMsg{} }
	}
	return p, nil
}

func (p *UnavailablePage) View() string {
	theme := DefaultTheme()
	return theme.Content.Width(p.width).Height(p.height).Render(
		theme.Title.Render(PageLabel(p.id)) + "\n\n" +
			theme.Muted.Render(UnavailableTitle+": "+UnavailableReason),
	)
}
