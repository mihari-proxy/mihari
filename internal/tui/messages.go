package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/tui/session"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

type OperationRecordMsg struct{ Record ui.OperationRecord }

type sessionEventMsg struct {
	Event session.Event
	Open  bool
}

func waitSessionEvent(events <-chan session.Event) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		event, open := <-events
		return sessionEventMsg{Event: event, Open: open}
	}
}
