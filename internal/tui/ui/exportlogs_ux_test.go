package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/logging"
)

func TestExportLogs_NavigationDoesNotEditOrSubmit(t *testing.T) {
	m := NewExportLogsModel(ExportLogsOptions{Now: exportTestNow})
	m.Open()
	original := m.output
	m.Update(key(tea.KeyDown, ""))
	m.Update(key('x', "x"))
	if m.focus != exportFocusOutput || m.output != original {
		t.Fatal("navigation must select output without editing")
	}
	m.Update(key(tea.KeyEnter, ""))
	if m.Pending() {
		t.Fatal("Enter on a parameter submitted the form")
	}
	m.Update(key('x', "x"))
	if m.output != original+"x" {
		t.Fatal("Enter did not enable parameter editing")
	}
	m.Update(key(tea.KeyEsc, ""))
	if m.Closed() || !strings.Contains(m.View(120, 35), "Discard changes?") {
		t.Fatal("Esc must request confirmation")
	}
	m.Update(key(tea.KeyEsc, ""))
	if m.output != original+"x" {
		t.Fatal("dismissing confirmation discarded edits")
	}
	m.Update(key(tea.KeyEsc, ""))
	m.Update(key(tea.KeyLeft, "")) // select Discard, default is Keep editing
	m.Update(key(tea.KeyEnter, ""))
	if m.output != original || m.Closed() {
		t.Fatal("discard must restore field and return to navigation")
	}
}

func TestExportLogs_RangeEditKeysAndFormatHint(t *testing.T) {
	for _, code := range []rune{tea.KeyDown, tea.KeyRight, tea.KeyTab} {
		m := NewExportLogsModel(ExportLogsOptions{Now: exportTestNow})
		m.Open()
		m.Update(key(tea.KeyEnter, ""))
		if m.rangeKind != logging.RangeLast24Hours {
			t.Fatal("enter must begin editing without changing range")
		}
		m.Update(key(code, ""))
		m.Update(key(code, ""))
		if m.rangeKind != logging.RangeBetween {
			t.Fatalf("key %v did not cycle range", code)
		}
		view := m.View(140, 35)
		if !strings.Contains(view, "Use YYYY-MM-DD HH:MM format") {
			t.Fatal("missing inline format hint")
		}
		m.Update(key(tea.KeyEnter, ""))
		m.Update(key(tea.KeyDown, ""))
		if m.focus != exportFocusFrom {
			t.Fatal("confirmed Range did not expose From navigation")
		}
	}
}

func TestExportLogs_CurrentTimeRefreshesWithoutChangingDraft(t *testing.T) {
	now := exportTestNow()
	m := NewExportLogsModel(ExportLogsOptions{Now: func() time.Time { return now }})
	cmd, _ := m.Update(OpenExportLogsMsg{})
	if cmd == nil {
		t.Fatal("opening must schedule a clock refresh")
	}
	from, to, output := m.from, m.to, m.output
	now = now.Add(3 * time.Second)
	// A redraw must show current time, even before the next periodic refresh.
	view := m.View(140, 35)
	if !strings.Contains(view, "Current Time") || !strings.Contains(view, "23:41:11") {
		t.Fatal("current time stayed frozen")
	}
	if m.from != from || m.to != to || m.output != output {
		t.Fatal("clock refresh changed draft")
	}
}

func TestExportLogs_ClockStopsAndIgnoresOldDialog(t *testing.T) {
	m := NewExportLogsModel(ExportLogsOptions{Now: exportTestNow})
	m.Update(OpenExportLogsMsg{})
	old := exportClockMsg{m.clockGeneration}
	if cmd, consumed := m.Update(old); cmd == nil || !consumed {
		t.Fatal("active clock stopped")
	}
	m.Update(key(tea.KeyEsc, ""))
	if cmd, _ := m.Update(old); cmd != nil {
		t.Fatal("closed dialog rescheduled clock")
	}
	m.Update(OpenExportLogsMsg{})
	if cmd, _ := m.Update(old); cmd != nil {
		t.Fatal("old clock joined reopened dialog")
	}
	m.pending = true
	if cmd, _ := m.Update(OpenExportLogsMsg{}); cmd != nil {
		t.Fatal("pending reopen duplicated clock")
	}
}

func TestExportLogs_RangeEditingAllowsGlobalQuit(t *testing.T) {
	m := NewExportLogsModel(ExportLogsOptions{Now: exportTestNow})
	m.Open()
	m.Update(key(tea.KeyEnter, ""))
	if _, consumed := m.Update(key('q', "q")); consumed {
		t.Fatal("range editing swallowed global quit")
	}
}
func TestExportLogs_CompletedResultStopsClock(t *testing.T) {
	m := NewExportLogsModel(ExportLogsOptions{Now: exportTestNow})
	m.Open()
	m.resultPath = "export.zip"
	if cmd, _ := m.Update(exportClockMsg{m.clockGeneration}); cmd != nil {
		t.Fatal("completed result kept clock running")
	}
}
