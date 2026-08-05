package system

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/elevate"
	"github.com/LeeShunEE/mihari/internal/service"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

const (
	rowDaemon           = "daemon"
	rowCore             = "core"
	rowCoreUpdate       = "core-update"
	rowCoreRestart      = "core-restart"
	rowEndpoints        = "endpoints"
	rowRunSetup         = "run-setup"
	rowServiceStatus    = "service-status"
	rowServiceInstall   = "service-install"
	rowServiceUninstall = "service-uninstall"
	rowServiceReinstall = "service-reinstall"
	rowServiceStart     = "service-start"
	rowServiceStop      = "service-stop"
	rowServiceRestart   = "service-restart"
	rowSystemProxy      = "system-proxy"
	rowTUN              = "tun"
)

// Client is the daemon control surface used by System page actions.
type Client interface {
	Onboarding(context.Context) (protocol.OnboardingStatus, error)
	Core(context.Context) (protocol.CoreStatus, error)
	InstallCore(context.Context, protocol.MutationRequest) (protocol.CoreInstallResult, error)
	RestartCore(context.Context, protocol.MutationRequest) (protocol.MutationResult, error)
	SystemProxy(context.Context) (protocol.SystemProxyStatus, error)
	EnableSystemProxy(context.Context, protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error)
	DisableSystemProxy(context.Context, protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error)
	Tun(context.Context) (protocol.TunStatus, error)
	EnableTun(context.Context, protocol.TunMutationRequest) (protocol.TunStatus, error)
	DisableTun(context.Context, protocol.TunMutationRequest) (protocol.TunStatus, error)
}

// ServiceController is the local OS service manager surface (not daemon IPC).
type ServiceController interface {
	Install() error
	Uninstall() error
	Reinstall() error
	Start() error
	Stop() error
	Restart() error
	Status() (service.StatusKind, error)
}

type row struct {
	id      string
	section string
	label   string
	value   string
	detail  string
}

type onboardingResultMsg struct {
	status protocol.OnboardingStatus
	err    error
}

type serviceStatusMsg struct {
	status   service.StatusKind
	elevated bool
	err      error
}

type serviceResultMsg struct {
	kind serviceActionKind
	err  error
}

type systemProxyStatusMsg struct {
	status protocol.SystemProxyStatus
	err    error
}

type systemProxyActionResultMsg struct {
	kind   proxyActionKind
	status protocol.SystemProxyStatus
	err    error
}

type tunStatusMsg struct {
	status protocol.TunStatus
	err    error
}

type tunActionResultMsg struct {
	kind   tunActionKind
	status protocol.TunStatus
	err    error
}

type actionKind uint8

const (
	actionUpdate actionKind = iota
	actionRestart
)

type serviceActionKind uint8

const (
	serviceInstall serviceActionKind = iota
	serviceUninstall
	serviceReinstall
	serviceStart
	serviceStop
	serviceRestart
)

// rowSpinTickMsg advances braille frames while a System row action is in flight.
type rowSpinTickMsg struct {
	t   time.Time
	gen uint64
}

type startRowSpinMsg struct{ gen uint64 }

const rowSpinInterval = 100 * time.Millisecond

type proxyActionKind uint8

const (
	proxyEnable proxyActionKind = iota
	proxyForceEnable
	proxyDisable
)

type tunActionKind uint8

const (
	tunEnable tunActionKind = iota
	tunDisable
)

type actionStartMsg struct {
	kind        actionKind
	operationID string
	revision    uint64
}

type actionResultMsg struct {
	kind    actionKind
	install protocol.CoreInstallResult
	restart protocol.MutationResult
	err     error
}

// Model is the System page.
type Model struct {
	ctx               context.Context
	client            Client
	service           ServiceController
	newOperationID    func() string
	status            protocol.Status
	core              protocol.CoreStatus
	onboarding        protocol.OnboardingStatus
	systemProxy       protocol.SystemProxyStatus
	systemProxyLoaded bool
	tun               protocol.TunStatus
	tunLoaded         bool
	serviceStatus     service.StatusKind
	serviceLoaded     bool
	elevated          bool
	focusID           string
	detail            *row
	pending           bool
	pendingRow        string // row id showing in-row braille progress
	pendingNote       string // short status text next to the row (e.g. Installing)
	doneRow           string // sticky success row; cleared on page leave or re-run
	rowSpinClock      time.Time
	rowSpinning       bool
	rowSpinGen        uint64
	mutationsEnabled  bool
	lastError         string
	contentFocused    bool
	width             int
	height            int
	theme             ui.Theme
}

// New constructs a System page without a service controller.
func New(client Client, newOperationID func() string) *Model {
	return NewWithService(client, nil, newOperationID)
}

// NewWithService constructs a System page with optional local service control.
func NewWithService(client Client, svc ServiceController, newOperationID func() string) *Model {
	return NewWithContext(context.Background(), client, svc, newOperationID)
}

// NewWithContext constructs a System page bound to ctx.
func NewWithContext(ctx context.Context, client Client, svc ServiceController, newOperationID func() string) *Model {
	if ctx == nil {
		ctx = context.Background()
	}
	if newOperationID == nil {
		newOperationID = defaultOperationID
	}
	return &Model{
		ctx:            ctx,
		client:         client,
		service:        svc,
		newOperationID: newOperationID,
		focusID:        rowDaemon,
		theme:          ui.DefaultTheme(),
	}
}

// SetServiceController injects or replaces the OS service controller.
func (m *Model) SetServiceController(svc ServiceController) {
	m.service = svc
}

