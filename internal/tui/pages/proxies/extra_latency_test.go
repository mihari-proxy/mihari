package proxies

import (
	tea "charm.land/bubbletea/v2"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestGroupHeader_ShowsSelectedLeafLatency(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(100, 20)
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{
		{Name: "YouTube", Now: "Auto", Nodes: []protocol.ProxyNode{{Name: "Auto"}}},
		{Name: "Auto", Now: "leaf", Nodes: []protocol.ProxyNode{{Name: "leaf"}}},
	}})
	m.delays["leaf"] = DelayState{Kind: DelayValue, Milliseconds: 149}
	header := m.renderGroupHeader(m.groups[0], 80, false)
	if !strings.Contains(header, "149 ms") || strings.Index(header, "149 ms") > strings.Index(header, "Jump to Selected") {
		t.Fatalf("latency must precede jump button: %s", header)
	}
}

func TestBasic_SettingsOnFirstRowAndKeyboardEntry(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(100, 20)
	m.SetRoutingAvailable(true, 1)
	m.SetRouting(protocol.RoutingStatus{DesiredMode: "rule", State: "applied"}, 1)
	lines := m.routingHeader()
	if !strings.Contains(lines[0], "Basic") || !strings.Contains(lines[1], "Page Settings") || strings.Contains(lines[2], "Page Settings") {
		t.Fatalf("Basic settings must occupy first row second column: %v", lines)
	}
	m.FocusFirst()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("no settings command")
	}
	if _, ok := cmd().(ui.OpenPageSettingsMsg); !ok {
		t.Fatal("did not open shared settings")
	}
}

func TestExtraLatency_DisabledAndUnresolvableSelections(t *testing.T) {
	m := New(nil, nil)
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{
		{Name: "A", Now: "B", Nodes: []protocol.ProxyNode{{Name: "B"}}},
		{Name: "B", Now: "A", Nodes: []protocol.ProxyNode{{Name: "A"}, {Name: "leaf"}}},
	}})
	if m.selectedLeaf("A") != "" || m.extraLatency("missing") != "" {
		t.Fatal("cycle or missing selection was resolved")
	}
	m.delays["leaf"] = DelayState{Kind: DelayValue, Milliseconds: 42}
	m.SetPreferences(protocol.TUIPreferences{Proxies: &protocol.ProxyPreferences{AutoLatencyTest: true}})
	if m.extraLatency("leaf") != "" {
		t.Fatal("extra delay visible when disabled")
	}
	if !strings.Contains(m.renderNode(m.groups[1], protocol.ProxyNode{Name: "leaf"}, 28), "42 ms") {
		t.Fatal("extra display toggle hid card delay")
	}
}
