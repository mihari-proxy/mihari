package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestEditTextField_PasteMsgAndClipboardMsg(t *testing.T) {
	content := "hello" + "\n" + "world" + "\r\n" + "paste"
	value, handled, command := EditTextField("pre-", tea.PasteMsg{Content: content}, 64)
	if !handled || command != nil || value != "pre-helloworldpaste" {
		t.Fatalf("paste value=%q handled=%v command=%v", value, handled, command != nil)
	}
	value, handled, command = EditTextField("x", ClipboardPasteMsg{Text: " y\tz"}, 64)
	if !handled || command != nil || value != "x y z" {
		t.Fatalf("clipboard value=%q handled=%v command=%v", value, handled, command != nil)
	}
}

func TestEditTextField_CtrlVRequestsClipboardRead(t *testing.T) {
	value, handled, command := EditTextField("keep", tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}, 64)
	if !handled || value != "keep" || command == nil {
		t.Fatalf("ctrl+v value=%q handled=%v command=%v", value, handled, command != nil)
	}
	if _, ok := command().(ClipboardPasteMsg); !ok {
		t.Fatalf("command message=%T", command())
	}
}

func TestEditTextField_TypingBackspaceAndLimit(t *testing.T) {
	value, handled, _ := EditTextField("", tea.KeyPressMsg{Code: 'a', Text: "a"}, 2)
	if !handled || value != "a" {
		t.Fatalf("type value=%q handled=%v", value, handled)
	}
	value, handled, _ = EditTextField(value, tea.KeyPressMsg{Code: 'b', Text: "b"}, 2)
	if !handled || value != "ab" {
		t.Fatalf("type2 value=%q handled=%v", value, handled)
	}
	value, handled, _ = EditTextField(value, tea.KeyPressMsg{Code: 'c', Text: "c"}, 2)
	if !handled || value != "ab" {
		t.Fatalf("limit value=%q handled=%v", value, handled)
	}
	value, handled, _ = EditTextField(value, tea.KeyPressMsg{Code: tea.KeyBackspace}, 2)
	if !handled || value != "a" {
		t.Fatalf("backspace value=%q handled=%v", value, handled)
	}
	value, handled, _ = EditTextField(value, tea.KeyPressMsg{Code: tea.KeyEnter}, 2)
	if handled || value != "a" {
		t.Fatalf("enter should not be consumed: value=%q handled=%v", value, handled)
	}
}

func TestEditTextField_PasteRespectsLimit(t *testing.T) {
	value, handled, _ := EditTextField("ab", tea.PasteMsg{Content: strings.Repeat("x", 10)}, 4)
	if !handled || value != "abxx" {
		t.Fatalf("value=%q handled=%v", value, handled)
	}
}