func (m *Model) ID() ui.PageID { return ui.PageSystem }

// SetContentFocused reports whether the root shell has given keyboard focus to this page.
func (m *Model) SetContentFocused(focused bool) { m.contentFocused = focused }

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }

func (m *Model) FocusFirst() {
	if m.rowIndex(m.focusID) < 0 {
		m.focusID = rowDaemon
	}
}

func (m *Model) SetSnapshot(status protocol.Status, core protocol.CoreStatus) {
	m.status, m.core = status, core
}

func (m *Model) SetOnboarding(status protocol.OnboardingStatus) { m.onboarding = status }

// SetSystemProxy injects system-proxy status (tests and optional external refresh).
func (m *Model) SetSystemProxy(status protocol.SystemProxyStatus) {
	m.systemProxy = status
	m.systemProxyLoaded = true
}

// SetTun injects TUN status (tests and optional external refresh).
func (m *Model) SetTun(status protocol.TunStatus) {
	m.tun = status
	m.tunLoaded = true
}

func (m *Model) SetMutationsEnabled(enabled bool) { m.mutationsEnabled = enabled }

// Load refreshes onboarding, OS service status, system proxy, and TUN when available.
func (m *Model) Load() tea.Cmd {
	var cmds []tea.Cmd
	if m.client != nil && m.hasCapability(protocol.CapabilityOnboarding) {
		cmds = append(cmds, func() tea.Msg {
			status, err := m.client.Onboarding(m.ctx)
			return onboardingResultMsg{status: status, err: err}
		})
	}
	if m.service != nil {
		cmds = append(cmds, m.loadServiceStatus())
	}
	if m.client != nil && m.hasCapability(protocol.CapabilitySystemProxy) {
		cmds = append(cmds, m.loadSystemProxy())
	}
	if m.client != nil && m.hasCapability(protocol.CapabilityTUN) {
		cmds = append(cmds, m.loadTun())
	}
	switch len(cmds) {
	case 0:
		return nil
	case 1:
		return cmds[0]
	default:
		return tea.Batch(cmds...)
	}
}

func (m *Model) loadServiceStatus() tea.Cmd {
	return func() tea.Msg {
		status, err := m.service.Status()
		return serviceStatusMsg{status: status, elevated: elevate.IsElevated(), err: err}
	}
}

func (m *Model) loadSystemProxy() tea.Cmd {
	return func() tea.Msg {
		status, err := m.client.SystemProxy(m.ctx)
		return systemProxyStatusMsg{status: status, err: err}
	}
}

