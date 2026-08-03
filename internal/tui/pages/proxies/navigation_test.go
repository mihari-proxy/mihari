package proxies

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestNavigation_GroupAndNodeArrowRules(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(56, 20)
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
	if command == nil {
		t.Fatal("group left did not return to rail")
	}
	if _, ok := command().(ui.FocusRailMsg); !ok {
		t.Fatalf("message=%T", command())
	}
}

func updateProxyKey(t *testing.T, model *Model, key tea.KeyPressMsg) tea.Cmd {
	t.Helper()
	updated, command := model.Update(key)
	if updated != model {
		t.Fatalf("model=%T", updated)
	}
	return command
}
