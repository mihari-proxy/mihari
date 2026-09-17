package rules

import (
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestSearch_CtrlFFocusesAndMovesToQueryEnd(t *testing.T) {
	for _, editing := range []bool{false, true} {
		m := New(nil, nil)
		m.query = "域名.test"
		m.queryCursor = 1
		m.searching = editing
		_, cmd := m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
		if !m.searching || m.query != "域名.test" || m.queryCursor != utf8.RuneCountInString(m.query) {
			t.Fatalf("editing=%v searching=%v query=%q cursor=%d", editing, m.searching, m.query, m.queryCursor)
		}
		if cmd == nil || cmd() != (ui.InputModeMsg{Mode: ui.InputText}) {
			t.Fatal("search did not enter text mode")
		}
	}
}

func TestSearch_CtrlFDoesNotStealDetailFocus(t *testing.T) {
	m := New(nil, nil)
	m.detail = &detailState{}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if m.detail == nil || m.searching || cmd != nil {
		t.Fatal("Ctrl+F escaped detail")
	}
}
