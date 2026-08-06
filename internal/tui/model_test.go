package tui

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/service"
	connectionspage "github.com/LeeShunEE/mihari/internal/tui/pages/connections"
	rulespage "github.com/LeeShunEE/mihari/internal/tui/pages/rules"
	setuppage "github.com/LeeShunEE/mihari/internal/tui/pages/setup"
	systempage "github.com/LeeShunEE/mihari/internal/tui/pages/system"
	webguipage "github.com/LeeShunEE/mihari/internal/tui/pages/webgui"
	"github.com/LeeShunEE/mihari/internal/tui/session"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

var errNetworkStatusTest = errors.New("network status test failure")

func TestModelRoutesConnectionSnapshotsAndPreferencesToPage(t *testing.T) {
	model := NewModel()
	page, ok := model.pages[ui.PageConnections].(*connectionspage.Model)
	if !ok {
		t.Fatalf("connections page=%T", model.pages[ui.PageConnections])
	}
	model.applySessionEvent(session.Event{Kind: session.EventPreferences, Preferences: protocol.TUIPreferences{
		Revision: 4, ConnectionsColumns: []string{"host", "chain"},
	}})
	model.applySessionEvent(session.Event{Kind: session.EventConnections, ObservedAt: time.Unix(2, 0), Connections: protocol.ConnectionList{
		Connections: []protocol.Connection{{ID: "one", Metadata: protocol.ConnectionMetadata{Host: "example.com"}}},
	}})
	view := page.View()
	// Dual-line cards: host on the primary line; chain lives on the secondary metadata line
	// (header is Host/Traffic, not the preference column list).
	if !strings.Contains(view, "example.com") || !strings.Contains(view, "Host") {
		t.Fatalf("view=%s", view)
	}
}

func TestModelRoutesRulesAndProvidersToPage(t *testing.T) {
	model := NewModel()
	page, ok := model.pages[ui.PageRules].(*rulespage.Model)
	if !ok {
		t.Fatalf("rules page=%T", model.pages[ui.PageRules])
	}
	model.applySessionEvent(session.Event{Kind: session.EventRules, Rules: protocol.RuleList{Rules: []protocol.Rule{{Type: "DOMAIN", Payload: "example.test", Proxy: "DIRECT"}}}})
	model.applySessionEvent(session.Event{Kind: session.EventRuleProviders, RuleProviders: protocol.RuleProviderList{Providers: []protocol.RuleProvider{{Name: "OpenAI", Type: "HTTP", Status: "Ready"}}}})
	view := page.View()
	if !strings.Contains(view, "example.test") {
		t.Fatalf("rules view=%s", view)
	}
	page.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	page.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(page.View(), "OpenAI") {
		t.Fatalf("providers view=%s", page.View())
	}
}

func TestModelRoutesStructuredLogsToPage(t *testing.T) {
	model := NewModel()
	model.applySessionEvent(session.Event{Kind: session.EventLog, ObservedAt: time.Unix(3, 0), Log: protocol.LogEntry{Level: "info", Message: "daemon ready"}})
	page := model.pages[ui.PageLogs]
	if !strings.Contains(page.View(), "daemon ready") || !strings.Contains(page.View(), "INFO") {
		t.Fatalf("logs view=%s", page.View())
	}
}

func TestModelRoutesSubscriptionsToPage(t *testing.T) {
	model := NewModel()
	model.applySessionEvent(session.Event{Kind: session.EventSubscriptions, Subscriptions: protocol.SubscriptionList{Revision: 3, ActiveID: "one", GlobalInterval: "12h", Subscriptions: []protocol.Subscription{{ID: "one", Name: "Main", Enabled: true, Cached: true}}}})
	view := model.pages[ui.PageSubscriptions].View()
	if !strings.Contains(view, "Main") || !strings.Contains(view, "●") || strings.Contains(view, "Generation") {
		t.Fatalf("subscriptions view=%s", view)
	}
}

