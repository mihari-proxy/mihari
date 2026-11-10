package system

import (
	"reflect"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func (m *Model) authDirty() bool {
	a := &m.auth
	if !reflect.DeepEqual(a.draft, a.status.Config) {
		return true
	}
	return m.authEditorDirty()
}

func (m *Model) authEditorDirty() bool {
	a := &m.auth
	switch a.edit {
	case "user":
		return a.inputs[1].Value() != "" || (a.original == "" && a.inputs[0].Value() != "")
	case "prefix":
		return strings.TrimSpace(a.inputs[0].Value()) != a.original
	}
	return false
}

func (m *Model) closeAuth() tea.Cmd {
	a := &m.auth
	if a.previewCancel != nil {
		a.previewCancel()
	}
	if a.previewTimerCancel != nil {
		a.previewTimerCancel()
	}
	epoch := a.epoch + 1
	*a = authUI{epoch: epoch}
	m.rowSpinCmdIfNeeded()
	return func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
}

func (m *Model) requestAuthSave(closeAfter bool) tea.Cmd {
	a := &m.auth
	a.exitOpen = false
	a.closeAfterSave = closeAfter
	if a.edit != "" {
		if !m.authEditorDirty() {
			a.edit = ""
		} else {
			cmd := m.updateAuthControls(tea.KeyPressMsg{Code: tea.KeyEnter})
			if a.edit != "" {
				a.closeAfterSave = false
				return cmd
			}
		}
	}
	return m.authSave(false)
}

func (m *Model) authExitKey(key string) tea.Cmd {
	a := &m.auth
	switch key {
	case "esc":
		a.exitOpen = false
	case "left", "up", "shift+tab":
		a.exitChoice = (a.exitChoice + 2) % 3
	case "right", "down", "tab":
		a.exitChoice = (a.exitChoice + 1) % 3
	case "ctrl+s":
		return m.requestAuthSave(true)
	case "enter", "space":
		switch a.exitChoice {
		case 0:
			return m.requestAuthSave(true)
		case 1:
			return m.closeAuth()
		default:
			a.exitOpen = false
		}
	}
	return nil
}

func (m *Model) authExitView(width int) string {
	width = max(6, min(68, width))
	choices := []string{"[Save]", "[Discard]", "[Keep editing]"}
	for i := range choices {
		if m.auth.exitChoice == i {
			choices[i] = m.theme.ButtonActive.Render(choices[i])
		}
	}
	body := "Save changes?\n\nYou have unsaved authentication changes.\n\n" + strings.Join(choices, "  ") + "\n\n←/→ or Tab select · Enter confirm · Esc keep editing"
	body = ansi.Wrap(body, max(1, width-m.theme.Dialog.GetHorizontalFrameSize()), "")
	return m.theme.Dialog.Width(width).Render(body)
}

func (m *Model) authErrorLines(width int) []string {
	a := &m.auth
	if a.err == "" {
		return nil
	}
	if a.errorText != a.err || a.errorWidth != width {
		a.errorText, a.errorWidth = a.err, width
		a.errorLines = strings.Split(ansi.Wrap(diagnostics.EscapeTerminal(a.err), max(1, width), ""), "\n")
	}
	limit := max(2, m.height/3)
	lines := a.errorLines
	if len(lines) > limit {
		lines = append(append([]string{}, lines[:limit-1]...), "F2: full error details")
	}
	styled := make([]string, len(lines))
	for i, line := range lines {
		styled[i] = m.theme.Danger.Render(line)
	}
	return styled
}
