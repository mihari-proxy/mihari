package proxies

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func newLocateModel() (*Model, *fakeClient) {
	client := &fakeClient{}
	m := New(client, nil)
	m.SetSize(80, 20)
	m.SetContentFocused(true)
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{
		{Name: "A", Now: "two", Nodes: []protocol.ProxyNode{{Name: "one"}, {Name: "two"}, {Name: "Auto"}}},
		{Name: "B", Now: "other", Nodes: []protocol.ProxyNode{{Name: "other"}}},
	}})
	return m, client
}

func locateCurrent(t *testing.T, m *Model) {
	t.Helper()
	if cmd := updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight}); cmd != nil {
		t.Fatal("focusing Locate returned a command")
	}
	if cmd := updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("Locate returned a command")
	}
}

func TestLocate_EnterFocusesCurrentCandidate(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		t.Run(map[bool]string{false: "collapsed", true: "expanded"}[expanded], func(t *testing.T) {
			m, client := newLocateModel()
			m.expanded["A"] = expanded
			locateCurrent(t, m)
			if !m.expanded["A"] || m.focus != (FocusID{Group: "A", Node: "two"}) {
				t.Fatalf("Locate did not expand and focus the selected card: %+v", m.focus)
			}
			if m.groups[0].Now != "two" || client.selectedGroup != "" || len(client.delayCalls) != 0 || len(m.pending) != 0 {
				t.Fatal("Locate changed selection or started a mutation")
			}
		})
	}
}

func TestLocate_HeaderButtonNavigation(t *testing.T) {
	m, _ := newLocateModel()
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.focus == (FocusID{Group: "A"}) {
		t.Fatal("Right did not focus Locate")
	}
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.focus != (FocusID{Group: "A"}) {
		t.Fatal("Left did not return to the header")
	}
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.expanded["A"] || m.focus != (FocusID{Group: "A"}) {
		t.Fatal("header Enter must only toggle expansion")
	}
}

func TestLocate_ButtonVerticalNavigation(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		m, _ := newLocateModel()
		m.expanded["A"] = expanded
		updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
		updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
		want := FocusID{Group: "B"}
		if expanded {
			want = FocusID{Group: "A", Node: "one"}
		}
		if m.focus != want {
			t.Fatalf("expanded=%v down focus=%+v want=%+v", expanded, m.focus, want)
		}
	}
	m, _ := newLocateModel()
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.focus != (FocusID{Group: "A"}) {
		t.Fatalf("up focus=%+v", m.focus)
	}
}

func TestLocate_EscapeReturnsToRail(t *testing.T) {
	m, _ := newLocateModel()
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	cmd := updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("Esc returned no command")
	}
	if _, ok := cmd().(ui.FocusRailMsg); !ok {
		t.Fatal("Esc did not return to rail")
	}
}

func TestLocate_MissingTargetIsDisabled(t *testing.T) {
	for _, now := range []string{"", "missing"} {
		m, client := newLocateModel()
		m.groups[0].Now = now
		updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
		button := m.focus
		if button == (FocusID{Group: "A"}) {
			t.Fatal("disabled Locate cannot be focused")
		}
		cmd := updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd != nil || m.focus != button || m.expanded["A"] || client.selectedGroup != "" {
			t.Fatal("disabled Locate changed state")
		}
	}
}

func TestLocate_NestedGroupTargetsDirectCandidate(t *testing.T) {
	m, _ := newLocateModel()
	m.groups[0].Now = "Auto"
	m.groups = append(m.groups, protocol.ProxyGroup{Name: "Auto", Now: "leaf", Nodes: []protocol.ProxyNode{{Name: "leaf"}}})
	locateCurrent(t, m)
	if m.focus != (FocusID{Group: "A", Node: "Auto"}) || m.expanded["Auto"] {
		t.Fatalf("Locate followed the nested group: %+v", m.focus)
	}
}

func TestLocate_StaleSnapshotRemainsUsable(t *testing.T) {
	m, _ := newLocateModel()
	m.ObserveSnapshot(protocol.ProxyGroups{Groups: m.groups}, time.Unix(100, 0), nil)
	m.ObserveSnapshot(protocol.ProxyGroups{}, time.Time{}, protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "Refresh failed"})
	if m.groupsFresh || !strings.Contains(m.View(), "Last selected:") {
		t.Fatal("fixture did not retain a stale snapshot")
	}
	locateCurrent(t, m)
	if m.focus != (FocusID{Group: "A", Node: "two"}) {
		t.Fatalf("stale target not located: %+v", m.focus)
	}
}

func TestLocate_UsesLatestSnapshotOnEnter(t *testing.T) {
	m, _ := newLocateModel()
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	groups := protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "A", Now: "one", Nodes: m.groups[0].Nodes}}}
	m.SetGroups(groups)
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.focus != (FocusID{Group: "A", Node: "one"}) {
		t.Fatalf("Locate used an old target: %+v", m.focus)
	}
}

func TestLocate_RefreshPreservesFocus(t *testing.T) {
	m, _ := newLocateModel()
	locateCurrent(t, m)
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "A", Now: "one", Nodes: m.groups[0].Nodes}}})
	if m.focus != (FocusID{Group: "A", Node: "two"}) {
		t.Fatalf("refresh stole focus: %+v", m.focus)
	}
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "A", Now: "one", Nodes: []protocol.ProxyNode{{Name: "one"}}}}})
	if m.focus != (FocusID{Group: "A"}) {
		t.Fatalf("missing candidate did not return to header: %+v", m.focus)
	}
}

func TestLocate_EmptySnapshotClearsFocus(t *testing.T) {
	m, _ := newLocateModel()
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	m.SetGroups(protocol.ProxyGroups{})
	if m.focus != (FocusID{}) {
		t.Fatalf("empty snapshot retained an invalid focus: %+v", m.focus)
	}
	for _, key := range []rune{tea.KeyEnter, tea.KeyRight} {
		if cmd := updateProxyKey(t, m, tea.KeyPressMsg{Code: key}); cmd != nil {
			t.Fatal("empty snapshot generated a command")
		}
	}
}

func TestLocate_FooterExplainsAction(t *testing.T) {
	m, _ := newLocateModel()
	before := m.FooterHints()
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if !strings.Contains(m.FooterHints(), "Enter locate") || !strings.Contains(m.FooterHints(), "← group") {
		t.Fatal("Locate footer does not explain its action and return key")
	}
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.FooterHints() != before {
		t.Fatal("header footer was not restored")
	}
}

func TestLocate_ButtonTracksTargetAvailability(t *testing.T) {
	m, _ := newLocateModel()
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	button := m.focus
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "A", Now: "two"}}})
	if m.focus != button {
		t.Fatal("missing target displaced the button focus")
	}
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.focus != button || m.expanded["A"] {
		t.Fatal("missing candidate remained actionable")
	}
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "A", Now: "two", Nodes: []protocol.ProxyNode{{Name: "two"}}}}})
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.focus != (FocusID{Group: "A", Node: "two"}) {
		t.Fatal("returning candidate did not enable Locate")
	}
}
