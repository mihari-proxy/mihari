package proxies

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestRoutingHeader_CompactBorderedCard(t *testing.T) {
	for _, width := range []int{58, 80, 160} {
		for _, state := range []string{"applied", "pending", "unknown"} {
			t.Run(fmt.Sprintf("%d/%s", width, state), func(t *testing.T) {
				m := New(nil, nil)
				m.SetSize(width, 22)
				m.SetRoutingAvailable(true, 1)
				m.SetRouting(protocol.RoutingStatus{DesiredMode: "rule", State: state, GlobalSelection: strings.Repeat("香港出口🌏Long-name", 8)}, 1)
				m.SetContentFocused(true)
				header := m.routingHeader()
				if len(header) != 4 {
					t.Fatalf("header has %d rows; want two entries plus borders", len(header))
				}
				plain := ansi.Strip(strings.Join(header, "\n"))
				for _, want := range []string{"╭", "Routing", "╮", "› Mode", "GLOBAL", "╰", "╯", "…"} {
					if !strings.Contains(plain, want) {
						t.Fatalf("missing %q in card:\n%s", want, plain)
					}
				}
				note := map[string]string{"applied": "Enter change", "pending": "Saved · pending", "unknown": "Live state unavailable"}[state]
				if !strings.Contains(plain, note) || !strings.Contains(plain, "Waiting for candidates") {
					t.Fatalf("status hints were clipped:\n%s", plain)
				}
				for _, line := range header {
					if got := lipgloss.Width(line); got != width-2 {
						t.Fatalf("card row width=%d; want %d", got, width-2)
					}
				}
				if !strings.Contains(header[1], "\x1b[7m") || strings.Contains(header[2], "\x1b[7m") {
					t.Fatal("focus highlight must appear only on Mode")
				}
				updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
				header = m.routingHeader()
				if strings.Contains(header[1], "\x1b[7m") || !strings.Contains(header[2], "\x1b[7m") {
					t.Fatal("focus highlight did not move to GLOBAL")
				}
				m.SetContentFocused(false)
				if header := strings.Join(m.routingHeader(), "\n"); strings.Contains(header, "›") || strings.Contains(header, "\x1b[7m") {
					t.Fatal("inactive content retained focus styling")
				}
				m.routing.status.Message = "Saved mode will apply when the core starts"
				header = m.routingHeader()
				if len(header) != 5 || !strings.Contains(ansi.Strip(header[3]), m.routing.status.Message) || !strings.Contains(header[4], "╰") {
					t.Fatalf("status message must remain inside the border:\n%s", strings.Join(header, "\n"))
				}
			})
		}
	}
}

func TestRouting_GLOBALJumpRevealsExpandedSection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		nodes    int
		trailing int
	}{
		{"fits", 3, 4},
		{"fits_at_end", 3, 0},
		{"taller_than_viewport", 30, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(nil, nil)
			m.SetSize(58, 16)
			m.SetRoutingAvailable(true, 1)
			revision := uint64(3)
			m.SetRouting(protocol.RoutingStatus{DesiredMode: "rule", State: "applied", Revision: revision}, 1)
			groups := make([]protocol.ProxyGroup, 10)
			for i := range groups {
				groups[i] = protocol.ProxyGroup{Name: fmt.Sprintf("Before-%02d", i), Type: "Selector"}
			}
			global := protocol.ProxyGroup{Name: "GLOBAL", Type: "Selector"}
			for i := 0; i < tc.nodes; i++ {
				global.Nodes = append(global.Nodes, protocol.ProxyNode{Name: fmt.Sprintf("candidate-%02d", i), Type: "Vless"})
			}
			groups = append(groups, global)
			for i := 0; i < tc.trailing; i++ {
				groups = append(groups, protocol.ProxyGroup{Name: fmt.Sprintf("After-%02d", i), Type: "Selector"})
			}
			m.SetGroups(protocol.ProxyGroups{Revision: &revision, Groups: groups})
			m.SetContentFocused(true)
			m.FocusFirst()
			updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if !m.expanded["GLOBAL"] || m.focus != (FocusID{Group: "GLOBAL"}) || m.routing.focus != -1 {
				t.Fatalf("GLOBAL was not expanded and focused: %+v", m.focus)
			}
			view := ansi.Strip(m.View())
			if !strings.Contains(view, "candidate-00") {
				t.Fatalf("jump exposed only the group header:\n%s", view)
			}
			if tc.nodes == 3 {
				if !strings.Contains(view, "candidate-02") {
					t.Fatalf("fitting section was not fully revealed:\n%s", view)
				}
				lines := strings.Split(view, "\n")
				last := strings.Index(view, "candidate-02")
				if !strings.Contains(view[last:], "╰") || len(lines) > 16 {
					t.Fatalf("section bottom clipped or viewport overflowed:\n%s", view)
				}
			} else {
				lines := strings.Split(view, "\n")
				if !strings.Contains(lines[len(m.routingHeader())], "GLOBAL") {
					t.Fatalf("oversized GLOBAL did not start at viewport top:\n%s", view)
				}
				for i := 0; i < 8; i++ {
					updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
				}
				if m.focus.Node == "" || !strings.Contains(m.View(), m.focus.Node) {
					t.Fatal("node navigation after jump lost the focused candidate")
				}
			}
			if lipgloss.Height(m.View()) > 16 {
				t.Fatal("boxed header and scrolled groups exceeded viewport height")
			}
		})
	}
}
