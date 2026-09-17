package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type levelEditorClient struct {
	systempage.Client
	status protocol.LoggingStatus
	calls  int
}

func (c *levelEditorClient) UpdateLogging(_ context.Context, req protocol.LoggingUpdateRequest) (protocol.LoggingStatus, error) {
	c.calls++
	c.status.Revision++
	c.status.Level = *req.Level
	return c.status, nil
}

// Deliver input-mode and mutation results through the shell, without starting
// timer commands. Timer behavior is exercised by the System page tests.
func deliverLevelEditorCommand(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, child := range msg {
			m = deliverLevelEditorCommand(t, m, child)
		}
	case ui.InputModeMsg:
		updated, followup := m.Update(msg)
		m = deliverLevelEditorCommand(t, updated.(Model), followup)
	case ui.PageResultMsg:
		if _, ok := msg.Result.(ui.LoggingObservedMsg); ok {
			updated, followup := m.Update(msg)
			m = deliverLevelEditorCommand(t, updated.(Model), followup)
		}
	}
	return m
}

func TestLoggingLevelEditor_ShellOwnsInputUntilSuccessOrDisconnect(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		m := NewModel()
		m.width, m.height = 120, 50
		m.applySessionEvent(session.Event{Kind: session.EventConnected, Epoch: 7})
		status := protocol.LoggingStatus{Schema: "mihari/v1", Revision: 4, Level: "info", MaxSizeMB: 10, MaxFiles: 3}
		caps := protocol.Status{Revision: 4, Capabilities: []string{protocol.CapabilityLogging}}
		m.applySessionEvent(session.Event{Kind: session.EventStatus, Epoch: 7, Status: caps})
		client := &levelEditorClient{status: status}
		page := systempage.New(client, func() string { return "level-edit-test" })
		page.SetSnapshot(caps, protocol.CoreStatus{})
		page.SetMutationsEnabled(true)
		page.SetSize(100, 40)
		m.pages[ui.PageSystem] = page
		m.active = ui.PageSystem
		m.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageSystem}
		m.applySessionEvent(session.Event{Kind: session.EventLogging, Epoch: 7, Logging: status})
		for n := 0; n < 40 && !strings.Contains(page.View(), ui.FocusMarker+"Level"); n++ {
			page.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		}
		if !strings.Contains(page.View(), ui.FocusMarker+"Level") {
			t.Fatal("could not focus Level")
		}
		updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = deliverLevelEditorCommand(t, updated.(Model), cmd)
		if m.inputMode != ui.InputText || page.HelpMode() != ui.ModeLoggingLevel {
			t.Fatal("editor did not claim shell input")
		}
		for _, key := range []tea.KeyPressMsg{{Code: '1', Text: "1"}, {Code: 'q', Text: "q"}, {Code: tea.KeyTab}, {Code: tea.KeyUp}} {
			updated, cmd = m.Update(key)
			m = updated.(Model)
			if cmd != nil || m.active != ui.PageSystem || m.focus.Area != ui.FocusContent || page.HelpMode() != ui.ModeLoggingLevel {
				t.Fatalf("key %q escaped editor", key.String())
			}
		}
		updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		m = updated.(Model)
		updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = updated.(Model)
		if page.HelpMode() != ui.ModeLoggingApplying {
			t.Fatal("missing applying mode")
		}
		if disconnect {
			m.syncSystemLoggingStatus(protocol.LoggingStatus{}, false)
			if m.inputMode != ui.InputNavigation || page.HelpMode() != "" {
				t.Fatal("disconnect retained editing input")
			}
			continue
		}
		m = deliverLevelEditorCommand(t, m, cmd)
		if m.inputMode != ui.InputNavigation || page.HelpMode() != "" || client.calls != 1 || m.loggingStatus.Level != "warn" {
			t.Fatalf("success mode=%v help=%q calls=%d logging=%+v", m.inputMode, page.HelpMode(), client.calls, m.loggingStatus)
		}
	}
}
