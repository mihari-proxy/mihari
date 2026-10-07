package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	proxypage "github.com/mihari-proxy/mihari/internal/tui/pages/proxies"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type autoProxyClient struct{ calls []string }

func (c *autoProxyClient) SelectProxy(context.Context, string, protocol.ProxySelectionRequest) (protocol.MutationResult, error) {
	return protocol.MutationResult{}, nil
}
func (c *autoProxyClient) DelayProxy(_ context.Context, name string, _ protocol.DelayTestRequest) (protocol.DelayResult, error) {
	c.calls = append(c.calls, name)
	return protocol.DelayResult{Delays: map[string]uint16{name: 42}}, nil
}

func TestProxiesAuto_EntryTestsCollapsedVisibleSelection(t *testing.T) {
	c := &autoProxyClient{}
	m := newModel(c)
	m.connected, m.preferencesLoaded = true, true
	m.core = protocol.CoreStatus{Status: "running", PID: 10}
	m.pages[ui.PageProxies].(*proxypage.Model).SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{
		{Name: "Group", Now: "selected", Nodes: []protocol.ProxyNode{{Name: "selected"}, {Name: "hidden"}}},
	}})
	next, cmd := m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	m = next.(Model)
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, child := range batch {
				run(child)
			}
			return
		}
		// Apply each immediate result; do not execute returned timer commands.
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	run(cmd)
	if len(c.calls) != 1 || c.calls[0] != "selected" {
		t.Fatalf("calls=%v; expected only collapsed group's selected leaf", c.calls)
	}
}