func TestModelRegistersCapabilityGatedWebGUIAndSystemPages(t *testing.T) {
	model := NewModel()
	web, ok := model.pages[ui.PageWebGUI].(*webguipage.Model)
	if !ok || !strings.Contains(web.View(), ui.UnavailableTitle) {
		t.Fatalf("web GUI page=%T view=%s", model.pages[ui.PageWebGUI], model.pages[ui.PageWebGUI].View())
	}
	system, ok := model.pages[ui.PageSystem].(*systempage.Model)
	if !ok {
		t.Fatalf("system page=%T", model.pages[ui.PageSystem])
	}
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{DaemonVersion: "v0.4.0", Health: "ok", Revision: 3}})
	model.applySessionEvent(session.Event{Kind: session.EventCore, Core: protocol.CoreStatus{Status: "running", Version: "v1.19.0"}})
	if view := system.View(); !strings.Contains(view, "v0.4.0") || !strings.Contains(view, "v1.19.0") {
		t.Fatalf("system view=%s", view)
	}
}

func TestRootAcceptsSystemRouteRequestForStandaloneSetup(t *testing.T) {
	model := NewModel()
	model.active = ui.PageSystem
	updated, _ := model.Update(ui.RouteRequestMsg{Page: ui.PageSetup})
	model = updated.(Model)
	if model.Route() != ui.PageSetup || model.focus.Area != ui.FocusContent {
		t.Fatalf("route=%v focus=%v", model.Route(), model.focus)
	}
	updated, _ = model.Update(setuppage.CancelledMsg{})
	model = updated.(Model)
	if model.Route() != ui.PageSystem || model.focus.Area != ui.FocusContent {
		t.Fatalf("cancel route=%v focus=%v", model.Route(), model.focus)
	}
}

func TestRootSetupRequiredRoutesToStandaloneSetupAndEscDoesNotComplete(t *testing.T) {
	model := NewModel()
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{SetupRequired: true}})
	if model.Route() != ui.PageSetup {
		t.Fatalf("route=%v", model.Route())
	}
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.Route() == ui.PageOverview && model.SetupComplete() {
		t.Fatal("escape completed onboarding")
	}
	if strings.Contains(model.View().Content, ui.PageLabel(ui.PageOverview)+"\n") {
		t.Fatalf("setup rendered navigation rail: %s", model.View().Content)
	}
}

func TestRootSetupCompletionReturnsToOverview(t *testing.T) {
	model := NewModel()
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{SetupRequired: true}})
	updated, _ := model.Update(setuppage.CompletedMsg{Status: protocol.OnboardingStatus{Complete: true}})
	model = updated.(Model)
	if model.Route() != ui.PageOverview || !model.SetupComplete() {
		t.Fatalf("route=%v complete=%v", model.Route(), model.SetupComplete())
	}
}

func TestRootSetupCompletionReportsRestartRequired(t *testing.T) {
	model := NewModel()
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{SetupRequired: true}})
	updated, _ := model.Update(setuppage.CompletedMsg{Status: protocol.OnboardingStatus{Complete: true, RestartRequired: true}})
	model = updated.(Model)
	if model.Route() != ui.PageOverview || model.modal == nil || !strings.Contains(model.View().Content, ui.RestartRequiredTitle) {
		t.Fatalf("route=%v modal=%v view=%s", model.Route(), model.modal != nil, model.View().Content)
	}
}

func TestModelMarksRetainedLogsStaleDuringReconnect(t *testing.T) {
	model := NewModel()
	model.applySessionEvent(session.Event{Kind: session.EventLog, ObservedAt: time.Unix(3, 0), Log: protocol.LogEntry{Level: "info", Message: "retained"}})
	model.applySessionEvent(session.Event{Kind: session.EventReconnecting})
	if view := model.pages[ui.PageLogs].View(); !strings.Contains(view, "retained") || !strings.Contains(view, ui.StaleLabel) {
		t.Fatalf("stale logs view=%s", view)
	}
	model.applySessionEvent(session.Event{Kind: session.EventConnected})
	if view := model.pages[ui.PageLogs].View(); strings.Contains(view, ui.StaleLabel) {
		t.Fatalf("connected logs remained stale: %s", view)
	}
}

