package logs

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

func filterKey(m *Model, code rune) {
	m.Update(tea.KeyPressMsg{Code: code})
}

// chooseLevels drives the actual dialog from the default all-selected state.
func chooseLevels(m *Model, indexes ...int) {
	filterKey(m, tea.KeyEnter)
	filterKey(m, tea.KeySpace) // Select all: clear the draft.
	for row := 1; row <= 4; row++ {
		filterKey(m, tea.KeyDown)
		for _, selected := range indexes {
			if row == selected {
				filterKey(m, tea.KeySpace)
			}
		}
	}
}

func TestLevelFilter_DraftConfirmCancelAndQueryIntersection(t *testing.T) {
	m := New(20)
	m.SetSize(100, 22)
	for i, level := range []string{"debug", "info", "WARNING", "error", "warn"} {
		m.Append(logAt("match-"+level, level, int64(i+1)))
	}
	m.Append(logAt("other", "warn", 6))
	m.SetFilter("", "match")
	chooseLevels(m, 3, 4)
	if got := len(m.visibleEntries()); got != 5 {
		t.Fatalf("draft changed filter: %d", got)
	}
	if !strings.Contains(logsStripANSI(m.View()), "Select all") {
		t.Fatal("Level did not open multi-select dialog")
	}
	filterKey(m, tea.KeyEnter)
	entries := m.visibleEntries()
	if len(entries) != 3 || entries[0].Log.Level != "WARNING" || entries[1].Log.Level != "error" || entries[2].Log.Level != "warn" {
		t.Fatalf("selected entries=%v", entries)
	}
	if !strings.Contains(logsStripANSI(m.View()), "WARNING+") {
		t.Fatal("missing WARNING+ summary")
	}
	if m.focus != focusControl || m.controlIndex != 0 {
		t.Fatal("confirm did not return focus to Level")
	}
	m.Append(logAt("match-new", "warn", 7))
	m.Append(logAt("match-hidden", "info", 8))
	if len(m.visibleEntries()) != 4 {
		t.Fatal("new entries ignored the committed filter")
	}
	filterKey(m, tea.KeyEnter) // Focuses first selected level, WARNING.
	filterKey(m, tea.KeySpace)
	filterKey(m, tea.KeyEsc)
	if len(m.visibleEntries()) != 4 || !strings.Contains(logsStripANSI(m.View()), "WARNING+") {
		t.Fatal("cancel changed selection")
	}
}

func TestLevelFilter_EmptySelectionRequiresChoiceAndSelectAllRestoresUnknown(t *testing.T) {
	m := New(10)
	m.SetSize(100, 22)
	m.Append(logAt("unknown", "notice", 1))
	m.Append(logAt("known", "info", 2))
	filterKey(m, tea.KeyEnter)
	filterKey(m, tea.KeySpace)
	filterKey(m, tea.KeyEnter)
	if !strings.Contains(logsStripANSI(m.View()), "Select at least one level") || len(m.visibleEntries()) != 2 {
		t.Fatal("empty draft was accepted or did not show validation")
	}
	filterKey(m, tea.KeySpace)
	filterKey(m, tea.KeyEnter)
	if len(m.visibleEntries()) != 2 || strings.Contains(logsStripANSI(m.View()), "Select all") {
		t.Fatal("Select all did not restore all records and close")
	}
}

func TestLevelFilter_Summaries(t *testing.T) {
	for _, tc := range []struct {
		selected []int
		summary  string
	}{
		{[]int{1, 2, 3, 4}, "DEBUG+"}, {[]int{2, 3, 4}, "INFO+"}, {[]int{3, 4}, "WARNING+"},
		{[]int{4}, "ERROR"}, {[]int{1, 3}, "DEBUG, WARNING"}, {[]int{1, 2}, "DEBUG, INFO"},
		{[]int{2, 4}, "INFO, ERROR"}, {[]int{1, 2, 4}, "DEBUG, INFO, ERROR"},
	} {
		t.Run(tc.summary, func(t *testing.T) {
			m := New(10)
			m.SetSize(100, 22)
			chooseLevels(m, tc.selected...)
			filterKey(m, tea.KeyEnter)
			control := logsStripANSI(logsSectionBodyLine(m.View(), 0))
			if !strings.Contains(control, "Level: "+tc.summary) {
				t.Fatalf("control=%q", control)
			}
		})
	}
}

func TestLevelFilter_DialogFitsAndKeepsSelectionVisible(t *testing.T) {
	for _, size := range [][2]int{{100, 22}, {48, 12}, {34, 8}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := New(10)
			m.SetSize(size[0], size[1])
			filterKey(m, tea.KeyEnter)
			for range 4 {
				filterKey(m, tea.KeyDown)
			}
			view := m.View()
			if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
				t.Fatalf("dialog overflow: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
			}
			plain := logsStripANSI(view)
			if !strings.Contains(plain, "ERROR") || !strings.Contains(plain, "Esc") {
				t.Fatalf("selection or cancel hint clipped:\n%s", plain)
			}
		})
	}
}

func TestLevelFilter_DialogBlocksPageShortcutsAndPreservesPause(t *testing.T) {
	m := New(10)
	m.SetSize(100, 22)
	m.Append(logAt("before", "info", 1))
	m.togglePause()
	filterKey(m, tea.KeyEnter)
	for _, key := range []tea.KeyPressMsg{{Code: 'p', Text: "p"}, {Code: 'w', Text: "w"}, {Code: 'e', Text: "e"}, {Code: '/', Text: "/"}, {Code: 'f', Mod: tea.ModCtrl}} {
		_, cmd := m.Update(key)
		if cmd != nil {
			t.Fatal("dialog leaked a page command")
		}
	}
	m.Append(logAt("during", "error", 2))
	if m.searching || m.wrap || !m.buffer.Paused() || !strings.Contains(logsStripANSI(m.View()), "Select all") {
		t.Fatal("dialog leaked a shortcut")
	}
	filterKey(m, tea.KeyEnter)
	if !m.buffer.Paused() || len(m.visibleEntries()) != 1 {
		t.Fatal("confirm changed pause state")
	}
}

func TestLevelFilter_EveryCombinationMatchesOnlyItsSelectedLevels(t *testing.T) {
	for mask := 1; mask < 16; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			m := New(10)
			var selected []int
			for i, level := range []string{"debug", "INFO", "warning", "ERROR"} {
				m.Append(logAt(level, level, int64(i+1)))
				if mask&(1<<i) != 0 {
					selected = append(selected, i+1)
				}
			}
			chooseLevels(m, selected...)
			filterKey(m, tea.KeyEnter)
			got := m.visibleEntries()
			if len(got) != len(selected) {
				t.Fatalf("visible=%v selected=%v", got, selected)
			}
			for i, row := range selected {
				if got[i].Log.Message != []string{"debug", "INFO", "warning", "ERROR"}[row-1] {
					t.Fatalf("unexpected entry %v", got[i])
				}
			}
		})
	}
}
