package proxies

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestNavigation_GroupAndNodeArrowRules(t *testing.T) {
	model := New(nil, nil)
	// Width must leave section text width >= 2*proxyBarMaxWidth for a 2-column node grid.
	model.SetSize(80, 20)
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{
		{Name: "A", Nodes: []protocol.ProxyNode{{Name: "one"}, {Name: "two"}, {Name: "three"}}},
		{Name: "B", Nodes: []protocol.ProxyNode{{Name: "four"}}},
	}})

	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !model.expanded["A"] {
		t.Fatal("group did not expand")
	}
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	if model.focus != (FocusID{Group: "A", Node: "one"}) {
		t.Fatalf("focus=%#v", model.focus)
	}
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyRight})
	if model.focus.Node != "two" {
		t.Fatalf("right focus=%#v", model.focus)
	}
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	if model.focus.Node != "three" {
		t.Fatalf("down focus=%#v", model.focus)
	}
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyUp})
	if model.focus.Node != "one" {
		t.Fatalf("up focus=%#v", model.focus)
	}
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyLeft})
	if model.focus != (FocusID{Group: "A"}) {
		t.Fatalf("left focus=%#v", model.focus)
	}

	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if command != nil || model.focus != (FocusID{Group: "A"}) {
		t.Fatalf("group right changed focus=%#v", model.focus)
	}
	_, command = model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if command != nil || model.focus != (FocusID{Group: "A"}) {
		t.Fatalf("group left should stay on page: focus=%#v command=%v", model.focus, command != nil)
	}
	_, command = model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if command == nil {
		t.Fatal("esc did not return to rail")
	}
	if _, ok := command().(ui.FocusRailMsg); !ok {
		t.Fatalf("message=%T", command())
	}
}

func TestNavigation_ScrollKeepsFocusVisible(t *testing.T) {
	// Many collapsed groups: moving down past the viewport must advance scrollY
	// so the focused header remains in View output.
	const height = 5
	groups := make([]protocol.ProxyGroup, 20)
	for i := range groups {
		groups[i] = protocol.ProxyGroup{
			Name:  fmtGroup(i),
			Type:  "Selector",
			Nodes: []protocol.ProxyNode{{Name: "n"}},
		}
	}
	model := New(nil, nil)
	model.SetSize(40, height)
	model.SetGroups(protocol.ProxyGroups{Groups: groups})
	model.FocusFirst()
	model.SetContentFocused(true)

	if model.focus.Group != "G00" {
		t.Fatalf("focus=%#v", model.focus)
	}
	// Move far below the initial window.
	for i := 0; i < 12; i++ {
		updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if model.focus.Group != "G12" {
		t.Fatalf("focus after downs=%#v want G12", model.focus)
	}
	if model.scrollY <= 0 {
		t.Fatalf("scrollY=%d want > 0 after navigating past viewport", model.scrollY)
	}
	view := model.View()
	if !strings.Contains(view, "G12") {
		t.Fatalf("focused group missing from scrolled view:\n%s", view)
	}
	if strings.Contains(view, "G00") {
		t.Fatalf("scrolled view still shows first group:\n%s", view)
	}
	// Visible line count must not exceed height.
	if lines := strings.Count(view, "\n") + 1; lines > height {
		t.Fatalf("view lines=%d exceed height=%d\n%s", lines, height, view)
	}

	// Moving back to the top should scroll up and reveal G00.
	for i := 0; i < 12; i++ {
		updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyUp})
	}
	if model.focus.Group != "G00" {
		t.Fatalf("focus after ups=%#v", model.focus)
	}
	if model.scrollY != 0 {
		t.Fatalf("scrollY=%d want 0 at top", model.scrollY)
	}
	if !strings.Contains(model.View(), "G00") {
		t.Fatalf("top group missing after scroll up:\n%s", model.View())
	}
}

func TestNavigation_ScrollKeepsExpandedNodeVisible(t *testing.T) {
	// One expanded group with many nodes (1 column → each card is multi-line).
	nodes := make([]protocol.ProxyNode, 12)
	for i := range nodes {
		nodes[i] = protocol.ProxyNode{Name: fmt.Sprintf("node-%02d", i), Type: "Vless"}
	}
	model := New(nil, nil)
	model.SetSize(30, 8) // force vertical scroll among tall cards
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
		Name: "GLOBAL", Type: "Selector", Nodes: nodes,
	}}})
	model.FocusFirst()
	model.SetContentFocused(true)
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter}) // expand
	// Enter nodes and walk down.
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	for i := 0; i < 8; i++ {
		updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if model.focus.Node == "" {
		t.Fatalf("expected node focus, got %#v", model.focus)
	}
	view := model.View()
	if !strings.Contains(view, model.focus.Node) {
		t.Fatalf("focused node %q missing from scrolled view:\n%s", model.focus.Node, view)
	}
	if lines := strings.Count(view, "\n") + 1; lines > 8 {
		t.Fatalf("view lines=%d exceed height=8", lines)
	}
}

func fmtGroup(i int) string { return fmt.Sprintf("G%02d", i) }

func updateProxyKey(t *testing.T, model *Model, key tea.KeyPressMsg) tea.Cmd {
	t.Helper()
	updated, command := model.Update(key)
	if updated != model {
		t.Fatalf("model=%T", updated)
	}
	return command
}
