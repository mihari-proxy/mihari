package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// TestSystemPortStatus_RootSchedulesProbe verifies that root owner updates route
// fresh probe results to System even while another page is active.
func TestSystemPortStatus_RootSchedulesProbe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*Model) tea.Cmd
	}{
		{"core", func(m *Model) tea.Cmd {
			return m.applySessionEvent(session.Event{Kind: session.EventCore, Core: protocol.CoreStatus{PID: 43, Status: "running"}})
		}},
		{"daemon", func(m *Model) tea.Cmd {
			return m.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{PID: 101}})
		}},
		{"observed", func(m *Model) tea.Cmd {
			_, cmd := m.Update(ui.CoreObservedMsg{Core: protocol.CoreStatus{PID: 43, Status: "running"}})
			return cmd
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := NewModel()
			model.status.PID = 100
			model.core = protocol.CoreStatus{PID: 42, Status: "running"}
			model.syncSystem()
			page := model.pages[ui.PageSystem].(*systempage.Model)
			// Invalid syntax makes the real probe deterministic without binding a socket.
			page.SetOnboarding(protocol.OnboardingStatus{MixedAddr: "fixture-invalid-endpoint"})
			var results []ui.PageResultMsg
			var collect func(tea.Cmd)
			collect = func(cmd tea.Cmd) {
				if cmd == nil {
					return
				}
				switch msg := cmd().(type) {
				case tea.BatchMsg:
					for _, child := range msg {
						collect(child)
					}
				case ui.PageResultMsg:
					results = append(results, msg)
				}
			}
			collect(tc.apply(&model))
			if len(results) != 1 || results[0].Page != ui.PageSystem {
				t.Fatalf("owner update did not route one System probe result: %+v", results)
			}
		})
	}
}