func (m *Model) loadTun() tea.Cmd {
	return func() tea.Msg {
		status, err := m.client.Tun(m.ctx)
		return tunStatusMsg{status: status, err: err}
	}
}

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	switch typed := message.(type) {
	case onboardingResultMsg:
		if typed.err != nil {
			m.lastError = ui.SystemStateUnavailable
		} else {
			m.lastError = ""
			m.onboarding = typed.status
		}
		return m, nil
	case serviceStatusMsg:
		m.serviceLoaded = true
		m.elevated = typed.elevated
		if typed.err != nil {
			m.serviceStatus = service.StatusUnknown
			if m.lastError == "" {
				m.lastError = ui.ServiceStatusUnavailable
			}
		} else {
			m.serviceStatus = typed.status
		}
		return m, nil
	case systemProxyStatusMsg:
		m.systemProxyLoaded = true
		if typed.err != nil {
			if m.lastError == "" {
				m.lastError = ui.SystemProxyStateUnavailable
			}
			return m, nil
		}
		m.systemProxy = typed.status
		return m, nil
	case tunStatusMsg:
		m.tunLoaded = true
		if typed.err != nil {
			if m.lastError == "" {
				m.lastError = ui.TunStateUnavailable
			}
			return m, nil
		}
		m.tun = typed.status
		return m, nil
	case ui.CoreObservedMsg:
		m.core = typed.Core
		return m, nil
	case ui.ActionPendingMsg:
		m.beginRowPending(typed.Action)
		return m, m.rowSpinCmdIfNeeded()
	case startRowSpinMsg:
		if typed.gen != m.rowSpinGen || !m.pending {
			if typed.gen == m.rowSpinGen {
				m.rowSpinning = false
			}
			return m, nil
		}
		return m, tea.Tick(rowSpinInterval, func(t time.Time) tea.Msg {
			return rowSpinTickMsg{t: t, gen: typed.gen}
		})
	case rowSpinTickMsg:
		if typed.gen != m.rowSpinGen {
			return m, nil
		}
		m.rowSpinClock = typed.t
		if !m.pending {
			m.rowSpinning = false
			return m, nil
		}
		return m, tea.Tick(rowSpinInterval, func(t time.Time) tea.Msg {
			return rowSpinTickMsg{t: t, gen: typed.gen}
		})
	case actionStartMsg:
		if m.pending {
			return m, nil
		}
		m.pending = true
		return m, m.runAction(typed)
	case actionResultMsg:
		rowID := m.pendingRow
		m.clearRowPending()
		if typed.err != nil {
			var apiError protocol.APIError
			if errors.As(typed.err, &apiError) && apiError.Code == protocol.CodeRevisionConflict {
				m.lastError = ui.SystemChangedMessage
				return m, tea.Batch(m.Load(), m.loadCore(), m.rowSpinCmdIfNeeded())
			}
			m.lastError = ui.SystemActionFailed
			return m, m.rowSpinCmdIfNeeded()
		}
		m.lastError = ""
		m.markRowDone(rowID)
		revision := typed.restart.Revision
		if typed.kind == actionUpdate {
			revision = typed.install.Revision
		}
		return m, tea.Batch(m.Load(), m.loadCore(), func() tea.Msg { return ui.RuntimeRevisionMsg{Revision: revision} }, m.rowSpinCmdIfNeeded())
	case serviceResultMsg:
		rowID := m.pendingRow
		m.clearRowPending()
		if typed.err != nil {
			m.lastError = serviceErrorMessage(typed.err)
			return m, tea.Batch(m.loadServiceStatus(), m.rowSpinCmdIfNeeded())
		}
		m.lastError = ""
		m.markRowDone(rowID)
		return m, tea.Batch(m.loadServiceStatus(), m.rowSpinCmdIfNeeded())
	case systemProxyActionResultMsg:
		return m.handleSystemProxyActionResult(typed)
	case tunActionResultMsg:
		return m.handleTunActionResult(typed)
	}

	key, ok := message.(tea.KeyPressMsg)
	if m.detail != nil {
		if ok && (key.String() == "esc" || key.String() == "enter") {
			m.detail = nil
		}
		return m, nil
	}
	if !ok {
		return m, nil
	}
	rows := m.rows()
	index := m.rowIndex(m.focusID)
	switch key.String() {
	case "esc":
		return m, func() tea.Msg { return ui.FocusRailMsg{} }
	case "up":
		if index > 0 {
			m.focusID = rows[index-1].id
		}
	case "down":
		if index >= 0 && index+1 < len(rows) {
			m.focusID = rows[index+1].id
		}
	case "enter":
		if index < 0 {
			return m, nil
		}
		switch m.focusID {
		case rowEndpoints, rowRunSetup:
			if m.client == nil || !m.mutationsEnabled || !m.hasCapability(protocol.CapabilityOnboarding) {
				return m, nil
			}
			return m, func() tea.Msg { return ui.RouteRequestMsg{Page: ui.PageSetup} }
		case rowCoreUpdate:
			if m.client == nil || !m.mutationsEnabled || !m.hasCapability(protocol.CapabilityCore) {
				return m, nil
			}
			return m, m.confirmAction(actionUpdate)
		case rowCoreRestart:
			if m.client == nil || !m.mutationsEnabled || !m.hasCapability(protocol.CapabilityCore) {
				return m, nil
			}
			return m, m.confirmAction(actionRestart)
		case rowServiceInstall:
			return m, m.confirmServiceAction(serviceInstall)
		case rowServiceUninstall:
			return m, m.confirmServiceAction(serviceUninstall)
		case rowServiceReinstall:
			return m, m.confirmServiceAction(serviceReinstall)
		case rowServiceStart:
			return m, m.confirmServiceAction(serviceStart)
		case rowServiceStop:
			return m, m.confirmServiceAction(serviceStop)
		case rowServiceRestart:
			return m, m.confirmServiceAction(serviceRestart)
		case rowSystemProxy:
			return m, m.confirmSystemProxyToggle()
		case rowTUN:
			return m, m.confirmTunToggle()
		default:
			selected := rows[index]
			m.detail = &selected
		}
	}
	return m, nil
}

func (m *Model) handleSystemProxyActionResult(typed systemProxyActionResultMsg) (ui.Page, tea.Cmd) {
	rowID := m.pendingRow
	m.clearRowPending()
	if typed.err != nil {
		var apiError protocol.APIError
		if errors.As(typed.err, &apiError) {
			switch apiError.Code {
			case protocol.CodeSystemProxyConflict:
				return m, tea.Batch(m.confirmForceSystemProxy(apiError), m.rowSpinCmdIfNeeded())
			case protocol.CodeSystemProxyNotOwned:
				m.lastError = ui.SystemProxyNotOwnedMessage
				return m, tea.Batch(m.loadSystemProxy(), m.rowSpinCmdIfNeeded())
			case protocol.CodeRevisionConflict:
				m.lastError = ui.SystemChangedMessage
				return m, tea.Batch(m.Load(), m.rowSpinCmdIfNeeded())
			}
		}
		m.lastError = ui.SystemProxyActionFailed
		return m, tea.Batch(m.loadSystemProxy(), m.rowSpinCmdIfNeeded())
	}
	m.lastError = ""
	m.markRowDone(rowID)
	m.systemProxy = typed.status
	m.systemProxyLoaded = true
	return m, tea.Batch(m.loadSystemProxy(), func() tea.Msg {
		return ui.RuntimeRevisionMsg{Revision: typed.status.Revision}
	}, m.rowSpinCmdIfNeeded())
}

func (m *Model) handleTunActionResult(typed tunActionResultMsg) (ui.Page, tea.Cmd) {
	rowID := m.pendingRow
	m.clearRowPending()
	if typed.err != nil {
		var apiError protocol.APIError
		if errors.As(typed.err, &apiError) && apiError.Code == protocol.CodeRevisionConflict {
			m.lastError = ui.SystemChangedMessage
			return m, tea.Batch(m.Load(), m.rowSpinCmdIfNeeded())
		}
		m.lastError = ui.TunActionFailed
		return m, tea.Batch(m.loadTun(), m.rowSpinCmdIfNeeded())
	}
	m.lastError = ""
	m.markRowDone(rowID)
	m.tun = typed.status
	m.tunLoaded = true
	return m, tea.Batch(m.loadTun(), func() tea.Msg {
		return ui.RuntimeRevisionMsg{Revision: typed.status.Revision}
	}, m.rowSpinCmdIfNeeded())
}

