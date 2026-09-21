package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"testing"
)

func TestEgressPoll_PreReconnectResultCannotReplaceNewSession(t *testing.T) {
	m := NewModel()
	m.status = protocol.Status{PID: 42, Capabilities: []string{protocol.CapabilityEgress}}
	m.statusEpoch = 2
	p := m.pages[ui.PageSystem].(*systempage.Model)
	p.SetSnapshot(m.status, protocol.CoreStatus{})
	p.SetSize(100, 45)
	p.SetEgress(protocol.EgressStatus{Revision: 1, Selection: protocol.EgressSelection{Mode: "manual", InterfaceName: "current"}})
	_, _ = m.update(networkStatusMsg{egressPID: 42, egress: &protocol.EgressStatus{Revision: 99, Selection: protocol.EgressSelection{Mode: "manual", InterfaceName: "stale-adapter"}}})
	if strings.Contains(p.View(), "stale-adapter") {
		t.Fatal("pre-reconnect result replaced current selection")
	}
}

type shellEgressClient struct{ systempage.Client }

func (shellEgressClient) Egress(context.Context) (protocol.EgressStatus, error) {
	return protocol.EgressStatus{}, nil
}
func (shellEgressClient) UpdateEgress(context.Context, protocol.EgressUpdateRequest) (protocol.EgressStatus, error) {
	panic("keyboard browsing must not apply")
}
func TestEgressDialog_ShellKeepsQuitHelpAndDigitsInsideDialog(t *testing.T) {
	m := NewModel()
	m.width, m.height = 100, 45
	m.status = protocol.Status{Capabilities: []string{protocol.CapabilityEgress}}
	m.connected, m.mutationsEnabled = true, true
	m.active = ui.PageSystem
	m.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageSystem}
	p := systempage.New(shellEgressClient{}, func() string { return "op" })
	m.pages[ui.PageSystem] = p
	p.SetSize(90, 40)
	p.SetSnapshot(m.status, protocol.CoreStatus{})
	p.SetMutationsEnabled(true)
	p.SetEgress(protocol.EgressStatus{State: "saved", Selection: protocol.EgressSelection{Mode: "automatic"}})
	p.FocusFirst()
	press := func(key tea.KeyPressMsg) { next, _ := m.update(key); m = next.(Model) }
	for range 80 {
		press(tea.KeyPressMsg{Code: tea.KeyEnter})
		if p.HasEgressDialog() {
			break
		}
		press(tea.KeyPressMsg{Code: tea.KeyEscape})
		press(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if !p.HasEgressDialog() || m.inputMode != ui.InputText {
		t.Fatal("dialog did not take keyboard ownership")
	}
	for _, key := range []rune{'q', '?', '1'} {
		press(tea.KeyPressMsg{Code: key, Text: string(key)})
		if m.quitting || m.modal != nil || m.active != ui.PageSystem || !p.HasEgressDialog() {
			t.Fatalf("key %q escaped dialog", key)
		}
	}
	press(tea.KeyPressMsg{Code: tea.KeyEscape})
	if p.HasEgressDialog() || m.inputMode != ui.InputNavigation {
		t.Fatal("cancel did not release keyboard ownership")
	}
}
