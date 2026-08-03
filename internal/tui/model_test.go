package tui

import (
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/tui/session"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestModelSessionReconnectMarksSnapshotStaleAndKeepsWaiting(t *testing.T) {
	events := make(chan session.Event, 2)
	model := NewModelWithEvents(events)
	command := model.Init()
	if command == nil {
		t.Fatal("model did not wait for session events")
	}
	events <- session.Event{Kind: session.EventReconnecting}
	updated, next := model.Update(command())
	model = updated.(Model)
	if !model.stale || model.connected || next == nil {
		t.Fatalf("stale=%v connected=%v next=%v", model.stale, model.connected, next != nil)
	}
	events <- session.Event{Kind: session.EventConnected}
	updated, next = model.Update(next())
	model = updated.(Model)
	if model.stale || !model.connected || next == nil {
		t.Fatalf("stale=%v connected=%v next=%v", model.stale, model.connected, next != nil)
	}
	close(events)
	updated, next = model.Update(next())
	model = updated.(Model)
	if next != nil {
		t.Fatal("closed session channel was reissued")
	}
}

func TestRail_EnterAndRightEnterContentButTabDoesNot(t *testing.T) {
	for _, enterKey := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: tea.KeyRight}} {
		model := NewModel()
		model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyTab})
		if model.focus.Area != ui.FocusRail {
			t.Fatalf("tab focus=%v", model.focus)
		}
		model = updateModelKey(t, model, enterKey)
		if model.focus.Area != ui.FocusContent {
			t.Fatalf("key=%q focus=%v", enterKey.String(), model.focus)
		}
	}
}

func TestRail_HJKLDisabledDuringTextEntry(t *testing.T) {
	model := NewModel()
	model.inputMode = ui.InputText
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: 'j', Text: "j"})
	if model.railIndex != 0 {
		t.Fatalf("rail index=%d", model.railIndex)
	}
	model.inputMode = ui.InputNavigation
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: 'j', Text: "j"})
	if model.railIndex != 1 {
		t.Fatalf("rail index=%d", model.railIndex)
	}
}

func TestContent_HAliasDoesNotNavigateDuringTextEntry(t *testing.T) {
	model := NewModel()
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyRight})
	model.inputMode = ui.InputText
	updated, command := model.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	model = updated.(Model)
	if command != nil || model.focus.Area != ui.FocusContent {
		t.Fatalf("text input command=%v focus=%v", command != nil, model.focus)
	}

	model.inputMode = ui.InputNavigation
	updated, command = model.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	model = updated.(Model)
	if command == nil {
		t.Fatal("navigation h did not request rail focus")
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if model.focus.Area != ui.FocusRail {
		t.Fatalf("focus=%v", model.focus)
	}
}

func TestModalReceivesInputBeforeRail(t *testing.T) {
	model := NewModel()
	model.modal = NewConfirmation("Restart core", "mihomo", "Connections will be interrupted", "The previous binary remains available")
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	if model.railIndex != 0 {
		t.Fatalf("rail moved behind modal: %d", model.railIndex)
	}
}

func TestConfirmationEmitsResultOnlyFromConfirmButton(t *testing.T) {
	model := NewModel()
	model.modal = NewConfirmation("Restart core", "mihomo", "interrupt", "available")
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyLeft})
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	if model.modal != nil || command == nil {
		t.Fatalf("modal=%v command=%v", model.modal != nil, command != nil)
	}
	if _, ok := command().(ModalConfirmedMsg); !ok {
		t.Fatalf("message=%T", command())
	}
}

func TestOperationLedgerKeepsNewestFiftyEntries(t *testing.T) {
	model := NewModel()
	for index := 0; index < 60; index++ {
		model.recordOperation(ui.OperationRecord{ID: strconv.Itoa(index)})
	}
	if len(model.operations) != 50 || model.operations[0].ID != "10" || model.operations[49].ID != "59" {
		t.Fatalf("operations=%v", model.operations)
	}
}

func updateModelKey(t *testing.T, model Model, key tea.KeyPressMsg) Model {
	t.Helper()
	updated, _ := model.Update(key)
	result, ok := updated.(Model)
	if !ok {
		t.Fatalf("model type=%T", updated)
	}
	return result
}
