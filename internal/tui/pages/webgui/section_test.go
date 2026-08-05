package webgui

import (
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
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
