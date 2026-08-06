package tui

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/buildinfo"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/service"
	connectionspage "github.com/mihari-proxy/mihari/internal/tui/pages/connections"
	logspage "github.com/mihari-proxy/mihari/internal/tui/pages/logs"
	"github.com/mihari-proxy/mihari/internal/tui/pages/overview"
	proxypage "github.com/mihari-proxy/mihari/internal/tui/pages/proxies"
	rulespage "github.com/mihari-proxy/mihari/internal/tui/pages/rules"
	setuppage "github.com/mihari-proxy/mihari/internal/tui/pages/setup"
	subscriptionspage "github.com/mihari-proxy/mihari/internal/tui/pages/subscriptions"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	webguipage "github.com/mihari-proxy/mihari/internal/tui/pages/webgui"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type Model struct {
	pages            map[ui.PageID]ui.Page
	rail             []ui.PageID
	railIndex        int
	active           ui.PageID
	focus            ui.Focus
	inputMode        ui.InputMode
	modal            *Modal
	width            int
	height           int
	theme            ui.Theme
	events           <-chan session.Event
	connected        bool
	stale            bool
	reconnecting     bool
	mutationsEnabled bool
	status           protocol.Status
	traffic          protocol.TrafficSample
	memory           protocol.MemorySample
	connections      protocol.ConnectionList
	core             protocol.CoreStatus
	subscriptions    protocol.SubscriptionList
	webGUI           *protocol.WebGUIStatus
	monitor          MonitorModel
	operations       []ui.OperationRecord
	confirmationCmd  tea.Cmd
	setupObserved    bool
	setupReturn      ui.PageID
	pendingActions   map[string]ui.Action
	globalState      ui.GlobalState
	now              time.Time // spinner clock; advanced only while work is pending
	spinning         bool      // true while a spinner tick loop is scheduled
	spinGen          uint64    // generation so only the latest tick loop may reschedule
	// OS service observation for top-right status badge (local, not daemon IPC).
	serviceCtrl   systempage.ServiceController
	serviceStatus service.StatusKind
	serviceLoaded bool
	// Daemon network features for Overview strip (via control client when live).
	pageCtx       context.Context
	networkClient networkStatusClient
	systemProxy   protocol.SystemProxyStatus
	systemProxyOK bool
	tunStatus     protocol.TunStatus
	tunOK         bool
}

// networkStatusClient is the minimal control surface for Overview network KPIs.
type networkStatusClient interface {
	SystemProxy(context.Context) (protocol.SystemProxyStatus, error)
	Tun(context.Context) (protocol.TunStatus, error)
}

// rootServiceStatusMsg is the root-level poll of OS service install state.
type rootServiceStatusMsg struct {
	status service.StatusKind
	err    error
}

// networkStatusMsg carries daemon-backed sysproxy/TUN snapshots for Overview and System.
type networkStatusMsg struct {
	proxy    protocol.SystemProxyStatus
	proxyErr error
	tun      protocol.TunStatus
	tunErr   error
}

type actionExecuteMsg struct{ Intent ui.ActionIntentMsg }

type actionCompletedMsg struct {
	Intent ui.ActionIntentMsg
	Result tea.Msg
}

// resultErr is the outcome contract every ActionIntentMsg Execute result must
// implement so the shell can classify actions for the Recent operations card.
type resultErr interface{ Err() error }

// spinnerTickMsg advances the braille spinner frame while pending work exists.
type spinnerTickMsg struct {
	t   time.Time
	gen uint64
}

// startSpinnerTickMsg kicks off the spinner tick loop without blocking callers
// (unlike tea.Tick). executeAction batches this with the action command.
type startSpinnerTickMsg struct{ gen uint64 }

const spinnerTickInterval = 100 * time.Millisecond

func spinnerTick(gen uint64) tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(t time.Time) tea.Msg {
		return spinnerTickMsg{t: t, gen: gen}
	})
}

func NewModel() Model {
	return newModel(nil)
}

func newModel(proxyClient proxypage.Client) Model {
	return newModelWithPageClients(proxyClient, nil, nil, nil)
}

