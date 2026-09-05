package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/logging"
)

func TestExportLogsModel_ClipboardShortcutRequestsAndConsumesPaste(t *testing.T) {
	for _, focus := range []exportFocus{exportFocusFrom, exportFocusTo, exportFocusOutput} {
		m := NewExportLogsModel(ExportLogsOptions{})
		m.Open()
		m.rangeKind, m.focus = logging.RangeBetween, focus
		cmd, consumed := m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
		if cmd == nil || !consumed {
			t.Fatalf("focus %v swallowed clipboard command", focus)
		}
		// Never execute the OS clipboard command in a test.
		_, consumed = m.Update(ClipboardPasteMsg{Text: "pasted"})
		values := map[exportFocus]string{exportFocusFrom: m.from, exportFocusTo: m.to, exportFocusOutput: m.output}
		if !consumed || !strings.HasSuffix(values[focus], "pasted") {
			t.Fatalf("focus %v did not consume/insert clipboard result", focus)
		}
	}
}

func TestExportLogsModel_SuccessRemindsReviewBeforeSharing(t *testing.T) {
	m := NewExportLogsModel(ExportLogsOptions{Export: func(context.Context, logging.ExportRequest) (logging.ExportResult, error) {
		return logging.ExportResult{Path: "/exports/result.zip"}, nil
	}})
	t.Cleanup(m.CancelAndWait)
	m.Open()
	m.focus = exportFocusOutput
	cmd, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	view := m.View(120, 35)
	for _, text := range []string{"Review before sharing", "node names", "domains/IPs", "traffic metadata"} {
		if !strings.Contains(view, text) {
			t.Errorf("missing %q in success view", text)
		}
	}
}
