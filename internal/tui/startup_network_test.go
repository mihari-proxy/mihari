package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestStartupNetwork_SystemBadgeUsesRootStatusAndAllowsNavigation(t *testing.T) {
	m := NewModel()
	m.applySessionEvent(session.Event{Kind: session.EventConnected})
	cmd := m.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{Capabilities: []string{protocol.CapabilityTUN, protocol.CapabilitySystemProxy}, StartupNetwork: &protocol.StartupNetworkStatus{TunApplying: true, SystemProxyApplying: true}}})
	p := m.pages[ui.PageSystem].(*systempage.Model)
	p.SetSize(100, 60)
	view := p.View()
	if cmd == nil || strings.Count(view, "Applying…") != 2 || !strings.Contains(view, ui.LoadingLabel) {
		t.Fatalf("startup not displayed before live snapshot:\n%s", view)
	}
	if m.modal != nil {
		t.Fatal("startup opened an action dialog")
	}
	m.active = ui.PageSystem
	m.focus = ui.Focus{Area: ui.FocusRail, Page: ui.PageSystem}
	updated, _ := m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	m = updated.(Model)
	if m.active != ui.PageOverview {
		t.Fatal("startup blocked page navigation")
	}
	m.applySessionEvent(session.Event{Kind: session.EventReconnecting})
	if strings.Contains(p.View(), "Applying…") {
		t.Fatal("disconnected badge still applying")
	}
	m.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{Capabilities: []string{protocol.CapabilityTUN, protocol.CapabilitySystemProxy}}})
	m.applySessionEvent(session.Event{Kind: session.EventConnected})
	if strings.Contains(p.View(), "Applying…") {
		t.Fatal("reconnected without flag but retained old badge")
	}
}
