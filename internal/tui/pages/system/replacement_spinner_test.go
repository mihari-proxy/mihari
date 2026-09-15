package system

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestMihariPreparation_SpinnerRunsUntilPreparationEnds(t *testing.T) {
	for _, outcome := range []string{"ready", "failed", "unavailable", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			m, updater := replacementFixture(t)
			t.Cleanup(func() { m.CancelMihariPreparation() })
			_, command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if updater.calls != 0 {
				t.Fatal("preparation ran inside Update")
			}
			var start *startRowSpinMsg
			var result *preparedMihariResultMsg
			var collect func(tea.Msg)
			collect = func(message tea.Msg) {
				switch typed := message.(type) {
				case tea.BatchMsg:
					for _, child := range typed {
						if child != nil {
							collect(child())
						}
					}
				case ui.PageResultMsg:
					if typed.Page != ui.PageSystem {
						t.Fatalf("preparation command routed to %q", typed.Page)
					}
					collect(typed.Result)
				case startRowSpinMsg:
					start = &typed
				case preparedMihariResultMsg:
					result = &typed
				}
			}
			if command == nil {
				t.Fatal("missing preparation command")
			}
			collect(command())
			if start == nil {
				t.Fatal("Preparing did not schedule its spinner")
			}
			if result == nil || updater.calls != 1 {
				t.Fatal("preparation did not produce exactly one result")
			}
			// Hold the result as if preparation were still in flight. Drive frames
			// with explicit timestamps, without waiting for real timer commands.
			_, tick := m.Update(*start)
			if tick == nil {
				t.Fatal("spinner start did not schedule a tick")
			}
			before := m.View()
			for frame := 1; frame <= 3; frame++ {
				at := time.Unix(0, 0).Add(time.Duration(frame) * rowSpinInterval)
				_, tick = m.Update(rowSpinTickMsg{gen: start.gen, t: at})
				after := m.View()
				if tick == nil || after == before || !strings.Contains(after, ui.SpinnerLabel(at, ui.MihariProgressPreparing)) {
					t.Fatal("Preparing spinner did not advance and schedule its next frame")
				}
				before = after
			}
			switch outcome {
			case "failed":
				result.err = errors.New("fixture preparation failed")
			case "unavailable":
				result.prepared.Available = false
			case "cancelled":
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			}
			m.Update(*result)
			if m.pending || strings.Contains(m.View(), ui.MihariProgressPreparing) {
				t.Fatal("Preparing remained visible after preparation ended")
			}
			_, tick = m.Update(rowSpinTickMsg{gen: start.gen, t: time.Unix(1, 0)})
			if tick != nil {
				t.Fatal("spinner kept scheduling after preparation ended")
			}
		})
	}
}
