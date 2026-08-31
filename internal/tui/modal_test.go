package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
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

func TestHelpModal_ScrollsInsideCompactTerminal(t *testing.T) {
	lines := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("line-%02d extra text to force wrapping checks", i))
	}
	modal := NewHelp("Keyboard help", strings.Join(lines, "\n"))
	view := modal.View(72, 22)
	if strings.Contains(view, "line-39") {
		t.Fatalf("compact help showed last line without scrolling: %s", view)
	}
	if !strings.Contains(view, "▾") {
		t.Fatalf("compact help missing overflow marker: %s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > 72 {
			t.Fatalf("help line width %d > 72: %q", w, line)
		}
	}
	if modal.Update(tea.KeyPressMsg{Code: tea.KeyDown}) != ModalNone {
		t.Fatal("down should scroll, not close")
	}
	scrolled := modal.View(72, 22)
	if !strings.Contains(scrolled, "line-") {
		t.Fatalf("scrolled view empty: %s", scrolled)
	}
	if scrolled == view {
		t.Fatal("down did not change help view")
	}
	if modal.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) != ModalClose {
		t.Fatal("esc should close help")
	}
}

func TestHelpModal_IgnoresEnterAndTab(t *testing.T) {
	modal := NewHelp("Keyboard help", "body")
	if modal.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) != ModalNone {
		t.Fatal("enter closed help")
	}
	if modal.Update(tea.KeyPressMsg{Code: tea.KeyTab}) != ModalNone {
		t.Fatal("tab closed help")
	}
}
