package ui

type SizeClass uint8

const (
	Full SizeClass = iota
	Compact
	TooSmall
)

type FocusArea uint8

const (
	FocusRail FocusArea = iota
	FocusContent
)

type Focus struct {
	Area FocusArea
	Page PageID
}

type InputMode uint8

const (
	InputNavigation InputMode = iota
	InputText
)

type FocusRailMsg struct{}

type InputModeMsg struct{ Mode InputMode }
