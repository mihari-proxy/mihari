package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	rulespage "github.com/mihari-proxy/mihari/internal/tui/pages/rules"
	webguipage "github.com/mihari-proxy/mihari/internal/tui/pages/webgui"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestWebGUIHelp_IncludesGatewaySafeguards(t *testing.T) {
	m := goldenModel(t, ui.PageWebGUI, 120, 28)
	p := webguipage.New(nil, []string{protocol.CapabilityWebGUI})
	p.SetStatus(protocol.WebGUIStatus{Safeguards: protocol.GatewaySafeguards{LoopbackBound: true, BrowserAuthenticated: true, ControllerIsolated: true, MutationsCoordinated: true}})
	m.pages[ui.PageWebGUI] = p
	next, _ := m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	got := next.(Model)
	if got.modal == nil || !strings.Contains(got.modal.body, "Gateway safeguards") || !strings.Contains(got.modal.body, "Loopback binding On") {
		t.Fatal("shell did not use contextual help")
	}
}

func TestPageOverlays_FitInsideShell(t *testing.T) {
	for _, pageID := range []ui.PageID{ui.PageWebGUI, ui.PageRules} {
		for _, width := range []int{72, 140} {
			m := goldenModel(t, pageID, width, 28)
			if pageID == ui.PageWebGUI {
				p := webguipage.New(nil, []string{protocol.CapabilityWebGUI})
				p.SetStatus(protocol.WebGUIStatus{GatewayAddr: "127.0.0.1:9191", Panels: []protocol.PanelStatus{{ID: "first", Name: "First", InstalledBuild: "v1"}}})
				m.pages[pageID] = p
				m.resizePages()
				p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
				p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			} else {
				p := rulespage.New(nil, nil)
				p.SetRules(protocol.RuleList{Rules: []protocol.Rule{{Type: "DOMAIN", Payload: "example.test", Proxy: "DIRECT"}}})
				m.pages[pageID] = p
				m.resizePages()
				p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
				p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
				p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			}

			mode := ""
			if page, ok := m.pages[pageID].(interface{ HelpMode() string }); ok {
				mode = page.HelpMode()
			}
			next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyF2})
			m = next.(Model)
			if !m.diagnosticWindow.open {
				t.Fatal("page overlay intercepted global F2")
			}
			next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			m = next.(Model)
			if page, ok := m.pages[pageID].(interface{ HelpMode() string }); ok && page.HelpMode() != mode {
				t.Fatal("closing diagnostics changed the underlying overlay")
			}
			view := m.View().Content
			if lipgloss.Height(view) > 28 || lipgloss.Width(view) > width {
				t.Fatalf("%s at %d: shell grew to %dx%d", pageID, width, lipgloss.Width(view), lipgloss.Height(view))
			}
			if !strings.Contains(ansi.Strip(view), "close") {
				t.Fatal("close hint clipped")
			}
		}
	}
}

func TestWebGUIShell_WideCardsKeepRightBorder(t *testing.T) {
	m := goldenModel(t, ui.PageWebGUI, 140, 28)
	p := webguipage.New(nil, []string{protocol.CapabilityWebGUI})
	p.SetStatus(protocol.WebGUIStatus{GatewayAddr: "127.0.0.1:9191", Panels: []protocol.PanelStatus{{ID: "a", Name: "First", InstalledBuild: "v1"}, {ID: "b", Name: "Second", InstalledBuild: "v2"}}})
	m.pages[ui.PageWebGUI] = p
	m.resizePages()
	for _, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if strings.Contains(line, "First") && strings.Contains(line, "Second") {
			if !strings.HasSuffix(strings.TrimRight(line, " "), "╮") {
				t.Fatalf("right card border clipped: %q", line)
			}
			return
		}
	}
	t.Fatal("missing paired panel headers")
}
