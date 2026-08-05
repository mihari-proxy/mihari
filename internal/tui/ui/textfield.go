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

// EditTextField applies typing, backspace, paste, and left/right cursor movement to a
// single-line text field. cursor is a rune offset; it is clamped to [0, runeCount].
// Returns the updated value, cursor, whether the message was consumed, and an optional
// command (for example clipboard read on ctrl+v). Callers still handle esc and vertical
// navigation keys.
func EditTextField(value string, cursor int, message tea.Msg, maxRunes int) (string, int, bool, tea.Cmd) {
	if maxRunes <= 0 {
		maxRunes = 2048
	}
	cursor = clampCursor(value, cursor)
	switch msg := message.(type) {
	case tea.PasteMsg:
		return insertLimited(value, cursor, sanitizePaste(msg.Content), maxRunes)
	case ClipboardPasteMsg:
		return insertLimited(value, cursor, sanitizePaste(msg.Text), maxRunes)
	case tea.KeyPressMsg:
		if isPasteKey(msg) {
			return value, cursor, true, ReadClipboard
		}
		switch msg.String() {
		case "left":
			if cursor > 0 {
				cursor--
			}
			return value, cursor, true, nil
		case "right":
			if cursor < utf8.RuneCountInString(value) {
				cursor++
			}
			return value, cursor, true, nil
		case "home", "ctrl+a":
			return value, 0, true, nil
		case "end", "ctrl+e":
			return value, utf8.RuneCountInString(value), true, nil
		case "backspace":
			if cursor == 0 || value == "" {
				return value, cursor, true, nil
			}
			before, after := splitAtCursor(value, cursor)
			_, size := utf8.DecodeLastRuneInString(before)
			before = before[:len(before)-size]
			return before + after, cursor - 1, true, nil
		case "delete", "ctrl+d":
			if cursor >= utf8.RuneCountInString(value) {
				return value, cursor, true, nil
			}
			before, after := splitAtCursor(value, cursor)
			_, size := utf8.DecodeRuneInString(after)
			return before + after[size:], cursor, true, nil
		default:
			if msg.Text != "" && msg.Mod == 0 {
				return insertLimited(value, cursor, msg.Text, maxRunes)
			}
		}
	}
	return value, cursor, false, nil
}

// IsTextEditMsg reports whether message is consumed by EditTextField as a text edit
// (printable character, backspace, paste, or horizontal cursor movement).
func IsTextEditMsg(message tea.Msg) bool {
	switch msg := message.(type) {
	case tea.PasteMsg, ClipboardPasteMsg:
		return true
	case tea.KeyPressMsg:
		if isPasteKey(msg) {
			return true
		}
		switch msg.String() {
		case "left", "right", "home", "end", "ctrl+a", "ctrl+e", "backspace", "delete", "ctrl+d":
			return true
		default:
			return msg.Text != "" && msg.Mod == 0
		}
	}
	return false
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

func insertLimited(value string, cursor int, addition string, maxRunes int) (string, int, bool, tea.Cmd) {
	if addition == "" {
		return value, cursor, true, nil
	}
	current := utf8.RuneCountInString(value)
	if current >= maxRunes {
		return value, cursor, true, nil
	}
	room := maxRunes - current
	runes := []rune(addition)
	if len(runes) > room {
		runes = runes[:room]
	}
	addition = string(runes)
	before, after := splitAtCursor(value, cursor)
	return before + addition + after, cursor + len(runes), true, nil
}

func splitAtCursor(value string, cursor int) (before, after string) {
	cursor = clampCursor(value, cursor)
	if cursor == 0 {
		return "", value
	}
	runes := []rune(value)
	if cursor >= len(runes) {
		return value, ""
	}
	return string(runes[:cursor]), string(runes[cursor:])
}

func clampCursor(value string, cursor int) int {
	if cursor < 0 {
		return 0
	}
	n := utf8.RuneCountInString(value)
	if cursor > n {
		return n
	}
	return cursor
}