func TestModelReconnectResetsSessionScopedConnectionHistory(t *testing.T) {
	model := NewModel()
	page := model.pages[ui.PageConnections].(*connectionspage.Model)
	model.applySessionEvent(session.Event{Kind: session.EventConnections, ObservedAt: time.Unix(1, 0), Connections: protocol.ConnectionList{
		Connections: []protocol.Connection{{ID: "one"}},
	}})
	model.applySessionEvent(session.Event{Kind: session.EventConnections, ObservedAt: time.Unix(2, 0), Connections: protocol.ConnectionList{}})
	if !strings.Contains(page.View(), "Closed 1") {
		t.Fatalf("history before reconnect=%s", page.View())
	}
	model.applySessionEvent(session.Event{Kind: session.EventReconnecting})
	if !strings.Contains(page.View(), "Closed 0") {
		t.Fatalf("history after reconnect=%s", page.View())
	}
}

func TestModelSessionReconnectMarksSnapshotStaleAndKeepsWaiting(t *testing.T) {
	events := make(chan session.Event, 2)
	model := NewModelWithEvents(events)
	command := model.Init()
	if command == nil {
		t.Fatal("model did not wait for session events")
	}
	events <- session.Event{Kind: session.EventReconnecting}
	updated, next := model.Update(command())
	model = updated.(Model)
	if !model.stale || !model.reconnecting || model.connected || next == nil {
		t.Fatalf("stale=%v reconnecting=%v connected=%v next=%v", model.stale, model.reconnecting, model.connected, next != nil)
	}
	if model.statusBarRightStatus() != ui.StatusRightReconnecting {
		t.Fatalf("right status=%q want reconnecting", model.statusBarRightStatus())
	}
	events <- session.Event{Kind: session.EventConnected}
	updated, next = model.Update(next())
	model = updated.(Model)
	if model.stale || model.reconnecting || !model.connected || next == nil {
		t.Fatalf("stale=%v reconnecting=%v connected=%v next=%v", model.stale, model.reconnecting, model.connected, next != nil)
	}
	close(events)
	updated, next = model.Update(next())
	model = updated.(Model)
	if next != nil {
		t.Fatal("closed session channel was reissued")
	}
}

