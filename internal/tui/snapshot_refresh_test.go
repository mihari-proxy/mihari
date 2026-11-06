package tui

import (
	tea "charm.land/bubbletea/v2"
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	subscriptionspage "github.com/mihari-proxy/mihari/internal/tui/pages/subscriptions"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestSnapshotLoading_DiffersFromConfirmedEmpty(t *testing.T) {
	m := NewModel()
	m.active = ui.PageRules
	m.pages[ui.PageRules].SetSize(100, 30)
	m.applySessionEvent(session.Event{Kind: session.EventSnapshotLoading, Resource: session.EventRules})
	if view := m.pages[ui.PageRules].View(); !strings.Contains(view, "Loading") {
		t.Fatal("unobserved rules displayed as empty")
	}
	m.applySessionEvent(session.Event{Kind: session.EventRules, Rules: protocol.RuleList{Schema: "mihari/v1"}})
	if view := m.pages[ui.PageRules].View(); strings.Contains(view, "Loading") {
		t.Fatal("confirmed empty rules remained loading")
	}
}

func TestSnapshotLoading_OverviewDoesNotInventZeroMetrics(t *testing.T) {
	m := NewModel()
	m.width, m.height = 140, 40
	m.setRefreshSession(session.New(nil, session.Options{}))
	view := m.View().Content
	if !strings.Contains(view, "Loading") || strings.Contains(view, "0 conn") {
		t.Fatal("first frame showed confirmed zero metrics before receiving data")
	}
}

func TestSnapshotLoading_NewDaemonClearsPreviousPageData(t *testing.T) {
	m := NewModel()
	m.setRefreshSession(session.New(nil, session.Options{}))
	m.status = protocol.Status{PID: 1}
	m.statusEpochKnown = true
	m.statusEpoch = 1
	m.applySessionEvent(session.Event{Kind: session.EventSubscriptions, Subscriptions: protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "old", Name: "Previous daemon profile"}}}})
	page := m.pages[ui.PageSubscriptions].(*subscriptionspage.Model)
	page.SetSize(120, 30)
	if !strings.Contains(page.View(), "Previous daemon profile") {
		t.Fatal("fixture missing")
	}
	m.applySessionEvent(session.Event{Kind: session.EventStatus, Epoch: 2, Status: protocol.Status{PID: 2}})
	if strings.Contains(page.View(), "Previous daemon profile") {
		t.Fatal("old daemon profile survived identity replacement")
	}
}

func TestSnapshotDemand_OptionalUnsupportedResourceDoesNotStayFetching(t *testing.T) {
	m := NewModel()
	m.setRefreshSession(session.New(nil, session.Options{}))
	m.applySessionEvent(session.Event{Kind: session.EventStatus, Epoch: 1, Status: protocol.Status{Capabilities: []string{protocol.CapabilityProxies}}})
	previous := m
	m.active = ui.PageProxies
	m.updateSnapshotDemand(previous)
	m.applySessionEvent(session.Event{Kind: session.EventProxies, Proxies: protocol.ProxyGroups{}})
	if m.snapshotStates[session.EventRouting].Pending || m.snapshotBanner() != "" {
		t.Fatal("unsupported optional routing left the page fetching")
	}
}

