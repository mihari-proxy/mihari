package webgui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestView_SectionsAndNoTokenLeak(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 24)
	model.available = true
	model.status = protocol.WebGUIStatus{
		GatewayHealth:   "ok",
		GatewayAddr:     "127.0.0.1:9191",
		ActivePanel:     "default",
		BrowserSessions: 0,
		Panels: []protocol.PanelStatus{{
			ID: "default", Name: "Default", Active: true,
			InstalledBuild: "1", LatestBuild: "1", Health: "ok",
		}},
		Safeguards: protocol.GatewaySafeguards{
			LoopbackBound: true, BrowserAuthenticated: true,
			ControllerIsolated: true, MutationsCoordinated: true,
		},
	}
	view := model.View()
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Fatalf("missing section borders:\n%s", view)
	}
	if !strings.Contains(view, ui.WebGUITitle) {
		t.Fatalf("missing gateway title:\n%s", view)
	}
	if !strings.Contains(view, ui.GatewaySafeguardsTitle) {
		t.Fatalf("missing safeguards section:\n%s", view)
	}
	if !strings.Contains(view, "Default") {
		t.Fatalf("panel missing:\n%s", view)
	}
	lower := strings.ToLower(view)
	if strings.Contains(lower, "token=") || strings.Contains(lower, "open_url") {
		t.Fatalf("token/url leaked:\n%s", view)
	}
}

func TestView_WebGUISectionEndsWithCacheRefreshHint(t *testing.T) {
	model := New(nil, []string{protocol.CapabilityWebGUI})
	model.SetStatus(sampleStatus())
	model.SetSize(100, 28)
	view := model.View()

	hint := ui.WebGUICacheRefreshHint
	if hint == "" {
		t.Fatal("WebGUICacheRefreshHint must not be empty")
	}
	if !strings.Contains(hint, "Ctrl+Shift+R") {
		t.Fatalf("hint should mention Ctrl+Shift+R: %q", hint)
	}
	if !strings.Contains(view, hint) {
		t.Fatalf("Web GUI section missing cache refresh hint:\n%s", view)
	}

	panelIdx := strings.Index(view, "Zashboard")
	hintIdx := strings.Index(view, hint)
	if panelIdx < 0 || hintIdx < 0 || hintIdx > panelIdx {
		t.Fatalf("hint should be the last line of the Web GUI section, before panel cards (hint=%d panel=%d)", hintIdx, panelIdx)
	}

	wantStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(ui.DefaultTheme().ColorWarning).
		Background(lipgloss.Color("15")).
		Render(hint)
	if !strings.Contains(view, wantStyle) {
		t.Fatalf("hint should be orange text on a white background\nwant style=%q\nview=%s", wantStyle, view)
	}
}