func TestStatusBarRightStatusDualServiceAndDaemon(t *testing.T) {
	model := NewModel()
	model.serviceLoaded = true

	// Service stopped + daemon offline (common: installed but not started).
	model.serviceStatus = service.StatusStopped
	model.connected = false
	model.reconnecting = false
	model.stale = true
	want := ui.StatusServiceStopped + ui.StatusRightJoin + ui.StatusDaemonOffline
	if got := model.statusBarRightStatus(); got != want {
		t.Fatalf("stopped+offline: got %q want %q", got, want)
	}

	// Service stopped + reconnecting.
	model.reconnecting = true
	want = ui.StatusServiceStopped + ui.StatusRightJoin + ui.StatusDaemonReconnecting
	if got := model.statusBarRightStatus(); got != want {
		t.Fatalf("stopped+reconnecting: got %q want %q", got, want)
	}

	// Service not installed + offline.
	model.serviceStatus = service.StatusNotInstalled
	model.reconnecting = false
	model.stale = true
	want = ui.StatusServiceNotInstalled + ui.StatusRightJoin + ui.StatusDaemonOffline
	if got := model.statusBarRightStatus(); got != want {
		t.Fatalf("not installed+offline: got %q want %q", got, want)
	}

	// Service not installed + connected → dual badge (service nudge + connected).
	model.connected = true
	model.stale = false
	want = ui.StatusServiceNotInstalled + ui.StatusRightJoin + ui.StatusDaemonConnected
	if got := model.statusBarRightStatus(); got != want {
		t.Fatalf("not installed+connected: got %q want %q", got, want)
	}

	// Service running + reconnecting.
	model.serviceStatus = service.StatusRunning
	model.connected = false
	model.reconnecting = true
	model.stale = true
	want = ui.StatusServiceRunning + ui.StatusRightJoin + ui.StatusDaemonReconnecting
	if got := model.statusBarRightStatus(); got != want {
		t.Fatalf("running+reconnecting: got %q want %q", got, want)
	}

	// Service running + offline (not mid-retry).
	model.reconnecting = false
	want = ui.StatusServiceRunning + ui.StatusRightJoin + ui.StatusDaemonOffline
	if got := model.statusBarRightStatus(); got != want {
		t.Fatalf("running+offline: got %q want %q", got, want)
	}

	// Healthy: service running + connected → still show dual badge.
	model.connected = true
	model.stale = false
	want = ui.StatusServiceRunning + ui.StatusRightJoin + ui.StatusDaemonConnected
	if got := model.statusBarRightStatus(); got != want {
		t.Fatalf("healthy: got %q want %q", got, want)
	}

	// Service unknown while reconnecting.
	model.serviceStatus = service.StatusUnknown
	model.connected = false
	model.reconnecting = true
	model.stale = true
	want = ui.StatusServiceUnknown + ui.StatusRightJoin + ui.StatusDaemonReconnecting
	if got := model.statusBarRightStatus(); got != want {
		t.Fatalf("unknown+reconnecting: got %q want %q", got, want)
	}

	// Service not loaded yet + reconnecting → daemon only.
	model.serviceLoaded = false
	if got := model.statusBarRightStatus(); got != ui.StatusDaemonReconnecting {
		t.Fatalf("unloaded+reconnecting: got %q want %q", got, ui.StatusDaemonReconnecting)
	}
}

func TestRail_EnterOpensContentButArrowsDoNot(t *testing.T) {
	model := NewModel()
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyTab})
	if model.focus.Area != ui.FocusRail {
		t.Fatalf("tab focus=%v", model.focus)
	}
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyRight})
	if model.focus.Area != ui.FocusRail {
		t.Fatalf("right should stay on rail: focus=%v", model.focus)
	}
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.focus.Area != ui.FocusContent {
		t.Fatalf("enter focus=%v", model.focus)
	}
}

func TestRail_HJKLNeverNavigates(t *testing.T) {
	model := NewModel()
	model.inputMode = ui.InputNavigation
	for _, key := range []tea.KeyPressMsg{
		{Code: 'h', Text: "h"},
		{Code: 'j', Text: "j"},
		{Code: 'k', Text: "k"},
		{Code: 'l', Text: "l"},
		{Code: 'i', Text: "i"},
	} {
		model = updateModelKey(t, model, key)
		if model.railIndex != 0 {
			t.Fatalf("key %q moved rail to %d", key.Text, model.railIndex)
		}
	}
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	if model.railIndex != 1 {
		t.Fatalf("arrow down rail index=%d", model.railIndex)
	}
}

func TestContent_EscReturnsToRailButArrowsDoNot(t *testing.T) {
	model := NewModel()
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.focus.Area != ui.FocusContent {
		t.Fatalf("focus=%v", model.focus)
	}
	updated, command := model.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	model = updated.(Model)
	if command != nil || model.focus.Area != ui.FocusContent {
		t.Fatalf("h alias command=%v focus=%v", command != nil, model.focus)
	}

	updated, command = model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	model = updated.(Model)
	if command != nil || model.focus.Area != ui.FocusContent {
		t.Fatalf("left should stay in content: command=%v focus=%v", command != nil, model.focus)
	}

	updated, command = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(Model)
	if command == nil {
		t.Fatal("esc did not request rail focus")
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if model.focus.Area != ui.FocusRail {
		t.Fatalf("focus=%v", model.focus)
	}
}

func TestModalReceivesInputBeforeRail(t *testing.T) {
	model := NewModel()
	model.modal = NewConfirmation("Restart core", "mihomo", "Connections will be interrupted", "The previous binary remains available")
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	if model.railIndex != 0 {
		t.Fatalf("rail moved behind modal: %d", model.railIndex)
	}
}

func TestConfirmationEmitsResultOnlyFromConfirmButton(t *testing.T) {
	model := NewModel()
	model.modal = NewConfirmation("Restart core", "mihomo", "interrupt", "available")
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyLeft})
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	if model.modal != nil || command == nil {
		t.Fatalf("modal=%v command=%v", model.modal != nil, command != nil)
	}
	if _, ok := command().(ModalConfirmedMsg); !ok {
		t.Fatalf("message=%T", command())
	}
}