func (m *Model) View() string {
	if m.detail != nil {
		return m.theme.Content.Width(m.width).Height(m.height).Render(
			m.theme.Title.Render(m.detail.label+" details") + "\n\n" + m.detail.detail + "\n\n" + ui.EscCloseHint,
		)
	}
	lines := []string{m.theme.Title.Render(ui.SystemTitle), ""}
	section := ""
	clock := m.rowSpinClock
	if clock.IsZero() {
		clock = time.Unix(0, 0)
	}
	for _, item := range m.rows() {
		if item.section != section {
			section = item.section
			if len(lines) > 2 {
				lines = append(lines, "")
			}
			lines = append(lines, m.theme.TableHeader.Render(section))
		}
		marker := "  "
		rowFocused := item.id == m.focusID
		if rowFocused {
			marker = ui.FocusMarker
		}
		value := item.value
		switch {
		case m.pending && m.pendingRow == item.id && m.pendingNote != "":
			// Prefer in-row braille + status note over static value while work runs.
			value = m.theme.Warning.Render(ui.SpinnerLabel(clock, m.pendingNote))
		case m.doneRow == item.id:
			// Sticky green Done until page leave or another action starts.
			value = m.doneBadge()
		}
		if value != "" {
			value = "  " + value
		}
		line := marker + item.label + value
		if rowFocused && m.contentFocused {
			line = m.theme.RowFocus.Render(line)
		}
		lines = append(lines, line)
	}
	if m.lastError != "" {
		lines = append(lines, "", m.lastError)
	}
	return m.theme.Content.Width(m.width).Height(m.height).Render(strings.Join(lines, "\n"))
}

func (m *Model) rows() []row {
	configState := ui.UnavailableTitle
	if m.status.Config != nil {
		configState = fmt.Sprintf("%s · desired %d / observed %d", m.status.Config.Status, m.status.Config.DesiredRevision, m.status.Config.ObservedRevision)
	}
	daemon := fmt.Sprintf("Version %s\nUptime %s\nHealth %s\nRevision %d\nConfig %s", valueOr(m.status.DaemonVersion, ui.UnknownLabel), uptime(m.status.StartedAt), valueOr(m.status.Health, ui.UnknownLabel), m.status.Revision, configState)
	core := fmt.Sprintf("Status %s\nVersion %s\nPID %d\nRestarts %d", valueOr(m.core.Status, ui.UnknownLabel), valueOr(m.core.Version, ui.UnknownLabel), m.core.PID, m.core.Restarts)
	endpoints := fmt.Sprintf("Mixed %s\nController %s\nWeb GUI %s", valueOr(m.onboarding.MixedAddr, ui.MissingValue), valueOr(m.onboarding.ControllerAddr, ui.MissingValue), valueOr(m.onboarding.WebAddr, ui.MissingValue))
	rows := []row{
		{id: rowDaemon, section: ui.DaemonSectionTitle, label: ui.DaemonLabel, value: valueOr(m.status.DaemonVersion, ui.UnknownLabel) + " · " + valueOr(m.status.Health, ui.UnknownLabel), detail: daemon},
		{id: rowCore, section: ui.CoreSectionTitle, label: ui.MihomoCoreLabel, value: valueOr(m.core.Status, ui.UnknownLabel) + " · " + valueOr(m.core.Version, ui.UnknownLabel), detail: core},
		{id: rowCoreUpdate, section: ui.CoreSectionTitle, label: m.coreActionLabel(), value: actionState(m.pending, m.hasCapability(protocol.CapabilityCore), m.mutationsEnabled), detail: ui.UpdateCoreImpact},
		{id: rowCoreRestart, section: ui.CoreSectionTitle, label: ui.RestartCoreLabel, value: actionState(m.pending, m.hasCapability(protocol.CapabilityCore), m.mutationsEnabled), detail: ui.RestartCoreImpact},
		{id: rowEndpoints, section: ui.LocalEndpointsLabel, label: ui.LocalEndpointsLabel, value: endpointSummary(m.onboarding), detail: endpoints},
		{id: rowRunSetup, section: ui.MaintenanceSectionTitle, label: ui.RunSetupLabel, detail: ui.RunSetupDetail},
	}
	rows = append(rows, m.serviceRows()...)
	rows = append(rows, m.networkRows()...)
	return rows
}

func (m *Model) networkRows() []row {
	section := ui.NetworkSectionTitle
	var rows []row
	if m.hasCapability(protocol.CapabilitySystemProxy) {
		value := ui.LoadingLabel
		detail := ui.EnableSystemProxyImpact
		if m.systemProxyLoaded {
			value = systemProxySummary(m.systemProxy)
			detail = systemProxyDetail(m.systemProxy)
		}
		if m.pending {
			value = ui.PendingLabel
		} else if !m.mutationsEnabled {
			value = actionState(false, true, false)
			if m.systemProxyLoaded {
				value = systemProxySummary(m.systemProxy) + " · " + ui.StaleLabel
			}
		}
		rows = append(rows, row{
			id: rowSystemProxy, section: section, label: ui.SystemProxyLabel,
			value: value, detail: detail,
		})
	}
	// TUN row is always listed; live status when capability is present.
	tunValue := ui.UnavailableTitle
	tunDetail := ui.TUNUnavailableDetail
	if m.hasCapability(protocol.CapabilityTUN) {
		tunValue = ui.LoadingLabel
		tunDetail = ui.EnableTunImpact
		if m.tunLoaded {
			tunValue = tunSummary(m.tun)
			tunDetail = tunDetailText(m.tun)
		}
		if m.pending {
			tunValue = ui.PendingLabel
		} else if !m.mutationsEnabled {
			if m.tunLoaded {
				tunValue = tunSummary(m.tun) + " · " + ui.StaleLabel
			} else {
				tunValue = ui.StaleLabel
			}
		}
	}
	rows = append(rows, row{
		id: rowTUN, section: section, label: ui.TUNLabel,
		value: tunValue, detail: tunDetail,
	})
	return rows
}

