package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

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

// OpenHelpMsg asks the root shell to open the keyboard help overlay.
type OpenHelpMsg struct{}

type RouteRequestMsg struct{ Page PageID }

type CoreObservedMsg struct{ Core protocol.CoreStatus }

type RuntimeRevisionMsg struct{ Revision uint64 }

type ConfirmationRequestMsg struct {
	Title     string
	Object    string
	Impact    string
	Rollback  string
	OnConfirm tea.Cmd
}