func newModelWithPageClients(proxyClient proxypage.Client, connectionsClient connectionspage.Client, rulesClient rulespage.Client, subscriptionsClient subscriptionspage.Client) Model {
	rail := ui.RailPages()
	pages := make(map[ui.PageID]ui.Page, len(rail))
	for _, id := range rail {
		pages[id] = ui.NewUnavailablePage(id)
	}
	pages[ui.PageOverview] = overview.New()
	pages[ui.PageProxies] = proxypage.New(proxyClient, nil)
	pages[ui.PageConnections] = connectionspage.New(connectionsClient, nil)
	pages[ui.PageRules] = rulespage.New(rulesClient, nil)
	pages[ui.PageLogs] = logspage.New(0)
	pages[ui.PageSubscriptions] = subscriptionspage.New(subscriptionsClient, nil, nil)
	pages[ui.PageSetup] = setuppage.New(nil, nil)
	pages[ui.PageWebGUI] = webguipage.New(nil, nil)
	pages[ui.PageSystem] = systempage.New(nil, nil)
	active := rail[0]
	model := Model{
		pages: pages, rail: rail, active: active,
		focus: ui.Focus{Area: ui.FocusRail, Page: active},
		width: 100, height: 28, theme: ui.DefaultTheme(), monitor: NewMonitor(),
		pendingActions: make(map[string]ui.Action),
	}
	model.resizePages()
	return model
}

func NewModelWithEvents(events <-chan session.Event) Model {
	model := newModel(nil)
	model.events = events
	return model
}

type pageClient interface {
	proxypage.Client
	connectionspage.Client
	rulespage.Client
	subscriptionspage.Client
	setuppage.Client
	systempage.Client
	webguipage.Client
}

func newModelWithClientContext(ctx context.Context, events <-chan session.Event, client pageClient) Model {
	model := newModelWithPageClients(client, client, client, client)
	model.pages[ui.PageSetup] = setuppage.NewWithContext(ctx, client, nil)
	model.pages[ui.PageSystem] = systempage.NewWithContext(ctx, client, nil, nil)
	model.pages[ui.PageWebGUI] = webguipage.NewWithContext(ctx, client, nil)
	model.resizePages()
	model.events = events
	if ctx == nil {
		ctx = context.Background()
	}
	model.pageCtx = ctx
	model.networkClient = client
	return model
}

const servicePollInterval = 2 * time.Second

// servicePollTickMsg drives periodic OS service status refresh for the top-right badge.
type servicePollTickMsg struct{}

func (model Model) Init() tea.Cmd {
	return tea.Batch(waitSessionEvent(model.events), model.loadRootServiceStatus(), model.scheduleServicePoll())
}

func (model Model) scheduleServicePoll() tea.Cmd {
	if model.serviceCtrl == nil {
		return nil
	}
	return tea.Tick(servicePollInterval, func(time.Time) tea.Msg {
		return servicePollTickMsg{}
	})
}

// SetServiceController injects the OS service controller used for the top-right badge
// and the System page. Safe to call before Run.
func (model *Model) SetServiceController(ctrl systempage.ServiceController) {
	model.serviceCtrl = ctrl
	if page, ok := model.pages[ui.PageSystem].(*systempage.Model); ok {
		page.SetServiceController(ctrl)
	}
}

func (model Model) loadRootServiceStatus() tea.Cmd {
	ctrl := model.serviceCtrl
	if ctrl == nil {
		return nil
	}
	return func() tea.Msg {
		status, err := ctrl.Status()
		return rootServiceStatusMsg{status: status, err: err}
	}
}

func isOSServiceAction(action ui.Action) bool {
	switch action {
	case ui.ActionServiceInstall, ui.ActionServiceUninstall, ui.ActionServiceReinstall,
		ui.ActionServiceStart, ui.ActionServiceStop, ui.ActionServiceRestart:
		return true
	default:
		return false
	}
}

// syncSystemServiceStatus pushes the root-polled OS service state into the System page
// so the in-page Status line does not lag behind (or lead) the top-right badge.
func (model *Model) syncSystemServiceStatus() {
	page, ok := model.pages[ui.PageSystem].(*systempage.Model)
	if !ok {
		return
	}
	page.ApplyServiceStatus(model.serviceStatus, model.serviceLoaded)
}

// syncSystemNetworkStatus pushes the root-polled system-proxy/TUN snapshot into the
// System page so Network rows do not stay on Loading… until the page's own Load().
func (model *Model) syncSystemNetworkStatus() {
	page, ok := model.pages[ui.PageSystem].(*systempage.Model)
	if !ok {
		return
	}
	page.ApplyRootNetworkStatus(model.systemProxy, model.systemProxyOK, model.tunStatus, model.tunOK)
}

