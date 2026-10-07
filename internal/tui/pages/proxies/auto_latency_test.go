package proxies

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func runImmediateTests(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runImmediateTests(t, m, c)
		}
		return
	}
	if routed, ok := msg.(ui.PageResultMsg); ok {
		msg = routed.Result
	}
	switch msg.(type) {
	case startDelaySpinMsg, delaySpinTickMsg:
		return
	}
	_, next := m.Update(msg)
	runImmediateTests(t, m, next)
}

func autoFixture() (*Model, *fakeClient) {
	c := &fakeClient{delay: 17}
	m := New(c, nil)
	m.SetSize(80, 6)
	nodes := []protocol.ProxyNode{}
	for i := 0; i < 9; i++ {
		nodes = append(nodes, protocol.ProxyNode{Name: fmt.Sprintf("n%d", i)})
	}
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "G", Now: "n8", Nodes: nodes}}})
	m.expanded["G"] = true
	return m, c
}

func TestAutoLatency_VisibleRowsAndSelectedNodeOnly(t *testing.T) {
	m, c := autoFixture()
	runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 1}))
	if len(c.delayCalls) != 3 || c.delayCalls["n0"] != 1 || c.delayCalls["n1"] != 1 || c.delayCalls["n8"] != 1 {
		t.Fatalf("initial calls=%v", c.delayCalls)
	}
	m.scrollY = 6
	m.autoDirty = true
	runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 1}))
	if c.delayCalls["n2"] != 1 || c.delayCalls["n3"] != 1 || c.delayCalls["n0"] != 1 {
		t.Fatalf("scrolled calls=%v", c.delayCalls)
	}
	m.scrollY = 0
	m.autoDirty = true
	runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 1}))
	if c.delayCalls["n0"] != 1 {
		t.Fatal("scrolling back retested seen node")
	}
	m.ReconcileAutoTests(false, true, 1, protocol.CoreStatus{PID: 1})
	if m.delays["n0"].Milliseconds != 17 {
		t.Fatal("leaving cleared completed result")
	}
	runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 1}))
	if c.delayCalls["n0"] != 2 {
		t.Fatal("reentry did not retest")
	}
}

func TestAutoLatency_PartialContentCountsButBorderDoesNot(t *testing.T) {
	for _, height := range []int{3, 4} {
		m, c := autoFixture()
		m.SetSize(80, height)
		runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 1}))
		want := 0
		if height == 4 {
			want = 1
		}
		if c.delayCalls["n0"] != want {
			t.Fatalf("height=%d calls=%v", height, c.delayCalls)
		}
	}
}

func TestAutoLatency_PageDownTestsNewlyVisibleCards(t *testing.T) {
	m, c := autoFixture()
	runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 1}))
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 1}))
	if c.delayCalls["n2"] != 1 || c.delayCalls["n3"] != 1 {
		t.Fatalf("page down did not discover visible cards: %v", c.delayCalls)
	}
	if c.delayCalls["n4"] != 1 || c.delayCalls["n5"] != 1 {
		t.Fatalf("page down did not discover partially visible cards: %v", c.delayCalls)
	}
	if c.delayCalls["n6"] != 0 || c.delayCalls["n0"] != 1 || c.delayCalls["n8"] != 1 {
		t.Fatalf("paging tested hidden or already seen nodes: %v", c.delayCalls)
	}
}

func TestAutoLatency_DisabledAndNewCoreReset(t *testing.T) {
	m, c := autoFixture()
	m.SetPreferences(protocol.TUIPreferences{Proxies: &protocol.ProxyPreferences{ExtraLatency: true}})
	if cmd := m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 1}); cmd != nil {
		t.Fatal("disabled auto started")
	}
	runImmediateTests(t, m, m.testNode("n0"))
	m.SetPreferences(protocol.TUIPreferences{})
	runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 1}))
	runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 2}))
	if c.delayCalls["n0"] != 3 {
		t.Fatalf("calls=%v", c.delayCalls)
	}
}

type cancelDelayClient struct {
	fakeClient
	started chan context.Context
}

