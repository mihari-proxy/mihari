package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// TestProxies_LocateThroughShellAndHelp verifies key forwarding and focus retention across the help dialog.
func TestProxies_LocateThroughShellAndHelp(t *testing.T) {
	model := goldenModel(t, ui.PageProxies, 100, 28)
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Capabilities: []string{protocol.CapabilityProxies},
	}})
	model.applySessionEvent(session.Event{Kind: session.EventProxies, Proxies: protocol.ProxyGroups{
		Groups: []protocol.ProxyGroup{{
			Name: "Selector", Now: "two", Nodes: []protocol.ProxyNode{{Name: "one"}, {Name: "two"}},
		}},
	}})
	model.pages[ui.PageProxies].FocusFirst()
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyRight})
	if !strings.Contains(model.View().Content, "Enter locate") {
		t.Fatal("shell did not forward Right to the Locate button")
	}
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: '?', Text: "?"})
	if model.modal == nil {
		t.Fatal("Locate focus could not open help")
	}
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyEsc})
	if model.modal != nil || !strings.Contains(model.View().Content, "Enter locate") {
		t.Fatal("closing help did not preserve Locate focus")
	}
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	view := normalizeRender(model.View().Content)
	if !strings.Contains(view, "› ● two") || strings.Contains(view, "Enter locate") {
		t.Fatalf("shell did not focus the selected card:\n%s", view)
	}
}