func (model Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case servicePollTickMsg:
		return model, tea.Batch(model.loadRootServiceStatus(), model.scheduleServicePoll())
	case rootServiceStatusMsg:
		model.serviceLoaded = true
		if typed.err != nil {
			// Status() normally maps not-installed to a nil error; keep a text fallback.
			if strings.Contains(strings.ToLower(typed.err.Error()), "not installed") {
				model.serviceStatus = service.StatusNotInstalled
			} else {
				model.serviceStatus = service.StatusUnknown
			}
		} else {
			model.serviceStatus = typed.status
		}
		model.syncOverview()
		// Keep System page status line in sync with the root badge source of truth.
		model.syncSystemServiceStatus()
		return model, nil
	case networkStatusMsg:
		if typed.proxyErr == nil {
			model.systemProxy = typed.proxy
			model.systemProxyOK = true
		} else {
			model.systemProxyOK = false
		}
		if typed.tunErr == nil {
			model.tunStatus = typed.tun
			model.tunOK = true
		} else {
			model.tunOK = false
		}
		model.syncOverview()
		// Keep System page Network rows aligned with the root poll (Overview already syncs).
		model.syncSystemNetworkStatus()
		return model, nil
	case sessionEventMsg:
		if !typed.Open {
			model.connected = false
			model.stale = true
			model.reconnecting = false
			model.mutationsEnabled = false
			model.globalState = ui.StateStale
			model.monitor.SetStale(true)
			model.setLogsStale(true)
			model.syncSystem()
			model.syncOverview()
			return model, model.loadRootServiceStatus()
		}
		// Refresh service install state when connectivity transitions, not on every stream tick.
		refreshService := typed.Event.Kind == session.EventConnected ||
			typed.Event.Kind == session.EventReconnecting ||
			typed.Event.Kind == session.EventTerminalError
		command := model.applySessionEvent(typed.Event)
		if refreshService {
			return model, tea.Batch(command, waitSessionEvent(model.events), model.loadRootServiceStatus())
		}
		return model, tea.Batch(command, waitSessionEvent(model.events))
	case setuppage.CompletedMsg:
		model.status.SetupRequired = !typed.Status.Complete
		model.setupObserved = true
		if typed.Status.Complete {
			model.setupReturn = ""
			model.activateOverview()
			if typed.Status.RestartRequired {
				model.modal = NewDetail(ui.RestartRequiredTitle, ui.RestartRequiredBody)
			}
		}
		return model, nil
	case setuppage.CancelledMsg:
		if model.setupReturn != "" {
			model.active = model.setupReturn
			model.focus = ui.Focus{Area: ui.FocusContent, Page: model.setupReturn}
			model.setupReturn = ""
		}
		return model, nil
	case ui.CoreObservedMsg:
		model.core = typed.Core
		model.syncOverview()
		model.syncSystem()
		return model, nil
	case ui.RuntimeRevisionMsg:
		model.status.Revision = max(model.status.Revision, typed.Revision)
		model.syncOverview()
		model.syncSystem()
		return model, nil
	case ui.ActionIntentMsg:
		return model.handleActionIntent(typed)
	case actionExecuteMsg:
		return model.executeAction(typed.Intent)
	case actionCompletedMsg:
		delete(model.pendingActions, typed.Intent.Key)
		if len(model.pendingActions) == 0 {
			model.globalState = ""
		} else {
			model.globalState = ui.StatePending
		}
		model.recordActionOutcome(typed.Intent, typed.Result)
		var pageCmd tea.Cmd
		if typed.Result != nil {
			var next tea.Model
			next, pageCmd = model.dispatchPageTo(typed.Intent.Page, typed.Result)
			model = next.(Model)
		}
		cmds := []tea.Cmd{pageCmd, model.spinnerCmdIfNeeded()}
		// Service mutations change OS state immediately; refresh the top-right badge now.
		if isOSServiceAction(typed.Intent.Action) {
			cmds = append(cmds, model.loadRootServiceStatus())
		}
		return model, tea.Batch(cmds...)
	case startSpinnerTickMsg:
		// Real tea.Tick starts here so executeAction's Batch stays non-blocking.
		if typed.gen != model.spinGen || !model.needsSpinner() {
			if typed.gen == model.spinGen {
				model.spinning = false
			}
			return model, nil
		}
		model.spinning = true
		return model, spinnerTick(typed.gen)
	case spinnerTickMsg:
		if typed.gen != model.spinGen {
			return model, nil
		}
		model.now = typed.t
		if model.needsSpinner() {
			model.spinning = true
			return model, spinnerTick(typed.gen)
		}
		model.spinning = false
		return model, nil
	case ui.GlobalStateMsg:
		model.globalState = typed.State
		return model, model.spinnerCmdIfNeeded()
	case OperationRecordMsg:
		model.recordOperation(typed.Record)
		return model, nil
	case tea.WindowSizeMsg:
		model.width, model.height = typed.Width, typed.Height
		model.resizePages()
		return model, nil
	case ui.FocusRailMsg:
		model.focus = ui.Focus{Area: ui.FocusRail, Page: model.active}
		return model, nil
	case ui.InputModeMsg:
		model.inputMode = typed.Mode
		return model, nil
	case ui.RouteRequestMsg:
		if typed.Page != ui.PageSetup {
			return model, nil
		}
		model.setupReturn = model.active
		model.active = ui.PageSetup
		model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageSetup}
		if page, ok := model.pages[ui.PageSetup].(*setuppage.Model); ok {
			return model, page.Load()
		}
		return model, nil
	case ui.ConfirmationRequestMsg:
		model.modal = NewConfirmation(typed.Title, typed.Object, typed.Impact, typed.Rollback)
		model.confirmationCmd = typed.OnConfirm
		return model, nil
	}

	key, isKey := message.(tea.KeyPressMsg)
	if !isKey {
		return model.dispatchPage(message)
	}
	if key.String() == "ctrl+c" {
		return model, tea.Quit
	}
	if model.modal != nil {
		switch model.modal.Update(key) {
		case ModalClose:
			model.modal = nil
			model.confirmationCmd = nil
		case ModalConfirm:
			command := model.confirmationCmd
			result := ModalConfirmedMsg{Title: model.modal.title, Object: model.modal.object}
			model.modal = nil
			model.confirmationCmd = nil
			if command != nil {
				return model, command
			}
			return model, func() tea.Msg { return result }
		}
		return model, nil
	}
	if model.active == ui.PageSetup {
		return model.dispatchPage(message)
	}
	name := key.String()
	if name == "?" {
		model.modal = NewDetail(ui.HelpTitle, ui.HelpBody)
		return model, nil
	}
	if name == "q" && model.inputMode != ui.InputText {
		return model, tea.Quit
	}
	if Classify(model.width, model.height) == ui.TooSmall {
		return model, nil
	}
	if model.focus.Area == ui.FocusRail {
		return model.updateRail(name)
	}
	return model.dispatchPage(message)
}

