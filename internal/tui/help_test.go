package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	connectionspage "github.com/mihari-proxy/mihari/internal/tui/pages/connections"
	subscriptionspage "github.com/mihari-proxy/mihari/internal/tui/pages/subscriptions"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
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

func TestHelpDialog_ShowsCurrentPageFirst(t *testing.T) {
	model := NewModel()
	model.width, model.height = 80, 24
	model.active = ui.PageProxies
	model.railIndex = 1
	model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageProxies}
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: '?', Text: "?"})
	content := model.View().Content
	if model.modal == nil || !strings.Contains(content, ui.HelpTitle) {
		t.Fatalf("help did not open: %s", content)
	}
	if !strings.Contains(content, ui.PageLabel(ui.PageProxies)) {
		t.Fatalf("title missing current page:\n%s", content)
	}
	thisPage := strings.Index(content, "This page · "+ui.PageLabel(ui.PageProxies))
	global := strings.Index(content, "Global:")
	if global < 0 || thisPage < 0 || global > thisPage {
		t.Fatalf("current page should follow Global in the visible help:\n%s", content)
	}
	body := ui.RenderHelp(ui.PageProxies, "")
	subs := strings.Index(body, ui.PageLabel(ui.PageSubscriptions)+":")
	if thisPage := strings.Index(body, "This page · "+ui.PageLabel(ui.PageProxies)); thisPage < 0 || subs < 0 || thisPage > subs {
		t.Fatalf("full help body current page not first:\n%s", body)
	}
	if strings.Contains(content, "Subscriptions: a add") {
		t.Fatal("stale HelpBody still rendered")
	}
}

func TestHelpDialog_SearchModeSectionComesFirst(t *testing.T) {
	model := NewModel()
	model.width, model.height = 80, 24
	model.resizePages()
	model.active = ui.PageConnections
	model.railIndex = 2
	model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageConnections}
	page, ok := model.pages[ui.PageConnections].(*connectionspage.Model)
	if !ok {
		t.Fatal("connections page missing")
	}
	page.SetContentFocused(true)
	page.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	updated, cmd := model.Update(ui.OpenHelpMsg{})
	model = updated.(Model)
	_ = cmd
	content := model.View().Content
	if !strings.Contains(content, "This mode · Search") {
		t.Fatalf("visible help missing search mode:\n%s", content)
	}
	body := ui.RenderHelp(ui.PageConnections, ui.ModeSearch)
	mode := strings.Index(body, "This mode · Search")
	pageIdx := strings.Index(body, "This page · "+ui.PageLabel(ui.PageConnections))
	if mode < 0 || pageIdx < 0 || mode > pageIdx {
		t.Fatalf("search mode should precede page:\n%s", body)
	}
}

func TestHelpDialog_DoesNotOpenInTextInput(t *testing.T) {
	model := NewModel()
	model.inputMode = ui.InputText
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: '?', Text: "?"})
	if model.modal != nil {
		t.Fatal("? opened help during text input")
	}
}

func TestHelpDialog_OpensFromSetupOnNonTextStep(t *testing.T) {
	model := NewModel()
	model.width, model.height = 80, 24
	model.active = ui.PageSetup
	model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageSetup}
	updated, _ := model.Update(ui.OpenHelpMsg{})
	model = updated.(Model)
	if model.modal == nil || !strings.Contains(model.View().Content, ui.HelpTitle) {
		t.Fatal("OpenHelpMsg did not open help")
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
	model.width, model.height = 132, 30
	model.resizePages()
	model.active = ui.PageSubscriptions
	model.railIndex = 5
	model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageSubscriptions}
	if page, ok := model.pages[ui.PageSubscriptions].(*subscriptionspage.Model); ok {
		page.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "a", Name: "A"}}})
		page.SetContentFocused(true)
	}
	content := model.View().Content
	for _, want := range []string{"r refresh", "Ctrl+R", "a add", "Space toggle", "p proxy", "d delete"} {
		if !strings.Contains(content, want) {
			t.Fatalf("footer missing %q in:\n%s", want, content)
		}
	}
	// Footer FitFooter keeps one terminal row and prefers retaining ?/q.
	for _, line := range strings.Split(content, "\n") {
		if width := lipgloss.Width(line); width > model.width {
			t.Fatalf("subscriptions footer line exceeded width: %d > %d %q", width, model.width, line)
		}
	}
}

func TestFooter_NarrowPreservesHelpAndQuit(t *testing.T) {
	// Unit-level guarantee is FitFooter; shell View pads footer to width via lipgloss.
	width := 40
	hints := ui.FooterSubscriptions
	global := ui.SpinnerLabel(time.Unix(0, 0), ui.GlobalStatePendingLabel)
	got := ui.FitFooter(hints, global, width)
	if lipgloss.Width(got) > width {
		t.Fatalf("FitFooter width %d > %d: %q", lipgloss.Width(got), width, got)
	}
	if !strings.Contains(got, "?") {
		t.Fatalf("narrow footer should keep help: %q", got)
	}
	if !strings.Contains(got, "q") {
		t.Fatalf("narrow footer should keep quit: %q", got)
	}
	if !strings.Contains(got, "Working") {
		t.Fatalf("narrow footer should keep spinner label: %q", got)
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
