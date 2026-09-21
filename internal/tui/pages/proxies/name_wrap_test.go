package proxies

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestRenderNode_WrapsCompleteName(t *testing.T) {
	for _, name := range []string{
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		"Hong Kong Premium Streaming Node",
		"香港高速线路专用节点支持流媒体解锁",
		"🇭🇰香港🌏e\u0301家庭线路👨‍👩‍👧‍👦专用节点尾部",
		"ABCDEFGHIJKLMN", // Exactly fills the name area at width 22.
	} {
		for _, width := range []int{18, 22, 28} {
			t.Run(fmt.Sprintf("%s/%d", name, width), func(t *testing.T) {
				m := New(nil, nil)
				node := protocol.ProxyNode{Name: name, Type: "VLESS"}
				group := protocol.ProxyGroup{Name: "G", Now: name}
				m.focus = FocusID{Group: "G", Node: name}
				m.delays[name] = DelayState{Kind: DelayValue, Milliseconds: 28}
				card := ansi.Strip(m.renderNode(group, node, width))
				lines := strings.Split(card, "\n")
				if !strings.Contains(lines[1], "› ● ") {
					t.Fatalf("missing focus/selection markers:\n%s", card)
				}
				var displayed strings.Builder
				for i, line := range lines[1 : len(lines)-2] {
					if i > 0 && !strings.HasPrefix(line, "│     ") {
						t.Fatalf("continuation does not align with name: %q", line)
					}
					displayed.WriteString(strings.TrimSpace(ansi.Cut(line, 6, width-2)))
				}
				compact := func(s string) string { return strings.Join(strings.Fields(s), "") }
				if got, want := compact(displayed.String()), compact(ui.DisplayProxyName(name)); got != want {
					t.Fatalf("name lost text: got %q want %q\n%s", got, want, card)
				}
				if !strings.Contains(lines[len(lines)-2], "VLESS  28 ms") {
					t.Fatalf("metadata must follow the full name:\n%s", card)
				}
				for _, line := range lines {
					if lipgloss.Width(line) != width {
						t.Fatalf("card width=%d want=%d: %q", lipgloss.Width(line), width, line)
					}
				}
			})
		}
	}
}

func TestView_WrappedNamesAlignCardsWithinEachRow(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(80, 30) // Two columns.
	m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "G", Nodes: []protocol.ProxyNode{
		{Name: "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789TAIL", Type: "VLESS"},
		{Name: "short", Type: "VLESS"},
		{Name: "next-row", Type: "VLESS"},
	}}}})
	m.expanded["G"] = true
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "TAIL") {
		t.Fatalf("wrapped name tail missing:\n%s", view)
	}
	var tops, bottoms []int
	for i, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "│ ╭") {
			tops = append(tops, i)
		}
		if strings.Contains(line, "│ ╰") {
			bottoms = append(bottoms, i)
			if len(bottoms) == 1 && strings.Count(line, "╰") != 2 {
				t.Fatalf("first row bottom borders are not aligned:\n%s", view)
			}
		}
		if strings.Contains(line, "VLESS") {
			if len(tops) == 1 && strings.Count(line, "VLESS") != 2 {
				t.Fatalf("first row metadata is not aligned:\n%s", view)
			}
		}
	}
	if len(tops) != 2 || len(bottoms) != 2 || bottoms[0]-tops[0]+1 != 5 || bottoms[1]-tops[1]+1 != 4 {
		t.Fatalf("rows must independently follow their tallest card:\n%s", view)
	}
	if !strings.Contains(view, "next-row") || lipgloss.Width(view) > m.width {
		t.Fatalf("grid overflow or next row missing:\n%s", view)
	}
}

func TestVisibleContent_WrappedRowUsesActualHeight(t *testing.T) {
	longName := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789TAIL"
	for _, tc := range []struct {
		scroll int
		want   string
	}{
		{3, "first row"}, // First name line.
		{5, "first row"}, // Metadata, including the padded short card.
		{6, ""},          // Bottom borders only.
		{7, ""},          // Next row's top border only.
		{8, "next row"},
	} {
		t.Run(fmt.Sprint(tc.scroll), func(t *testing.T) {
			m := New(nil, nil)
			m.SetSize(80, 1)
			m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "G", Nodes: []protocol.ProxyNode{
				{Name: longName, Type: "VLESS"}, {Name: "short", Type: "VLESS"}, {Name: "next-row", Type: "VLESS"},
			}}}})
			m.expanded["G"] = true
			m.scrollY = tc.scroll
			var visible []string
			m.buildVisibleContent(false, &visible)
			want := ""
			if tc.want == "first row" {
				want = longName + ",short"
			} else if tc.want == "next row" {
				want = "next-row"
			}
			if strings.Join(visible, ",") != want {
				t.Fatalf("visible=%q want=%q", visible, want)
			}
		})
	}
}

func TestNavigation_WrappedNamesStayVisibleAfterLocatePagingAndResize(t *testing.T) {
	for _, width := range []int{30, 80, 100, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := New(nil, nil)
			var nodes []protocol.ProxyNode
			for i := range 30 {
				nodes = append(nodes, protocol.ProxyNode{Name: fmt.Sprintf("Hong-Kong-Premium-Streaming-Node-%02d-TAIL", i), Type: "VLESS"})
			}
			m.SetSize(width, 18)
			m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "G", Now: nodes[20].Name, Nodes: nodes}}})
			locateCurrent(t, m)
			assertPagingFocusVisible(t, m)
			for _, key := range []rune{tea.KeyPgUp, tea.KeyPgDown, tea.KeyUp, tea.KeyDown} {
				updateProxyKey(t, m, tea.KeyPressMsg{Code: key})
				assertPagingFocusVisible(t, m)
			}
			focus := m.focus
			m.SetSize(30, 18)
			assertPagingFocusVisible(t, m)
			if m.focus != focus || m.groups[0].Now != nodes[20].Name {
				t.Fatal("wrapping or navigation changed node identity or selection")
			}
			if !strings.Contains(ansi.Strip(m.View()), "TAIL") {
				t.Fatalf("focused card tail is not visible:\n%s", ansi.Strip(m.View()))
			}
		})
	}
}
