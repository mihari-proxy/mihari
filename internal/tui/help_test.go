package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	connectionspage "github.com/LeeShunEE/mihari/internal/tui/pages/connections"
	subscriptionspage "github.com/LeeShunEE/mihari/internal/tui/pages/subscriptions"
	"github.com/LeeShunEE/mihari/internal/tui/session"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestHelpDialogOpensFromRailAndContentAndClosesOnEsc(t *testing.T) {
	for _, area := range []ui.FocusArea{ui.FocusRail, ui.FocusContent} {
		model := NewModel()
		model.focus = ui.Focus{Area: area, Page: model.active}
		model = updateModelKey(t, model, tea.KeyPressMsg{Code: '?', Text: "?"})
		if model.modal == nil || !strings.Contains(model.View().Content, ui.HelpTitle) {
			t.Fatalf("area=%v help did not open: %s", area, model.View().Content)
		}
		model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyEscape})
		if model.modal != nil {
			t.Fatalf("area=%v help did not close", area)
		}
	}
}

func TestQuitIsReachableOutsideTextEntry(t *testing.T) {
	model := NewModel()
	updated, command := model.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if command == nil {
		t.Fatal("q did not request quit")
	}
	if _, ok := updated.(Model); !ok {
		t.Fatalf("model type=%T", updated)
	}
}

func TestModalKeysDoNotLeakToRailOrPage(t *testing.T) {
	model := NewModel()
	model.focus = ui.Focus{Area: ui.FocusRail, Page: ui.PageOverview}
	model.mutationsEnabled = true
	model.status.Capabilities = []string{protocol.CapabilityCore}
	updated, _ := model.Update(ui.ActionIntentMsg{
		Action: ui.ActionUpdateCore, Capability: protocol.CapabilityCore, Key: "core-update",
		Title: "Update", Object: "core", Impact: "replace", Rollback: "retained",
		Execute: func() tea.Msg { return nil },
	})
	model = updated.(Model)
	if model.modal == nil {
		t.Fatal("confirmation modal did not open")
	}
	before := model.railIndex
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: tea.KeyUp}, {Code: tea.KeyLeft}, {Code: tea.KeyRight},
	} {
		model = updateModelKey(t, model, key)
	}
	if model.railIndex != before {
		t.Fatalf("modal keys leaked to rail: %d", model.railIndex)
	}
}

func TestConfirmationDialogCancelsWithoutRunning(t *testing.T) {
	runs := 0
	model := NewModel()
	model.mutationsEnabled = true
	model.status.Capabilities = []string{protocol.CapabilityCore}
	updated, _ := model.Update(ui.ActionIntentMsg{
		Action: ui.ActionUpdateCore, Capability: protocol.CapabilityCore, Key: "core-update",
		Title: "Update", Object: "core", Impact: "replace", Rollback: "retained",
		Execute: func() tea.Msg { runs++; return nil },
	})
	model = updated.(Model)
	if model.modal == nil {
		t.Fatal("confirmation modal did not open")
	}
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.modal != nil {
		t.Fatal("esc did not cancel confirmation")
	}
	if runs != 0 {
		t.Fatalf("cancelled confirmation ran execute: runs=%d", runs)
	}
	if len(model.pendingActions) != 0 {
		t.Fatalf("cancelled confirmation left pending: %v", model.pendingActions)
	}
}

func TestConfirmationDialogTogglesSelection(t *testing.T) {
	model := NewModel()
	model.mutationsEnabled = true
	model.status.Capabilities = []string{protocol.CapabilityCore}
	updated, _ := model.Update(ui.ActionIntentMsg{
		Action: ui.ActionUpdateCore, Capability: protocol.CapabilityCore, Key: "core-update",
		Title: "Update", Object: "core", Impact: "replace", Rollback: "retained",
		Execute: func() tea.Msg { return nil },
	})
	model = updated.(Model)
	if got := model.modal.selected; got != 1 {
		t.Fatalf("default selection=%d want 1 (cancel)", got)
	}
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyLeft})
	if model.modal.selected != 0 {
		t.Fatalf("left selection=%d want 0 (confirm)", model.modal.selected)
	}
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyTab})
	if model.modal.selected != 1 {
		t.Fatalf("tab selection=%d want 1 (cancel)", model.modal.selected)
	}
}

