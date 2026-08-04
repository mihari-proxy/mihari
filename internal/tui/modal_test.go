package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestConfirmationStatesImpactAndRollbackWithoutRetyping(t *testing.T) {
	modal := NewConfirmation("Restart core", "mihomo", "Connections will be interrupted", "The previous binary remains available")
	view := modal.View(70, 20)
	for _, want := range []string{"mihomo", "Connections will be interrupted", "The previous binary remains available", "Confirm", "Cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q: %s", want, view)
		}
	}
	// Plain-language body: no Object:/Impact:/Rollback: field labels.
	for _, forbidden := range []string{"Object:", "Impact:", "Rollback:"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("confirmation still uses field dump %q: %s", forbidden, view)
		}
	}
	if strings.Contains(strings.ToLower(view), "type the") {
		t.Fatalf("confirmation requires retyping: %s", view)
	}
}

func TestDetailModalEscapeCloses(t *testing.T) {
	modal := NewDetail("Details", "content")
	action := modal.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if action != ModalClose {
		t.Fatalf("action=%v", action)
	}
}
