package tui

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	connectionspage "github.com/mihari-proxy/mihari/internal/tui/pages/connections"
	proxypage "github.com/mihari-proxy/mihari/internal/tui/pages/proxies"
	rulespage "github.com/mihari-proxy/mihari/internal/tui/pages/rules"
	subscriptionspage "github.com/mihari-proxy/mihari/internal/tui/pages/subscriptions"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	webguipage "github.com/mihari-proxy/mihari/internal/tui/pages/webgui"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

var observedSnapshots = []session.EventKind{session.EventCore, session.EventSubscriptions, session.EventPreferences,
	session.EventProxies, session.EventRules, session.EventRuleProviders, session.EventRouting, session.EventLogging,
	session.EventWebGUI, session.EventSystemProxy, session.EventTUN, session.EventEgress, session.EventOnboarding}

func pageSnapshots(page ui.PageID) []session.EventKind {
	switch page {
	case ui.PageOverview:
		return []session.EventKind{session.EventCore, session.EventSubscriptions, session.EventWebGUI, session.EventSystemProxy, session.EventTUN}
	case ui.PageProxies:
		return []session.EventKind{session.EventProxies, session.EventRouting}
	case ui.PageRules:
		return []session.EventKind{session.EventRules, session.EventRuleProviders}
	case ui.PageSubscriptions:
		return []session.EventKind{session.EventSubscriptions}
	case ui.PageWebGUI:
		return []session.EventKind{session.EventWebGUI}
	case ui.PageSystem:
		return []session.EventKind{session.EventCore, session.EventWebGUI, session.EventSystemProxy, session.EventTUN, session.EventEgress, session.EventOnboarding, session.EventLogging}
	}
	return nil
}
func (model *Model) setRefreshSession(s *session.Session) {
	model.refreshSession = s
	model.snapshotStates = make(map[session.EventKind]ui.SnapshotState)
	model.snapshotValues = make(map[session.EventKind]session.Event)
	model.streamSeen = make(map[session.EventKind]bool)
	for _, kind := range observedSnapshots {
		model.setSnapshotState(kind, ui.SnapshotState{Pending: true})
	}
	for id, page := range model.pages {
		if owner, ok := page.(interface{ SetSnapshotRefresh(func(bool) tea.Cmd) }); ok {
			pageID := id
			states := model.snapshotStates
			owner.SetSnapshotRefresh(func(after bool) tea.Cmd {
				pending, wanted := true, false
				for _, kind := range pageSnapshots(pageID) {
					state := states[kind]
					if state.Failure == "not supported" {
						continue
					}
					wanted = true
					pending = pending && state.Pending
				}
				if wanted && pending {
					return nil
				}
				return func() tea.Msg { return ui.RefreshSnapshotsMsg{Page: pageID, AfterMutation: after} }
			})
		}
	}
	s.SetDemand(session.Demand{Page: string(model.active)})
	model.syncOverview()
}
func (model *Model) updateSnapshotDemand(previous Model) {
	if model.refreshSession == nil {
		return
	}
	changed := previous.active != model.active
	opened := previous.pageSettings == nil && model.pageSettings != nil
	if changed {
		if page, ok := model.pages[ui.PageProxies].(*proxypage.Model); ok {
			page.InvalidateGroups()
		}
		for _, kind := range pageSnapshots(model.active) {
			state := model.snapshotStates[kind]
			model.markSnapshotPending(kind, state)
		}
		model.refreshSession.Refresh(pageSnapshots(model.active)...)
	}
	if opened {
		model.refreshSession.Refresh(session.EventPreferences)
	}
	model.refreshSession.SetDemand(session.Demand{Page: string(model.active), PreferencesOpen: model.pageSettings != nil})
}
func (model *Model) setSnapshotState(kind session.EventKind, state ui.SnapshotState) {
	if previous, ok := model.snapshotStates[kind]; ok && previous == state {
		return
	}
	if model.snapshotStates == nil {
		model.snapshotStates = make(map[session.EventKind]ui.SnapshotState)
	}
	model.snapshotStates[kind] = state
	for _, page := range model.pages {
		if receiver, ok := page.(interface {
			SetSnapshotState(string, ui.SnapshotState)
		}); ok {
			receiver.SetSnapshotState(string(kind), state)
		}
	}
}
func (model *Model) observeSnapshotState(event session.Event) bool {
	kind := event.Kind
	if model.snapshotStates == nil && kind != session.EventSnapshotLoading {
		return false
	}
	if kind == session.EventSnapshotLoading {
		kind = event.Resource
		if kind == session.EventProxies {
			if page, ok := model.pages[ui.PageProxies].(*proxypage.Model); ok {
				page.InvalidateGroups()
			}
		}
		state := model.snapshotStates[kind]
		state.Pending = true
		model.setSnapshotState(kind, state)
		return false
	}
	if !slices.Contains(observedSnapshots, kind) {
		return false
	}
	state := model.snapshotStates[kind]
	before := state
	state.Pending = false
	if event.Err != nil {
		state.Failure = diagnostics.EscapeTerminal(event.Err.Error())
	} else {
		state.Loaded = true
		state.Failure = ""
	}
	model.setSnapshotState(kind, state)
	return state != before
}
func snapshotContent(event session.Event) session.Event {
	event.Status = protocol.Status{}
	event.Generation = 0
	event.Epoch = 0
	event.Scheduled = false
	event.Discarded = false
	event.ObservedAt = time.Time{}
	// ObservedAt is reserved for streams, never part of a snapshot's content.
	event.Batch = nil
	event.Subscriptions.WarningOutcome = protocol.WarningOutcome{}
	event.Routing.WarningOutcome = protocol.WarningOutcome{}
	event.Preferences.WarningOutcome = protocol.WarningOutcome{}
	event.WebGUI.WarningOutcome = protocol.WarningOutcome{}
	event.Logging.WarningOutcome = protocol.WarningOutcome{}
	event.SystemProxy.WarningOutcome = protocol.WarningOutcome{}
	event.TUN.WarningOutcome = protocol.WarningOutcome{}
	event.Egress.WarningOutcome = protocol.WarningOutcome{}
	event.Onboarding.WarningOutcome = protocol.WarningOutcome{}
	return event
}
func (model *Model) unchangedSnapshot(event session.Event) bool {
	if event.Kind == session.EventStatus && model.refreshSession != nil {
		return model.statusEpochKnown && event.Epoch == model.statusEpoch && reflect.DeepEqual(model.status, event.Status)
	}
	if event.Err != nil || !slices.Contains(observedSnapshots, event.Kind) {
		return false
	}
	if model.snapshotValues == nil {
		model.snapshotValues = make(map[session.EventKind]session.Event)
	}
	content := snapshotContent(event)
	previous, known := model.snapshotValues[event.Kind]
	model.snapshotValues[event.Kind] = content
	return known && reflect.DeepEqual(previous, content)
}
func (model *Model) resetRuntimeSnapshots(daemon bool) {
	kinds := []session.EventKind{session.EventProxies, session.EventRouting, session.EventRules, session.EventRuleProviders, session.EventSystemProxy, session.EventTUN, session.EventEgress}
	if daemon {
		kinds = observedSnapshots
		model.core = protocol.CoreStatus{}
		model.subscriptions = protocol.SubscriptionList{}
		model.webGUI = nil
		if page, ok := model.pages[ui.PageSubscriptions].(*subscriptionspage.Model); ok {
			page.SetSubscriptions(protocol.SubscriptionList{})
		}
		if page, ok := model.pages[ui.PageWebGUI].(*webguipage.Model); ok {
			page.SetStatus(protocol.WebGUIStatus{})
		}
	}
	for _, kind := range kinds {
		model.markSnapshotPending(kind, ui.SnapshotState{})
		delete(model.snapshotValues, kind)
	}
	if page, ok := model.pages[ui.PageProxies].(*proxypage.Model); ok {
		page.SetGroups(protocol.ProxyGroups{})
		page.InvalidateGroups()
	}
	if page, ok := model.pages[ui.PageRules].(*rulespage.Model); ok {
		page.SetRules(protocol.RuleList{})
		page.SetProviders(protocol.RuleProviderList{})
	}
	if page, ok := model.pages[ui.PageConnections].(*connectionspage.Model); ok {
		page.RetireSession()
	}
	if page, ok := model.pages[ui.PageSystem].(*systempage.Model); ok {
		page.SetSystemProxy(protocol.SystemProxyStatus{})
		page.SetTun(protocol.TunStatus{})
		page.SetEgress(protocol.EgressStatus{})
		if daemon {
			page.SetWebGUI(protocol.WebGUIStatus{})
			page.SetOnboarding(protocol.OnboardingStatus{})
		}
	}
	model.systemProxyOK = false
	model.tunOK = false
	if model.streamSeen != nil {
		clear(model.streamSeen)
	}
}
func (model Model) snapshotActionReady(intent ui.ActionIntentMsg) bool {
	if model.refreshSession == nil || intent.Page == ui.PageSetup {
		return true
	}
	var kinds []session.EventKind
	switch intent.Capability {
	case protocol.CapabilityProxies:
		kinds = []session.EventKind{session.EventProxies}
	case protocol.CapabilityRouting:
		kinds = []session.EventKind{session.EventProxies, session.EventRouting}
	case protocol.CapabilityRuleProviders:
		kinds = []session.EventKind{session.EventRuleProviders}
	case protocol.CapabilitySubscriptions:
		kinds = []session.EventKind{session.EventSubscriptions}
	case protocol.CapabilityWebGUI:
		kinds = []session.EventKind{session.EventWebGUI}
	case protocol.CapabilitySystemProxy:
		kinds = []session.EventKind{session.EventSystemProxy}
	case protocol.CapabilityTUN:
		kinds = []session.EventKind{session.EventTUN}
	case protocol.CapabilityOnboarding:
		kinds = []session.EventKind{session.EventOnboarding}
	case protocol.CapabilityEgress:
		kinds = []session.EventKind{session.EventEgress}
	}
	for _, kind := range kinds {
		if state, ok := model.snapshotStates[kind]; ok && (!state.Loaded || state.Pending || state.Failure != "") {
			return false
		}
	}
	return true
}
func (model *Model) refreshAfterOperation(page ui.PageID) {
	if model.refreshSession == nil {
		return
	}
	kinds := pageSnapshots(page)
	if page == ui.PageSystem {
		kinds = append(kinds, session.EventProxies, session.EventRouting, session.EventRules, session.EventRuleProviders)
	}
	if page == ui.PageSubscriptions {
		kinds = append(kinds, session.EventCore, session.EventProxies, session.EventRouting, session.EventRules, session.EventRuleProviders)
	}
	model.refreshSession.Invalidate(kinds...)
	for _, kind := range kinds {
		state := model.snapshotStates[kind]
		// Inactive page-only resources are invalidated, not scheduled.
		if slices.Contains(pageSnapshots(model.active), kind) || kind == session.EventCore || kind == session.EventSubscriptions {
			model.markSnapshotPending(kind, state)
			continue
		}
		model.setSnapshotState(kind, state)
	}
	if slices.Contains(kinds, session.EventProxies) || slices.Contains(kinds, session.EventRouting) {
		if page, ok := model.pages[ui.PageProxies].(*proxypage.Model); ok {
			page.InvalidateGroups()
		}
	}
}
func (model Model) snapshotBanner() string {
	if model.refreshSession == nil {
		return ""
	}
	pending := false
	for _, kind := range pageSnapshots(model.active) {
		state := model.snapshotStates[kind]
		if state.Failure == "not supported" {
			continue
		}
		if state.Failure != "" {
			return model.theme.Warning.Render("Refresh failed: " + state.Failure)
		}
		pending = pending || state.Pending
	}
	if pending {
		return model.theme.Muted.Render("⠋ Fetching…")
	}
	return ""
}
func (model *Model) applySharedNetwork(event session.Event) {
	page, _ := model.pages[ui.PageSystem].(*systempage.Model)
	switch event.Kind {
	case session.EventSystemProxy:
		if event.Err == nil {
			model.systemProxy = event.SystemProxy
			model.systemProxyOK = true
			if page != nil {
				page.SetSystemProxy(event.SystemProxy)
			}
		}
	case session.EventTUN:
		if event.Err == nil {
			model.tunStatus = event.TUN
			model.tunOK = true
			if page != nil {
				page.SetTun(event.TUN)
			}
		}
	case session.EventEgress:
		if event.Err == nil && page != nil {
			page.SetEgress(event.Egress)
		}
	case session.EventOnboarding:
		if event.Err == nil && page != nil {
			page.SetOnboarding(event.Onboarding)
		}
	}
}
func (model *Model) applySharedWebGUI(status protocol.WebGUIStatus) tea.Cmd {
	model.webGUI = &status
	if page, ok := model.pages[ui.PageSystem].(*systempage.Model); ok {
		page.SetWebGUI(status)
	}
	if page, ok := model.pages[ui.PageWebGUI].(*webguipage.Model); ok {
		return page.ObserveStatus(status)
	}
	return nil
}
func (model Model) viewSnapshotBody(page ui.Page) string {
	body := page.View()
	if banner := model.snapshotBanner(); banner != "" {
		body = banner + "\n" + body
	}
	if model.refreshSession != nil && model.active == ui.PageConnections && !model.streamSeen[session.EventConnections] {
		body = "Waiting for connection data…"
	}
	if model.refreshSession != nil && model.active == ui.PageLogs && !model.streamSeen[session.EventLog] {
		body = "Waiting for logs…"
	}
	return strings.TrimRight(body, "\n")
}