func (model *Model) applySessionEvent(event session.Event) tea.Cmd {
	var command tea.Cmd
	model.monitor.Observe(event)
	switch event.Kind {
	case session.EventStatus:
		model.status = event.Status
		if page, ok := model.pages[ui.PageWebGUI].(*webguipage.Model); ok {
			page.SetCapabilities(event.Status.Capabilities)
		}
		model.setupObserved = true
		if event.Status.SetupRequired {
			entering := model.active != ui.PageSetup
			model.setupReturn = ""
			model.active = ui.PageSetup
			model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageSetup}
			if entering {
				if page, ok := model.pages[ui.PageSetup].(*setuppage.Model); ok {
					command = page.Load()
				}
			}
		} else if model.active == ui.PageSetup {
			model.activateOverview()
		}
		// Refresh network strip when status/capabilities land after connect.
		if model.connected {
			command = tea.Batch(command, model.loadNetworkStatus())
		}
	case session.EventTraffic:
		model.traffic = event.Traffic
	case session.EventMemory:
		model.memory = event.Memory
	case session.EventConnections:
		model.connections = event.Connections
		if page, ok := model.pages[ui.PageConnections].(*connectionspage.Model); ok {
			page.Observe(event.Connections, event.ObservedAt)
		}
	case session.EventCore:
		model.core = event.Core
	case session.EventSubscriptions:
		model.subscriptions = event.Subscriptions
		if page, ok := model.pages[ui.PageSubscriptions].(*subscriptionspage.Model); ok {
			page.SetSubscriptions(event.Subscriptions)
		}
	case session.EventProxies:
		if page, ok := model.pages[ui.PageProxies].(*proxypage.Model); ok {
			page.SetGroups(event.Proxies)
		}
	case session.EventPreferences:
		if page, ok := model.pages[ui.PageConnections].(*connectionspage.Model); ok {
			page.SetPreferences(event.Preferences)
		}
	case session.EventRules:
		if page, ok := model.pages[ui.PageRules].(*rulespage.Model); ok {
			page.SetRules(event.Rules)
		}
	case session.EventRuleProviders:
		if page, ok := model.pages[ui.PageRules].(*rulespage.Model); ok {
			page.SetProviders(event.RuleProviders)
		}
	case session.EventWebGUI:
		webGUI := event.WebGUI
		model.webGUI = &webGUI
	case session.EventLog:
		if page, ok := model.pages[ui.PageLogs].(*logspage.Model); ok {
			page.Observe(event.Log, event.ObservedAt)
		}
	case session.EventConnected:
		model.connected = true
		model.stale = false
		model.reconnecting = false
		model.mutationsEnabled = true
		model.setLogsStale(false)
		model.globalState = ui.StateReconnected
		command = tea.Batch(command, model.loadNetworkStatus())
	case session.EventReconnecting:
		model.connected = false
		model.stale = true
		model.reconnecting = true
		model.mutationsEnabled = false
		model.globalState = ui.StateStale
		model.setLogsStale(true)
		model.systemProxyOK = false
		model.tunOK = false
		if page, ok := model.pages[ui.PageConnections].(*connectionspage.Model); ok {
			page.ResetSession()
		}
	case session.EventTerminalError:
		model.connected = false
		model.stale = true
		model.reconnecting = false
		model.mutationsEnabled = false
		model.globalState = ui.StateStale
		model.setLogsStale(true)
		model.systemProxyOK = false
		model.tunOK = false
		if page, ok := model.pages[ui.PageConnections].(*connectionspage.Model); ok {
			page.ResetSession()
		}
	}
	// Transient reconnect banner clears once live data resumes.
	if model.globalState == ui.StateReconnected && event.Kind != session.EventConnected {
		model.globalState = ""
	}
	model.syncSystem()
	model.syncOverview()
	return command
}