func (m *Model) serviceRows() []row {
	section := ui.SystemServiceSectionTitle
	if m.service == nil {
		return []row{{
			id: rowServiceStatus, section: section, label: ui.ServiceStatusLabel,
			value: ui.UnavailableTitle, detail: ui.ServiceUnavailableDetail,
		}}
	}
	statusValue := string(m.serviceStatus)
	if !m.serviceLoaded || statusValue == "" {
		statusValue = ui.UnknownLabel
	}
	privilege := ui.ServiceNotElevatedLabel
	if m.elevated {
		privilege = ui.ServiceElevatedLabel
	}
	statusDetail := fmt.Sprintf("Status %s\nPrivileges %s\n%s", statusValue, privilege, ui.ServiceElevationRequired)
	if m.elevated {
		statusDetail = fmt.Sprintf("Status %s\nPrivileges %s", statusValue, privilege)
	}
	return []row{
		{id: rowServiceStatus, section: section, label: ui.ServiceStatusLabel, value: statusValue + " · " + privilege, detail: statusDetail},
		{id: rowServiceInstall, section: section, label: ui.ServiceInstallLabel, value: m.serviceActionState(serviceInstall), detail: ui.ServiceInstallImpact},
		{id: rowServiceUninstall, section: section, label: ui.ServiceUninstallLabel, value: m.serviceActionState(serviceUninstall), detail: ui.ServiceUninstallImpact},
		{id: rowServiceReinstall, section: section, label: ui.ServiceReinstallLabel, value: m.serviceActionState(serviceReinstall), detail: ui.ServiceReinstallImpact},
		{id: rowServiceStart, section: section, label: ui.ServiceStartLabel, value: m.serviceActionState(serviceStart), detail: ui.ServiceStartImpact},
		{id: rowServiceStop, section: section, label: ui.ServiceStopLabel, value: m.serviceActionState(serviceStop), detail: ui.ServiceStopImpact},
		{id: rowServiceRestart, section: section, label: ui.ServiceRestartLabel, value: m.serviceActionState(serviceRestart), detail: ui.ServiceRestartImpact},
	}
}

func (m *Model) serviceActionState(kind serviceActionKind) string {
	if m.service == nil {
		return ui.UnavailableTitle
	}
	if m.pending {
		return ui.PendingLabel
	}
	if !m.elevated {
		return ui.ServiceNeedsElevation
	}
	if !m.serviceActionAllowed(kind) {
		return ui.UnavailableTitle
	}
	return ""
}

func (m *Model) serviceActionAllowed(kind serviceActionKind) bool {
	status := m.serviceStatus
	if !m.serviceLoaded {
		status = service.StatusUnknown
	}
	switch kind {
	case serviceInstall:
		return status == service.StatusNotInstalled || status == service.StatusUnknown
	case serviceUninstall:
		return status != service.StatusNotInstalled
	case serviceReinstall:
		// Always available when elevated: upgrades replace ImagePath even if not installed.
		return true
	case serviceStart:
		return status == service.StatusStopped || status == service.StatusUnknown
	case serviceStop:
		return status == service.StatusRunning || status == service.StatusUnknown
	case serviceRestart:
		return status == service.StatusRunning || status == service.StatusStopped || status == service.StatusUnknown
	default:
		return false
	}
}

func (m *Model) confirmServiceAction(kind serviceActionKind) tea.Cmd {
	if m.service == nil || m.pending {
		return nil
	}
	if !m.elevated {
		m.lastError = ui.ServiceElevationRequired
		return nil
	}
	if !m.serviceActionAllowed(kind) {
		return nil
	}
	title, impact, rollback, action := serviceActionCopy(kind)
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: action, Page: ui.PageSystem, Capability: "", Key: "system:" + string(action),
			Title: title, Object: ui.SystemServiceLabel, Impact: impact, Rollback: rollback,
			Execute: m.runServiceAction(kind),
		}
	}
}

func serviceActionCopy(kind serviceActionKind) (title, impact, rollback string, action ui.Action) {
	switch kind {
	case serviceInstall:
		return ui.ServiceInstallTitle, ui.ServiceInstallImpact, ui.ServiceInstallRollback, ui.ActionServiceInstall
	case serviceUninstall:
		return ui.ServiceUninstallTitle, ui.ServiceUninstallImpact, ui.ServiceUninstallRollback, ui.ActionServiceUninstall
	case serviceReinstall:
		return ui.ServiceReinstallTitle, ui.ServiceReinstallImpact, ui.ServiceReinstallRollback, ui.ActionServiceReinstall
	case serviceStart:
		return ui.ServiceStartTitle, ui.ServiceStartImpact, ui.ServiceStartRollback, ui.ActionServiceStart
	case serviceStop:
		return ui.ServiceStopTitle, ui.ServiceStopImpact, ui.ServiceStopRollback, ui.ActionServiceStop
	case serviceRestart:
		return ui.ServiceRestartTitle, ui.ServiceRestartImpact, ui.ServiceRestartRollback, ui.ActionServiceRestart
	default:
		return ui.ServiceInstallTitle, ui.ServiceInstallImpact, ui.ServiceInstallRollback, ui.ActionServiceInstall
	}
}

