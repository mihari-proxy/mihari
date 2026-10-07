package proxies

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// TestPaging_CollapsedGroups verifies viewport-sized movement and clamped endpoints.
func TestPaging_CollapsedGroups(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(100, 12)
	var groups []protocol.ProxyGroup
	for i := range 20 {
		groups = append(groups, protocol.ProxyGroup{Name: fmtGroup(i)})
	}
	m.SetGroups(protocol.ProxyGroups{Groups: groups})
	for _, step := range []struct {
		key  rune
		want string
	}{
		{tea.KeyPgDown, "G04"}, {tea.KeyPgDown, "G08"}, {tea.KeyPgUp, "G04"},
		{tea.KeyPgUp, "G00"}, {tea.KeyPgUp, "G00"},
	} {
		if cmd := updateProxyKey(t, m, tea.KeyPressMsg{Code: step.key}); cmd != nil {
			t.Fatal("paging emitted an action")
		}
		if m.focus.Group != step.want {
			t.Fatalf("focus=%+v want=%s", m.focus, step.want)
		}
		assertPagingFocusVisible(t, m)
	}
	for range 10 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	if m.focus.Group != "G19" {
		t.Fatalf("bottom focus=%+v", m.focus)
	}
	assertPagingFocusVisible(t, m)
}

// TestPaging_NodeGrid preserves the column while paging across cards and window sizes.
func TestPaging_NodeGrid(t *testing.T) {
	for _, width := range []int{30, 80, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := New(nil, nil)
			m.SetSize(width, 12)
			var nodes []protocol.ProxyNode
			for i := range 60 {
				nodes = append(nodes, protocol.ProxyNode{Name: fmt.Sprintf("node-%02d", i)})
			}
			m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "A", Nodes: nodes}}})
			m.expanded["A"] = true
			column := m.columns() - 1
			m.focus = FocusID{Group: "A", Node: nodes[column].Name}
			m.ensureFocusVisible()
			m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
			if m.focus.Node != nodes[column+3*m.columns()].Name {
				t.Fatalf("page down focus=%+v", m.focus)
			}
			assertPagingFocusVisible(t, m)
			m.SetSize(width, 8)
			m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
			if m.focus.Node != nodes[column+m.columns()].Name {
				t.Fatalf("resized page up focus=%+v", m.focus)
			}
			assertPagingFocusVisible(t, m)
		})
	}
}

// TestPaging_RoutingHeader accounts for the fixed header and leaves dialogs in control.
func TestPaging_RoutingHeader(t *testing.T) {
	m := New(nil, nil)
	m.SetRoutingAvailable(true, 1)
	m.SetSize(100, 18)
	var groups []protocol.ProxyGroup
	for i := range 20 {
		groups = append(groups, protocol.ProxyGroup{Name: fmtGroup(i)})
	}
	m.SetGroups(protocol.ProxyGroups{Groups: groups})
	m.FocusFirst()
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.routing.focus != -1 || m.focus.Group == "G00" {
		t.Fatalf("page down did not enter the list: %+v", m.focus)
	}
	_, start, _ := m.buildContent(false)
	if delta := start - (m.height - len(m.routingHeader())); delta < -2 || delta > 2 {
		t.Fatalf("paging ignored fixed header: start=%d", start)
	}
	assertPagingFocusVisible(t, m)
	m.routing.open = true
	before, scroll := m.focus, m.scrollY
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.focus != before || m.scrollY != scroll {
		t.Fatal("page key escaped routing dialog")
	}
}

// TestPaging_MixedGroups handles partial grid rows, empty groups, and stale-data banners.
func TestPaging_MixedGroups(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			m := New(nil, nil)
			m.SetSize(100, 8)
			var nodes []protocol.ProxyNode
			for i := range 7 {
				nodes = append(nodes, protocol.ProxyNode{Name: fmt.Sprintf("node-%02d", i)})
			}
			m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{
				{Name: "A", Now: "node-00", Nodes: nodes}, {Name: "B"}, {Name: "C"},
			}})
			m.expanded["A"] = true
			if stale {
				m.loadError, m.lastError = "Synthetic refresh error", "Synthetic action error"
			}
			m.focus = FocusID{Group: "A", Node: "node-02"}
			m.ensureFocusVisible()
			for _, step := range []struct {
				key  rune
				want FocusID
			}{
				{tea.KeyPgDown, FocusID{Group: "A", Node: "node-06"}},
				{tea.KeyPgDown, FocusID{Group: "C"}},
				{tea.KeyPgDown, FocusID{Group: "C"}},
				{tea.KeyPgUp, FocusID{Group: "A", Node: "node-06"}},
			} {
				m.Update(tea.KeyPressMsg{Code: step.key})
				if m.focus != step.want {
					t.Fatalf("focus=%+v want=%+v", m.focus, step.want)
				}
				assertPagingFocusVisible(t, m)
			}
			if m.groups[0].Now != "node-00" || !m.expanded["A"] || m.expanded["B"] || m.expanded["C"] {
				t.Fatal("paging changed selection or expansion state")
			}
		})
	}
}

// TestPaging_EmptyPage keeps page keys safe before any groups are loaded.
func TestPaging_EmptyPage(t *testing.T) {
	m := New(nil, nil)
	for _, key := range []rune{tea.KeyPgUp, tea.KeyPgDown} {
		m.Update(tea.KeyPressMsg{Code: key})
		if m.focus != (FocusID{}) || m.scrollY != 0 {
			t.Fatal("empty page moved")
		}
	}
}

// assertPagingFocusVisible checks both the selected block and the rendered viewport height.
func assertPagingFocusVisible(t *testing.T, m *Model) {
	t.Helper()
	_, start, end := m.buildContent(false)
	height := max(1, m.height-len(m.routingHeader()))
	if start < m.scrollY || end > m.scrollY+height {
		t.Fatalf("focus [%d,%d) outside viewport [%d,%d)", start, end, m.scrollY, m.scrollY+height)
	}
	if strings.Count(m.View(), "\n")+1 > m.height {
		t.Fatal("view exceeds available height")
	}
}
