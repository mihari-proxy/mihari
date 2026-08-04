package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func RenderRail(theme Theme, pages []PageID, selected int, focused bool, width, height int) string {
	// Brand lives only on the status bar; do not repeat AppName here.
	lines := make([]string, 0, len(pages))
	for index, page := range pages {
		label := "  " + PageLabel(page)
		if index == selected {
			label = FocusMarker + PageLabel(page)
			if focused {
				// Keyboard focus is on the rail: strong selection highlight.
				lines = append(lines, theme.RailSelected.Render(label))
				continue
			}
			// Content owns keyboard focus: keep a quieter "you are here" marker.
			lines = append(lines, theme.RailCurrent.Render(label))
			continue
		}
		lines = append(lines, theme.Rail.Render(label))
	}
	return theme.Rail.Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

// ContentFocusable pages can suppress in-page selection chrome while the rail owns focus.
type ContentFocusable interface {
	SetContentFocused(bool)
}

// FooterHintProvider supplies contextual footer shortcuts for the focused page.
type FooterHintProvider interface {
	FooterHints() string
}

// PageFooterHints returns the contextual footer line for a page while content is focused.
func PageFooterHints(id PageID) string {
	switch id {
	case PageOverview:
		return FooterOverview
	case PageProxies:
		return FooterProxies
	case PageConnections:
		return FooterConnections
	case PageRules:
		return FooterRules
	case PageLogs:
		return FooterLogs
	case PageSubscriptions:
		return FooterSubscriptions
	case PageWebGUI:
		return FooterWebGUI
	case PageSystem:
		return FooterSystem
	default:
		return FooterContent
	}
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
	if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "left" {
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
