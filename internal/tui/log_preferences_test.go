package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	logspage "github.com/mihari-proxy/mihari/internal/tui/pages/logs"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestModel_RestoresLogPreferencesOnlyOnInitialLoad(t *testing.T) {
	m := goldenModel(t, ui.PageLogs, 100, 28)
	m.applySessionEvent(session.Event{Kind: session.EventPreferences, Preferences: protocol.TUIPreferences{LogLevels: []string{"debug", "warn"}}})
	if view := ansi.Strip(m.pages[ui.PageLogs].View()); !strings.Contains(view, "Level: DEBUG, WARNING") {
		t.Fatalf("saved selection not restored:\n%s", view)
	}
	m.applySessionEvent(session.Event{Kind: session.EventReconnecting})
	m.applySessionEvent(session.Event{Kind: session.EventPreferences, Preferences: protocol.TUIPreferences{LogLevels: []string{"error"}}})
	if view := ansi.Strip(m.pages[ui.PageLogs].View()); !strings.Contains(view, "Level: DEBUG, WARNING") {
		t.Fatalf("reconnect replaced this window's selection:\n%s", view)
	}
}

type logPreferenceClient struct{ calls int }

func (c *logPreferenceClient) UpdateTUIPreferences(context.Context, protocol.UpdateTUIPreferencesRequest) (protocol.TUIPreferences, error) {
	c.calls++
	return protocol.TUIPreferences{}, errors.New("fixture preference write failed")
}

func TestModel_LogSaveResultRoutesAfterNavigationAndDoesNotPreventQuit(t *testing.T) {
	m := goldenModel(t, ui.PageLogs, 100, 28)
	client := &logPreferenceClient{}
	m.pages[ui.PageLogs].(*logspage.Model).SetPreferenceClient(client, nil)
	t.Cleanup(m.pages[ui.PageLogs].(*logspage.Model).Stop)
	send := func(key tea.KeyPressMsg) tea.Cmd { next, cmd := m.Update(key); m = next.(Model); return cmd }
	send(tea.KeyPressMsg{Code: tea.KeyEnter})
	send(tea.KeyPressMsg{Code: tea.KeyDown})
	send(tea.KeyPressMsg{Code: tea.KeySpace}) // Deselect DEBUG, leaving INFO+.
	save := send(tea.KeyPressMsg{Code: tea.KeyEnter})
	if save == nil {
		t.Fatal("selection did not schedule save")
	}
	quit := send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if quit == nil {
		t.Fatal("saving blocked quit")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("saving inserted a quit confirmation")
	}
	if client.calls != 0 {
		t.Fatal("quit executed the save synchronously")
	}
	// Exercise the independent navigation path without executing QuitMsg.
	send(tea.KeyPressMsg{Code: '1', Text: "1"})
	if m.active != ui.PageOverview {
		t.Fatal("saving blocked navigation")
	}
	batch := save().(tea.BatchMsg)
	result := batch[0]()
	next, followup := m.Update(result)
	m = next.(Model)
	if followup != nil || m.active != ui.PageOverview || !strings.Contains(ansi.Strip(m.pages[ui.PageLogs].View()), "Unsaved") {
		t.Fatal("background save result was lost or retried")
	}
	if len(m.diagnosticWindow.entries) != 1 || m.diagnosticWindow.entries[0].page != ui.PageLogs || m.diagnosticWindow.open {
		t.Fatal("save failure did not reach F2 once without stealing focus")
	}
}
