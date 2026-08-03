package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func routedKey(key tea.KeyPressMsg, mode ui.InputMode) string {
	name := key.String()
	if mode == ui.InputText {
		return name
	}
	switch name {
	case "h":
		return "left"
	case "j":
		return "down"
	case "k":
		return "up"
	case "l":
		return "right"
	default:
		return name
	}
}

func routedMessage(message tea.Msg, mode ui.InputMode) tea.Msg {
	key, ok := message.(tea.KeyPressMsg)
	if !ok || mode == ui.InputText {
		return message
	}
	code := rune(0)
	switch key.String() {
	case "h":
		code = tea.KeyLeft
	case "j":
		code = tea.KeyDown
	case "k":
		code = tea.KeyUp
	case "l":
		code = tea.KeyRight
	default:
		return message
	}
	return tea.KeyPressMsg{Code: code}
}
