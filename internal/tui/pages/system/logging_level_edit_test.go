package system

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func levelKey(m *Model, code rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return cmd
}

func levelContent(m *Model) string {
	lines, _, _ := m.buildSectionContent()
	return strings.Join(lines, "\n")
}

func TestLoggingLevelEdit_EnterMovesFocusWithoutPatch(t *testing.T) {
	m, client := loggingModel("info", 4)
	m.focusID = rowLogLevel
	m.SetContentFocused(true)
	cmd := levelKey(m, tea.KeyEnter)
	if m.editID != rowLogLevel || m.pending {
		t.Fatalf("edit=%q pending=%v; first Enter must only edit", m.editID, m.pending)
	}
	if cmd == nil || cmd() != (ui.InputModeMsg{Mode: ui.InputText}) {
		t.Fatal("editor did not claim shell input")
	}
	view := levelContent(m)
	if !strings.Contains(view, ui.ApplyFocusStyle("< INFO >", m.theme.RowFocus)) || strings.Contains(view, ui.FocusMarker+"Level") {
		t.Fatalf("focus must move from label to complete candidate:\n%s", view)
	}
	if client.updateLoggingCalls != 0 || !strings.Contains(m.FooterHints(), "←/→") || strings.Contains(m.FooterHints(), "Type") {
		t.Fatalf("calls=%d footer=%q", client.updateLoggingCalls, m.FooterHints())
	}
}

func TestLoggingLevelEdit_CyclesPreviewAndSilentEntry(t *testing.T) {
	for _, tc := range []struct {
		start, want string
		key         rune
	}{
		{"debug", "INFO", tea.KeyRight}, {"info", "WARN", tea.KeyRight},
		{"warn", "ERROR", tea.KeyRight}, {"error", "DEBUG", tea.KeyRight},
		{"debug", "ERROR", tea.KeyLeft}, {"info", "DEBUG", tea.KeyLeft},
		{"warn", "INFO", tea.KeyLeft}, {"error", "WARN", tea.KeyLeft},
		{"silent", "DEBUG", tea.KeyRight}, {"silent", "ERROR", tea.KeyLeft},
	} {
		t.Run(tc.start+" to "+tc.want, func(t *testing.T) {
			m, client := loggingModel(tc.start, 0)
			m.focusID = rowLogLevel
			levelKey(m, tea.KeyEnter)
			if !strings.Contains(levelContent(m), "< "+strings.ToUpper(tc.start)+" >") {
				t.Fatal("editor did not start at actual level")
			}
			levelKey(m, tc.key)
			if !strings.Contains(levelContent(m), "< "+tc.want+" >") || m.logging.Level != tc.start || m.pending || client.updateLoggingCalls != 0 {
				t.Fatalf("preview changed actual state or missed candidate:\n%s", levelContent(m))
			}
			observed := loggingObservedFromCommand(t, levelKey(m, tea.KeyEnter))
			if observed.Status.Level != strings.ToLower(tc.want) || client.lastLogging.IfRevision == nil || *client.lastLogging.IfRevision != 0 {
				t.Fatalf("request=%+v observed=%+v", client.lastLogging, observed)
			}
		})
	}
}

func TestLoggingLevelEdit_CancelAndUnchangedDoNotPatch(t *testing.T) {
	for _, level := range []string{"info", "silent"} {
		m, client := loggingModel(level, 4)
		m.focusID = rowLogLevel
		levelKey(m, tea.KeyEnter)
		for _, key := range []rune{tea.KeyUp, tea.KeyDown, tea.KeyTab} {
			if cmd := levelKey(m, key); cmd != nil || m.focusID != rowLogLevel || m.editID != rowLogLevel {
				t.Fatal("navigation escaped editor")
			}
		}
		cmd := levelKey(m, tea.KeyEnter)
		if m.editID != "" || cmd == nil || cmd() != (ui.InputModeMsg{Mode: ui.InputNavigation}) || client.updateLoggingCalls != 0 {
			t.Fatal("unchanged confirmation did not leave without patch")
		}
		levelKey(m, tea.KeyEnter)
		levelKey(m, tea.KeyRight)
		cmd = levelKey(m, tea.KeyEsc)
		if m.editID != "" || m.logging.Level != level || cmd == nil || cmd() != (ui.InputModeMsg{Mode: ui.InputNavigation}) || client.updateLoggingCalls != 0 {
			t.Fatal("cancel changed actual level or retained editor")
		}
	}
}

