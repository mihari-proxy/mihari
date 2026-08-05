package ui

import (
	"image/color"

	lipgloss "charm.land/lipgloss/v2"
)

// StatusChipKind is the lifecycle state rendered as a solid row-end badge.
type StatusChipKind uint8

const (
	// StatusChipPending is in-flight work (warning background).
	StatusChipPending StatusChipKind = iota
	// StatusChipDone is a sticky success outcome (success background).
	StatusChipDone
	// StatusChipFailed is a sticky failure outcome (danger background).
	StatusChipFailed
)

// RenderStatusChip paints a short solid-background label for row-end lifecycle state.
// Status is never color-only: callers must pass a non-empty text label.
// Foreground is always the on-solid token so chips stay readable on Success/Warning/Danger fills.
func RenderStatusChip(theme Theme, kind StatusChipKind, label string) string {
	if label == "" {
		return ""
	}
	bg := theme.ColorWarning
	switch kind {
	case StatusChipDone:
		bg = theme.ColorSuccess
	case StatusChipFailed:
		bg = theme.ColorDanger
	case StatusChipPending:
		bg = theme.ColorWarning
	}
	fg := theme.ColorOnSolid
	if fg == nil {
		fg = color.Black
	}
	return lipgloss.NewStyle().
		Foreground(fg).
		Background(bg).
		Padding(0, 1).
		Render(label)
}
