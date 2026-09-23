package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func pressPageSettings(m Model, key tea.KeyPressMsg) Model {
	next, _ := m.Update(key)
	return next.(Model)
}

func openPageSettings(t *testing.T, page ui.PageID) Model {
	t.Helper()
	m := NewModel()
	m.active = page
	m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyF4})
	if m.pageSettings == nil {
		t.Fatal("F4 did not open page settings")
	}
	return m
}

func assertEverySection(t *testing.T, m Model, expanded bool) {
	t.Helper()
	if len(m.pageSettings.pages) != 8 {
		t.Fatalf("sections=%d", len(m.pageSettings.pages))
	}
	for _, id := range ui.RailPages() {
		if m.pageSettings.expanded[id] != expanded {
			t.Fatalf("%s expanded=%v want %v", id, m.pageSettings.expanded[id], expanded)
		}
	}
}

func TestPageSettings_OpenExpandsEverySectionAndKeepsSourceVisible(t *testing.T) {
	m := openPageSettings(t, ui.PageConnections)
	assertEverySection(t, m, true)
	if m.pageSettings.area != 1 || m.pageSettings.directory != 2 || m.pageSettings.focus != (settingsFocus{2, -1}) {
		t.Fatalf("open focus area=%d dir=%d focus=%+v", m.pageSettings.area, m.pageSettings.directory, m.pageSettings.focus)
	}
	if got := strings.Count(ansi.Strip(m.pageSettings.view(120, 40)), "No settings available yet"); got != 7 {
		t.Fatalf("placeholders=%d", got)
	}

	m = openPageSettings(t, ui.PageSystem)
	plain := ansi.Strip(m.pageSettings.view(72, 22))
	if !strings.Contains(plain, "System") || strings.Count(plain, "No settings available yet") != 5 {
		t.Fatalf("source header is not the visible end of the list:\n%s", plain)
	}
	if lipgloss.Width(m.pageSettings.view(72, 22)) > 72 || lipgloss.Height(m.pageSettings.view(72, 22)) > 22 {
		t.Fatal("compact open exceeds 72x22")
	}

	m.width, m.height = 71, 22
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, ui.ResizeRequired) || strings.Contains(view, "] all") {
		t.Fatalf("too-small view:\n%s", view)
	}
	expanded := map[ui.PageID]bool{}
	for id, open := range m.pageSettings.expanded {
		expanded[id] = open
	}
	m = pressPageSettings(m, tea.KeyPressMsg{Code: ']', Text: "]"})
	for id, open := range expanded {
		if m.pageSettings.expanded[id] != open {
			t.Fatalf("%s changed while the terminal was too small", id)
		}
	}
	m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.pageSettings != nil {
		t.Fatal("Esc did not close the too-small dialog")
	}

	m = NewModel()
	m = pressPageSettings(m, tea.KeyPressMsg{Code: '?', Text: "?"})
	help := ansi.Strip(m.View().Content)
	if strings.Contains(help, "] all") || strings.Contains(help, "[ all") {
		t.Fatal("global help lists fold-all keys")
	}
	for _, binding := range ui.Catalog() {
		for _, key := range binding.Keys {
			if key == "]" || key == "[" {
				t.Fatalf("catalog lists %s", key)
			}
		}
	}
}

func TestPageSettings_BracketsFromEveryAreaLeaveDraftAndDirectory(t *testing.T) {
	tabs := []int{0, 1, 2, 3}
	for _, wantArea := range tabs {
		m := openPageSettings(t, ui.PageProxies)
		m.pageSettings.draft.ExtraLatency = false
		switch wantArea {
		case 0:
			m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
			m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyDown})
		case 2:
			m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyTab})
		case 3:
			m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyTab})
			m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyTab})
		}
		wantDir := m.pageSettings.directory
		wantFocus := m.pageSettings.focus
		m = pressPageSettings(m, tea.KeyPressMsg{Code: '[', Text: "["})
		assertEverySection(t, m, false)
		if m.pageSettings.area != wantArea || m.pageSettings.directory != wantDir || m.pageSettings.focus != wantFocus || m.pageSettings.draft.ExtraLatency {
			t.Fatalf("collapse area=%d dir=%d focus=%+v draft=%+v", m.pageSettings.area, m.pageSettings.directory, m.pageSettings.focus, m.pageSettings.draft)
		}
		m = pressPageSettings(m, tea.KeyPressMsg{Code: ']', Text: "]"})
		assertEverySection(t, m, true)
		if m.pageSettings.area != wantArea || m.pageSettings.directory != wantDir || m.pageSettings.focus != wantFocus || m.pageSettings.draft.ExtraLatency {
			t.Fatalf("expand area=%d dir=%d focus=%+v", m.pageSettings.area, m.pageSettings.directory, m.pageSettings.focus)
		}
	}
}