func TestConfirmationRequestRunsOnlyAfterConfirm(t *testing.T) {
	model := NewModel()
	run := false
	updated, command := model.Update(ui.ConfirmationRequestMsg{
		Title: "Close all", Object: "connections", Impact: "closed", Rollback: "none",
		OnConfirm: func() tea.Msg { run = true; return nil },
	})
	model = updated.(Model)
	if model.modal == nil || command != nil || run {
		t.Fatalf("modal=%v command=%v run=%v", model.modal != nil, command != nil, run)
	}
	model.modal.selected = 0
	updated, command = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	if model.modal != nil || command == nil || run {
		t.Fatalf("modal=%v command=%v run=%v", model.modal != nil, command != nil, run)
	}
	command()
	if !run {
		t.Fatal("confirmed command did not run")
	}
}

func TestOperationLedgerKeepsNewestFiftyEntries(t *testing.T) {
	model := NewModel()
	for index := 0; index < 60; index++ {
		model.recordOperation(ui.OperationRecord{ID: strconv.Itoa(index)})
	}
	if len(model.operations) != 50 || model.operations[0].ID != "10" || model.operations[49].ID != "59" {
		t.Fatalf("operations=%v", model.operations)
	}
}

// loadCountingPage records Load() calls for rail-preview refresh tests.
type loadCountingPage struct {
	id    ui.PageID
	loads int
}

func (p *loadCountingPage) ID() ui.PageID                     { return p.id }
func (p *loadCountingPage) SetSize(int, int)                  {}
func (p *loadCountingPage) FocusFirst()                       {}
func (p *loadCountingPage) View() string                      { return "" }
func (p *loadCountingPage) Update(tea.Msg) (ui.Page, tea.Cmd) { return p, nil }
func (p *loadCountingPage) Load() tea.Cmd {
	p.loads++
	return nil
}

// Rail preview must Load Web GUI when the cursor lands on it (not only after Enter),
// matching System. Regression: empty "No panels installed" until content focus.
func TestRailLandingOnWebGUILoadsPage(t *testing.T) {
	page := &loadCountingPage{id: ui.PageWebGUI}
	model := NewModel()
	model.pages[ui.PageWebGUI] = page
	webIdx := -1
	for index, id := range model.rail {
		if id == ui.PageWebGUI {
			webIdx = index
			break
		}
	}
	if webIdx <= 0 {
		t.Fatalf("web GUI rail index=%d", webIdx)
	}
	model.railIndex = webIdx - 1
	model.active = model.rail[webIdx-1]
	model.focus = ui.Focus{Area: ui.FocusRail, Page: model.active}

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyDown, Text: "down"})
	model = updated.(Model)
	if model.active != ui.PageWebGUI {
		t.Fatalf("active=%s", model.active)
	}
	if page.loads != 1 {
		t.Fatalf("web GUI Load on rail land: loads=%d cmd=%v", page.loads, cmd != nil)
	}
	// Stay on rail; do not force content focus.
	if model.focus.Area != ui.FocusRail {
		t.Fatalf("focus=%v", model.focus.Area)
	}
	// Re-selecting the same page (another down then up) should Load again when re-entering.
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyUp, Text: "up"})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown, Text: "down"})
	model = updated.(Model)
	if page.loads != 2 {
		t.Fatalf("re-land should Load again: loads=%d", page.loads)
	}
}