func (m *Model) runServiceAction(kind serviceActionKind) tea.Cmd {
	return func() tea.Msg {
		if err := elevate.RequireElevated(); err != nil {
			return serviceResultMsg{kind: kind, err: err}
		}
		var err error
		switch kind {
		case serviceInstall:
			err = m.service.Install()
		case serviceUninstall:
			err = m.service.Uninstall()
		case serviceReinstall:
			err = m.service.Reinstall()
		case serviceStart:
			err = m.service.Start()
		case serviceStop:
			err = m.service.Stop()
		case serviceRestart:
			err = m.service.Restart()
		}
		return serviceResultMsg{kind: kind, err: err}
	}
}

func (m *Model) beginRowPending(action ui.Action) {
	rowID, note := rowProgressForAction(action, m.core.Version == "")
	if rowID == "" {
		return
	}
	m.pending = true
	m.pendingRow = rowID
	m.pendingNote = note
	m.doneRow = "" // new run replaces any sticky Done
}

func (m *Model) clearRowPending() {
	m.pending = false
	m.pendingRow = ""
	m.pendingNote = ""
}

func (m *Model) markRowDone(rowID string) {
	if rowID == "" {
		return
	}
	m.doneRow = rowID
}

// ClearDone drops sticky success badges (call when leaving the System page).
func (m *Model) ClearDone() {
	m.doneRow = ""
}

func (m *Model) doneBadge() string {
	// Green background pill so completion is obvious next to the action label.
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("0")).
		Background(m.theme.ColorSuccess).
		Padding(0, 1).
		Render(ui.DoneLabel)
}

func (m *Model) rowSpinCmdIfNeeded() tea.Cmd {
	if !m.pending || m.pendingRow == "" {
		m.rowSpinning = false
		return nil
	}
	if m.rowSpinning {
		return nil
	}
	m.rowSpinGen++
	gen := m.rowSpinGen
	m.rowSpinning = true
	return func() tea.Msg { return startRowSpinMsg{gen: gen} }
}

func rowProgressForAction(action ui.Action, coreMissing bool) (rowID, note string) {
	switch action {
	case ui.ActionServiceInstall:
		return rowServiceInstall, ui.ServiceProgressInstalling
	case ui.ActionServiceUninstall:
		return rowServiceUninstall, ui.ServiceProgressUninstalling
	case ui.ActionServiceReinstall:
		return rowServiceReinstall, ui.ServiceProgressReinstalling
	case ui.ActionServiceStart:
		return rowServiceStart, ui.ServiceProgressStarting
	case ui.ActionServiceStop:
		return rowServiceStop, ui.ServiceProgressStopping
	case ui.ActionServiceRestart:
		return rowServiceRestart, ui.ServiceProgressRestarting
	case ui.ActionUpdateCore:
		if coreMissing {
			return rowCoreUpdate, ui.CoreProgressInstalling
		}
		return rowCoreUpdate, ui.CoreProgressUpdating
	case ui.ActionRestartCore:
		return rowCoreRestart, ui.CoreProgressRestarting
	case ui.ActionEnableSystemProxy, ui.ActionForceSystemProxy:
		return rowSystemProxy, ui.ProxyProgressEnabling
	case ui.ActionDisableSystemProxy:
		return rowSystemProxy, ui.ProxyProgressDisabling
	case ui.ActionEnableTun:
		return rowTUN, ui.TunProgressEnabling
	case ui.ActionDisableTun:
		return rowTUN, ui.TunProgressDisabling
	default:
		return "", ""
	}
}

func serviceErrorMessage(err error) string {
	var apiError protocol.APIError
	if errors.As(err, &apiError) && apiError.Message != "" {
		return apiError.Message
	}
	return ui.ServiceActionFailed
}

func (m *Model) confirmSystemProxyToggle() tea.Cmd {
	if m.client == nil || m.pending || !m.mutationsEnabled || !m.hasCapability(protocol.CapabilitySystemProxy) {
		return nil
	}
	// Foreign observed → secondary force-enable confirm (never claim disable clears it).
	if m.systemProxy.Observed.Foreign {
		return m.confirmForceSystemProxyFromStatus()
	}
	// Owned or desired on → confirm disable.
	if m.systemProxy.Desired || m.systemProxy.Observed.Owned {
		return m.confirmSystemProxyAction(proxyDisable)
	}
	return m.confirmSystemProxyAction(proxyEnable)
}

func (m *Model) confirmSystemProxyAction(kind proxyActionKind) tea.Cmd {
	revision, operationID := m.proxyRevision(), m.newOperationID()
	title, impact, rollback, action := ui.EnableSystemProxyTitle, ui.EnableSystemProxyImpact, ui.EnableSystemProxyRollback, ui.ActionEnableSystemProxy
	force := false
	switch kind {
	case proxyForceEnable:
		title, impact, rollback, action = ui.ForceSystemProxyTitle, forceImpactFromStatus(m.systemProxy), ui.ForceSystemProxyRollback, ui.ActionForceSystemProxy
		force = true
	case proxyDisable:
		title, impact, rollback, action = ui.DisableSystemProxyTitle, ui.DisableSystemProxyImpact, ui.DisableSystemProxyRollback, ui.ActionDisableSystemProxy
	}
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: action, Page: ui.PageSystem, Capability: protocol.CapabilitySystemProxy,
			Key: "system:" + string(action), Title: title, Object: ui.SystemProxyLabel,
			Impact: impact, Rollback: rollback,
			Execute: m.runSystemProxyAction(kind, operationID, revision, force),
		}
	}
}

