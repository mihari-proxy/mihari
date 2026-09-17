package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	logspage "github.com/mihari-proxy/mihari/internal/tui/pages/logs"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestSearchShortcut_FromRailAndContentSynchronizesTextMode(t *testing.T) {
	for _, page := range []ui.PageID{ui.PageConnections, ui.PageRules, ui.PageLogs} {
		for _, area := range []ui.FocusArea{ui.FocusRail, ui.FocusContent} {
			t.Run(fmt.Sprintf("%s/%d", page, area), func(t *testing.T) {
				m := goldenModel(t, page, 100, 28)
				m.focus.Area = area
				next, _ := m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
				m = next.(Model)
				if m.focus.Area != ui.FocusContent || m.inputMode != ui.InputText || m.pages[page].(ui.HelpModeProvider).HelpMode() != ui.ModeSearch {
					t.Fatal("Ctrl+F did not synchronously focus search")
				}
				next, _ = m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
				if next.(Model).active != page {
					t.Fatal("fast digit input navigated away")
				}
			})
		}
	}
}

func TestSearchShortcut_DoesNotEscapeGlobalModal(t *testing.T) {
	m := goldenModel(t, ui.PageLogs, 100, 28)
	m.focus.Area = ui.FocusRail
	m.modal = NewHelp("Help", "body")
	next, _ := m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	m = next.(Model)
	if m.modal == nil || m.focus.Area != ui.FocusRail || m.inputMode == ui.InputText {
		t.Fatal("search escaped shell modal")
	}
}

func TestLogsLevelDialog_ShellIsolationAndSessionMemory(t *testing.T) {
	m := goldenModel(t, ui.PageLogs, 100, 28)
	send := func(key tea.KeyPressMsg) tea.Cmd { next, cmd := m.Update(key); m = next.(Model); return cmd }
	send(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, key := range []tea.KeyPressMsg{{Code: 'q', Text: "q"}, {Code: '?', Text: "?"}, {Code: '1', Text: "1"}, {Code: '/', Text: "/"}, {Code: 'f', Mod: tea.ModCtrl}} {
		if cmd := send(key); cmd != nil {
			t.Fatal("dialog leaked global command")
		}
		if m.active != ui.PageLogs || m.modal != nil {
			t.Fatal("dialog leaked global navigation")
		}
	}
	send(tea.KeyPressMsg{Code: tea.KeyF2})
	if !m.diagnosticWindow.open {
		t.Fatal("global diagnostics unavailable")
	}
	send(tea.KeyPressMsg{Code: tea.KeyEsc})
	send(tea.KeyPressMsg{Code: tea.KeySpace})
	for row := 1; row <= 4; row++ {
		send(tea.KeyPressMsg{Code: tea.KeyDown})
		if row >= 3 {
			send(tea.KeyPressMsg{Code: tea.KeySpace})
		}
	}
	send(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(ansi.Strip(m.pages[ui.PageLogs].View()), "WARNING+") {
		t.Fatal("multi-select not committed")
	}
	send(tea.KeyPressMsg{Code: '1', Text: "1"})
	send(tea.KeyPressMsg{Code: '5', Text: "5"})
	m.applySessionEvent(session.Event{Kind: session.EventReconnecting})
	if !strings.Contains(ansi.Strip(m.pages[ui.PageLogs].View()), "WARNING+") {
		t.Fatal("navigation/reconnection reset filter")
	}
	if !strings.Contains(ansi.Strip(logspage.New(10).View()), "DEBUG+") {
		t.Fatal("new session did not select all")
	}
}

func TestLogsLevelDialog_FitsShellAndAllowsCtrlC(t *testing.T) {
	for _, size := range [][2]int{{72, 22}, {100, 28}} {
		m := goldenModel(t, ui.PageLogs, size[0], size[1])
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = next.(Model)
		view := m.View().Content
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] || !strings.Contains(ansi.Strip(view), "Select all") {
			t.Fatal("dialog missing or exceeds shell")
		}
		if strings.Contains(ansi.Strip(view), "q quit") {
			t.Fatal("footer advertises blocked quit")
		}
		_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		if cmd == nil {
			t.Fatal("Ctrl+C was blocked")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatal("Ctrl+C did not quit")
		}
	}
}

func TestGoldenLogsLevelDialog(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"compact", 72, 22}, {"full", 100, 28}} {
		t.Run(size.name, func(t *testing.T) {
			m := goldenModel(t, ui.PageLogs, size.width, size.height)
			next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m = next.(Model)
			assertGolden(t, "logs-level-dialog-"+size.name, m)
		})
	}
}

func TestSearchShortcut_OtherPagesKeepTheirKeyHandling(t *testing.T) {
	m := goldenModel(t, ui.PageProxies, 100, 28)
	page := &allMessageRecordingPage{id: ui.PageProxies}
	m.pages[ui.PageProxies] = page
	key := tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}
	m.Update(key)
	if page.count(key) != 1 {
		t.Fatal("shell swallowed unrelated page Ctrl+F")
	}
}

func TestLogsLevelDialog_TooSmallCannotApplyHiddenSelection(t *testing.T) {
	m := goldenModel(t, ui.PageLogs, 100, 28)
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	page := m.pages[ui.PageLogs].(*logspage.Model)
	next, _ = m.Update(tea.WindowSizeMsg{Width: 50, Height: 15})
	m = next.(Model)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if !page.HasLevelDialog() {
		t.Fatal("hidden dialog accepted Enter")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if page.HasLevelDialog() {
		t.Fatal("hidden dialog cannot be dismissed")
	}
}
