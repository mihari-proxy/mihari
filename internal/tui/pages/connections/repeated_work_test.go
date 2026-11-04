package connections

import (
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestVisibleRows_ReusesProjectionAndInvalidatesInputs(t *testing.T) {
	m := New(nil, nil)
	snapshot := protocol.ConnectionList{Connections: []protocol.Connection{
		{ID: "a", Download: 1, Metadata: protocol.ConnectionMetadata{Host: "alpha", SourceIP: "1", Process: "worker"}},
		{ID: "b", Download: 2, Metadata: protocol.ConnectionMetadata{Host: "beta", SourceIP: "2"}},
	}}
	m.Observe(snapshot, time.Unix(1, 0))
	first, second := m.visibleRows(), m.visibleRows()
	if len(first) != 2 || &first[0] != &second[0] {
		t.Fatal("unchanged projection rebuilt")
	}
	m.sortColumn, m.sortDirection = "download", sortDescending
	if rows := m.visibleRows(); rows[0].ID != "b" {
		t.Fatal("sort not invalidated")
	}
	m.sortDirection = sortAscending
	if rows := m.visibleRows(); rows[0].ID != "a" {
		t.Fatal("direction not invalidated")
	}
	m.query = "alpha"
	if rows := m.visibleRows(); len(rows) != 1 || rows[0].ID != "a" {
		t.Fatal("query not invalidated")
	}
	m.query, m.source = "", "2"
	if rows := m.visibleRows(); len(rows) != 1 || rows[0].ID != "b" {
		t.Fatal("source not invalidated")
	}
	m.source, m.query = allSources, "worker"
	if rows := m.visibleRows(); len(rows) != 0 {
		t.Fatal("unexpected process match in hidden column")
	}
	m.SetPreferences(protocol.TUIPreferences{ConnectionsColumns: []string{"process"}})
	if rows := m.visibleRows(); len(rows) != 1 || rows[0].ID != "a" {
		t.Fatal("columns not invalidated")
	}
	m.query = ""
	m.Observe(protocol.ConnectionList{}, time.Unix(2, 0))
	if rows := m.visibleRows(); len(rows) != 0 {
		t.Fatal("history update not invalidated")
	}
	m.dataset = datasetClosed
	if rows := m.visibleRows(); len(rows) != 2 || rows[0].ClosedAt.IsZero() {
		t.Fatal("dataset not invalidated")
	}
	m.ResetSession()
	if rows := m.visibleRows(); len(rows) != 0 {
		t.Fatal("reset retained projection")
	}
}

func BenchmarkVisibleRows_Unchanged(b *testing.B) {
	m := New(nil, nil)
	snapshot := protocol.ConnectionList{Connections: make([]protocol.Connection, 1000)}
	for i := range snapshot.Connections {
		snapshot.Connections[i] = protocol.Connection{ID: time.Unix(int64(i), 0).String(), Download: int64(i)}
	}
	m.Observe(snapshot, time.Unix(1, 0))
	m.sortColumn, m.sortDirection = "download", sortDescending
	m.visibleRows()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		m.visibleRows()
	}
}
