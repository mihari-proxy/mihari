package ui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

// ClipboardPasteMsg delivers text read from the system clipboard for plain text fields.
type ClipboardPasteMsg struct{ Text string }

// ReadClipboard is a command that reads the system clipboard for paste into text fields.
func ReadClipboard() tea.Msg {
	text, err := clipboard.ReadAll()
	if err != nil {
		return ClipboardPasteMsg{}
	}
	return ClipboardPasteMsg{Text: text}
}

// EditTextField applies typing, backspace, and paste to a single-line text field.
// It returns the updated value, whether the message was consumed, and an optional command
// (for example clipboard read on ctrl+v). Callers still handle enter/esc and navigation keys.
func EditTextField(value string, message tea.Msg, maxRunes int) (string, bool, tea.Cmd) {
	if maxRunes <= 0 {
		maxRunes = 2048
	}
	switch msg := message.(type) {
	case tea.PasteMsg:
		return appendLimited(value, sanitizePaste(msg.Content), maxRunes), true, nil
	case ClipboardPasteMsg:
		return appendLimited(value, sanitizePaste(msg.Text), maxRunes), true, nil
	case tea.KeyPressMsg:
		if isPasteKey(msg) {
			return value, true, ReadClipboard
		}
		switch msg.String() {
		case "backspace":
			if value == "" {
				return value, true, nil
			}
			_, size := utf8.DecodeLastRuneInString(value)
			return value[:len(value)-size], true, nil
		default:
			if msg.Text != "" && msg.Mod == 0 {
				return appendLimited(value, msg.Text, maxRunes), true, nil
			}
		}
	}
	return value, false, nil
}

func isPasteKey(msg tea.KeyPressMsg) bool {
	if msg.String() == "ctrl+v" {
		return true
	}
	// Some terminals report ctrl+v with Code 'v' and ModCtrl while String() is still "v".
	return msg.Mod&tea.ModCtrl != 0 && (msg.Code == 'v' || msg.Code == 'V')
}

func sanitizePaste(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\n", "")
	text = strings.ReplaceAll(text, "\t", " ")
	return text
}

func appendLimited(value, addition string, maxRunes int) string {
	if addition == "" {
		return value
	}
	current := utf8.RuneCountInString(value)
	if current >= maxRunes {
		return value
	}
	room := maxRunes - current
	runes := []rune(addition)
	if len(runes) > room {
		runes = runes[:room]
	}
	return value + string(runes)
}