func TestFooterRendersPendingGlobalState(t *testing.T) {
	model := NewModel()
	model.mutationsEnabled = true
	model.status.Capabilities = []string{protocol.CapabilityProxies}
	updated, _ := model.Update(ui.ActionIntentMsg{
		Action: ui.ActionSelectProxy, Capability: protocol.CapabilityProxies, Key: "proxy:GLOBAL",
		Execute: func() tea.Msg { return nil },
	})
	model = updated.(Model)
	if !strings.Contains(model.View().Content, ui.GlobalStatePendingLabel) {
		t.Fatalf("footer missing pending state: %s", model.View().Content)
	}
}

func TestFooterRendersDisconnectedGlobalState(t *testing.T) {
	model := NewModel()
	model.applySessionEvent(session.Event{Kind: session.EventReconnecting})
	if !strings.Contains(model.View().Content, ui.GlobalStateStaleLabel) {
		t.Fatalf("footer missing stale state: %s", model.View().Content)
	}
	model.applySessionEvent(session.Event{Kind: session.EventConnected})
	if !strings.Contains(model.View().Content, ui.GlobalStateReconnectedLabel) {
		t.Fatalf("footer missing reconnected state: %s", model.View().Content)
	}
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{Health: "ok"}})
	if strings.Contains(model.View().Content, ui.GlobalStateReconnectedLabel) {
		t.Fatalf("reconnected banner was not cleared after live data: %s", model.View().Content)
	}
}

func TestCompactFooterRendersWithoutOverflow(t *testing.T) {
	model := NewModel()
	model.width, model.height = 72, 22
	model.resizePages()
	content := model.View().Content
	if !strings.Contains(content, "help") || !strings.Contains(content, "q quit") {
		t.Fatalf("compact footer missing hints: %s", content)
	}
	for _, line := range strings.Split(content, "\n") {
		if width := lipgloss.Width(line); width > model.width {
			t.Fatalf("compact line exceeded width: %d > %d %q", width, model.width, line)
		}
	}
}

func TestFooterShowsSubscriptionPageActionsWhenContentFocused(t *testing.T) {
	model := NewModel()
	model.width, model.height = 120, 30
	model.resizePages()
	model.active = ui.PageSubscriptions
	model.railIndex = 5
	model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageSubscriptions}
	if page, ok := model.pages[ui.PageSubscriptions].(*subscriptionspage.Model); ok {
		page.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "a", Name: "A"}}})
		page.SetContentFocused(true)
	}
	content := model.View().Content
	for _, want := range []string{"r refresh", "Ctrl+R", "a add", "Space toggle", "d delete"} {
		if !strings.Contains(content, want) {
			t.Fatalf("footer missing %q in:\n%s", want, content)
		}
	}
}

func TestFooterShowsConnectionsModeHints(t *testing.T) {
	model := NewModel()
	model.width, model.height = 120, 30
	model.resizePages()
	model.active = ui.PageConnections
	model.railIndex = 2
	model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageConnections}
	page, ok := model.pages[ui.PageConnections].(*connectionspage.Model)
	if !ok {
		t.Fatal("connections page missing")
	}
	page.SetContentFocused(true)
	page.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", Metadata: protocol.ConnectionMetadata{Host: "one.test"},
	}}}, time.Unix(1, 0))

	page.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	if hints := page.FooterHints(); hints != ui.FooterSearchMode {
		t.Fatalf("search footer=%q", hints)
	}
	content := model.View().Content
	if !strings.Contains(content, "Type to filter") {
		t.Fatalf("footer missing search hints:\n%s", content)
	}

	page.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if hints := page.FooterHints(); !strings.Contains(hints, "/ search") {
		t.Fatalf("default footer=%q", hints)
	}
}
