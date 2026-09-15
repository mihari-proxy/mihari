package proxies

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// locateHeader finds the rendered control row without depending on preceding status rows.
func locateHeader(t *testing.T, m *Model) string {
	t.Helper()
	lines, _, _ := m.buildContent(false)
	for _, line := range lines {
		if strings.Contains(ansi.Strip(line), "[Locate]") {
			return line
		}
	}
	t.Fatal("group header has no Locate button")
	return ""
}

// TestLocateHeader_PreservesButtonWithLongName checks raw width budgets and full target identity.
func TestLocateHeader_PreservesButtonWithLongName(t *testing.T) {
	for _, width := range []int{30, 58, 80, 160} {
		for _, stale := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/stale=%v", width, stale), func(t *testing.T) {
				m, _ := newLocateModel()
				name := strings.Repeat("香港🌏Long-node", 20)
				m.SetSize(width, 20)
				m.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
					Name: "A", Now: name, Nodes: []protocol.ProxyNode{{Name: name}},
				}}})
				if stale {
					m.loadError = "Refresh failed"
				}
				// Check before the section painter can clip or pad the header.
				textWidth := ui.SectionTextWidth(ui.FullSectionInner(width))
				raw := m.renderGroupHeader(m.groups[0], textWidth, true)
				if lipgloss.Width(raw) > textWidth || !strings.Contains(ansi.Strip(raw), "[Locate]") {
					t.Fatalf("raw header exceeds its budget or lost Locate: %q", raw)
				}
				if header := locateHeader(t, m); !strings.Contains(header, "…") {
					t.Fatalf("long name was not truncated: %q", header)
				}
				locateCurrent(t, m)
				if m.focus.Node != name {
					t.Fatal("truncated display name changed target identity")
				}
			})
		}
	}
}

// TestLocateHeader_ButtonFollowsShortName rejects alignment that detaches Locate from its label.
func TestLocateHeader_ButtonFollowsShortName(t *testing.T) {
	m, _ := newLocateModel()
	if !strings.Contains(ansi.Strip(locateHeader(t, m)), "Now: two  [Locate]") {
		t.Fatal("Locate must immediately follow the name")
	}
}

// TestLocateHeader_FocusAndDisabledStyles distinguishes header, button, inactive, and disabled states.
func TestLocateHeader_FocusAndDisabledStyles(t *testing.T) {
	m, _ := newLocateModel()
	header := locateHeader(t, m)
	if strings.Contains(header, m.theme.RowFocus.Render("[Locate]")) {
		t.Fatal("header focus also highlighted Locate")
	}
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	header = locateHeader(t, m)
	if !strings.Contains(header, m.theme.RowFocus.Render("[Locate]")) {
		t.Fatal("Locate focus lacks the page focus style")
	}
	if strings.Contains(header[:strings.Index(header, "Now:")], "\x1b[7m") {
		t.Fatal("Locate focus also highlighted the header")
	}
	m.SetContentFocused(false)
	header = locateHeader(t, m)
	if strings.Contains(header, "\x1b[7m") || strings.Contains(header, "›") {
		t.Fatal("inactive content retained focus chrome")
	}
	m.groups[0].Now = "missing"
	header = locateHeader(t, m)
	if !strings.Contains(header, m.theme.Muted.Render("[Locate]")) {
		t.Fatal("disabled Locate is not muted")
	}
	m.SetContentFocused(true)
	header = locateHeader(t, m)
	if !strings.Contains(header, "\x1b[7m") {
		t.Fatal("disabled Locate lost its focus indicator")
	}
}

// TestLocate_ScrollRevealsCurrentCard verifies full card visibility across grid widths and list positions.
func TestLocate_ScrollRevealsCurrentCard(t *testing.T) {
	for _, width := range []int{30, 100} {
		for _, target := range []int{18, 29} {
			t.Run(fmt.Sprintf("%d/target=%d", width, target), func(t *testing.T) {
				m := newRoutingJumpModel(30, 0)
				m.SetSize(width, 16)
				m.groups[10].Now = fmt.Sprintf("candidate-%02d", target)
				jumpToGLOBAL(t, m)
				locateCurrent(t, m)
				if m.focus != (FocusID{Group: "GLOBAL", Node: m.groups[10].Now}) {
					t.Fatalf("wrong target: %+v", m.focus)
				}
				view := m.View()
				card := m.renderNode(m.groups[10], m.groups[10].Nodes[target], min(proxyBarMaxWidth, max(18, ui.SectionTextWidth(ui.FullSectionInner(width))/m.columns()-1)))
				for _, line := range strings.Split(ansi.Strip(card), "\n") {
					if !strings.Contains(ansi.Strip(view), line) {
						t.Fatalf("focused card line missing: %q\n%s", line, ansi.Strip(view))
					}
				}
				if lipgloss.Height(view) > m.height || m.scrollY == 0 {
					t.Fatal("Locate did not respect the scrolling viewport")
				}
				updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
				if m.focus.Node == "" || m.focus.Node == m.groups[10].Now {
					t.Fatal("navigation after Locate did not move to previous row")
				}
			})
		}
	}
}

// TestLocateHeader_OffscreenButtonStaysVisible accounts for stale notices and fixed Routing chrome.
func TestLocateHeader_OffscreenButtonStaysVisible(t *testing.T) {
	m := newRoutingJumpModel(30, 2)
	jumpToGLOBAL(t, m)
	updateProxyKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	m.ObserveSnapshot(protocol.ProxyGroups{}, time.Time{}, protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "Refresh failed"})
	m.SetSize(58, 12)
	_, start, end := m.buildContent(false)
	if start < 0 || start < m.scrollY || end > m.scrollY+m.height-len(m.routingHeader()) {
		t.Fatalf("focused button is outside viewport: scroll=%d range=%d:%d", m.scrollY, start, end)
	}
}

// TestLocate_SmallViewportPinsCardStart preserves the target through a short viewport and resize.
func TestLocate_SmallViewportPinsCardStart(t *testing.T) {
	m, _ := newLocateModel()
	m.SetSize(30, 2)
	locateCurrent(t, m)
	_, start, end := m.buildContent(false)
	if start != m.scrollY || end-start <= m.height || lipgloss.Height(m.View()) > m.height {
		t.Fatalf("short viewport did not pin card start: scroll=%d range=%d:%d", m.scrollY, start, end)
	}
	m.SetSize(80, 20)
	if m.focus != (FocusID{Group: "A", Node: "two"}) || !strings.Contains(ansi.Strip(m.View()), "› ✓ two") {
		t.Fatal("resize lost the located card")
	}
}