func TestSnapshotFreshness_BrowseOldDataButDoNotAuthorizeSelection(t *testing.T) {
	m := NewModel()
	m.setRefreshSession(session.New(nil, session.Options{}))
	m.applySessionEvent(session.Event{Kind: session.EventProxies, Proxies: protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "Fixture old group"}}}})
	intent := ui.ActionIntentMsg{Page: ui.PageProxies, Capability: protocol.CapabilityProxies}
	if !m.snapshotActionReady(intent) {
		t.Fatal("successful data not usable")
	}
	m.applySessionEvent(session.Event{Kind: session.EventSnapshotLoading, Resource: session.EventProxies})
	if m.snapshotActionReady(intent) {
		t.Fatal("refreshing candidate remained actionable")
	}
	if len(m.snapshotValues[session.EventProxies].Proxies.Groups) != 1 {
		t.Fatal("refresh removed old display")
	}
	m.applySessionEvent(session.Event{Kind: session.EventProxies, Err: errors.New("fixture read failed")})
	if m.snapshotActionReady(intent) {
		t.Fatal("failed candidate query authorized selection")
	}
	if !m.snapshotActionReady(ui.ActionIntentMsg{Page: ui.PageSystem, Capability: protocol.CapabilityCore}) {
		t.Fatal("unrelated core installation was frozen")
	}
}
func TestSnapshotBatch_NewWarningsSurviveUnchangedBusinessData(t *testing.T) {
	m := NewModel()
	m.setRefreshSession(session.New(nil, session.Options{}))
	first := session.Event{Kind: session.EventRouting, Routing: protocol.RoutingStatus{DesiredMode: "rule"}}
	second := first
	first.Routing.Warnings = []protocol.Warning{{Message: "first", Diagnostic: &protocol.Diagnostic{ID: "fixture:1", InstanceID: "fixture", Sequence: 1, State: protocol.DiagnosticAvailable, Detail: "first detail"}}}
	second.Routing.Warnings = []protocol.Warning{{Message: "second", Diagnostic: &protocol.Diagnostic{ID: "fixture:2", InstanceID: "fixture", Sequence: 2, State: protocol.DiagnosticAvailable, Detail: "second detail"}}}
	next, _ := m.Update(sessionEventMsg{Open: true, Event: session.Event{Batch: []session.Event{first, second}}})
	m = next.(Model)
	if len(m.diagnosticWindow.entries) != 2 {
		t.Fatalf("warning occurrences=%d", len(m.diagnosticWindow.entries))
	}
	if !m.unchangedSnapshot(second) {
		t.Fatal("warning metadata caused business snapshot changes")
	}
}

type resetSnapshotFixture struct{ err error }

func (resetSnapshotFixture) InvalidatesAllSnapshots() bool { return true }
func (r resetSnapshotFixture) Err() error                  { return r.err }
func TestSnapshotReset_FailedAttemptRetainsDisplayButRetiresCandidates(t *testing.T) {
	m := NewModel()
	m.setRefreshSession(session.New(nil, session.Options{}))
	m.active = ui.PageProxies
	groups := session.Event{Kind: session.EventProxies, Proxies: protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "old"}}}}
	m.applySessionEvent(groups)
	m.refreshAfterResult(ui.PageSystem, resetSnapshotFixture{err: errors.New("reset failed")})
	if m.snapshotActionReady(ui.ActionIntentMsg{Page: ui.PageProxies, Capability: protocol.CapabilityProxies}) {
		t.Fatal("failed reset left old candidates actionable")
	}
	if len(m.snapshotValues[session.EventProxies].Proxies.Groups) != 1 {
		t.Fatal("failed reset wiped old display")
	}
	m.refreshAfterResult(ui.PageSystem, resetSnapshotFixture{})
	if len(m.snapshotValues[session.EventProxies].Proxies.Groups) != 0 {
		t.Fatal("successful reset retained old runtime display")
	}
}

func TestSnapshotActionRejected_PreservesCancellation(t *testing.T) {
	m := NewModel()
	m.setRefreshSession(session.New(nil, session.Options{}))
	called := false
	_, cmd := m.Update(ui.ActionIntentMsg{Capability: protocol.CapabilitySubscriptions, Cancel: func() tea.Msg { called = true; return nil }})
	if cmd == nil {
		t.Fatal("rejection dropped cleanup")
	}
	cmd()
	if !called {
		t.Fatal("cleanup did not run")
	}
}
func TestSnapshotStatusBar_UsesBoundedFailureLabel(t *testing.T) {
	m := NewModel()
	m.width = 140
	m.height = 40
	m.setRefreshSession(session.New(nil, session.Options{}))
	m.setSnapshotState(session.EventCore, ui.SnapshotState{Failure: strings.Repeat("fixture failure ", 100)})
	if strings.Contains(strings.Split(m.View().Content, "\n")[0], strings.Repeat("fixture failure ", 3)) {
		t.Fatal("raw failure escaped into status bar")
	}
}

func TestSnapshotDiagnostics_NewResourceFailuresReachSystemSummary(t *testing.T) {
	for _, kind := range []session.EventKind{session.EventSystemProxy, session.EventTUN, session.EventEgress, session.EventOnboarding} {
		m := NewModel()
		m.active = ui.PageSystem
		m.observeDiagnosticMessage(sessionEventMsg{Open: true, Event: session.Event{Kind: kind, Err: errors.New("fixture failure")}})
		if m.diagnosticResourceSummary() == "" {
			t.Fatalf("missing %s summary", kind)
		}
	}
}