func (m *Model) confirmForceSystemProxyFromStatus() tea.Cmd {
	return m.confirmSystemProxyAction(proxyForceEnable)
}

func (m *Model) confirmForceSystemProxy(apiError protocol.APIError) tea.Cmd {
	revision, operationID := m.proxyRevision(), m.newOperationID()
	current := detailString(apiError.Details, "current_server")
	target := detailString(apiError.Details, "target_server")
	if current == "" {
		current = valueOr(m.systemProxy.Observed.Server, ui.MissingValue)
	}
	if target == "" {
		target = valueOr(m.systemProxy.Target, ui.MissingValue)
	}
	impact := fmt.Sprintf(ui.ForceSystemProxyImpact, valueOr(current, ui.MissingValue), valueOr(target, ui.MissingValue))
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionForceSystemProxy, Page: ui.PageSystem, Capability: protocol.CapabilitySystemProxy,
			Key: "system:" + string(ui.ActionForceSystemProxy), Title: ui.ForceSystemProxyTitle, Object: ui.SystemProxyLabel,
			Impact: impact, Rollback: ui.ForceSystemProxyRollback,
			Execute: m.runSystemProxyAction(proxyForceEnable, operationID, revision, true),
		}
	}
}

func (m *Model) runSystemProxyAction(kind proxyActionKind, operationID string, revision uint64, force bool) tea.Cmd {
	return func() tea.Msg {
		request := protocol.SystemProxyMutationRequest{OperationID: operationID, Force: force}
		if revision > 0 {
			request.IfRevision = &revision
		}
		var (
			status protocol.SystemProxyStatus
			err    error
		)
		switch kind {
		case proxyDisable:
			status, err = m.client.DisableSystemProxy(m.ctx, request)
		default:
			status, err = m.client.EnableSystemProxy(m.ctx, request)
		}
		return systemProxyActionResultMsg{kind: kind, status: status, err: err}
	}
}

func (m *Model) confirmTunToggle() tea.Cmd {
	if m.client == nil || m.pending || !m.mutationsEnabled || !m.hasCapability(protocol.CapabilityTUN) {
		return nil
	}
	if m.tun.DesiredEnable {
		return m.confirmTunAction(tunDisable)
	}
	return m.confirmTunAction(tunEnable)
}

func (m *Model) confirmTunAction(kind tunActionKind) tea.Cmd {
	revision, operationID := m.tunRevision(), m.newOperationID()
	title, impact, rollback, action := ui.EnableTunTitle, ui.EnableTunImpact, ui.EnableTunRollback, ui.ActionEnableTun
	if kind == tunDisable {
		title, impact, rollback, action = ui.DisableTunTitle, ui.DisableTunImpact, ui.DisableTunRollback, ui.ActionDisableTun
	}
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: action, Page: ui.PageSystem, Capability: protocol.CapabilityTUN,
			Key: "system:" + string(action), Title: title, Object: ui.TUNLabel,
			Impact: impact, Rollback: rollback,
			Execute: m.runTunAction(kind, operationID, revision),
		}
	}
}

func (m *Model) runTunAction(kind tunActionKind, operationID string, revision uint64) tea.Cmd {
	return func() tea.Msg {
		request := protocol.TunMutationRequest{OperationID: operationID}
		if revision > 0 {
			request.IfRevision = &revision
		}
		var (
			status protocol.TunStatus
			err    error
		)
		if kind == tunDisable {
			status, err = m.client.DisableTun(m.ctx, request)
		} else {
			status, err = m.client.EnableTun(m.ctx, request)
		}
		return tunActionResultMsg{kind: kind, status: status, err: err}
	}
}

func (m *Model) rowIndex(id string) int {
	for index, item := range m.rows() {
		if item.id == id {
			return index
		}
	}
	return -1
}

func (m *Model) currentRevision() uint64 {
	return max(m.status.Revision, m.core.Revision, m.onboarding.Revision, m.systemProxy.Revision, m.tun.Revision)
}

func (m *Model) proxyRevision() uint64 {
	if m.systemProxy.Revision > 0 {
		return m.systemProxy.Revision
	}
	return m.currentRevision()
}

func (m *Model) tunRevision() uint64 {
	if m.tun.Revision > 0 {
		return m.tun.Revision
	}
	return m.currentRevision()
}

func (m *Model) confirmAction(kind actionKind) tea.Cmd {
	revision, operationID := m.currentRevision(), m.newOperationID()
	title, object, impact, rollback := ui.UpdateCoreTitle, ui.MihomoCoreLabel, ui.UpdateCoreImpact, ui.UpdateCoreRollback
	action := ui.ActionUpdateCore
	if kind == actionUpdate && m.core.Version == "" {
		title, impact = ui.InstallCoreTitle, ui.InstallCoreImpact
	}
	if kind == actionRestart {
		title, impact, rollback = ui.RestartCoreTitle, ui.RestartCoreImpact, ui.RestartCoreRollback
		action = ui.ActionRestartCore
	}
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: action, Page: ui.PageSystem, Capability: protocol.CapabilityCore, Key: "system:" + string(action),
			Title: title, Object: object, Impact: impact, Rollback: rollback,
			Execute: m.runAction(actionStartMsg{kind: kind, operationID: operationID, revision: revision}),
		}
	}
}