func (c *cancelDelayClient) DelayProxy(ctx context.Context, _ string, _ protocol.DelayTestRequest) (protocol.DelayResult, error) {
	c.started <- ctx
	<-ctx.Done()
	return protocol.DelayResult{}, ctx.Err()
}

func TestAutoLatency_LeaveCancelsAndLateResultCannotOverwrite(t *testing.T) {
	c := &cancelDelayClient{started: make(chan context.Context, 1)}
	m := New(c, nil)
	m.SetSize(80, 10)
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "G", Now: "n", Nodes: []protocol.ProxyNode{{Name: "n"}}}}})
	cmd := m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 1})
	batch := cmd().(tea.BatchMsg)
	result := make(chan tea.Msg, 1)
	go func() { result <- batch[0]() }()
	t.Cleanup(m.Stop)
	select {
	case <-c.started:
	case <-time.After(time.Second):
		t.Fatal("test did not start")
	}
	m.ReconcileAutoTests(false, true, 1, protocol.CoreStatus{PID: 1})
	select {
	case msg := <-result:
		reported, ok := msg.(ui.PageResultMsg).Result.(interface{ DiagnosticErrors() []error })
		if !ok || len(reported.DiagnosticErrors()) != 0 {
			t.Fatal("owned cancellation must not create an error diagnostic")
		}
		m.delays["n"] = DelayState{Kind: DelayValue, Milliseconds: 88}
		m.Update(msg)
		if m.delays["n"].Milliseconds != 88 || len(m.inFlight) != 0 {
			t.Fatal("canceled result overwrote state or leaked slot")
		}
	case <-time.After(time.Second):
		t.Fatal("leave did not cancel request")
	}
}

func TestAutoLatency_OverlayPreservesRoundAndSubscriptionResetsIt(t *testing.T) {
	m, c := autoFixture()
	core := protocol.CoreStatus{PID: 1}
	runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, core))
	m.SetObscured(true)
	m.scrollY = 6
	m.autoDirty = true
	if cmd := m.ReconcileAutoTests(true, true, 1, core); cmd != nil {
		t.Fatal("covered page discovered new nodes")
	}
	m.SetObscured(false)
	runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, core))
	if c.delayCalls["n2"] != 1 || c.delayCalls["n0"] != 1 {
		t.Fatalf("overlay calls=%v", c.delayCalls)
	}
	m.SetGroups(protocol.ProxyGroups{SubscriptionID: "replacement", Groups: m.groups})
	runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, core))
	if c.delayCalls["n0"] != 2 {
		t.Fatalf("subscription did not reset round: %v", c.delayCalls)
	}
}

func TestAutoLatency_QueueSurvivesScrollAndFailureWaitsForNextRound(t *testing.T) {
	m, c := autoFixture()
	m.SetSize(160, 7)
	c.delayErr = errors.New("synthetic upstream failure")
	core := protocol.CoreStatus{PID: 1}
	cmd := m.ReconcileAutoTests(true, true, 1, core)
	if len(m.inFlight) != 5 || len(m.queue) == 0 {
		t.Fatalf("inflight=%d queued=%v", len(m.inFlight), m.queue)
	}
	m.scrollY = 100
	m.autoDirty = true
	runImmediateTests(t, m, cmd)
	before := len(c.delayCalls)
	if before < 6 {
		t.Fatalf("queued nodes lost after scroll: %v", c.delayCalls)
	}
	m.scrollY = 0
	m.autoDirty = true
	runImmediateTests(t, m, m.ReconcileAutoTests(true, true, 1, core))
	for name, calls := range c.delayCalls {
		if calls != 1 {
			t.Fatalf("failed node %s retried %d times", name, calls)
		}
	}
}

func TestDelayDiagnostics_ForeignCancellationAndCleanupAreReported(t *testing.T) {
	for _, cause := range []error{context.Canceled, errors.Join(context.Canceled, errors.New("cleanup failed"))} {
		m := New(&fakeClient{delayErr: cause}, nil)
		result := m.startDelay("node")().(ui.PageResultMsg).Result.(delayResultMsg)
		if len(result.DiagnosticErrors()) != 1 || !errors.Is(result.Err(), context.Canceled) {
			t.Fatalf("lost cause: %+v", result)
		}
		m.Update(result)
	}
}
