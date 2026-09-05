package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestExportLogsModel_DefaultPreviewBoundsProbes(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "occupied", true: "error"}[fail], func(t *testing.T) {
			calls := 0
			m := NewExportLogsModel(ExportLogsOptions{DefaultDir: t.TempDir(), Exists: func(string, string) (bool, error) {
				calls++
				if calls > 16 {
					t.Fatal("preview blocks UI with unbounded existence probes")
				}
				if fail {
					return false, errors.New("probe unavailable")
				}
				return true, nil
			}})
			got := m.defaultPath(time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC))
			if filepath.Ext(got) != ".zip" || calls == 0 {
				t.Fatalf("missing fallback candidate: %q calls=%d", got, calls)
			}
			if fail && calls != 1 {
				t.Fatalf("retried failed preview %d times", calls)
			}
		})
	}
}

func TestExportLogsModel_WrappedDeadlineReturnsToEditableForm(t *testing.T) {
	m := NewExportLogsModel(ExportLogsOptions{})
	m.Open()
	m.pending, m.generation, m.focus = true, 1, exportFocusOutput
	m.Update(exportResultMsg{Generation: 1, Err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded), Warning: true})
	if m.pending || m.message != ExportCancelled || !m.warning {
		t.Fatal("deadline did not retain cancellation/warning state")
	}
	before := m.output
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.output == before {
		t.Fatal("deadline did not return editable form")
	}
}

func TestExportLogsModel_PendingQuitRespectsTextFocus(t *testing.T) {
	for _, focus := range []exportFocus{exportFocusRange, exportFocusFrom, exportFocusTo, exportFocusOutput} {
		m := NewExportLogsModel(ExportLogsOptions{})
		m.Open()
		m.pending, m.focus = true, focus
		_, consumed := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
		if consumed != (focus != exportFocusRange) {
			t.Fatalf("focus=%v consumed quit=%v", focus, consumed)
		}
	}
}
