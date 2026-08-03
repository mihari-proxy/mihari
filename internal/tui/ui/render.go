package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func RenderRail(theme Theme, pages []PageID, selected int, focused bool, width, height int) string {
	lines := []string{theme.Title.Render(AppName), ""}
	for index, page := range pages {
		label := "  " + PageLabel(page)
		if index == selected {
			label = "› " + PageLabel(page)
			if focused {
				lines = append(lines, theme.RailSelected.Render(label))
				continue
			}
		}
		lines = append(lines, theme.Rail.Render(label))
	}
	return theme.Rail.Width(width).Height(height).Render(strings.Join(lines, "\n"))
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