func TestPageSettings_CollapseAllSnapsSettingCursorToHeader(t *testing.T) {
	m := openPageSettings(t, ui.PageProxies)
	m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = pressPageSettings(m, tea.KeyPressMsg{Code: ' ', Text: " "})
	if m.pageSettings.focus != (settingsFocus{1, 0}) || m.pageSettings.draft.ExtraLatency {
		t.Fatalf("cursor before collapse focus=%+v draft=%v", m.pageSettings.focus, m.pageSettings.draft.ExtraLatency)
	}
	dir := m.pageSettings.directory
	m = pressPageSettings(m, tea.KeyPressMsg{Code: '[', Text: "["})
	assertEverySection(t, m, false)
	if m.pageSettings.focus != (settingsFocus{1, -1}) || m.pageSettings.directory != dir || m.pageSettings.area != 1 || m.pageSettings.draft.ExtraLatency {
		t.Fatalf("snap focus=%+v dir=%d area=%d draft=%v", m.pageSettings.focus, m.pageSettings.directory, m.pageSettings.area, m.pageSettings.draft.ExtraLatency)
	}
	m = pressPageSettings(m, tea.KeyPressMsg{Code: ']', Text: "]"})
	assertEverySection(t, m, true)
	if m.pageSettings.focus != (settingsFocus{1, -1}) || m.pageSettings.draft.ExtraLatency {
		t.Fatalf("expand moved cursor focus=%+v draft=%v", m.pageSettings.focus, m.pageSettings.draft.ExtraLatency)
	}
}

func TestPageSettings_HeaderToggleAndDirectoryEnterKeepOtherSections(t *testing.T) {
	m := openPageSettings(t, ui.PageProxies)
	m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.pageSettings.expanded[ui.PageProxies] || !m.pageSettings.expanded[ui.PageOverview] || !m.pageSettings.expanded[ui.PageSystem] {
		t.Fatal("Enter on the focused header did not toggle only that section")
	}
	m = pressPageSettings(m, tea.KeyPressMsg{Code: ' ', Text: " "})
	if !m.pageSettings.expanded[ui.PageProxies] || !m.pageSettings.expanded[ui.PageOverview] {
		t.Fatal("Space did not expand only the focused header")
	}

	m = openPageSettings(t, ui.PageProxies)
	m = pressPageSettings(m, tea.KeyPressMsg{Code: '[', Text: "["})
	m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.pageSettings.area != 1 || m.pageSettings.focus != (settingsFocus{2, -1}) || !m.pageSettings.expanded[ui.PageConnections] || m.pageSettings.expanded[ui.PageProxies] || m.pageSettings.expanded[ui.PageOverview] {
		t.Fatalf("directory enter=%+v expanded proxies=%v overview=%v conns=%v", m.pageSettings.focus, m.pageSettings.expanded[ui.PageProxies], m.pageSettings.expanded[ui.PageOverview], m.pageSettings.expanded[ui.PageConnections])
	}
}

func TestPageSettings_FooterShowsFoldKeysInEveryArea(t *testing.T) {
	const normal = "Tab area  ↑/↓ move  Enter select  ] all  [ all  Esc cancel"
	const adjust = "←/→ adjust (1–50)  Tab area  ] all  [ all  Esc cancel"
	m := openPageSettings(t, ui.PageProxies)
	for range 4 {
		plain := ansi.Strip(m.pageSettings.view(100, 28))
		if !strings.Contains(plain, normal) {
			t.Fatalf("area %d footer:\n%s", m.pageSettings.area, plain)
		}
		if lipgloss.Width(m.pageSettings.view(72, 22)) > 72 {
			t.Fatalf("area %d footer exceeds 72 columns", m.pageSettings.area)
		}
		m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyTab})
	}
	m = openPageSettings(t, ui.PageProxies)
	for range 3 {
		m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	plain := ansi.Strip(m.pageSettings.view(72, 22))
	if m.pageSettings.focus.field != 2 || !strings.Contains(plain, adjust) {
		t.Fatalf("concurrency footer focus=%+v\n%s", m.pageSettings.focus, plain)
	}
}

func TestPageSettings_FoldKeysIgnoredWhileSaving(t *testing.T) {
	m := openPageSettings(t, ui.PageProxies)
	m = pressPageSettings(m, tea.KeyPressMsg{Code: '[', Text: "["})
	assertEverySection(t, m, false)
	m = pressPageSettings(m, tea.KeyPressMsg{Code: ']', Text: "]"})
	m.preferencesClient = &settingsTestClient{}
	m.applyPreferences(protocol.TUIPreferences{Revision: 7})
	m.pageSettings.original = m.preferences.EffectiveProxies()
	m = pressPageSettings(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = pressPageSettings(m, tea.KeyPressMsg{Code: ' ', Text: " "})
	m = pressPageSettings(m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.pageSettings == nil || !m.pageSettings.saving {
		t.Fatal("Ctrl+S did not start a save")
	}
	focus := m.pageSettings.focus
	m = pressPageSettings(m, tea.KeyPressMsg{Code: '[', Text: "["})
	m = pressPageSettings(m, tea.KeyPressMsg{Code: ']', Text: "]"})
	assertEverySection(t, m, true)
	if !m.pageSettings.saving || m.pageSettings.focus != focus || m.pageSettings.draft.ExtraLatency {
		t.Fatalf("fold keys changed an in-progress save focus=%+v draft=%v", m.pageSettings.focus, m.pageSettings.draft.ExtraLatency)
	}
}
