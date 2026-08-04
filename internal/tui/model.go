package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	connectionspage "github.com/LeeShunEE/mihari/internal/tui/pages/connections"
	logspage "github.com/LeeShunEE/mihari/internal/tui/pages/logs"
	"github.com/LeeShunEE/mihari/internal/tui/pages/overview"
	proxypage "github.com/LeeShunEE/mihari/internal/tui/pages/proxies"
	rulespage "github.com/LeeShunEE/mihari/internal/tui/pages/rules"
	setuppage "github.com/LeeShunEE/mihari/internal/tui/pages/setup"
	subscriptionspage "github.com/LeeShunEE/mihari/internal/tui/pages/subscriptions"
	"github.com/LeeShunEE/mihari/internal/tui/session"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
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
	mutationsEnabled bool
	status           protocol.Status
	traffic          protocol.TrafficSample
	memory           protocol.MemorySample
	connections      protocol.ConnectionList
	core             protocol.CoreStatus
	subscriptions    protocol.SubscriptionList
	monitor          MonitorModel
	operations       []ui.OperationRecord
	confirmationCmd  tea.Cmd
	setupObserved    bool
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
	active := rail[0]
	model := Model{
		pages: pages, rail: rail, active: active,
		focus: ui.Focus{Area: ui.FocusRail, Page: active},
		width: 100, height: 28, theme: ui.DefaultTheme(), monitor: NewMonitor(),
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
}

func newModelWithClient(events <-chan session.Event, client pageClient) Model {
	return newModelWithClientContext(context.Background(), events, client)
}

func newModelWithClientContext(ctx context.Context, events <-chan session.Event, client pageClient) Model {
	model := newModelWithPageClients(client, client, client, client)
	model.pages[ui.PageSetup] = setuppage.NewWithContext(ctx, client, nil)
	model.resizePages()
	model.events = events
	return model
}

func (model Model) Init() tea.Cmd { return waitSessionEvent(model.events) }

func (model Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case sessionEventMsg:
		if !typed.Open {
			model.connected = false
			model.stale = true
			model.mutationsEnabled = false
			model.monitor.SetStale(true)
			model.setLogsStale(true)
			model.syncOverview()
			return model, nil
		}
		command := model.applySessionEvent(typed.Event)
		return model, tea.Batch(command, waitSessionEvent(model.events))
	case setuppage.CompletedMsg:
		model.status.SetupRequired = !typed.Status.Complete
		model.setupObserved = true
		if typed.Status.Complete {
			model.activateOverview()
			if typed.Status.RestartRequired {
				model.modal = NewDetail(ui.RestartRequiredTitle, ui.RestartRequiredBody)
			}
		}
		return model, nil
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
	name := routedKey(key, model.inputMode)
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
		model.setupObserved = true
		if event.Status.SetupRequired {
			entering := model.active != ui.PageSetup
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
	case session.EventLog:
		if page, ok := model.pages[ui.PageLogs].(*logspage.Model); ok {
			page.Observe(event.Log, event.ObservedAt)
		}
	case session.EventConnected:
		model.connected = true
		model.stale = false
		model.mutationsEnabled = true
		model.setLogsStale(false)
	case session.EventReconnecting, session.EventTerminalError:
		model.connected = false
		model.stale = true
		model.mutationsEnabled = false
		model.setLogsStale(true)
		if page, ok := model.pages[ui.PageConnections].(*connectionspage.Model); ok {
			page.ResetSession()
		}
	}
	model.syncOverview()
	return command
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
	page.SetSnapshot(overview.Snapshot{
		Status: model.status, Core: model.core, Subscriptions: model.subscriptions,
		Monitor: model.monitor.Snapshot(), Operations: model.operations,
	})
}

func (model Model) updateRail(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up":
		model.railIndex = max(0, model.railIndex-1)
	case "down":
		model.railIndex = min(len(model.rail)-1, model.railIndex+1)
	case "enter", "right":
		model.focus = ui.Focus{Area: ui.FocusContent, Page: model.active}
		model.pages[model.active].FocusFirst()
		return model, nil
	default:
		return model, nil
	}
	model.active = model.rail[model.railIndex]
	model.focus.Page = model.active
	return model, nil
}

func (model Model) dispatchPage(message tea.Msg) (tea.Model, tea.Cmd) {
	page := model.pages[model.active]
	if page == nil || model.focus.Area != ui.FocusContent {
		return model, nil
	}
	updated, command := page.Update(routedMessage(message, model.inputMode))
	model.pages[model.active] = updated
	return model, command
}

func (model Model) View() tea.View {
	if model.active == ui.PageSetup {
		body := model.pages[ui.PageSetup].View()
		content := model.theme.Content.Width(model.width).Height(max(1, model.height-1)).Render(body) + "\n" +
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
		rail := ui.RenderRail(model.theme, model.rail, model.railIndex, model.focus.Area == ui.FocusRail, layout.RailWidth, layout.RailNavHeight)
		if layout.MonitorHeight > 0 {
			rail += "\n" + model.monitor.ViewFull(layout.RailWidth-2, layout.MonitorHeight)
		}
		page := model.pages[model.active]
		body := model.theme.Content.Width(layout.ContentWidth).Height(layout.ContentHeight).Render(page.View())
		main := lipgloss.JoinHorizontal(lipgloss.Top, rail, body)
		footer := ui.FooterRail
		if model.focus.Area == ui.FocusContent {
			footer = ui.FooterContent
		}
		if layout.Class == ui.Compact {
			footer += "  ·  " + model.monitor.ViewSummary(model.width)
		}
		content = strings.TrimRight(main, "\n") + "\n" + model.theme.Footer.Width(model.width).Render(footer)
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
