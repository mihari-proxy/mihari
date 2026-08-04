package proxies

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

func TestModel_SelectsNodeAndRendersProtocolAndLatencyUnit(t *testing.T) {
	client := &fakeClient{delay: 28}
	model := New(client, func() string { return "op-1" })
	model.SetSize(80, 20)
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
		Name: "GLOBAL", Now: "old", Nodes: []protocol.ProxyNode{{Name: "node-a", Type: "VLESS", UDP: true, XUDP: true}},
	}}})
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	command := updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil {
		t.Fatal("node selection did not return a command")
	}
	updated, _ := model.Update(command())
	model = updated.(*Model)
	if client.selectedGroup != "GLOBAL" || client.selectedNode != "node-a" || client.operationID != "op-1" {
		t.Fatalf("client=%#v", client)
	}

	command = updateProxyKey(t, model, tea.KeyPressMsg{Code: 't', Text: "t"})
	updated, _ = model.Update(command())
	model = updated.(*Model)
	view := model.View()
	for _, want := range []string{"VLESS / XUDP", "28 ms"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q: %s", want, view)
		}
	}
}

func TestView_FocusedNodeAccentOnlyWhenContentFocused(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 20)
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
		Name: "GLOBAL", Now: "node-a", Nodes: []protocol.ProxyNode{{Name: "node-a", Type: "VLESS"}},
	}}})
	model.expanded["GLOBAL"] = true
	model.focus = FocusID{Group: "GLOBAL", Node: "node-a"}

	model.SetContentFocused(false)
	plain := model.View()
	if !strings.Contains(plain, "> ") {
		t.Fatalf("focus marker missing while rail owns focus:\n%s", plain)
	}

	model.SetContentFocused(true)
	accented := model.View()
	if plain == accented {
		t.Fatal("content focus should change node border accent styling")
	}
}

func TestModel_ControlTTestsEveryUniqueNodeOnce(t *testing.T) {
	client := &fakeClient{delay: 10}
	model := New(client, func() string { return "unused" })
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{
		{Name: "A", Nodes: []protocol.ProxyNode{{Name: "shared"}, {Name: "only-a"}}},
		{Name: "B", Nodes: []protocol.ProxyNode{{Name: "shared"}, {Name: "only-b"}}},
	}})
	_, command := model.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	batch, ok := command().(tea.BatchMsg)
	if !ok || len(batch) != 3 {
		t.Fatalf("message=%T commands=%d", command(), len(batch))
	}
	for _, delayCommand := range batch {
		model.Update(delayCommand())
	}
	if client.delayCalls["shared"] != 1 || client.delayCalls["only-a"] != 1 || client.delayCalls["only-b"] != 1 {
		t.Fatalf("calls=%v", client.delayCalls)
	}
}

type fakeClient struct {
	selectedGroup string
	selectedNode  string
	operationID   string
	delay         uint16
	delayCalls    map[string]int
}

func (c *fakeClient) SelectProxy(_ context.Context, group string, request protocol.ProxySelectionRequest) (protocol.MutationResult, error) {
	c.selectedGroup, c.selectedNode, c.operationID = group, request.Name, request.OperationID
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID}, nil
}

func (c *fakeClient) DelayProxy(_ context.Context, name string, _ protocol.DelayTestRequest) (protocol.DelayResult, error) {
	if c.delayCalls == nil {
		c.delayCalls = make(map[string]int)
	}
	c.delayCalls[name]++
	return protocol.DelayResult{Schema: "mihari/v1", Delays: map[string]uint16{name: c.delay}}, nil
}
