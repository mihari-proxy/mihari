package tui

import (
	tea "charm.land/bubbletea/v2"
	setuppage "github.com/mihari-proxy/mihari/internal/tui/pages/setup"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Keep delayed setup results on their owning page even after navigation.
func setupCommand(command tea.Cmd) tea.Cmd {
	if command == nil {
		return nil
	}
	return func() tea.Msg {
		result := command()
		switch value := result.(type) {
		case tea.BatchMsg:
			for i := range value {
				value[i] = setupCommand(value[i])
			}
			return value
		case ui.ConfirmationRequestMsg, ui.ErrorDetailMsg, ui.OpenHelpMsg, ui.ActionIntentMsg,
			setuppage.ReadyMsg, setuppage.CompletedMsg, setuppage.CancelledMsg, tea.QuitMsg:
			return result
		default:
			return ui.PageResultMsg{Page: ui.PageSetup, Result: result}
		}
	}
}
