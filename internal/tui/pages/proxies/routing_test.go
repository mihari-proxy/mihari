package proxies

import (
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"testing"
	"time"
)

type modeClient struct {
	fakeClient
	updates int
	request protocol.RoutingUpdateRequest
}

func TestModePicker_AppliesOnceWithRevisionAndIgnoresOldResult(t *testing.T) {
	c := &modeClient{}
	m := New(c, func() string { return "mode-ui" })
	m.SetSize(58, 20)
	m.SetRoutingAvailable(true, 1)
	m.SetRouting(protocol.RoutingStatus{DesiredMode: "rule", LiveMode: "rule", State: "applied", Revision: 3}, 1)
	m.FocusFirst()
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("no change intent")
	}
	intent := cmd().(ui.ActionIntentMsg)
	_, duplicate := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if duplicate != nil {
		t.Fatal("duplicate submission")
	}
	result := intent.Execute()
	if c.updates != 1 || c.request.Mode != "global" || c.request.IfRevision == nil || *c.request.IfRevision != 3 {
		t.Fatalf("request=%+v", c.request)
	}
	m.SetRoutingAvailable(false, 2)
	m.SetRoutingAvailable(true, 2)
	m.SetRouting(protocol.RoutingStatus{DesiredMode: "direct", Revision: 1}, 2)
	m.Update(result)
	if m.routing.status.DesiredMode != "direct" {
		t.Fatal("old result overwrote new session")
	}
}

func TestModePicker_FailureKeepsSelectionAndFitsCompactViewport(t *testing.T) {
	m := New(&modeClient{}, nil)
	m.SetSize(58, 20)
	m.SetRoutingAvailable(true, 1)
	m.SetRouting(protocol.RoutingStatus{DesiredMode: "rule", State: "applied"}, 1)
	m.FocusFirst()
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(routingResultMsg{epoch: 1, err: errors.New("private error")})
	if m.routing.known {
		t.Fatal("failed or uncertain response allowed retry before refresh")
	}
	view := m.View()
	if !m.routing.open || m.routing.cursor != 1 || strings.Contains(view, "private error") || !strings.Contains(view, "Could not confirm") {
		t.Fatal("failure state was lost or leaked")
	}
	if lipgloss.Width(view) > 58 || lipgloss.Height(view) > 20 {
		t.Fatalf("dialog overflows: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
	}
	if m.submitRouting() != nil {
		t.Fatal("retry did not wait for refresh")
	}
}

func TestRouting_GLOBALRequiresCurrentSubscriptionCandidates(t *testing.T) {
	m := New(&modeClient{}, nil)
	m.SetRoutingAvailable(true, 1)
	m.SetRouting(protocol.RoutingStatus{DesiredMode: "rule", SubscriptionID: "a", Revision: 3, State: "applied"}, 1)
	revision := uint64(3)
	m.SetGroups(protocol.ProxyGroups{Revision: &revision, SubscriptionID: "b", Groups: []protocol.ProxyGroup{{Name: "GLOBAL", Type: "Selector", All: []string{"DIRECT"}, Nodes: []protocol.ProxyNode{{Name: "DIRECT"}}}}})
	m.routing.focus = -1
	m.focus = FocusID{Group: "GLOBAL", Node: "DIRECT"}
	if m.selectFocused() != nil {
		t.Fatal("foreign subscription candidate accepted")
	}
	m.groupsSubscription = "a"
	if m.selectFocused() == nil {
		t.Fatal("current candidate disabled")
	}
	m.InvalidateGroups()
	if m.selectFocused() != nil {
		t.Fatal("stale candidate accepted")
	}
}

func (c *modeClient) UpdateRouting(_ context.Context, request protocol.RoutingUpdateRequest) (protocol.RoutingStatus, error) {
	c.updates++
	c.request = request
	return protocol.RoutingStatus{Schema: "mihari/v1", Revision: 4, DesiredMode: request.Mode, LiveMode: request.Mode, State: "applied"}, nil
}

func TestModePicker_EnterOpensAndEscapeDoesNotApply(t *testing.T) {
	c := &modeClient{}
	m := New(c, func() string { return "mode-ui" })
	m.SetSize(80, 24)
	m.SetRoutingAvailable(true, 1)
	m.SetRouting(protocol.RoutingStatus{DesiredMode: "rule", LiveMode: "rule", State: "applied", Revision: 3}, 1)
	m.FocusFirst()
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.View(), "Routing Mode") || !strings.Contains(m.View(), "Global") {
		t.Fatalf("picker did not open: %s", m.View())
	}
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if c.updates != 0 || strings.Contains(m.View(), "Routing Mode") {
		t.Fatal("cancel applied a mode or left picker open")
	}
}

func TestRouting_LateGLOBALSelectionCannotOverwriteNewSubscription(t *testing.T) {
	m := New(&modeClient{}, nil)
	m.SetRoutingAvailable(true, 1)
	revision := uint64(3)
	m.SetRouting(protocol.RoutingStatus{DesiredMode: "rule", State: "applied", Revision: revision, SubscriptionID: "a"}, 1)
	groups := protocol.ProxyGroups{Revision: &revision, SubscriptionID: "a", Groups: []protocol.ProxyGroup{{Name: "GLOBAL", Type: "Selector", Now: "DIRECT", All: []string{"DIRECT", "REJECT"}, Nodes: []protocol.ProxyNode{{Name: "DIRECT"}, {Name: "REJECT"}}}}}
	m.SetGroups(groups)
	m.routing.focus = -1
	m.focus = FocusID{Group: "GLOBAL", Node: "REJECT"}
	cmd := m.selectFocused()
	if cmd == nil {
		t.Fatal("no selection command")
	}
	m.SetRouting(protocol.RoutingStatus{DesiredMode: "rule", State: "applied", Revision: 4, SubscriptionID: "b"}, 1)
	groups.SubscriptionID = "b"
	m.SetGroups(groups)
	result := cmd()
	if routed, ok := result.(ui.PageResultMsg); ok {
		result = routed.Result
	}
	m.Update(result)
	if m.groups[0].Now != "DIRECT" {
		t.Fatal("late result changed new subscription")
	}
}

func TestRouting_FailedProviderSnapshotRetainsModeAndDisablesGLOBAL(t *testing.T) {
	m := New(&modeClient{}, nil)
	m.SetSize(84, 26)
	m.SetRoutingAvailable(true, 1)
	revision := uint64(3)
	m.SetRouting(protocol.RoutingStatus{Revision: revision, DesiredMode: "global", LiveMode: "global", State: "applied", GlobalSelection: "DIRECT"}, 1)
	m.ObserveSnapshot(protocol.ProxyGroups{Revision: &revision, Groups: []protocol.ProxyGroup{{Name: "GLOBAL", Type: "Selector", Now: "DIRECT", All: []string{"DIRECT"}, Nodes: []protocol.ProxyNode{{Name: "DIRECT", Type: "Direct"}}}}}, time.Now(), nil)
	if !m.globalCandidatesCurrent() {
		t.Fatal("fresh GLOBAL disabled")
	}
	m.ObserveSnapshot(protocol.ProxyGroups{}, time.Now(), errors.New("provider failure"))
	if m.globalCandidatesCurrent() {
		t.Fatal("failed provider snapshot kept stale GLOBAL selectable")
	}
	view := m.View()
	for _, want := range []string{"Mode", "Global", "Stale data", "Waiting for candidates"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %s", want)
		}
	}
}
