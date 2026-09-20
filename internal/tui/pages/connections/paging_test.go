package connections

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// TestModel_PageKeys verifies filtered paging, resize-aware steps, and visible selection.
func TestModel_PageKeys(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(fmt.Sprintf("closed=%v", closed), func(t *testing.T) {
			m := New(nil, nil)
			m.SetSize(100, 24)
			var connections []protocol.Connection
			for i := range 80 {
				host := fmt.Sprintf("keep-%02d.test", i)
				if i%2 != 0 {
					host = "hidden.test"
				}
				connections = append(connections, protocol.Connection{ID: fmt.Sprint(i), Download: int64(i), Metadata: protocol.ConnectionMetadata{Host: host}})
			}
			m.Observe(protocol.ConnectionList{Connections: connections}, time.Unix(1, 0))
			if closed {
				m.Observe(protocol.ConnectionList{}, time.Unix(2, 0))
				m.dataset = datasetClosed
			}
			m.query, m.sortColumn, m.sortDirection = "keep-", "download", sortDescending
			rows := m.visibleRows()
			m.focus = pageFocus{kind: focusRow, rowID: rows[0].ID}
			for _, step := range []struct {
				key          rune
				height, want int
			}{
				{tea.KeyPgDown, 24, 14}, {tea.KeyPgDown, 24, 28},
				{tea.KeyPgDown, 24, 39}, {tea.KeyPgDown, 24, 39},
				{tea.KeyPgUp, 24, 25}, {tea.KeyPgUp, 18, 17},
				{tea.KeyPgUp, 10, 16}, {tea.KeyPgUp, 40, 0}, {tea.KeyPgUp, 24, 0},
			} {
				m.SetSize(100, step.height)
				_, cmd := m.Update(tea.KeyPressMsg{Code: step.key})
				if cmd != nil || m.focus.kind != focusRow || m.focus.rowID != rows[step.want].ID {
					t.Fatalf("key=%v focus=%+v want row=%d", step.key, m.focus, step.want)
				}
				if !strings.Contains(m.View(), rows[step.want].Metadata.Host) {
					t.Fatal("selected connection is outside the rendered window")
				}
			}
		})
	}
}

// TestModel_PageKeysShortAndEmptyList verifies clamping and safe paging without rows.
func TestModel_PageKeysShortAndEmptyList(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(100, 24)
	m.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{ID: "one"}, {ID: "two"}}}, time.Unix(1, 0))
	rows := m.visibleRows()
	m.focus = pageFocus{kind: focusRow, rowID: rows[0].ID}
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.focus.rowID != rows[1].ID {
		t.Fatal("page down did not stop at last row")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.focus.rowID != rows[0].ID {
		t.Fatal("page up did not stop at first row")
	}
	m.history = NewHistory(defaultHistoryLimit)
	before := m.focus
	for _, key := range []rune{tea.KeyPgUp, tea.KeyPgDown} {
		m.Update(tea.KeyPressMsg{Code: key})
		if m.focus.rowID != before.rowID || m.focus.kind != before.kind {
			t.Fatal("paging an empty list changed focus")
		}
	}
}

// TestModel_PageKeysRespectInputOwner keeps page keys from moving a list owned by another input mode.
func TestModel_PageKeysRespectInputOwner(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(100, 24)
	m.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{ID: "one"}, {ID: "two"}}}, time.Unix(1, 0))
	rows := m.visibleRows()
	m.focus = pageFocus{kind: focusRow, rowID: rows[0].ID}
	for _, tt := range []struct {
		name    string
		prepare func(*Model)
	}{
		{"control", func(m *Model) { m.focus.kind = focusControl }},
		{"search", func(m *Model) { m.focus.kind = focusSearch; m.searching = true }},
		{"header", func(m *Model) { m.focus.kind = focusHeader }},
		{"detail", func(m *Model) { m.detail = NewDetail(rows[0], false) }},
		{"columns", func(m *Model) { m.openColumns() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := *m
			tt.prepare(&candidate)
			m := &candidate
			before := m.focus
			for _, key := range []rune{tea.KeyPgDown, tea.KeyPgUp} {
				m.Update(tea.KeyPressMsg{Code: key})
				if m.focus.rowID != before.rowID || m.focus.kind != before.kind {
					t.Fatal("page key moved the underlying list or stole focus")
				}
			}
		})
	}
}