func TestLoggingLevelEdit_ExternalChangePreservesCandidate(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		m, client := loggingModel("info", 4)
		m.focusID = rowLogLevel
		levelKey(m, tea.KeyEnter)
		levelKey(m, tea.KeyRight)
		latest := m.logging
		latest.Level, latest.Revision = "error", 8
		m.Update(ui.LoggingSyncMsg{Epoch: 7, Available: true, Status: latest})
		if !strings.Contains(levelContent(m), "< WARN >") {
			t.Fatal("external observation overwrote candidate")
		}
		if cancel {
			levelKey(m, tea.KeyEsc)
			if m.editID != "" || !strings.Contains(levelContent(m), "error") || client.updateLoggingCalls != 0 {
				t.Fatal("cancel did not reveal latest actual value")
			}
		} else {
			loggingObservedFromCommand(t, levelKey(m, tea.KeyEnter))
			if *client.lastLogging.Level != "warn" || *client.lastLogging.IfRevision != 8 {
				t.Fatalf("request=%+v", client.lastLogging)
			}
		}
	}
}

func TestLoggingLevelEdit_ApplyingLocksInputAndSuccessReturnsFocus(t *testing.T) {
	m, _ := loggingModel("info", 4)
	m.focusID = rowLogLevel
	levelKey(m, tea.KeyEnter)
	levelKey(m, tea.KeyRight)
	cmd := levelKey(m, tea.KeyEnter)
	before := levelContent(m)
	if !m.pending || !strings.Contains(before, "Applying") || strings.Contains(before, "< WARN >") {
		t.Fatalf("missing Applying state:\n%s", before)
	}
	for _, key := range []rune{tea.KeyEnter, tea.KeyEsc, tea.KeyLeft, tea.KeyRight, tea.KeyUp, tea.KeyDown, tea.KeyTab} {
		if levelKey(m, key) != nil || m.editID != rowLogLevel || m.focusID != rowLogLevel || !m.pending {
			t.Fatal("input changed editor during Applying")
		}
	}
	_, tick := m.Update(rowSpinTickMsg{t: time.Unix(0, 0).Add(rowSpinInterval), gen: m.rowSpinGen})
	if tick == nil {
		t.Fatal("Applying did not schedule another animation frame")
	}
	if before == levelContent(m) {
		t.Fatal("Applying spinner did not change frame")
	}
	observed := loggingObservedFromCommand(t, cmd)
	m.Update(ui.LoggingSyncMsg{Epoch: observed.Epoch, Available: true, Status: observed.Status})
	_, done := m.Update(observed)
	if m.editID != "" || m.pending || m.focusID != rowLogLevel || m.logging.Level != "warn" || !m.outcomeOK || done == nil {
		t.Fatalf("success edit=%q pending=%v level=%q", m.editID, m.pending, m.logging.Level)
	}
}

