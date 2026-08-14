package proxies

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
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
	if command == nil {
		t.Fatal("delay test did not return a command")
	}
	// 't' returns Batch(delayResult, startDelaySpin); apply delay only (skip sleeping ticks).
	applyProxyCmd(t, model, command)
	view := model.View()
	for _, want := range []string{"VLESS / XUDP", "28 ms"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q: %s", want, view)
		}
	}
}

// applyProxyCmd expands BatchMsg and applies non-sleeping messages (delay/selection results).
func applyProxyCmd(t *testing.T, model *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			applyProxyCmd(t, model, child)
		}
		return
	}
	switch msg.(type) {
	case startDelaySpinMsg, delaySpinTickMsg:
		return
	}
	updated, next := model.Update(msg)
	if updated != model {
		t.Fatalf("model identity changed: %T", updated)
	}
	if next != nil {
		// Do not auto-run tea.Tick children.
		_, _ = next().(tea.BatchMsg)
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
	zero := time.Unix(0, 0)
	got := renderDelay(theme, DelayState{Kind: DelayTimeout}, zero)
	want := theme.DelayBad.Render(ui.TimeoutLabel)
	if got != want {
		t.Fatalf("timeout render=%q want=%q", got, want)
	}
	if !strings.Contains(got, ui.TimeoutLabel) {
		t.Fatalf("timeout label missing: %q", got)
	}
	// Untested is muted dash; good delay is green text.
	if renderDelay(theme, DelayState{Kind: DelayUntested}, zero) != theme.Muted.Render(ui.MissingValue) {
		t.Fatal("untested should use muted MissingValue")
	}
	if renderDelay(theme, DelayState{Kind: DelayValue, Milliseconds: 28}, zero) != theme.DelayGood.Render("28 ms") {
		t.Fatal("low latency should use DelayGood")
	}
	testingGot := renderDelay(theme, DelayState{Kind: DelayTesting}, zero)
	testingWant := theme.Warning.Render(ui.SpinnerLabel(zero, "Testing"))
	if testingGot != testingWant {
		t.Fatalf("testing should use Warning + braille SpinnerLabel: got %q want %q", testingGot, testingWant)
	}
	if !strings.Contains(testingGot, "Testing") || strings.Contains(testingGot, ui.TestingLabel) {
		// SpinnerLabel uses "Testing" without the old static ellipsis-only label alone.
		if !strings.Contains(testingGot, "Testing") {
			t.Fatalf("testing label missing: %q", testingGot)
		}
	}
}

