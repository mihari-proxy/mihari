package proxies

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// newLocateModel supplies a non-first selection and a second group for navigation tests.
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

// locateCurrent exercises the public key path and rejects asynchronous side effects.
func locateCurrent(t *testing.T, m *Model) {
	t.Helper()
	if cmd := updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight}); cmd != nil {
		t.Fatal("focusing Locate returned a command")
	}
	if cmd := updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("Locate returned a command")
	}
}

// TestLocate_EnterFocusesCurrentCandidate covers both expansion states without changing the selection.
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

// TestLocate_HeaderButtonNavigation keeps expansion separate from the Locate action.
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

// TestLocate_ButtonVerticalNavigation verifies that the button shares its header's vertical position.
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

// TestLocate_EscapeReturnsToRail preserves the page's existing escape contract.
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

// TestLocate_MissingTargetIsDisabled keeps missing selections focusable but inert.
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

// TestLocate_NestedGroupTargetsDirectCandidate prevents recursive navigation to another group.
func TestLocate_NestedGroupTargetsDirectCandidate(t *testing.T) {
	m, _ := newLocateModel()
	m.groups[0].Now = "Auto"
	m.groups = append(m.groups, protocol.ProxyGroup{Name: "Auto", Now: "leaf", Nodes: []protocol.ProxyNode{{Name: "leaf"}}})
	locateCurrent(t, m)
	if m.focus != (FocusID{Group: "A", Node: "Auto"}) || m.expanded["Auto"] {
		t.Fatalf("Locate followed the nested group: %+v", m.focus)
	}
}

// TestLocate_StaleSnapshotRemainsUsable separates local navigation from mutation freshness checks.
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

// TestLocate_UsesLatestSnapshotOnEnter avoids caching a target when the button first receives focus.
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

// TestLocate_RefreshPreservesFocus leaves browsing focus in place when Now changes.
func TestLocate_RefreshPreservesFocus(t *testing.T) {
	m, _ := newLocateModel()
	locateCurrent(t, m)
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "A", Now: "one", Nodes: m.groups[0].Nodes}}})
	if m.focus != (FocusID{Group: "A", Node: "two"}) {
		t.Fatalf("refresh stole focus: %+v", m.focus)
	}
}

// TestLocate_RemovingFocusedNodeReturnsToHeader checks recovery when the focused card disappears.
func TestLocate_RemovingFocusedNodeReturnsToHeader(t *testing.T) {
	m, _ := newLocateModel()
	locateCurrent(t, m)
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "A", Now: "one", Nodes: []protocol.ProxyNode{{Name: "one"}}}}})
	if m.focus != (FocusID{Group: "A"}) {
		t.Fatalf("missing candidate did not return to header: %+v", m.focus)
	}
}

// TestLocate_EmptySnapshotClearsFocus prevents stale controls from surviving an empty snapshot.
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

// TestLocate_FooterExplainsAction checks the button's hint and restoration of header hints.
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

// TestLocate_ButtonTracksTargetAvailability follows a missing candidate through its return.
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
