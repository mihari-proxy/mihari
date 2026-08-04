package ui

import lipgloss "charm.land/lipgloss/v2"

type Theme struct {
	Rail         lipgloss.Style
	RailSelected lipgloss.Style
	// RailCurrent marks the open page while keyboard focus is inside content.
	// It deliberately avoids the same bold+accent treatment as RailSelected/RowSelected.
	RailCurrent  lipgloss.Style
	Content      lipgloss.Style
	Footer       lipgloss.Style
	Muted        lipgloss.Style
	Title        lipgloss.Style
	Dialog       lipgloss.Style
	Button       lipgloss.Style
	ButtonActive lipgloss.Style
	// RowSelected highlights the focused list row inside a content page.
	RowSelected lipgloss.Style
}

func DefaultTheme() Theme {
	accent := lipgloss.Color("63")
	muted := lipgloss.Color("245")
	return Theme{
		Rail:         lipgloss.NewStyle().Padding(0, 1),
		RailSelected: lipgloss.NewStyle().Bold(true).Foreground(accent).Padding(0, 1),
		RailCurrent:  lipgloss.NewStyle().Underline(true).Padding(0, 1),
		Content:      lipgloss.NewStyle().Padding(0, 1),
		Footer:       lipgloss.NewStyle().Foreground(muted).Padding(0, 1),
		Muted:        lipgloss.NewStyle().Foreground(muted),
		Title:        lipgloss.NewStyle().Bold(true).Foreground(accent),
		Dialog:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2),
		Button:       lipgloss.NewStyle().Padding(0, 1),
		ButtonActive: lipgloss.NewStyle().Bold(true).Foreground(accent).Padding(0, 1),
		RowSelected:  lipgloss.NewStyle().Bold(true).Foreground(accent),
	}
}
