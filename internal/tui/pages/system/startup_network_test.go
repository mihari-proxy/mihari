package system

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestNetworkStartup_ApplyingDoesNotBlockManualActions(t *testing.T) {
	for _, target := range []string{rowSystemProxy, rowTUN} {
		for _, desired := range []bool{false, true} {
			t.Run(target+"/"+map[bool]string{false: "off", true: "on"}[desired], func(t *testing.T) {
				m := New(&fakeClient{}, func() string { return "manual" })
				m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilitySystemProxy, protocol.CapabilityTUN}, StartupNetwork: &protocol.StartupNetworkStatus{SystemProxyApplying: target == rowSystemProxy, TunApplying: target == rowTUN}}, protocol.CoreStatus{})
				m.SetMutationsEnabled(true)
				m.SetSize(120, 45)
				m.SetContentFocused(true)
				live := !desired
				m.SetSystemProxy(protocol.SystemProxyStatus{Desired: desired, Observed: protocol.SystemProxyObserved{Enabled: live, Owned: live}})
				m.SetTun(protocol.TunStatus{DesiredEnable: desired, LiveEnable: &live, Managed: true})
				m.rowSpinClock = time.Unix(0, 0)
				for _, row := range m.networkRows() {
					if strings.Contains(row.value, "Applying…") != (row.id == target) {
						t.Errorf("row %s: %q", row.id, row.value)
					}
				}
				if cmd := m.rowSpinCmdIfNeeded(); cmd == nil {
					t.Fatal("startup did not arm animation")
				}
				before := m.View()
				_, tick := m.Update(rowSpinTickMsg{t: time.Unix(0, int64(rowSpinInterval)), gen: m.rowSpinGen})
				if tick == nil || before == m.View() {
					t.Fatal("startup animation did not advance")
				}
				if m.pending {
					t.Fatal("background application set manual pending")
				}
				m.focusID = rowTUNAction
				if target == rowSystemProxy {
					m.focusID = rowSystemProxyAction
				}
				_, command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				if command == nil {
					t.Fatal("manual toggle blocked")
				}
				if _, ok := command().(ui.ActionIntentMsg); !ok {
					t.Fatal("manual toggle lost its confirmation intent")
				}
				m.status.StartupNetwork = nil
				_, tick = m.Update(rowSpinTickMsg{t: time.Unix(1, 0), gen: m.rowSpinGen})
				if tick != nil || strings.Contains(m.View(), "Applying…") {
					t.Fatal("completed application kept spinning")
				}
			})
		}
	}
}

func TestNetworkStartup_DriftAloneAndDisconnectedDoNotAnimate(t *testing.T) {
	m := New(nil, nil)
	m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityTUN, protocol.CapabilitySystemProxy}}, protocol.CoreStatus{Status: "starting"})
	m.SetMutationsEnabled(true)
	live := false
	m.SetTun(protocol.TunStatus{DesiredEnable: true, LiveEnable: &live, Managed: true})
	m.SetSystemProxy(protocol.SystemProxyStatus{Desired: true})
	if m.rowSpinCmdIfNeeded() != nil {
		t.Fatal("drift or core starting inferred application")
	}
	m.status.StartupNetwork = &protocol.StartupNetworkStatus{SystemProxyApplying: true, TunApplying: true}
	m.SetMutationsEnabled(false)
	if m.rowSpinCmdIfNeeded() != nil || strings.Contains(m.View(), "Applying…") {
		t.Fatal("disconnected startup still animates")
	}
}

func TestNetworkStartup_BadgeFitsNarrowPage(t *testing.T) {
	m := New(nil, nil)
	m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilitySystemProxy, protocol.CapabilityTUN}, StartupNetwork: &protocol.StartupNetworkStatus{SystemProxyApplying: true, TunApplying: true}}, protocol.CoreStatus{})
	m.SetMutationsEnabled(true)
	live := false
	m.SetTun(protocol.TunStatus{DesiredEnable: true, LiveEnable: &live, Managed: true, Stack: "system"})
	m.SetSystemProxy(protocol.SystemProxyStatus{Desired: true, Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:9190", Owned: true}})
	for _, width := range []int{60, 80, 120} {
		m.SetSize(width, 60)
		if view := m.View(); strings.Count(view, "Applying…") != 2 {
			t.Fatalf("width=%d lost badge:\n%s", width, view)
		}
	}
}
