package webgui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
	if strings.Contains(view, ui.GatewaySafeguardsTitle) || !strings.Contains(model.HelpContent(), ui.GatewaySafeguardsTitle) {
		t.Fatalf("safeguards should be in help:\n%s", view)
	}
	if !strings.Contains(view, "Default") {
		t.Fatalf("panel missing:\n%s", view)
	}
	lower := strings.ToLower(view)
	if strings.Contains(lower, "token=") || strings.Contains(lower, "open_url") {
		t.Fatalf("token/url leaked:\n%s", view)
	}
}

func TestView_WebGUIRefreshCalloutBeforeCards(t *testing.T) {
	hint := ui.WebGUICacheRefreshHint
	if hint == "" || !strings.Contains(hint, "Ctrl+Shift+R") {
		t.Fatalf("WebGUICacheRefreshHint is empty or missing Ctrl+Shift+R: %q", hint)
	}
	for _, zh := range []string{"白屏", "强制刷新", "如果"} {
		if strings.Contains(hint, zh) {
			t.Fatalf("hint should be English, found %q in %q", zh, hint)
		}
	}

	t.Run("wide fits one warning line before panel cards", func(t *testing.T) {
		model := New(nil, []string{protocol.CapabilityWebGUI})
		model.SetStatus(sampleStatus())
		model.SetSize(100, 28)
		view := model.View()
		assertHintBeforePanel(t, ansi.Strip(view), hint)
		if !strings.Contains(ansi.Strip(view), hint) {
			t.Fatalf("hint should remain complete:\n%s", view)
		}
		warning := lipgloss.NewStyle().
			Bold(true).
			Foreground(ui.DefaultTheme().ColorWarning).
			Render("Ctrl+Shift+R")
		if !strings.Contains(view, warning) {
			t.Fatalf("refresh shortcut must be bold and warning-colored\nview=%s", view)
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
		for _, want := range []string{"blank", "missing"} {
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
