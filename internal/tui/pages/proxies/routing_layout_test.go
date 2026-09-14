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

func forRoutingHeaderCases(t *testing.T, check func(*testing.T, *Model)) {
	t.Helper()
	for _, width := range []int{58, 80, 160} {
		for _, state := range []string{"applied", "pending", "unknown"} {
			t.Run(fmt.Sprintf("%d/%s", width, state), func(t *testing.T) {
				m := New(nil, nil)
				m.SetSize(width, 22)
				m.SetRoutingAvailable(true, 1)
				m.SetRouting(protocol.RoutingStatus{DesiredMode: "rule", State: state, GlobalSelection: strings.Repeat("香港出口🌏Long-name", 8)}, 1)
				m.SetContentFocused(true)
				check(t, m)
			})
		}
	}
}

func TestRoutingHeader_CompactBorderedCard(t *testing.T) {
	forRoutingHeaderCases(t, func(t *testing.T, m *Model) {
		header := m.routingHeader()
		if len(header) != 4 {
			t.Fatalf("header has %d rows; want two entries plus borders", len(header))
		}
		plain := ansi.Strip(strings.Join(header, "\n"))
		for _, want := range []string{"╭", "Routing", "╮", "Mode", "GLOBAL", "╰", "╯"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("missing %q in card:\n%s", want, plain)
			}
		}
		for _, line := range header {
			if got := lipgloss.Width(line); got != m.width-2 {
				t.Fatalf("card row width=%d; want %d", got, m.width-2)
			}
		}
	})
}

func TestRoutingHeader_LongSelectionPreservesHints(t *testing.T) {
	forRoutingHeaderCases(t, func(t *testing.T, m *Model) {
		plain := ansi.Strip(strings.Join(m.routingHeader(), "\n"))
		note := map[string]string{"applied": "Enter change", "pending": "Saved · pending", "unknown": "Live state unavailable"}[m.routing.status.State]
		for _, want := range []string{"…", note, "Waiting for candidates"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("missing %q in truncated card:\n%s", want, plain)
			}
		}
	})
}

func TestRoutingHeader_FocusMovesBetweenEntries(t *testing.T) {
	forRoutingHeaderCases(t, func(t *testing.T, m *Model) {
		header := m.routingHeader()
		if !strings.Contains(ansi.Strip(header[1]), "› Mode") || !strings.Contains(header[1], "\x1b[7m") || strings.Contains(header[2], "\x1b[7m") {
			t.Fatal("focus highlight must appear only on Mode")
		}
		updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
		header = m.routingHeader()
		if !strings.Contains(ansi.Strip(header[2]), "› GLOBAL") || strings.Contains(header[1], "\x1b[7m") || !strings.Contains(header[2], "\x1b[7m") {
			t.Fatal("focus highlight did not move to GLOBAL")
		}
	})
}

func TestRoutingHeader_InactiveContentHasNoFocusStyle(t *testing.T) {
	forRoutingHeaderCases(t, func(t *testing.T, m *Model) {
		updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
		m.SetContentFocused(false)
		if header := strings.Join(m.routingHeader(), "\n"); strings.Contains(header, "›") || strings.Contains(header, "\x1b[7m") {
			t.Fatal("inactive content retained focus styling")
		}
	})
}

func TestRoutingHeader_StatusMessageInsideBorder(t *testing.T) {
	forRoutingHeaderCases(t, func(t *testing.T, m *Model) {
		m.routing.status.Message = "Saved mode will apply when the core starts"
		header := m.routingHeader()
		if len(header) != 5 || !strings.Contains(ansi.Strip(header[3]), m.routing.status.Message) || !strings.Contains(header[4], "╰") {
			t.Fatalf("status message must remain inside the border:\n%s", strings.Join(header, "\n"))
		}
	})
}

func newRoutingJumpModel(nodes, trailing int) *Model {
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
	for i := 0; i < nodes; i++ {
		global.Nodes = append(global.Nodes, protocol.ProxyNode{Name: fmt.Sprintf("candidate-%02d", i), Type: "Vless"})
	}
	groups = append(groups, global)
	for i := 0; i < trailing; i++ {
		groups = append(groups, protocol.ProxyGroup{Name: fmt.Sprintf("After-%02d", i), Type: "Selector"})
	}
	m.SetGroups(protocol.ProxyGroups{Revision: &revision, Groups: groups})
	m.SetContentFocused(true)
	m.FocusFirst()
	return m
}

func jumpToGLOBAL(t *testing.T, m *Model) {
	t.Helper()
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func forRoutingJumpCases(t *testing.T, check func(*testing.T, *Model)) {
	t.Helper()
	for _, tc := range []struct {
		name            string
		nodes, trailing int
	}{
		{"fits", 3, 4}, {"fits_at_end", 3, 0}, {"taller_than_viewport", 30, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newRoutingJumpModel(tc.nodes, tc.trailing)
			jumpToGLOBAL(t, m)
			check(t, m)
		})
	}
}

func TestRouting_GLOBALJumpExpandsAndFocusesSection(t *testing.T) {
	forRoutingJumpCases(t, func(t *testing.T, m *Model) {
		if !m.expanded["GLOBAL"] || m.focus != (FocusID{Group: "GLOBAL"}) || m.routing.focus != -1 {
			t.Fatalf("GLOBAL was not expanded and focused: %+v", m.focus)
		}
	})
}

func TestRouting_GLOBALJumpRevealsFittingSection(t *testing.T) {
	for _, trailing := range []int{0, 4} {
		t.Run(fmt.Sprintf("trailing_groups_%d", trailing), func(t *testing.T) {
			m := newRoutingJumpModel(3, trailing)
			jumpToGLOBAL(t, m)
			view := ansi.Strip(m.View())
			for _, want := range []string{"GLOBAL · SELECTOR", "candidate-00", "candidate-01", "candidate-02"} {
				if !strings.Contains(view, want) {
					t.Fatalf("fitting section missing %q:\n%s", want, view)
				}
			}
			last := strings.Index(view, "candidate-02")
			for _, line := range strings.Split(view[last:], "\n") {
				if strings.HasPrefix(line, "╰") && strings.HasSuffix(line, "╯") {
					return // The outer section bottom is visible, not just a node's border.
				}
			}
			t.Fatalf("fitting section bottom was clipped:\n%s", view)
		})
	}
}

func TestRouting_GLOBALJumpPinsOversizedSectionToTop(t *testing.T) {
	m := newRoutingJumpModel(30, 4)
	jumpToGLOBAL(t, m)
	view := ansi.Strip(m.View())
	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[len(m.routingHeader())], "GLOBAL") || !strings.Contains(view, "candidate-00") {
		t.Fatalf("oversized GLOBAL did not start at viewport top with candidates:\n%s", view)
	}
}

func TestRouting_GLOBALJumpNavigationKeepsCandidateVisible(t *testing.T) {
	m := newRoutingJumpModel(30, 4)
	jumpToGLOBAL(t, m)
	for i := 0; i < 8; i++ {
		updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.focus.Node == "" || !strings.Contains(m.View(), m.focus.Node) {
		t.Fatal("node navigation after jump lost the focused candidate")
	}
}

func TestRouting_GLOBALJumpStaysWithinViewportHeight(t *testing.T) {
	forRoutingJumpCases(t, func(t *testing.T, m *Model) {
		if lipgloss.Height(m.View()) > m.height {
			t.Fatal("boxed header and scrolled groups exceeded viewport height")
		}
	})
}
