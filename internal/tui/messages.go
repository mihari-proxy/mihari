package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
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
		if open {
			select {
			case next, more := <-events:
				if !more {
					return sessionEventMsg{Event: event, Open: true}
				}
				batch := []session.Event{event, next}
				for len(batch) < 32 {
					select {
					case item, more := <-events:
						if !more {
							return sessionEventMsg{Event: session.Event{Batch: batch}, Open: true}
						}
						batch = append(batch, item)
					default:
						return sessionEventMsg{Event: session.Event{Batch: batch}, Open: true}
					}
				}
				event = session.Event{Batch: batch}
			default:
			}
		}
		return sessionEventMsg{Event: event, Open: open}
	}
}
