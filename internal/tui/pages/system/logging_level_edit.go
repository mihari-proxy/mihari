package system

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func (m *Model) beginLoggingLevelEdit() tea.Cmd {
	if !m.loggingMutationAvailable() || m.pending {
		return nil
	}
	m.clearLoggingOutcome(rowLogLevel)
	m.editID = rowLogLevel
	m.loggingLevelCandidate = m.logging.Level
	return func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }
}

func (m *Model) updateLoggingLevelEdit(message tea.Msg) (ui.Page, tea.Cmd) {
	key, ok := message.(tea.KeyPressMsg)
	if !ok || m.pending {
		return m, nil
	}
	switch key.String() {
	case "esc":
		return m, m.cancelLoggingEdit()
	case "left", "right":
		levels := []string{"debug", "info", "warn", "error"}
		index := slices.Index(levels, m.loggingLevelCandidate)
		if key.String() == "right" {
			index = (index + 1) % len(levels)
		} else if index < 0 {
			index = len(levels) - 1
		} else {
			index = (index + len(levels) - 1) % len(levels)
		}
		m.loggingLevelCandidate = levels[index]
		m.clearLoggingOutcome(rowLogLevel)
	case "enter":
		if m.loggingLevelCandidate == m.logging.Level || m.loggingLevelCandidate == "silent" {
			return m, m.cancelLoggingEdit()
		}
		// Copy the candidate so later UI input cannot change an in-flight request.
		level := m.loggingLevelCandidate
		return m, m.startLoggingUpdate(rowLogLevel, protocol.LoggingUpdateRequest{
			OperationID: m.newOperationID(), IfRevision: loggingRevisionPointer(m.logging.Revision), Level: &level,
		})
	}
	return m, nil
}

func (m *Model) loggingLevelEditorView(clock time.Time) string {
	if m.pending {
		return ui.RenderStatusChip(m.theme, ui.StatusChipPending, ui.SpinnerLabel(clock, m.pendingNote))
	}
	value := "< " + strings.ToUpper(m.loggingLevelCandidate) + " >"
	if m.contentFocused {
		value = ui.ApplyFocusStyle(value, m.theme.RowFocus)
	}
	if m.outcomeRow == rowLogLevel && !m.outcomeOK {
		value += "  " + ui.RenderStatusChip(m.theme, ui.StatusChipFailed, ui.FailedLabel)
		if m.outcomeDetail != "" {
			value += "  " + m.theme.Danger.Render(ui.TruncateVisible(m.outcomeDetail, 48))
		}
	}
	return value
}
