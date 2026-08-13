package ui

import (
	"image/color"

	lipgloss "charm.land/lipgloss/v2"
)

// Theme holds shared lipgloss styles and semantic color tokens for the TUI shell.
type Theme struct {
	// Color tokens (status-shell palette). Prefer these over hardcoding lipgloss.Color("…").
	ColorAccent        color.Color
	ColorSuccess       color.Color
	ColorWarning       color.Color
	ColorDanger        color.Color
	ColorInfo          color.Color
	ColorMuted         color.Color
	ColorSurfaceBorder color.Color
	// ColorOnSolid is the foreground for solid-background chips (Done/Failed/Pending).
	// Dark text keeps contrast on Success/Warning/Danger fills in 256-color terminals.
	ColorOnSolid color.Color

	Rail         lipgloss.Style
	RailSelected lipgloss.Style
	// RailCurrent marks the open page while keyboard focus is inside content.
	// Muted foreground keeps it quieter than RailSelected (bold+accent).
	RailCurrent  lipgloss.Style
	Content      lipgloss.Style
	Footer       lipgloss.Style
	Muted        lipgloss.Style
	Title        lipgloss.Style
	Dialog       lipgloss.Style
	Button       lipgloss.Style
	ButtonActive lipgloss.Style
	// RowSelected marks business selection (e.g. active proxy), not keyboard focus alone.
	RowSelected lipgloss.Style
	// RowFocus marks keyboard focus on a list/table row (distinct from RowSelected).
	RowFocus lipgloss.Style

	Success lipgloss.Style
	Warning lipgloss.Style
	Danger  lipgloss.Style
	Info    lipgloss.Style

	SurfaceBorder lipgloss.Style
	Control       lipgloss.Style
	ControlActive lipgloss.Style
	TableHeader   lipgloss.Style
	DelayGood     lipgloss.Style
	DelayMid      lipgloss.Style
	DelayBad      lipgloss.Style
}

// DefaultTheme returns the default 256-color status-shell palette and derived styles.
func DefaultTheme() Theme {
	accent := lipgloss.Color("63")
	success := lipgloss.Color("78")
	warning := lipgloss.Color("214")
	danger := lipgloss.Color("203")
	info := lipgloss.Color("75")
	surfaceBorder := lipgloss.Color("240")
	muted := lipgloss.Color("245")
	onSolid := lipgloss.Color("0")

	return Theme{
		ColorAccent:        accent,
		ColorSuccess:       success,
		ColorWarning:       warning,
		ColorDanger:        danger,
		ColorInfo:          info,
		ColorMuted:         muted,
		ColorSurfaceBorder: surfaceBorder,
		ColorOnSolid:       onSolid,

		Rail:         lipgloss.NewStyle().Padding(0, 1),
		RailSelected: lipgloss.NewStyle().Bold(true).Foreground(accent).Padding(0, 1),
		RailCurrent:  lipgloss.NewStyle().Foreground(muted).Padding(0, 1),
		Content:      lipgloss.NewStyle().Padding(0, 1),
		Footer:       lipgloss.NewStyle().Foreground(muted).Padding(0, 1),
		Muted:        lipgloss.NewStyle().Foreground(muted),
		Title:        lipgloss.NewStyle().Bold(true).Foreground(accent),
		Dialog:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(surfaceBorder).Padding(1, 2),
		Button:       lipgloss.NewStyle().Padding(0, 1),
		ButtonActive: lipgloss.NewStyle().Bold(true).Foreground(accent).Padding(0, 1),
		RowSelected:  lipgloss.NewStyle().Bold(true).Foreground(accent),
		RowFocus:     lipgloss.NewStyle().Reverse(true),

		Success: lipgloss.NewStyle().Foreground(success),
		Warning: lipgloss.NewStyle().Foreground(warning),
		Danger:  lipgloss.NewStyle().Foreground(danger),
		Info:    lipgloss.NewStyle().Foreground(info),

		SurfaceBorder: lipgloss.NewStyle().Foreground(surfaceBorder),
		Control:       lipgloss.NewStyle().Foreground(muted).Padding(0, 1),
		ControlActive: lipgloss.NewStyle().Bold(true).Foreground(accent).Padding(0, 1),
		TableHeader:   lipgloss.NewStyle().Bold(true).Foreground(muted),
		DelayGood:     lipgloss.NewStyle().Foreground(success),
		DelayMid:      lipgloss.NewStyle().Foreground(warning),
		DelayBad:      lipgloss.NewStyle().Foreground(danger),
	}
}
