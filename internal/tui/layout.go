package tui

import "github.com/LeeShunEE/mihari/internal/tui/ui"

const (
	minimumWidth  = 72
	minimumHeight = 22
	fullWidth     = 100
	fullHeight    = 28
)

type Layout struct {
	Class         ui.SizeClass
	Width         int
	Height        int
	RailWidth     int
	ContentWidth  int
	ContentHeight int
	FooterHeight  int
}

func Classify(width, height int) ui.SizeClass {
	if width < minimumWidth || height < minimumHeight {
		return ui.TooSmall
	}
	if width >= fullWidth && height >= fullHeight {
		return ui.Full
	}
	return ui.Compact
}

func calculateLayout(width, height int) Layout {
	class := Classify(width, height)
	result := Layout{Class: class, Width: width, Height: height, FooterHeight: 1}
	if class == ui.TooSmall {
		return result
	}
	result.RailWidth = 24
	if class == ui.Compact {
		result.RailWidth = 18
	}
	result.ContentWidth = max(1, width-result.RailWidth)
	result.ContentHeight = max(1, height-result.FooterHeight)
	return result
}