func (model Model) loadNetworkStatus() tea.Cmd {
	client := model.networkClient
	if client == nil || !model.connected {
		return nil
	}
	ctx := model.pageCtx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		proxy, proxyErr := client.SystemProxy(ctx)
		tun, tunErr := client.Tun(ctx)
		return networkStatusMsg{proxy: proxy, proxyErr: proxyErr, tun: tun, tunErr: tunErr}
	}
}

// Route reports the root-level route currently presented to the user.
func (model Model) Route() ui.PageID { return model.active }

// SetupComplete reports an authoritative completed onboarding state.
func (model Model) SetupComplete() bool { return model.setupObserved && !model.status.SetupRequired }

func (model *Model) activateOverview() {
	model.active = ui.PageOverview
	model.focus = ui.Focus{Area: ui.FocusRail, Page: ui.PageOverview}
	for index, page := range model.rail {
		if page == ui.PageOverview {
			model.railIndex = index
			break
		}
	}
}

func (model *Model) setLogsStale(stale bool) {
	if page, ok := model.pages[ui.PageLogs].(*logspage.Model); ok {
		page.SetStale(stale)
	}
}

// recordActionOutcome appends a completed action to the Recent operations
// ledger. A result reporting no error — including nil and results that do not
// implement Err() — is recorded as Succeeded; an error is recorded as Failed.
func (model *Model) recordActionOutcome(intent ui.ActionIntentMsg, result tea.Msg) {
	state := ui.SucceededLabel
	if res, ok := result.(resultErr); ok && res.Err() != nil {
		state = ui.FailedLabel
	}
	object := intent.Object
	if object == "" {
		object = intent.Title
	}
	model.recordOperation(ui.OperationRecord{
		ID: intent.Key, Object: object, State: state, At: time.Now(),
	})
}

func (model *Model) recordOperation(operation ui.OperationRecord) {
	model.operations = append(model.operations, operation)
	if len(model.operations) > 50 {
		model.operations = append([]ui.OperationRecord(nil), model.operations[len(model.operations)-50:]...)
	}
	model.syncOverview()
}

