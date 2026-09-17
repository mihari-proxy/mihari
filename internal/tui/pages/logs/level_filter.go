package logs

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type levelSelection uint8

const allLevels levelSelection = 0b1111

var selectableLevels = [...]string{"debug", "info", "warn", "error"}
var levelLabels = [...]string{"DEBUG", "INFO", "WARNING", "ERROR"}

func selectionForLevel(level string) levelSelection {
	if level == "" {
		return allLevels
	}
	for i, value := range selectableLevels {
		if normalizeLevel(level) == value {
			return 1 << i
		}
	}
	return 0
}

func (s levelSelection) matches(level string) bool {
	// Full selection preserves the former All filter, including unknown levels.
	return s == allLevels || (s&selectionForLevel(level) != 0 && level != "")
}

func (m *Model) renderLevelSummary() string {
	var parts []string
	for i, label := range levelLabels {
		bit := levelSelection(1 << i)
		if m.levels&bit == 0 {
			continue
		}
		// Compress only a consecutive suffix that includes every level through ERROR.
		if m.levels == allLevels & ^(bit-1) && i < len(levelLabels)-1 {
			return ui.StyleLogLevel(m.theme, label) + "+"
		}
		parts = append(parts, ui.StyleLogLevel(m.theme, label))
	}
	return strings.Join(parts, ", ")
}

type levelDialogState struct {
	draft   levelSelection
	cursor  int
	invalid bool
}

// HasLevelDialog reports whether the log display filter owns navigation keys.
func (m *Model) HasLevelDialog() bool { return m.levelDialog != nil }

func (m *Model) openLevelDialog() {
	m.levelDialog = &levelDialogState{draft: m.levels}
	if m.levels != allLevels {
		for i := range selectableLevels {
			if m.levels&(1<<i) != 0 {
				m.levelDialog.cursor = i + 1
				break
			}
		}
	}
}

func (m *Model) updateLevelDialog(message tea.Msg) (ui.Page, tea.Cmd) {
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	dialog := m.levelDialog
	switch key.String() {
	case "up":
		dialog.cursor = max(0, dialog.cursor-1)
	case "down":
		dialog.cursor = min(len(selectableLevels), dialog.cursor+1)
	case "space":
		if dialog.cursor == 0 {
			if dialog.draft == allLevels {
				dialog.draft = 0
			} else {
				dialog.draft = allLevels
			}
		} else {
			dialog.draft ^= 1 << (dialog.cursor - 1)
		}
		dialog.invalid = false
	case "esc":
		m.levelDialog = nil
	case "enter":
		if dialog.draft == 0 {
			dialog.invalid = true
			return m, nil
		}
		m.levels = dialog.draft
		m.levelDialog = nil
		m.reconcileFocus()
		m.focus, m.controlIndex = focusControl, 0
	}
	return m, nil
}

func (m *Model) renderLevelDialog(background string) string {
	width := max(1, m.layoutWidth()-m.theme.Content.GetHorizontalPadding())
	height := m.height
	if height <= 0 {
		height = 20
	}
	dialog := m.levelDialog
	style := m.theme.Dialog.Padding(0, 1)
	innerWidth := max(1, min(42, width)-style.GetHorizontalFrameSize())
	bodyHeight := max(1, height-style.GetVerticalFrameSize())
	header := []string{m.theme.Title.Render("Log levels")}
	if dialog.invalid {
		header = append(header, m.theme.Warning.Render("Select at least one level"))
	}
	footer := []string{m.theme.Muted.Render("Space toggle"), m.theme.Muted.Render("Enter apply · Esc cancel")}
	rows := max(1, bodyHeight-len(header)-len(footer))
	start, end := ui.VisibleWindow(len(selectableLevels)+1, rows, 0, false, dialog.cursor)
	lines := append([]string(nil), header...)
	for i := start; i < end; i++ {
		checked, label := dialog.draft == allLevels, "Select all"
		if i > 0 {
			checked = dialog.draft&(1<<(i-1)) != 0
			label = ui.StyleLogLevel(m.theme, levelLabels[i-1])
		}
		check := "[ ] "
		if checked {
			check = "[x] "
		} else if i == 0 && dialog.draft != 0 {
			check = "[-] "
		}
		line := ui.PadCell(ui.FocusPrefix(i == dialog.cursor)+check+label, innerWidth, ui.AlignLeft)
		if i == dialog.cursor {
			line = ui.ApplyFocusStyle(line, m.theme.RowFocus)
		}
		lines = append(lines, line)
	}
	lines = append(lines, footer...)
	for i, line := range lines {
		lines[i] = ui.PadCell(line, innerWidth, ui.AlignLeft)
	}
	content := style.Width(innerWidth + style.GetHorizontalPadding()).Render(strings.Join(lines, "\n"))
	return ui.CenterOverlay(m.theme, background, content, width, height)
}