func (model Model) viewWaitingMonitor(width, height int) string {
	snap := model.monitor.Snapshot()
	lines := []string{
		"Waiting for samples…",
		"Conn " + ui.ObservedMetric(fmt.Sprint(snap.Connections), !model.streamSeen[session.EventConnections]),
		"Mem " + ui.ObservedMetric(ui.FormatBytes(snap.MemoryInUse), !model.streamSeen[session.EventMemory]),
		"UL " + ui.ObservedMetric(ui.FormatRate(snap.UploadRate), !model.streamSeen[session.EventTraffic]),
		"DL " + ui.ObservedMetric(ui.FormatRate(snap.DownloadRate), !model.streamSeen[session.EventTraffic]),
	}
	return clampLines(strings.Join(ui.SliceLines(lines, 0, height), "\n"), width)
}

func (model *Model) refreshAfterResult(page ui.PageID, result tea.Msg) {
	if model.refreshSession == nil {
		return
	}
	if all, ok := result.(interface{ InvalidatesAllSnapshots() bool }); ok && all.InvalidatesAllSnapshots() {
		model.refreshSession.Invalidate(observedSnapshots...)
		visible := pageSnapshots(model.active)
		for _, kind := range observedSnapshots {
			if slices.Contains(visible, kind) || kind == session.EventCore || kind == session.EventSubscriptions || kind == session.EventPreferences || kind == session.EventLogging {
				model.markSnapshotPending(kind, model.snapshotStates[kind])
			}
		}
		if page, ok := model.pages[ui.PageProxies].(*proxypage.Model); ok {
			page.InvalidateGroups()
		}
		if outcome, ok := result.(interface{ Err() error }); ok && outcome.Err() == nil {
			model.resetRuntimeSnapshots(true)
		}
		return
	}
	if mutation, ok := result.(interface{ InvalidatesSnapshots() bool }); ok && mutation.InvalidatesSnapshots() {
		model.refreshAfterOperation(page)
	}
}

func (model *Model) markSnapshotPending(kind session.EventKind, state ui.SnapshotState) {
	if model.statusEpochKnown && !slices.Contains(model.status.Capabilities, session.SnapshotCapability(kind)) {
		state.Pending = false
		state.Failure = "not supported"
	} else {
		state.Pending = true
	}
	model.setSnapshotState(kind, state)
}
