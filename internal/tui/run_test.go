package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestLoadingModel_ViewUsesAlternateScreen(t *testing.T) {
	view := (loadingModel{}).View()
	if !view.AltScreen {
		t.Fatal("loading view must use the alternate screen")
	}
}

func TestLoadingModel_QuitKeysStopProgram(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'q', Text: "q"},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		_, command := (loadingModel{}).Update(key)
		if command == nil {
			t.Fatalf("key=%q did not return a quit command", key.String())
		}
		if message := command(); message != tea.Quit() {
			t.Fatalf("key=%q message=%#v", key.String(), message)
		}
	}
}
