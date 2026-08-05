package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

func TestEditTextField_PasteMsgAndClipboardMsg(t *testing.T) {
	content := "hello" + "\n" + "world" + "\r\n" + "paste"
	value, cursor, handled, command := EditTextField("pre-", 4, tea.PasteMsg{Content: content}, 64)
	if !handled || command != nil || value != "pre-helloworldpaste" || cursor != utf8.RuneCountInString(value) {
		t.Fatalf("paste value=%q cursor=%d handled=%v command=%v", value, cursor, handled, command != nil)
	}
	value, cursor, handled, command = EditTextField("x", 1, ClipboardPasteMsg{Text: " y\tz"}, 64)
	if !handled || command != nil || value != "x y z" || cursor != 5 {
		t.Fatalf("clipboard value=%q cursor=%d handled=%v command=%v", value, cursor, handled, command != nil)
	}
}

func TestEditTextField_CtrlVRequestsClipboardRead(t *testing.T) {
	value, cursor, handled, command := EditTextField("keep", 4, tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}, 64)
	if !handled || value != "keep" || cursor != 4 || command == nil {
		t.Fatalf("ctrl+v value=%q cursor=%d handled=%v command=%v", value, cursor, handled, command != nil)
	}
	if _, ok := command().(ClipboardPasteMsg); !ok {
		t.Fatalf("command message=%T", command())
	}
}

func TestEditTextField_TypingBackspaceAndLimit(t *testing.T) {
	value, cursor, handled, _ := EditTextField("", 0, tea.KeyPressMsg{Code: 'a', Text: "a"}, 2)
	if !handled || value != "a" || cursor != 1 {
		t.Fatalf("type value=%q cursor=%d handled=%v", value, cursor, handled)
	}
	value, cursor, handled, _ = EditTextField(value, cursor, tea.KeyPressMsg{Code: 'b', Text: "b"}, 2)
	if !handled || value != "ab" || cursor != 2 {
		t.Fatalf("type2 value=%q cursor=%d handled=%v", value, cursor, handled)
	}
	value, cursor, handled, _ = EditTextField(value, cursor, tea.KeyPressMsg{Code: 'c', Text: "c"}, 2)
	if !handled || value != "ab" || cursor != 2 {
		t.Fatalf("limit value=%q cursor=%d handled=%v", value, cursor, handled)
	}
	value, cursor, handled, _ = EditTextField(value, cursor, tea.KeyPressMsg{Code: tea.KeyBackspace}, 2)
	if !handled || value != "a" || cursor != 1 {
		t.Fatalf("backspace value=%q cursor=%d handled=%v", value, cursor, handled)
	}
	value, cursor, handled, _ = EditTextField(value, cursor, tea.KeyPressMsg{Code: tea.KeyEnter}, 2)
	if handled || value != "a" || cursor != 1 {
		t.Fatalf("enter should not be consumed: value=%q cursor=%d handled=%v", value, cursor, handled)
	}
}

func TestEditTextField_PasteRespectsLimit(t *testing.T) {
	value, cursor, handled, _ := EditTextField("ab", 2, tea.PasteMsg{Content: strings.Repeat("x", 10)}, 4)
	if !handled || value != "abxx" || cursor != 4 {
		t.Fatalf("value=%q cursor=%d handled=%v", value, cursor, handled)
	}
}

func TestEditTextField_LeftRightInsertAndDelete(t *testing.T) {
	value, cursor, handled, _ := EditTextField("abc", 3, tea.KeyPressMsg{Code: tea.KeyLeft}, 16)
	if !handled || value != "abc" || cursor != 2 {
		t.Fatalf("left value=%q cursor=%d handled=%v", value, cursor, handled)
	}
	value, cursor, handled, _ = EditTextField(value, cursor, tea.KeyPressMsg{Code: 'X', Text: "X"}, 16)
	if !handled || value != "abXc" || cursor != 3 {
		t.Fatalf("insert mid value=%q cursor=%d handled=%v", value, cursor, handled)
	}
	value, cursor, handled, _ = EditTextField(value, cursor, tea.KeyPressMsg{Code: tea.KeyBackspace}, 16)
	if !handled || value != "abc" || cursor != 2 {
		t.Fatalf("backspace mid value=%q cursor=%d handled=%v", value, cursor, handled)
	}
	value, cursor, handled, _ = EditTextField(value, 0, tea.KeyPressMsg{Code: tea.KeyRight}, 16)
	if !handled || value != "abc" || cursor != 1 {
		t.Fatalf("right value=%q cursor=%d handled=%v", value, cursor, handled)
	}
	value, cursor, handled, _ = EditTextField(value, cursor, tea.KeyPressMsg{Code: tea.KeyDelete}, 16)
	if !handled || value != "ac" || cursor != 1 {
		t.Fatalf("delete value=%q cursor=%d handled=%v", value, cursor, handled)
	}
	value, cursor, handled, _ = EditTextField("hello", 3, tea.KeyPressMsg{Code: tea.KeyHome}, 16)
	if !handled || value != "hello" || cursor != 0 {
		t.Fatalf("home value=%q cursor=%d handled=%v", value, cursor, handled)
	}
	value, cursor, handled, _ = EditTextField(value, cursor, tea.KeyPressMsg{Code: tea.KeyEnd}, 16)
	if !handled || value != "hello" || cursor != 5 {
		t.Fatalf("end value=%q cursor=%d handled=%v", value, cursor, handled)
	}
}

func TestIsTextEditMsg(t *testing.T) {
	if !IsTextEditMsg(tea.KeyPressMsg{Code: 'a', Text: "a"}) {
		t.Fatal("printable should be text edit")
	}
	if !IsTextEditMsg(tea.KeyPressMsg{Code: tea.KeyLeft}) {
		t.Fatal("left should be text edit")
	}
	if IsTextEditMsg(tea.KeyPressMsg{Code: tea.KeyUp}) {
		t.Fatal("up is not a text edit")
	}
	if IsTextEditMsg(tea.KeyPressMsg{Code: tea.KeyEnter}) {
		t.Fatal("enter is not a text edit")
	}
	if IsTextEditMsg(tea.KeyPressMsg{Code: tea.KeyEsc}) {
		t.Fatal("esc is not a text edit")
	}
}
