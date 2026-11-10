package proxies

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestSelectorCard_CurrentLeafDelay(t *testing.T) {
	m := New(nil, nil)
	m.preferences.ExtraLatency = false
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{
		{Name: "Parent", Nodes: []protocol.ProxyNode{{Name: "Selector", Type: "Selector"}}},
		{Name: "Selector", Now: "Nested", Nodes: []protocol.ProxyNode{{Name: "Nested"}, {Name: "other"}, {Name: "Selector"}}},
		{Name: "Nested", Now: "leaf", Nodes: []protocol.ProxyNode{{Name: "leaf"}}},
	}})
	m.delays["Selector"] = DelayState{Kind: DelayValue, Milliseconds: 999}
	for _, state := range []DelayState{{}, {Kind: DelayValue, Milliseconds: 42}, {Kind: DelayTesting}, {Kind: DelayFailed}, {Kind: DelayTimeout}} {
		m.delays["leaf"] = state
		card := ansi.Strip(m.renderNode(m.groups[0], m.groups[0].Nodes[0], 40))
		want := ansi.Strip(renderDelay(m.theme, state, m.now))
		if !strings.Contains(card, want) || strings.Contains(card, "999 ms") {
			t.Fatalf("card must use final leaf delay %q: %s", want, card)
		}
	}
	m.delays["other"] = DelayState{Kind: DelayValue, Milliseconds: 73}
	m.groups[1].Now = "other"
	if card := m.renderNode(m.groups[0], m.groups[0].Nodes[0], 40); !strings.Contains(card, "73 ms") {
		t.Fatalf("selection change did not update delay: %s", card)
	}
	for _, selection := range []string{"", "missing", "Selector"} {
		m.groups[1].Now = selection
		if card := m.renderNode(m.groups[0], m.groups[0].Nodes[0], 40); strings.Contains(card, "999 ms") || !strings.Contains(card, "—") {
			t.Fatalf("unresolved selection must not reuse group delay: %s", card)
		}
	}
}

func TestEmptySelection_ShortEnglishLabel(t *testing.T) {
	m := newRoutingDetailsModel()
	m.routing.status.GlobalSelection = ""
	if header := strings.Join(m.routingHeader(), "\n"); !strings.Contains(header, "No node") {
		t.Fatalf("GLOBAL needs short empty selection label: %s", header)
	}
	m.groups[0].Now = ""
	if header := m.renderGroupHeader(m.groups[0], 80, false); !strings.Contains(header, "Now: No node") {
		t.Fatalf("group needs empty selection label: %s", header)
	}
}