func (model *Model) syncOverview() {
	page, ok := model.pages[ui.PageOverview].(*overview.Model)
	if !ok {
		return
	}
	monitorSnap := model.monitor.Snapshot()
	snap := overview.Snapshot{
		Status: model.status, Core: model.core, Subscriptions: model.subscriptions,
		Monitor: monitorSnap, Operations: model.operations,
		ServiceStatus: model.serviceStatus, ServiceLoaded: model.serviceLoaded,
		Connected: model.connected, MihariVersion: buildinfo.Version,
		Stale: monitorSnap.Stale, WebGUI: model.webGUI,
	}
	if model.systemProxyOK {
		proxy := model.systemProxy
		snap.SystemProxy = &proxy
	}
	if model.tunOK {
		tun := model.tunStatus
		snap.Tun = &tun
	}
	page.SetSnapshot(snap)
}

func (model *Model) syncSystem() {
	if page, ok := model.pages[ui.PageSystem].(*systempage.Model); ok {
		page.SetSnapshot(model.status, model.core)
		page.SetMutationsEnabled(model.mutationsEnabled)
	}
}

func (model Model) updateRail(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up":
		model.railIndex = max(0, model.railIndex-1)
	case "down":
		model.railIndex = min(len(model.rail)-1, model.railIndex+1)
	case "enter":
		// Enter opens the selected page; arrow keys never cross rail ↔ content.
		model.focus = ui.Focus{Area: ui.FocusContent, Page: model.active}
		model.pages[model.active].FocusFirst()
		if page, ok := model.pages[model.active].(interface{ Load() tea.Cmd }); ok {
			return model, page.Load()
		}
		return model, nil
	default:
		return model, nil
	}
	prev := model.active
	model.active = model.rail[model.railIndex]
	model.focus.Page = model.active
	if prev != model.active {
		model.clearSystemDoneIfLeaving(prev)
		// Refresh page-owned snapshots when the rail lands on the page so previews
		// are not empty until Enter. System (network/service) and Web GUI (panels)
		// both need this; Enter still Load()s after content focus.
		switch model.active {
		case ui.PageSystem, ui.PageWebGUI:
			if page, ok := model.pages[model.active].(interface{ Load() tea.Cmd }); ok {
				return model, page.Load()
			}
		}
	}
	return model, nil
}

func (model *Model) clearSystemDoneIfLeaving(prev ui.PageID) {
	if prev != ui.PageSystem {
		return
	}
	if page, ok := model.pages[ui.PageSystem].(*systempage.Model); ok {
		page.ClearDone()
	}
}

// dispatchPage delivers non-root messages to the active page.
// Keyboard focus is enforced by Update before keys reach here (rail keys never
// call this). Async Load/IO results must still land while the rail is focused,
// so the page preview can leave Loading… without requiring Enter.
func (model Model) dispatchPage(message tea.Msg) (tea.Model, tea.Cmd) {
	return model.dispatchPageTo(model.active, message)
}

func (model Model) dispatchPageTo(id ui.PageID, message tea.Msg) (tea.Model, tea.Cmd) {
	page := model.pages[id]
	if page == nil {
		return model, nil
	}
	updated, command := page.Update(message)
	model.pages[id] = updated
	return model, command
}

func (model Model) handleActionIntent(intent ui.ActionIntentMsg) (tea.Model, tea.Cmd) {
	if intent.Page == "" {
		intent.Page = model.active
	}
	if !knownAction(intent.Action) {
		model.globalState = ui.StateCapabilityLost
		return model, nil
	}
	if RequiresDaemon(intent.Action) {
		if !model.mutationsEnabled {
			model.globalState = ui.StateStale
			return model, nil
		}
		if intent.Capability != "" && !slices.Contains(model.status.Capabilities, intent.Capability) {
			model.globalState = ui.StateCapabilityLost
			return model, nil
		}
	}
	key := intent.Key
	if key == "" {
		key = string(intent.Action)
		intent.Key = key
	}
	if _, pending := model.pendingActions[key]; pending {
		model.globalState = ui.StatePending
		return model, model.spinnerCmdIfNeeded()
	}
	if RequiresConfirmation(intent.Action) {
		model.modal = NewConfirmation(intent.Title, intent.Object, intent.Impact, intent.Rollback)
		model.confirmationCmd = func() tea.Msg { return actionExecuteMsg{Intent: intent} }
		return model, nil
	}
	return model.executeAction(intent)
}