func TestLoggingLevelEdit_FailureKeepsCandidateForRetry(t *testing.T) {
	m, client := loggingModel("info", 4)
	client.updateLoggingErr = errors.New("synthetic write failure")
	m.focusID = rowLogLevel
	levelKey(m, tea.KeyEnter)
	levelKey(m, tea.KeyRight)
	m.Update(firstSystemPageResult(t, levelKey(m, tea.KeyEnter)))
	view := levelContent(m)
	if m.pending || m.editID != rowLogLevel || !strings.Contains(view, "< WARN >") || !strings.Contains(view, ui.LoggingUpdateFailed) {
		t.Fatalf("failed editor lost candidate or error:\n%s", view)
	}
	client.updateLoggingErr = nil
	loggingObservedFromCommand(t, levelKey(m, tea.KeyEnter))
	if client.updateLoggingCalls != 2 || *client.lastLogging.Level != "warn" || !m.pending || strings.Contains(levelContent(m), ui.LoggingUpdateFailed) {
		t.Fatal("retry did not submit same candidate and clear old error")
	}
}

func TestLoggingLevelEdit_ConflictReloadKeepsCandidateAndShowsError(t *testing.T) {
	m, client := loggingModel("info", 4)
	client.updateLoggingErr = protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "conflict"}
	client.logging.Level, client.logging.Revision = "error", 9
	m.focusID = rowLogLevel
	levelKey(m, tea.KeyEnter)
	levelKey(m, tea.KeyRight)
	_, reload := m.Update(firstSystemPageResult(t, levelKey(m, tea.KeyEnter)))
	observed := loggingObservedFromCommand(t, reload)
	m.Update(ui.LoggingSyncMsg{Epoch: observed.Epoch, Available: true, Status: observed.Status})
	m.Update(observed)
	view := levelContent(m)
	if m.pending || m.editID != rowLogLevel || !strings.Contains(view, "< WARN >") || !strings.Contains(view, ui.FailedLabel) || client.updateLoggingCalls != 1 {
		t.Fatalf("conflict must retain draft and report error without replay:\n%s", view)
	}
	client.updateLoggingErr = nil
	loggingObservedFromCommand(t, levelKey(m, tea.KeyEnter))
	if *client.lastLogging.IfRevision != 9 || *client.lastLogging.Level != "warn" {
		t.Fatalf("retry request=%+v", client.lastLogging)
	}
}

func TestLoggingLevelEdit_UnavailableClearsDraftAndPending(t *testing.T) {
	for _, pending := range []bool{false, true} {
		m, _ := loggingModel("info", 4)
		m.focusID = rowLogLevel
		levelKey(m, tea.KeyEnter)
		levelKey(m, tea.KeyRight)
		if pending {
			levelKey(m, tea.KeyEnter)
		}
		_, cmd := m.Update(ui.LoggingSyncMsg{Epoch: 8, Available: false})
		if m.editID != "" || m.pending || cmd == nil || cmd() != (ui.InputModeMsg{Mode: ui.InputNavigation}) || strings.Contains(levelContent(m), "< WARN >") {
			t.Fatal("unavailability retained editor or shell input mode")
		}
	}
}

func TestLoggingLevelEdit_SupersededConflictReloadStillReportsConflict(t *testing.T) {
	m, client := loggingModel("info", 4)
	client.updateLoggingErr = protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "conflict"}
	client.logging.Level, client.logging.Revision = "error", 9
	m.focusID = rowLogLevel
	levelKey(m, tea.KeyEnter)
	levelKey(m, tea.KeyRight)
	_, reload := m.Update(firstSystemPageResult(t, levelKey(m, tea.KeyEnter)))
	observed := loggingObservedFromCommand(t, reload)
	// A newer session observation wins before the GET response reaches the page.
	latest := observed.Status
	latest.Level, latest.Revision = "debug", 10
	m.Update(ui.LoggingSyncMsg{Epoch: observed.Epoch, Available: true, Status: latest})
	m.Update(observed)
	view := levelContent(m)
	if m.pending || m.logging.Revision != 10 || m.logging.Level != "debug" || !strings.Contains(view, "< WARN >") || !strings.Contains(view, ui.SystemChangedMessage) {
		t.Fatalf("superseded reload lost conflict feedback or rolled back current state:\n%s", view)
	}
	if client.updateLoggingCalls != 1 {
		t.Fatal("conflict automatically replayed mutation")
	}
}