func TestRenderDelay_TestingUsesBrailleSpinner(t *testing.T) {
	theme := ui.DefaultTheme()
	a := renderDelay(theme, DelayState{Kind: DelayTesting}, time.Unix(0, 0))
	b := renderDelay(theme, DelayState{Kind: DelayTesting}, time.Unix(0, 100*int64(time.Millisecond)))
	if a == b {
		t.Fatalf("expected spinner frame to advance with time: %q vs %q", a, b)
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	found := false
	for _, frame := range frames {
		if strings.Contains(a, frame) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("testing render missing braille frame: %q", a)
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
	if !ok || len(batch) < 3 {
		t.Fatalf("message=%T commands=%d", command(), len(batch))
	}
	// Apply delay results only; skip spinner start/tick cmds that would sleep.
	for _, delayCommand := range batch {
		msg := delayCommand()
		if _, isStart := msg.(startDelaySpinMsg); isStart {
			continue
		}
		if _, isTick := msg.(delaySpinTickMsg); isTick {
			continue
		}
		if _, isBatch := msg.(tea.BatchMsg); isBatch {
			continue
		}
		model.Update(msg)
	}
	if client.delayCalls["shared"] != 1 || client.delayCalls["only-a"] != 1 || client.delayCalls["only-b"] != 1 {
		t.Fatalf("calls=%v", client.delayCalls)
	}
}

type fakeClient struct {
	selectedGroup string
	selectedNode  string
	operationID   string
	selectErr     error
	delay         uint16
	delayErr      error
	delayCalls    map[string]int
}

func (c *fakeClient) SelectProxy(_ context.Context, group string, request protocol.ProxySelectionRequest) (protocol.MutationResult, error) {
	c.selectedGroup, c.selectedNode, c.operationID = group, request.Name, request.OperationID
	if c.selectErr != nil {
		return protocol.MutationResult{}, c.selectErr
	}
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

func TestSelectionResultMsg_ImplementsActionOutcomeContract(t *testing.T) {
	boom := errors.New("boom")
	if got := (selectionResultMsg{err: boom}).Err(); got != boom {
		t.Fatalf("Err()=%v want boom", got)
	}
	if got := (selectionResultMsg{}).Err(); got != nil {
		t.Fatalf("zero-value Err()=%v want nil", got)
	}
}

func TestModel_SelectProxyFailureShowsError(t *testing.T) {
	client := &fakeClient{selectErr: errors.New("upstream down")}
	model := New(client, func() string { return "op-1" })
	model.SetSize(80, 24)
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
		Name: "GLOBAL", Now: "old", Nodes: []protocol.ProxyNode{{Name: "node-a", Type: "VLESS"}},
	}}})
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter}) // 展开组
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})  // 焦点到节点
	applyProxyCmd(t, model, updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter}))

	if model.groups[0].Now != "old" {
		t.Fatalf("failed selection must not update Now, got %q", model.groups[0].Now)
	}
	if _, pending := model.pending[FocusID{Group: "GLOBAL", Node: "node-a"}]; pending {
		t.Fatal("pending marker should clear after failure")
	}
	view := model.View()
	if !strings.Contains(view, model.theme.Danger.Render(ui.ProxySelectFailed)) {
		t.Fatalf("view missing failure feedback %q:\n%s", ui.ProxySelectFailed, view)
	}
}

func TestView_GroupWrappedInBorderedSection(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 24)
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
		Name: "GLOBAL", Type: "Selector", Now: "node-a",
		Nodes: []protocol.ProxyNode{{Name: "node-a", Type: "VLESS"}},
	}}})
	view := model.View()
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Fatalf("missing section border:\n%s", view)
	}
	if !strings.Contains(view, "GLOBAL") || !strings.Contains(view, "SELECTOR") {
		t.Fatalf("missing title metadata:\n%s", view)
	}
	title := ui.FormatProxyGroupTitle("GLOBAL", "Selector", 1)
	if !strings.Contains(view, title) {
		t.Fatalf("missing group title %q in:\n%s", title, view)
	}
	if !strings.Contains(view, "Now:") {
		t.Fatalf("missing Now: body field:\n%s", view)
	}
}

func TestView_ExpandedNodesInsideSection(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 24)
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
		Name: "GLOBAL", Type: "Selector", Now: "node-a",
		Nodes: []protocol.ProxyNode{{Name: "node-a", Type: "VLESS"}},
	}}})
	model.expanded["GLOBAL"] = true
	view := model.View()
	plain := stripProxyANSI(view)
	top := strings.Index(plain, "╭")
	bottom := strings.LastIndex(plain, "╰")
	if top < 0 || bottom < 0 || bottom <= top {
		t.Fatalf("section borders missing:\n%s", plain)
	}
	block := plain[top : bottom+1]
	if !strings.Contains(block, "node-a") {
		t.Fatalf("node not inside section:\n%s", block)
	}
	if !strings.Contains(block, "VLESS") {
		t.Fatalf("node type not inside section:\n%s", block)
	}
}

func TestView_EmptyGroupsInSection(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 24)
	view := model.View()
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Fatalf("empty state missing section border:\n%s", view)
	}
	if !strings.Contains(view, ui.ProxiesSectionTitle) {
		t.Fatalf("empty state missing Proxies title:\n%s", view)
	}
	if !strings.Contains(view, ui.NoProxyGroups) {
		t.Fatalf("empty state missing empty message:\n%s", view)
	}
}

func stripProxyANSI(value string) string {
	var builder strings.Builder
	inEscape := false
	for _, r := range value {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && ((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')):
			inEscape = false
		case !inEscape:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