func (model Model) executeAction(intent ui.ActionIntentMsg) (tea.Model, tea.Cmd) {
	if intent.Execute == nil {
		model.globalState = ui.StateCapabilityLost
		return model, nil
	}
	model.pendingActions[intent.Key] = intent.Action
	model.globalState = ui.StatePending
	page := intent.Page
	if page == "" {
		page = model.active
	}
	// Arm row-local braille progress on the origin page before the work cmd runs.
	var pageCmd tea.Cmd
	var next tea.Model
	next, pageCmd = model.dispatchPageTo(page, ui.ActionPendingMsg{Page: page, Action: intent.Action, Key: intent.Key})
	model = next.(Model)
	exec := func() tea.Msg {
		return actionCompletedMsg{Intent: intent, Result: intent.Execute()}
	}
	return model, tea.Batch(pageCmd, exec, model.spinnerCmdIfNeeded())
}

func (model Model) needsSpinner() bool {
	return len(model.pendingActions) > 0 || model.globalState == ui.StatePending
}

// spinnerCmdIfNeeded schedules a spinner tick when pending work exists.
// If a loop is already running (spinning), returns nil so only one generation is active.
// A new generation is started only when spinning is false (idle → pending).
// Idle models clear spinning and return nil.
func (model *Model) spinnerCmdIfNeeded() tea.Cmd {
	if !model.needsSpinner() {
		model.spinning = false
		return nil
	}
	if model.spinning {
		return nil
	}
	model.spinGen++
	gen := model.spinGen
	model.spinning = true
	// Instant message first so Batch expansion in tests does not block on tea.Tick.
	return func() tea.Msg { return startSpinnerTickMsg{gen: gen} }
}

func (model Model) statusBarData() ui.StatusBarData {
	snap := model.monitor.Snapshot()
	return ui.StatusBarData{
		CoreStatus:          model.core.Status,
		CoreVersion:         model.core.Version,
		Subscription:        activeSubscriptionName(model.subscriptions),
		SubscriptionCompact: activeSubscriptionNameCompact(model.subscriptions),
		Connections:         snap.Connections,
		UploadRate:          snap.UploadRate,
		DownloadRate:        snap.DownloadRate,
		MemoryInUse:         snap.MemoryInUse,
		Stale:               snap.Stale,
		RightStatus:         model.statusBarRightStatus(),
	}
}

// statusBarRightStatus builds the top-right dual badge: Service (OS) + Daemon (IPC).
//
//	Service: not installed | stopped | running | unknown
//	Daemon:  Connected | Reconnecting | Offline
//
// Always show known dimensions so the corner remains informative (including healthy).
func (model Model) statusBarRightStatus() string {
	servicePart := model.serviceRightLabel()
	daemonPart := model.daemonRightLabel()
	switch {
	case servicePart != "" && daemonPart != "":
		return servicePart + ui.StatusRightJoin + daemonPart
	case servicePart != "":
		return servicePart
	default:
		return daemonPart
	}
}

func (model Model) serviceRightLabel() string {
	if !model.serviceLoaded {
		return ""
	}
	switch model.serviceStatus {
	case service.StatusNotInstalled:
		return ui.StatusServiceNotInstalled
	case service.StatusStopped:
		return ui.StatusServiceStopped
	case service.StatusRunning:
		return ui.StatusServiceRunning
	default:
		return ui.StatusServiceUnknown
	}
}

func (model Model) daemonRightLabel() string {
	if model.connected {
		return ui.StatusDaemonConnected
	}
	if model.reconnecting {
		return ui.StatusDaemonReconnecting
	}
	// Disconnected and not mid-retry (terminal error, closed session, or initial offline).
	if model.stale || model.monitor.Snapshot().Stale {
		return ui.StatusDaemonOffline
	}
	// Initial frame before any session event: treat as offline.
	return ui.StatusDaemonOffline
}

func activeSubscription(list protocol.SubscriptionList) (name string, upload, download, total int64) {
	if list.ActiveID == "" {
		return "", 0, 0, 0
	}
	for _, sub := range list.Subscriptions {
		if sub.ID == list.ActiveID {
			name := strings.TrimSpace(sub.Name)
			if name == "" {
				name = sub.ID
			}
			return name, sub.Upload, sub.Download, sub.Total
		}
	}
	return "", 0, 0, 0
}