func (m *Model) coreActionLabel() string {
	if m.core.Version == "" {
		return ui.InstallCoreLabel
	}
	return ui.UpdateCoreLabel
}

func (m *Model) runAction(start actionStartMsg) tea.Cmd {
	return func() tea.Msg {
		revision := start.revision
		request := protocol.MutationRequest{OperationID: start.operationID, IfRevision: &revision}
		if start.kind == actionUpdate {
			result, err := m.client.InstallCore(m.ctx, request)
			return actionResultMsg{kind: start.kind, install: result, err: err}
		}
		result, err := m.client.RestartCore(m.ctx, request)
		return actionResultMsg{kind: start.kind, restart: result, err: err}
	}
}

func (m *Model) loadCore() tea.Cmd {
	if m.client == nil || !m.hasCapability(protocol.CapabilityCore) {
		return nil
	}
	return func() tea.Msg {
		core, err := m.client.Core(m.ctx)
		if err != nil {
			return actionResultMsg{err: err}
		}
		return ui.CoreObservedMsg{Core: core}
	}
}

func endpointSummary(status protocol.OnboardingStatus) string {
	return fmt.Sprintf("%s · %s · %s", valueOr(status.MixedAddr, ui.MissingValue), valueOr(status.ControllerAddr, ui.MissingValue), valueOr(status.WebAddr, ui.MissingValue))
}

func actionState(pending, available, enabled bool) string {
	if !available {
		return ui.UnavailableTitle
	}
	if !enabled {
		return ui.StaleLabel
	}
	if pending {
		return ui.PendingLabel
	}
	return ""
}

func systemProxySummary(status protocol.SystemProxyStatus) string {
	desired := ui.OffLabel
	if status.Desired {
		desired = ui.OnLabel
	}
	observed := ui.OffLabel
	if status.Observed.Enabled {
		observed = valueOr(status.Observed.Server, ui.OnLabel)
		switch {
		case status.Observed.Owned:
			observed += " · " + ui.OwnedLabel
		case status.Observed.Foreign:
			observed += " · " + ui.ForeignLabel
		}
	} else if status.Observed.Foreign {
		// Residual foreign config with ProxyEnable=0 still surfaces classification.
		observed = valueOr(status.Observed.Server, ui.OffLabel) + " · " + ui.ForeignLabel
	}
	return fmt.Sprintf("%s %s · %s %s", ui.DesiredLabel, desired, ui.ObservedLabel, observed)
}

func systemProxyDetail(status protocol.SystemProxyStatus) string {
	return fmt.Sprintf(
		"%s %v\n%s %s\n%s %v\n%s %s\n%s %v\n%s %v\n%s %s",
		ui.DesiredLabel, status.Desired,
		"Target", valueOr(status.Target, ui.MissingValue),
		"Enabled", status.Observed.Enabled,
		"Server", valueOr(status.Observed.Server, ui.MissingValue),
		ui.OwnedLabel, status.Observed.Owned,
		ui.ForeignLabel, status.Observed.Foreign,
		"Last error", valueOr(status.LastError, ui.MissingValue),
	)
}

func tunSummary(status protocol.TunStatus) string {
	desired := ui.OffLabel
	if status.DesiredEnable {
		desired = ui.OnLabel
	}
	live := ui.UnknownLabel
	if status.LiveEnable != nil {
		if *status.LiveEnable {
			live = ui.OnLabel
		} else {
			live = ui.OffLabel
		}
	}
	managed := ui.UnmanagedLabel
	if status.Managed {
		managed = ui.ActiveLabel
	}
	parts := []string{
		fmt.Sprintf("%s %s", ui.DesiredLabel, desired),
		fmt.Sprintf("%s %s", ui.LiveLabel, live),
		managed,
	}
	if status.Stack != "" {
		parts = append(parts, status.Stack)
	}
	return strings.Join(parts, " · ")
}

func tunDetailText(status protocol.TunStatus) string {
	live := ui.UnknownLabel
	if status.LiveEnable != nil {
		if *status.LiveEnable {
			live = ui.OnLabel
		} else {
			live = ui.OffLabel
		}
	}
	return fmt.Sprintf(
		"%s %v\n%s %s\nStack %s\nManaged %v\nLast error %s",
		ui.DesiredLabel, status.DesiredEnable,
		ui.LiveLabel, live,
		valueOr(status.Stack, ui.MissingValue),
		status.Managed,
		valueOr(status.LastError, ui.MissingValue),
	)
}

func forceImpactFromStatus(status protocol.SystemProxyStatus) string {
	return fmt.Sprintf(
		ui.ForceSystemProxyImpact,
		valueOr(status.Observed.Server, ui.MissingValue),
		valueOr(status.Target, ui.MissingValue),
	)
}

func detailString(details map[string]any, key string) string {
	if details == nil {
		return ""
	}
	value, ok := details[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func (m *Model) hasCapability(capability string) bool {
	return slices.Contains(m.status.Capabilities, capability)
}

func uptime(started time.Time) string {
	if started.IsZero() {
		return ui.UnknownLabel
	}
	duration := time.Since(started)
	if duration < 0 {
		duration = 0
	}
	return duration.Round(time.Second).String()
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

var fallbackOperationID atomic.Uint64

func defaultOperationID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "tui-system-" + hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("tui-system-%d-%d", time.Now().UnixNano(), fallbackOperationID.Add(1))
}
