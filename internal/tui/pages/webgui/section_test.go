package webgui

import (
	"strings"
	"testing"

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
	hint := ui.WebGUICacheRefreshHint
	if hint == "" || !strings.Contains(hint, "Ctrl+Shift+R") {
		t.Fatalf("WebGUICacheRefreshHint is empty or missing Ctrl+Shift+R: %q", hint)
	}

	t.Run("wide fits one styled line before panel cards", func(t *testing.T) {
		model := New(nil, []string{protocol.CapabilityWebGUI})
		model.SetStatus(sampleStatus())
		model.SetSize(100, 28)
		view := model.View()
		assertHintBeforePanel(t, view, hint)
		wantStyle := ui.RenderWarningCallout(ui.DefaultTheme(), hint, 0)
		if !strings.Contains(view, wantStyle) {
			t.Fatalf("hint should be orange text on a white background\nwant style=%q\nview=%s", wantStyle, view)
		}
	})

	t.Run("narrow wraps and stays before panel cards", func(t *testing.T) {
		model := New(nil, []string{protocol.CapabilityWebGUI})
		model.SetStatus(sampleStatus())
		model.SetSize(40, 28)
		view := model.View()
		if strings.Contains(view, hint) {
			t.Fatalf("narrow view should wrap the hint, not keep it contiguous:\n%s", view)
		}
		assertHintBeforePanel(t, view, "Ctrl+Shift+R")
		for _, want := range []string{"白屏", "强制刷新"} {
			if !strings.Contains(view, want) {
				t.Fatalf("wrapped hint dropped %q:\n%s", want, view)
			}
		}
	})
}

func assertHintBeforePanel(t *testing.T, view, marker string) {
	t.Helper()
	panelIdx := strings.Index(view, "Zashboard")
	hintIdx := strings.Index(view, marker)
	if panelIdx < 0 {
		t.Fatalf("missing panel card:\n%s", view)
	}
	if hintIdx < 0 {
		t.Fatalf("missing hint marker %q:\n%s", marker, view)
	}
	if hintIdx >= panelIdx {
		t.Fatalf("hint should appear in the Web GUI section before panel cards (hint=%d panel=%d)\n%s", hintIdx, panelIdx, view)
	}
}
