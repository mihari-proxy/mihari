package logs

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestModel_PageKeys verifies filtered paging, resize-aware steps, and visible selection.
func TestModel_PageKeys(t *testing.T) {
	m := New(100)
	for i := range 80 {
		level := "info"
		if i%2 != 0 {
			level = "debug"
		}
		m.Append(logAt(fmt.Sprintf("entry-%02d", i), level, int64(i)))
	}
	m.SetFilter("info", "entry-")
	m.focus = focusRow
	for _, step := range []struct {
		key          rune
		height, want int
	}{
		{tea.KeyPgUp, 24, 24}, {tea.KeyPgUp, 24, 9},
		{tea.KeyPgUp, 24, 0}, {tea.KeyPgUp, 24, 0},
		{tea.KeyPgDown, 24, 15}, {tea.KeyPgDown, 18, 24},
		{tea.KeyPgDown, 9, 25}, {tea.KeyPgDown, 40, 39}, {tea.KeyPgDown, 24, 39},
	} {
		m.SetSize(100, step.height)
		_, cmd := m.Update(tea.KeyPressMsg{Code: step.key})
		if cmd != nil || m.focus != focusRow || m.focused != step.want || m.following {
			t.Fatalf("key=%v focus=%v row=%d following=%v want row=%d", step.key, m.focus, m.focused, m.following, step.want)
		}
		if !strings.Contains(m.View(), fmt.Sprintf("entry-%02d", step.want*2)) {
			t.Fatal("selected log is outside the rendered window")
		}
	}
	m.Append(logAt("entry-new", "info", 100))
	if m.focused != 39 || m.Unread() != 1 {
		t.Fatal("new log moved the selection or lost unread count")
	}
	m.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	if !m.following || m.focused != 40 || m.Unread() != 0 {
		t.Fatal("G did not restore follow")
	}
}

// TestModel_PageKeysShortAndEmptyList verifies clamping and safe paging without rows.
func TestModel_PageKeysShortAndEmptyList(t *testing.T) {
	m := New(10)
	m.SetSize(100, 24)
	m.Append(logAt("one", "info", 1))
	m.Append(logAt("two", "info", 2))
	m.focus, m.focused, m.following = focusRow, 0, false
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.focused != 1 {
		t.Fatal("page down did not stop at last row")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.focused != 0 {
		t.Fatal("page up did not stop at first row")
	}
	m.buffer = NewBuffer(10)
	before := m.focus
	for _, key := range []rune{tea.KeyPgUp, tea.KeyPgDown} {
		m.Update(tea.KeyPressMsg{Code: key})
		if m.focus != before || m.focused != 0 {
			t.Fatal("paging an empty list changed focus")
		}
	}
}

// TestModel_PageKeysRespectInputOwner keeps page keys from moving a list owned by another input mode.
func TestModel_PageKeysRespectInputOwner(t *testing.T) {
	m := New(10)
	m.SetSize(100, 24)
	m.Append(logAt("one", "info", 1))
	m.Append(logAt("two", "info", 2))
	m.focus, m.focused, m.following = focusRow, 0, false
	for _, tt := range []struct {
		name    string
		prepare func(*Model)
	}{
		{"control", func(m *Model) { m.focus = focusControl }},
		{"search", func(m *Model) { m.focus = focusSearch; m.searching = true }},
		{"detail", func(m *Model) { m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) }},
		{"levels", func(m *Model) { m.openLevelDialog() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := *m
			tt.prepare(&candidate)
			m := &candidate
			before := m.focus
			for _, key := range []rune{tea.KeyPgDown, tea.KeyPgUp} {
				m.Update(tea.KeyPressMsg{Code: key})
				if m.focus != before || m.focused != 0 {
					t.Fatal("page key moved the underlying list or stole focus")
				}
			}
		})
	}
}
