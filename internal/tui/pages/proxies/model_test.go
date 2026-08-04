package proxies

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
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
	if !strings.Contains(plain, ui.FocusMarker) {
		t.Fatalf("focus marker missing while rail owns focus:\n%s", plain)
	}

	model.SetContentFocused(true)
	accented := model.View()
	if plain == accented {
		t.Fatal("content focus should change node border accent styling")
	}
}

func TestDelayStyle_BandsAndTimeout(t *testing.T) {
	theme := ui.DefaultTheme()
	cases := []struct {
		name  string
		delay DelayState
		want  string // good | mid | bad | warning | muted
	}{
		{"untested", DelayState{Kind: DelayUntested}, "muted"},
		{"testing", DelayState{Kind: DelayTesting}, "warning"},
		{"timeout", DelayState{Kind: DelayTimeout}, "bad"},
		{"good_28", DelayState{Kind: DelayValue, Milliseconds: 28}, "good"},
		{"good_99", DelayState{Kind: DelayValue, Milliseconds: 99}, "good"},
		{"mid_100", DelayState{Kind: DelayValue, Milliseconds: 100}, "mid"},
		{"mid_150", DelayState{Kind: DelayValue, Milliseconds: 150}, "mid"},
		{"mid_399", DelayState{Kind: DelayValue, Milliseconds: 399}, "mid"},
		{"bad_400", DelayState{Kind: DelayValue, Milliseconds: 400}, "bad"},
		{"bad_500", DelayState{Kind: DelayValue, Milliseconds: 500}, "bad"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := delayStyle(theme, tc.delay).Render("probe")
			var want string
			switch tc.want {
			case "good":
				want = theme.DelayGood.Render("probe")
			case "mid":
				want = theme.DelayMid.Render("probe")
			case "bad":
				want = theme.DelayBad.Render("probe")
			case "warning":
				want = theme.Warning.Render("probe")
			case "muted":
				want = theme.Muted.Render("probe")
			}
			if got != want {
				t.Fatalf("delayStyle(%+v) band=%s mismatch", tc.delay, tc.want)
			}
		})
	}
}

func TestRenderDelay_TimeoutUsesDangerStyle(t *testing.T) {
	theme := ui.DefaultTheme()
	got := renderDelay(theme, DelayState{Kind: DelayTimeout})
	want := theme.DelayBad.Render(ui.TimeoutLabel)
	if got != want {
		t.Fatalf("timeout render=%q want=%q", got, want)
	}
	if !strings.Contains(got, ui.TimeoutLabel) {
		t.Fatalf("timeout label missing: %q", got)
	}
	// Untested is muted dash; good delay is green text.
	if renderDelay(theme, DelayState{Kind: DelayUntested}) != theme.Muted.Render(ui.MissingValue) {
		t.Fatal("untested should use muted MissingValue")
	}
	if renderDelay(theme, DelayState{Kind: DelayValue, Milliseconds: 28}) != theme.DelayGood.Render("28 ms") {
		t.Fatal("low latency should use DelayGood")
	}
	if renderDelay(theme, DelayState{Kind: DelayTesting}) != theme.Warning.Render(ui.TestingLabel) {
		t.Fatal("testing should use Warning style")
	}
}

func TestModel_DelayTimeoutPath(t *testing.T) {
	client := &fakeClient{delayErr: errors.New("timeout")}
	model := New(client, func() string { return "op" })
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
		Name: "G", Nodes: []protocol.ProxyNode{{Name: "n1", Type: "ss"}},
	}}})
	model.expanded["G"] = true
	model.focus = FocusID{Group: "G", Node: "n1"}
	cmd := model.testNode("n1")
	if cmd == nil {
		t.Fatal("expected delay command")
	}
	updated, _ := model.Update(cmd())
	model = updated.(*Model)
	if model.delays["n1"].Kind != DelayTimeout {
		t.Fatalf("delay kind=%v", model.delays["n1"].Kind)
	}
	view := model.View()
	if !strings.Contains(view, ui.TimeoutLabel) {
		t.Fatalf("view missing Timeout: %s", view)
	}
	// Timeout text should carry danger styling (ANSI), not plain Timeout alone.
	styled := themeDelayBadContains(model.theme, view, ui.TimeoutLabel)
	if !styled {
		t.Fatalf("Timeout should be styled with DelayBad in view:\n%s", view)
	}
}

func themeDelayBadContains(theme ui.Theme, view, label string) bool {
	return strings.Contains(view, theme.DelayBad.Render(label))
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
	delayErr      error
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
	if c.delayErr != nil {
		return protocol.DelayResult{}, c.delayErr
	}
	return protocol.DelayResult{Schema: "mihari/v1", Delays: map[string]uint16{name: c.delay}}, nil
}