func activeSubscriptionName(list protocol.SubscriptionList) string {
	name, upload, download, total := activeSubscription(list)
	if name == "" {
		return ""
	}
	return ui.FormatSubscriptionLabel(name, upload, download, total)
}

func activeSubscriptionNameCompact(list protocol.SubscriptionList) string {
	name, upload, download, total := activeSubscription(list)
	if name == "" {
		return ""
	}
	return ui.FormatSubscriptionLabelCompact(name, upload, download, total)
}

func (model Model) footerGlobalSegment() string {
	if model.needsSpinner() {
		label := ui.GlobalStatePendingLabel
		if model.globalState != ui.StatePending && model.globalState != "" {
			if mapped := ui.GlobalStateLabel(model.globalState); mapped != "" {
				label = mapped
			}
		}
		return ui.SpinnerLabel(model.now, label)
	}
	return ui.GlobalStateLabel(model.globalState)
}

func (model Model) View() tea.View {
	if model.active == ui.PageSetup {
		body := model.pages[ui.PageSetup].View()
		status := ui.RenderStatusBar(model.theme, model.statusBarData(), model.width, true)
		content := status + "\n" +
			model.theme.Content.Width(model.width).Height(max(1, model.height-2)).Render(body) + "\n" +
			model.theme.Footer.Width(model.width).Render(ui.SetupFooter)
		if model.modal != nil {
			content = model.modal.View(model.width, model.height)
		}
		view := tea.NewView(content)
		view.AltScreen = true
		view.WindowTitle = ui.AppName
		return view
	}
	layout := calculateLayout(model.width, model.height)
	var content string
	if layout.Class == ui.TooSmall {
		content = lipgloss.Place(model.width, model.height, lipgloss.Center, lipgloss.Center,
			model.theme.Title.Render(ui.ResizeRequired)+"\n"+model.theme.Muted.Render(ui.ResizeInstructions))
	} else {
		status := ui.RenderStatusBar(model.theme, model.statusBarData(), model.width, layout.Class == ui.Compact)
		// Keep the left column at a hard RailWidth so long monitor lines cannot
		// push the content pane past the terminal edge (which clips card borders).
		railNav := ui.RenderRail(model.theme, model.rail, model.railIndex, model.focus.Area == ui.FocusRail, layout.RailWidth, layout.RailNavHeight)
		left := railNav
		if layout.MonitorHeight > 0 {
			monitor := model.monitor.ViewFull(layout.RailWidth, layout.MonitorHeight)
			left = lipgloss.JoinVertical(lipgloss.Left, railNav, monitor)
		}
		rail := lipgloss.NewStyle().Width(layout.RailWidth).MaxWidth(layout.RailWidth).Height(layout.ContentHeight).MaxHeight(layout.ContentHeight).Render(left)
		page := model.pages[model.active]
		if focused, ok := page.(ui.ContentFocusable); ok {
			focused.SetContentFocused(model.focus.Area == ui.FocusContent)
		}
		body := model.theme.Content.Width(layout.ContentWidth).MaxWidth(layout.ContentWidth).Height(layout.ContentHeight).Render(page.View())
		main := lipgloss.JoinHorizontal(lipgloss.Top, rail, body)
		footer := ui.FooterRail
		if model.focus.Area == ui.FocusContent {
			if hints, ok := page.(ui.FooterHintProvider); ok {
				footer = hints.FooterHints()
			} else {
				footer = ui.PageFooterHints(model.active)
			}
		}
		// Compact mode metrics live in the status bar — never append ViewSummary.
		// Prefer dropping middle shortcuts before ?/q and the global spinner segment.
		// Budget = width−2: the Footer style pads 1 cell each side (design S2),
		// so the full width would word-wrap candidate strings at the edge.
		footer = ui.FitFooter(footer, model.footerGlobalSegment(), max(1, model.width-2))
		content = status + "\n" + strings.TrimRight(main, "\n") + "\n" + model.theme.Footer.Width(model.width).MaxWidth(model.width).Render(footer)
	}
	if model.modal != nil {
		content = model.modal.View(model.width, model.height)
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = ui.AppName
	return view
}

func (model Model) resizePages() {
	layout := calculateLayout(model.width, model.height)
	for _, page := range model.pages {
		page.SetSize(layout.ContentWidth, layout.ContentHeight)
	}
}
