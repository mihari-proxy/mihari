package proxies

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestSharedRefresh_OldCandidatesCannotStartSelectionOrLatency(t *testing.T) {
	m := New(&fakeClient{}, func() string { return "op" })
	m.SetSnapshotRefresh(func(bool) tea.Cmd { return nil })
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "group", Nodes: []protocol.ProxyNode{{Name: "node"}}}}})
	m.focus = FocusID{Group: "group", Node: "node"}
	m.InvalidateGroups()
	if m.selectFocused() != nil {
		t.Fatal("stale ordinary candidate could select")
	}
	if m.testNode("node") != nil || m.testAll() != nil {
		t.Fatal("stale candidate could start latency work")
	}
}
func TestSharedRefresh_SelectionResultKeepsItsPage(t *testing.T) {
	m := New(&fakeClient{}, func() string { return "op" })
	m.SetSnapshotRefresh(func(bool) tea.Cmd { return nil })
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "group", Nodes: []protocol.ProxyNode{{Name: "node"}}}}})
	m.focus = FocusID{Group: "group", Node: "node"}
	cmd := m.selectFocused()
	if cmd == nil {
		t.Fatal("fresh candidate disabled")
	}
	message, ok := cmd().(ui.PageResultMsg)
	if !ok || message.Page != ui.PageProxies {
		t.Fatal("off-page selection completion lost its owner")
	}
}

func TestSharedRefresh_FreshGroupsClearOnlyGateWarning(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSnapshotRefresh(func(bool) tea.Cmd { return nil })
	m.InvalidateGroups()
	m.testAll()
	m.SetGroups(protocol.ProxyGroups{})
	if m.lastError != "" {
		t.Fatal("fresh groups kept stale gate warning")
	}
	m.lastError = "fixture operation failure"
	m.SetGroups(protocol.ProxyGroups{})
	if m.lastError == "" {
		t.Fatal("snapshot erased unrelated operation failure")
	}
}