// Page-owned async results (Load completions, etc.) must reach the active page even when
// the shell focus is still on the rail. Focus gates keyboard routing only — not IO results.
// Regression: rail-preview Load() left System Network on Loading… forever.
func TestDispatchPageDeliversAsyncResultsWhileRailFocused(t *testing.T) {
	page := &recordingPage{}
	model := NewModel()
	model.pages[ui.PageSystem] = page
	model.active = ui.PageSystem
	model.focus = ui.Focus{Area: ui.FocusRail, Page: ui.PageSystem}

	updated, _ := model.Update(asyncMarkerMsg{})
	model = updated.(Model)
	if page.received != 1 {
		t.Fatalf("async result dropped while rail focused: received=%d", page.received)
	}
	// Rail focus must be unchanged — delivery is not "enter content".
	if model.focus.Area != ui.FocusRail {
		t.Fatalf("focus leaked to %v", model.focus.Area)
	}
}

// Root loadNetworkStatus must push into System page (same pattern as service status),
// otherwise Network rows stay on Loading… when the user only arrows onto System.
func TestNetworkStatusMsgSyncsSystemPageProxyAndTun(t *testing.T) {
	model := NewModel()
	system, ok := model.pages[ui.PageSystem].(*systempage.Model)
	if !ok {
		t.Fatalf("system page=%T", model.pages[ui.PageSystem])
	}
	// Connected + mutations enabled matches the live TUI path that shows Loading….
	model.applySessionEvent(session.Event{Kind: session.EventConnected})
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Capabilities: []string{protocol.CapabilitySystemProxy, protocol.CapabilityTUN},
	}})
	system = model.pages[ui.PageSystem].(*systempage.Model)
	if view := system.View(); !strings.Contains(view, ui.LoadingLabel) {
		t.Fatalf("expected Loading before networkStatusMsg, view=%s", view)
	}

	live := true
	updated, _ := model.Update(networkStatusMsg{
		proxy: protocol.SystemProxyStatus{
			Revision: 5, Desired: true, Target: "127.0.0.1:9190",
			Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:9190", Owned: true},
		},
		tun: protocol.TunStatus{
			Revision: 5, DesiredEnable: false, LiveEnable: &live, Stack: "system", Managed: true,
		},
	})
	model = updated.(Model)
	system = model.pages[ui.PageSystem].(*systempage.Model)
	view := system.View()
	if strings.Contains(view, ui.LoadingLabel) {
		t.Fatalf("system Network still Loading after networkStatusMsg: %s", view)
	}
	for _, want := range []string{ui.SystemProxyLabel, "127.0.0.1:9190", ui.OwnedLabel, ui.TUNLabel, "system"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in system view=%s", want, view)
		}
	}
}

func TestNetworkStatusMsgFailureClearsSystemPageLoading(t *testing.T) {
	// Failed root poll must not leave permanent Loading when the attempt completed.
	model := NewModel()
	model.applySessionEvent(session.Event{Kind: session.EventConnected})
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Capabilities: []string{protocol.CapabilitySystemProxy, protocol.CapabilityTUN},
	}})
	updated, _ := model.Update(networkStatusMsg{
		proxyErr: errNetworkStatusTest,
		tunErr:   errNetworkStatusTest,
	})
	model = updated.(Model)
	system := model.pages[ui.PageSystem].(*systempage.Model)
	view := system.View()
	if strings.Contains(view, ui.LoadingLabel) {
		t.Fatalf("failed network poll still Loading: %s", view)
	}
}

func updateModelKey(t *testing.T, model Model, key tea.KeyPressMsg) Model {
	t.Helper()
	updated, _ := model.Update(key)
	result, ok := updated.(Model)
	if !ok {
		t.Fatalf("model type=%T", updated)
	}
	return result
}
