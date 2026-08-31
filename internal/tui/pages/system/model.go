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

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"github.com/mihari-proxy/mihari/internal/update"
)

const (
	rowDaemon            = "daemon"
	rowCore              = "core"
	rowCoreChannel       = "core-channel"
	rowCoreUpdate        = "core-update"
	rowCoreRestart       = "core-restart"
	rowMihariChannel     = "mihari-channel"
	rowMihariUpdate      = "mihari-update"
	rowZashboard         = "zashboard"
	rowMetaCubeXD        = "metacubexd"
	rowRunSetup          = "run-setup"
	rowServiceStatus     = "service-status"
	rowServiceHint       = "service-hint"
	rowServiceInstall    = "service-install"
	rowServiceUninstall  = "service-uninstall"
	rowServiceReinstall  = "service-reinstall"
	rowServiceStart      = "service-start"
	rowServiceStop       = "service-stop"
	rowServiceRestart    = "service-restart"
	rowSystemProxy       = "system-proxy"
	rowSystemProxyAction = "system-proxy-action"
	rowTUN               = "tun"
	rowTUNAction         = "tun-action"
	rowAbout             = "about"
	rowGitHub            = "github"
	rowMixed             = "port-mixed"
	rowController        = "port-controller"
	rowWeb               = "port-web"
)

// Panel IDs mirrored from internal/panel/catalog.go; local constants keep the
// panel package out of the presentation layer.
const (
	panelIDZashboard  = "zashboard"
	panelIDMetaCubeXD = "metacubexd"
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
	WebGUI(context.Context) (protocol.WebGUIStatus, error)
	OpenWebGUI(context.Context, string) (protocol.WebGUIOpenResult, error)
	UpdateOnboarding(context.Context, protocol.OnboardingUpdateRequest) (protocol.OnboardingStatus, error)
}

// SelfUpdater is the local Mihari binary lifecycle surface used by the System page.
type SelfUpdater interface {
	Check(context.Context, string, string) (update.CheckResult, error)
	Update(context.Context, string, string, string) (update.Result, error)
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

type selfCheckResultMsg struct {
	generation uint64
	result     update.CheckResult
	err        error
}

type selfUpdateResultMsg struct {
	result update.Result
	err    error
}

type mihariChannelResultMsg struct {
	channel string
	err     error
}

func (m mihariChannelResultMsg) Err() error { return m.err }

var _ interface{ Err() error } = mihariChannelResultMsg{}

// Err implements the shell action-outcome contract. Once replacement commits,
// a service restart error is a warning and must not classify the update as failed.
func (m selfUpdateResultMsg) Err() error {
	if m.result.Updated {
		return nil
	}
	return m.err
}

var _ interface{ Err() error } = selfUpdateResultMsg{}

type serviceStatusMsg struct {
	status   service.StatusKind
	elevated bool
	err      error
}

type serviceResultMsg struct {
	kind serviceActionKind
	err  error
}

// Err implements the shell's action-outcome contract so OS service actions are
// classified Succeeded/Failed in the Recent operations ledger.
func (m serviceResultMsg) Err() error { return m.err }

var _ interface{ Err() error } = serviceResultMsg{}

type systemProxyStatusMsg struct {
	status protocol.SystemProxyStatus
	err    error
}

type systemProxyActionResultMsg struct {
	kind   proxyActionKind
	status protocol.SystemProxyStatus
	err    error
}

// Err implements the shell's action-outcome contract so system proxy actions
// are classified Succeeded/Failed in the Recent operations ledger.
func (m systemProxyActionResultMsg) Err() error { return m.err }

func (m systemProxyActionResultMsg) ProxyStatus() protocol.SystemProxyStatus { return m.status }

var _ interface{ Err() error } = systemProxyActionResultMsg{}

type tunStatusMsg struct {
	status protocol.TunStatus
	err    error
}

type webGUIStatusMsg struct {
	status protocol.WebGUIStatus
	err    error
}

// webGUIOpenResultMsg reports a panel browser-open outcome. rowID is captured
// inside the Execute closure: focus may move while the open request is in flight.
type webGUIOpenResultMsg struct {
	rowID string
	err   error
}

// Err implements the shell's action-outcome contract so browser-open actions
// are classified Succeeded/Failed in the Recent operations ledger.
func (m webGUIOpenResultMsg) Err() error { return m.err }

var _ interface{ Err() error } = webGUIOpenResultMsg{}

type tunActionResultMsg struct {
	kind   tunActionKind
	status protocol.TunStatus
	err    error
}

// Err implements the shell's action-outcome contract so TUN actions are
// classified Succeeded/Failed in the Recent operations ledger.
func (m tunActionResultMsg) Err() error { return m.err }

func (m tunActionResultMsg) TunStatus() protocol.TunStatus { return m.status }

var _ interface{ Err() error } = tunActionResultMsg{}

type actionKind uint8

const (
	actionUpdate actionKind = iota
	actionRestart
	actionSwitchChannel
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

// Outcome fade: a successful System proxy / TUN toggle shows a Done badge that
// self-clears after outcomeFadeInterval (the status row keeps showing the live
// result). Failures stay sticky. The gen + row guard on outcomeFadeMsg means a
// newer action, a later failure, or leaving the page invalidates a pending fade.
type startOutcomeFadeMsg struct {
	gen uint64
	row string
}

type outcomeFadeMsg struct {
	gen uint64
	row string
}

const outcomeFadeInterval = 3 * time.Second

type proxyActionKind uint8

const (
	proxyEnable proxyActionKind = iota
	proxyForceEnable
	proxyDisable
)

type tunActionKind uint8

const (
	tunEnable tunActionKind = iota
	tunForceEnable
	tunDisable
)

type actionStartMsg struct {
	kind        actionKind
	operationID string
	revision    uint64
	channel     string
	source      string
}

type actionResultMsg struct {
	kind    actionKind
	install protocol.CoreInstallResult
	restart protocol.MutationResult
	err     error
}

type coreLoadResultMsg struct {
	core protocol.CoreStatus
	err  error
}

// Err implements the shell's action-outcome contract so core update/restart
// actions are classified Succeeded/Failed in the Recent operations ledger.
func (m actionResultMsg) Err() error { return m.err }

var _ interface{ Err() error } = actionResultMsg{}

// Model is the System page.
type Model struct {
	ctx                 context.Context
	client              Client
	service             ServiceController
	openBrowser         func(string) error
	newOperationID      func() string
	selfUpdater         SelfUpdater
	currentVersion      string
	binaryPath          string
	isElevated          func() bool
	selfCheckResult     update.CheckResult
	selfCheckLoaded     bool
	selfCheckGeneration uint64
	channelPath         func() (string, error)
	loadChannel         func(string) (string, error)
	saveChannel         func(string, string) error
	mihariChannel       string
	mihariChannelLoaded bool
	mihariChannelFailed bool
	status              protocol.Status
	core                protocol.CoreStatus
	onboarding          protocol.OnboardingStatus
	systemProxy         protocol.SystemProxyStatus
	systemProxyLoaded   bool
	tun                 protocol.TunStatus
	tunLoaded           bool
	webGUI              protocol.WebGUIStatus
	webGUILoaded        bool
	webGUIErr           bool
	serviceStatus       service.StatusKind
	serviceLoaded       bool
	elevated            bool
	focusID             string
	detail              *row
	pending             bool
	pendingRow          string // row id showing in-row braille progress
	pendingNote         string // short status text next to the row (e.g. Installing)
	// Sticky outcome after an action finishes (cleared on page leave or re-run).
	outcomeRow       string
	outcomeOK        bool   // true=Done (green), false=Failed (red)
	outcomeDetail    string // failure reason (shown under title + next to Failed)
	rowSpinClock     time.Time
	rowSpinning      bool
	rowSpinGen       uint64
	outcomeFadeGen   uint64
	mutationsEnabled bool
	lastError        string
	contentFocused   bool
	width            int
	height           int
	scrollY          int // top visible section line; excludes error chrome
	theme            ui.Theme
	editID           string
	editInput        textinput.Model
	portHolds        map[string]ui.PortHold
	listenFree       func(string) bool
	lookupOccupant   func(string) (platform.TCPOccupant, bool)
}

// RestartRequiredMsg tells the root shell to show the existing restart dialog.
type RestartRequiredMsg struct{}

type portHoldsMsg struct {
	holds map[string]ui.PortHold
}

type portsApplyResultMsg struct {
	status protocol.OnboardingStatus
	err    error
}

func (m portsApplyResultMsg) Err() error { return m.err }

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
		openBrowser:    platform.OpenBrowser,
		newOperationID: newOperationID,
		focusID:        rowMixed,
		theme:          ui.DefaultTheme(),
		portHolds:      map[string]ui.PortHold{},
	}
}

func (m *Model) HelpMode() string {
	if m.editID != "" {
		return ui.ModePortsEdit
	}
	return ""
}

// FooterHints returns edit-mode shortcuts while a port row is being typed.
func (m *Model) FooterHints() string {
	return ui.RenderFooter(m.ID(), m.HelpMode(), ui.FooterOpt{})
}

// ApplyServiceStatus updates the OS service observation from the root shell poll
// so the System page Status line stays aligned with the top-right badge.
func (m *Model) ApplyServiceStatus(status service.StatusKind, loaded bool) {
	if m == nil {
		return
	}
	m.serviceLoaded = loaded
	if loaded {
		m.serviceStatus = status
	}
	m.ensureFocusVisible()
}

// SetServiceController injects or replaces the OS service controller.
func (m *Model) SetServiceController(svc ServiceController) {
	m.service = svc
}

// SetOpenBrowser injects the browser launcher (tests and headless environments).
func (m *Model) SetOpenBrowser(open func(string) error) {
	if open != nil {
		m.openBrowser = open
	}
}

// SetSelfUpdater configures local Mihari release checks and binary updates.
func (m *Model) SetSelfUpdater(updater SelfUpdater, currentVersion, binaryPath string, elevated func() bool) {
	m.selfUpdater = updater
	m.currentVersion = currentVersion
	m.binaryPath = binaryPath
	if elevated == nil {
		m.isElevated = elevate.IsElevated
	} else {
		m.isElevated = elevated
	}
}

// SetWebGUI injects Web GUI status (tests and optional external refresh).
func (m *Model) SetWebGUI(status protocol.WebGUIStatus) {
	m.webGUI = status
	m.webGUILoaded = true
	m.ensureFocusVisible()
}

func (m *Model) ID() ui.PageID { return ui.PageSystem }

// SetContentFocused reports whether the root shell has given keyboard focus to this page.
func (m *Model) SetContentFocused(focused bool) { m.contentFocused = focused }

func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
	m.ensureFocusVisible()
}

func (m *Model) layoutWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 100
}

func (m *Model) FocusFirst() {
	if m.rowIndex(m.focusID) < 0 {
		m.focusID = rowDaemon
	}
	m.ensureFocusVisible()
}

func (m *Model) SetSnapshot(status protocol.Status, core protocol.CoreStatus) {
	m.status, m.core = status, core
	m.ensureFocusVisible()
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

// ApplyRootNetworkStatus applies a completed root-level network poll so Network
// rows leave Loading… even when the page has not run Load() yet.
// A failed half of the poll marks that row settled without clearing a prior good snapshot.
func (m *Model) ApplyRootNetworkStatus(proxy protocol.SystemProxyStatus, proxyOK bool, tun protocol.TunStatus, tunOK bool) {
	if m == nil {
		return
	}
	if proxyOK {
		m.systemProxy = proxy
	}
	m.systemProxyLoaded = true
	if tunOK {
		m.tun = tun
	}
	m.tunLoaded = true
}

func (m *Model) SetMutationsEnabled(enabled bool) { m.mutationsEnabled = enabled }

// Load refreshes onboarding, OS service status, system proxy, and TUN when available.
func (m *Model) Load() tea.Cmd {
	return m.load(true)
}

func (m *Model) refresh() tea.Cmd {
	return m.load(false)
}

func (m *Model) load(checkMihari bool) tea.Cmd {
	var cmds []tea.Cmd
	if checkMihari && m.selfUpdater != nil && !m.pending {
		cmds = append(cmds, m.checkMihariVersion())
	}
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
	if m.client != nil && m.hasCapability(protocol.CapabilityWebGUI) {
		cmds = append(cmds, m.loadWebGUI())
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

func (m *Model) checkMihariVersion() tea.Cmd {
	if m.selfUpdater == nil || m.pending {
		return nil
	}
	path, err := m.channelFilePath()
	if err != nil {
		m.mihariChannelFailed = true
		m.markRowOutcome(rowMihariChannel, false, actionErrorDetail(err, ui.MihariChannelFailed))
		return nil
	}
	channel, err := m.readChannel(path)
	if err != nil {
		m.mihariChannelFailed = true
		m.markRowOutcome(rowMihariChannel, false, actionErrorDetail(err, ui.MihariChannelFailed))
		return nil
	}
	m.mihariChannel = channel
	m.mihariChannelLoaded = true
	m.mihariChannelFailed = false
	m.selfCheckGeneration++
	generation := m.selfCheckGeneration
	m.pending = true
	m.pendingRow = rowMihariUpdate
	m.pendingNote = ui.MihariProgressChecking
	if m.outcomeRow == rowMihariUpdate {
		m.outcomeRow = ""
		m.outcomeOK = false
		m.outcomeDetail = ""
		m.lastError = ""
	}
	check := func() tea.Msg {
		result, err := m.selfUpdater.Check(m.ctx, m.currentVersion, channel)
		return ui.PageResultMsg{
			Page:   ui.PageSystem,
			Result: selfCheckResultMsg{generation: generation, result: result, err: err},
		}
	}
	return tea.Batch(check, m.rowSpinCmdIfNeeded())
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

func (m *Model) loadWebGUI() tea.Cmd {
	return func() tea.Msg {
		status, err := m.client.WebGUI(m.ctx)
		return webGUIStatusMsg{status: status, err: err}
	}
}

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	defer func() {
		if m.detail != nil {
			return
		}
		m.ensureFocusVisible()
	}()
	switch typed := message.(type) {
	case selfCheckResultMsg:
		if typed.generation != m.selfCheckGeneration {
			return m, nil
		}
		m.clearRowPending()
		if typed.err != nil {
			m.selfCheckLoaded = false
			m.markRowOutcome(rowMihariUpdate, false, actionErrorDetail(typed.err, ui.UpdateMihariCheckFailed))
			return m, m.rowSpinCmdIfNeeded()
		}
		m.selfCheckResult = typed.result
		m.selfCheckLoaded = true
		m.outcomeRow = ""
		m.outcomeDetail = ""
		m.lastError = ""
		return m, m.rowSpinCmdIfNeeded()
	case mihariChannelResultMsg:
		m.clearRowPending()
		if typed.err != nil {
			m.markRowOutcome(rowMihariChannel, false, actionErrorDetail(typed.err, ui.MihariChannelFailed))
			return m, m.rowSpinCmdIfNeeded()
		}
		m.mihariChannel = typed.channel
		m.mihariChannelLoaded = true
		m.mihariChannelFailed = false
		m.markRowOutcome(rowMihariChannel, true, "")
		return m, tea.Batch(m.checkMihariVersion(), m.rowSpinCmdIfNeeded())
	case selfUpdateResultMsg:
		m.clearRowPending()
		if !typed.result.Updated {
			if typed.err != nil {
				m.markRowOutcome(rowMihariUpdate, false, actionErrorDetail(typed.err, ui.UpdateMihariActionFailed))
				return m, m.rowSpinCmdIfNeeded()
			}
			m.selfCheckResult = update.CheckResult{
				Current:   m.currentVersion,
				Latest:    typed.result.Version,
				Available: false,
				Ahead:     typed.result.Ahead,
				Channel:   typed.result.Channel,
			}
			m.selfCheckLoaded = true
			m.outcomeRow = ""
			return m, m.rowSpinCmdIfNeeded()
		}
		m.markRowOutcome(rowMihariUpdate, true, "")
		warning := ""
		if typed.err != nil {
			warning = actionErrorDetail(typed.err, ui.UpdateMihariActionFailed)
		}
		return m, tea.Batch(func() tea.Msg { return ui.RelaunchRequestMsg{Warning: warning} }, m.rowSpinCmdIfNeeded())
	case onboardingResultMsg:
		if typed.err != nil {
			m.lastError = ui.SystemStateUnavailable
		} else {
			m.lastError = ""
			m.onboarding = typed.status
		}
		return m, m.probePortHolds()
	case portHoldsMsg:
		m.portHolds = typed.holds
		return m, nil
	case portsApplyResultMsg:
		m.clearRowPending()
		if typed.err != nil {
			m.markRowOutcome(m.focusID, false, ui.PortsApplyFailed)
			return m, m.rowSpinCmdIfNeeded()
		}
		m.onboarding = typed.status
		m.markRowOutcome(m.focusID, true, "")
		cmds := []tea.Cmd{m.probePortHolds(), m.rowSpinCmdIfNeeded()}
		if typed.status.RestartRequired {
			cmds = append(cmds, func() tea.Msg { return RestartRequiredMsg{} })
		}
		return m, tea.Batch(cmds...)
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
	case webGUIStatusMsg:
		m.webGUILoaded = true
		if typed.err != nil {
			m.webGUIErr = true
			if m.lastError == "" {
				m.lastError = ui.WebGUIUnavailable
			}
			return m, nil
		}
		m.webGUIErr = false
		m.webGUI = typed.status
		return m, nil
	case webGUIOpenResultMsg:
		if typed.err == nil {
			// Browser open is silent on success; no sticky Done badge.
			return m, nil
		}
		m.markRowOutcome(typed.rowID, false, actionErrorDetail(typed.err, ui.WebGUIUnavailable))
		return m, nil
	case ui.CoreObservedMsg:
		m.core = typed.Core
		return m, nil
	case coreLoadResultMsg:
		if typed.err == nil {
			m.core = typed.core
		}
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
			return ui.PageResultMsg{Page: ui.PageSystem, Result: rowSpinTickMsg{t: t, gen: typed.gen}}
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
			return ui.PageResultMsg{Page: ui.PageSystem, Result: rowSpinTickMsg{t: t, gen: typed.gen}}
		})
	case startOutcomeFadeMsg:
		return m, tea.Tick(outcomeFadeInterval, func(time.Time) tea.Msg {
			// outcomeFadeMsg has the same shape as startOutcomeFadeMsg; convert
			// rather than rebuild the literal (staticcheck S1016).
			return ui.PageResultMsg{Page: ui.PageSystem, Result: outcomeFadeMsg(typed)}
		})
	case outcomeFadeMsg:
		// Only clear a still-current successful outcome; a newer action, a later
		// failure, or leaving the page must keep its badge.
		if typed.gen != m.outcomeFadeGen || m.outcomeRow != typed.row || !m.outcomeOK {
			return m, nil
		}
		m.outcomeRow = ""
		m.outcomeOK = false
		m.outcomeDetail = ""
		return m, nil
	case actionStartMsg:
		if m.pending {
			return m, nil
		}
		m.pending = true
		return m, m.runAction(typed)
	case actionResultMsg:
		rowID := m.outcomeRowID(coreRowForKind(typed.kind))
		m.clearRowPending()
		if typed.err != nil {
			var apiError protocol.APIError
			if errors.As(typed.err, &apiError) && apiError.Code == protocol.CodeRevisionConflict {
				m.markRowOutcome(rowID, false, ui.SystemChangedMessage)
				return m, tea.Batch(m.refresh(), m.loadCore(), m.rowSpinCmdIfNeeded())
			}
			m.markRowOutcome(rowID, false, actionErrorDetail(typed.err, ui.SystemActionFailed))
			return m, m.rowSpinCmdIfNeeded()
		}
		m.markRowOutcome(rowID, true, "")
		revision := typed.restart.Revision
		if typed.kind == actionUpdate || typed.kind == actionSwitchChannel {
			revision = typed.install.Revision
		}
		return m, tea.Batch(m.refresh(), m.loadCore(), func() tea.Msg { return ui.RuntimeRevisionMsg{Revision: revision} }, m.rowSpinCmdIfNeeded())
	case serviceResultMsg:
		rowID := m.outcomeRowID(serviceRowForKind(typed.kind))
		m.clearRowPending()
		if typed.err != nil {
			m.markRowOutcome(rowID, false, actionErrorDetail(typed.err, ui.ServiceActionFailed))
			return m, tea.Batch(m.loadServiceStatus(), m.rowSpinCmdIfNeeded())
		}
		m.markRowOutcome(rowID, true, "")
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
	if m.editID != "" {
		return m.updatePortEdit(message)
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
		if m.pending {
			return m, nil
		}
		switch m.focusID {
		case rowZashboard:
			return m, m.openPanelBrowser(panelIDZashboard)
		case rowMetaCubeXD:
			return m, m.openPanelBrowser(panelIDMetaCubeXD)
		case rowMihariChannel:
			return m, m.confirmSwitchMihariChannel()
		case rowMihariUpdate:
			if m.pending || m.selfUpdater == nil {
				return m, nil
			}
			if m.selfCheckLoaded && m.selfCheckResult.Available {
				return m, m.confirmMihariUpdate()
			}
			return m, m.checkMihariVersion()
		case rowRunSetup:
			if m.client == nil || !m.mutationsEnabled || !m.hasCapability(protocol.CapabilityOnboarding) {
				return m, nil
			}
			return m, func() tea.Msg { return ui.RouteRequestMsg{Page: ui.PageSetup} }
		case rowCoreChannel:
			if m.client == nil || !m.mutationsEnabled || !m.hasCapability(protocol.CapabilityCore) {
				return m, nil
			}
			return m, m.confirmSwitchCoreChannel(otherCoreChannel(m.core.Channel))
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
		case rowSystemProxyAction:
			return m, m.confirmSystemProxyToggle()
		case rowTUNAction:
			return m, m.confirmTunToggle()
		case rowGitHub:
			return m, m.openGitHub()
		case rowMixed, rowController, rowWeb:
			return m, m.beginPortEdit(m.focusID)
		default:
			selected := rows[index]
			m.detail = &selected
		}
	}
	return m, nil
}

func (m *Model) confirmMihariUpdate() tea.Cmd {
	current := valueOr(m.currentVersion, ui.UnknownLabel)
	latest := valueOr(m.selfCheckResult.Latest, ui.UnknownLabel)
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionUpdateMihari, Page: ui.PageSystem, Key: "mihari:update",
			Title: ui.UpdateMihariTitle, Object: fmt.Sprintf("Mihari %s → %s", current, latest),
			Impact: ui.UpdateMihariImpact, Rollback: ui.UpdateMihariRollback,
			Execute: m.updateMihari(),
		}
	}
}

func (m *Model) updateMihari() tea.Cmd {
	updater := m.selfUpdater
	binaryPath := m.binaryPath
	currentVersion := m.currentVersion
	channel := m.currentMihariChannel()
	isElevated := m.isElevated
	return func() tea.Msg {
		if isElevated == nil || !isElevated() {
			return selfUpdateResultMsg{err: protocol.APIError{
				Code:    protocol.CodePermissionDenied,
				Message: "administrator privileges are required; re-run Mihari from an elevated shell",
			}}
		}
		result, err := updater.Update(m.ctx, binaryPath, currentVersion, channel)
		return selfUpdateResultMsg{result: result, err: err}
	}
}

func (m *Model) handleSystemProxyActionResult(typed systemProxyActionResultMsg) (ui.Page, tea.Cmd) {
	rowID := m.outcomeRowID(rowSystemProxyAction)
	m.clearRowPending()
	if typed.err != nil {
		var apiError protocol.APIError
		if errors.As(typed.err, &apiError) {
			switch apiError.Code {
			case protocol.CodeSystemProxyConflict:
				// Secondary confirm is not a terminal failure; keep waiting for force path.
				return m, tea.Batch(m.confirmForceSystemProxy(apiError), m.rowSpinCmdIfNeeded())
			case protocol.CodeSystemProxyNotOwned:
				// Fixed copy: never imply Mihari will clear a foreign proxy.
				m.markRowOutcome(rowID, false, ui.SystemProxyNotOwnedMessage)
				return m, tea.Batch(m.loadSystemProxy(), m.rowSpinCmdIfNeeded())
			case protocol.CodeRevisionConflict:
				m.markRowOutcome(rowID, false, ui.SystemChangedMessage)
				return m, tea.Batch(m.refresh(), m.rowSpinCmdIfNeeded())
			}
		}
		m.markRowOutcome(rowID, false, actionErrorDetail(typed.err, ui.SystemProxyActionFailed))
		return m, tea.Batch(m.loadSystemProxy(), m.rowSpinCmdIfNeeded())
	}
	m.markRowOutcome(rowID, true, "")
	m.systemProxy = typed.status
	m.systemProxyLoaded = true
	return m, tea.Batch(m.loadSystemProxy(), func() tea.Msg {
		return ui.RuntimeRevisionMsg{Revision: typed.status.Revision}
	}, m.rowSpinCmdIfNeeded(), m.scheduleOutcomeFade(rowID))
}

func (m *Model) handleTunActionResult(typed tunActionResultMsg) (ui.Page, tea.Cmd) {
	rowID := m.outcomeRowID(rowTUNAction)
	m.clearRowPending()
	if typed.err != nil {
		var apiError protocol.APIError
		if errors.As(typed.err, &apiError) {
			switch apiError.Code {
			case protocol.CodeTunConflict:
				// 二次确认非终态失败；保持等待 force 路径。
				return m, tea.Batch(m.confirmForceTun(apiError), m.rowSpinCmdIfNeeded())
			case protocol.CodeRevisionConflict:
				m.markRowOutcome(rowID, false, ui.SystemChangedMessage)
				return m, tea.Batch(m.refresh(), m.rowSpinCmdIfNeeded())
			}
		}
		m.markRowOutcome(rowID, false, actionErrorDetail(typed.err, ui.TunActionFailed))
		return m, tea.Batch(m.loadTun(), m.rowSpinCmdIfNeeded())
	}
	m.markRowOutcome(rowID, true, "")
	m.tun = typed.status
	m.tunLoaded = true
	return m, tea.Batch(m.loadTun(), func() tea.Msg {
		return ui.RuntimeRevisionMsg{Revision: typed.status.Revision}
	}, m.rowSpinCmdIfNeeded(), m.scheduleOutcomeFade(rowID))
}

func (m *Model) View() string {
	if m.detail != nil {
		return m.theme.Content.Width(m.width).Height(m.height).Render(
			m.theme.Title.Render(strings.TrimSpace(m.detail.label)+" details") + "\n\n" + m.detail.detail + "\n\n" + ui.EscCloseHint,
		)
	}
	errorLines := m.errorChromeLines()
	sectionLines, _, _ := m.buildSectionContent()
	if m.height <= 0 {
		return strings.Join(append(append([]string{}, errorLines...), sectionLines...), "\n")
	}
	if len(errorLines) >= m.height {
		return strings.Join(errorLines[:m.height], "\n")
	}
	avail := m.height - len(errorLines)
	return strings.Join(append(errorLines, ui.SliceLines(sectionLines, m.scrollY, avail)...), "\n")
}

func (m *Model) buildSectionContent() (lines []string, focusStart, focusEnd int) {
	focusStart, focusEnd = -1, -1
	inner := ui.FullSectionInner(m.layoutWidth())
	clock := m.rowSpinClock
	if clock.IsZero() {
		clock = time.Unix(0, 0)
	}
	type sectionBuf struct {
		title        string
		body         []string
		focusIndex   int
		focusIsFirst bool
		focusLines   int
	}
	var sections []sectionBuf
	for _, item := range m.rows() {
		if len(sections) == 0 || sections[len(sections)-1].title != item.section {
			sections = append(sections, sectionBuf{title: item.section, focusIndex: -1})
		}
		idx := len(sections) - 1
		marker := "  "
		rowFocused := item.id == m.focusID
		if rowFocused {
			marker = ui.FocusMarker
		}
		labelPart := marker + item.label
		value := item.value
		if m.editID == item.id {
			value = m.editInput.View()
		}
		switch {
		case m.pending && m.pendingRow == item.id && m.pendingNote != "":
			value = ui.RenderStatusChip(m.theme, ui.StatusChipPending, ui.SpinnerLabel(clock, m.pendingNote))
		case m.outcomeRow == item.id:
			if m.outcomeOK {
				value = ui.RenderStatusChip(m.theme, ui.StatusChipDone, ui.DoneLabel)
			} else {
				value = ui.RenderStatusChip(m.theme, ui.StatusChipFailed, ui.FailedLabel)
				if m.outcomeDetail != "" {
					value += "  " + m.theme.Danger.Render(truncateRunes(m.outcomeDetail, 48))
				}
			}
		}
		if value != "" {
			value = "  " + value
		}
		if rowFocused && m.contentFocused && m.editID == "" {
			labelPart = ui.ApplyFocusStyle(labelPart, m.theme.RowFocus)
		}
		rowLines := strings.Split(labelPart+value, "\n")
		if item.id == m.focusID {
			sections[idx].focusIndex = len(sections[idx].body)
			sections[idx].focusIsFirst = sections[idx].focusIndex == 0
			sections[idx].focusLines = len(rowLines)
		}
		sections[idx].body = append(sections[idx].body, rowLines...)
	}
	for _, sec := range sections {
		body := strings.Join(sec.body, "\n")
		if body == "" {
			body = " "
		}
		section := ui.RenderBorderedSection(m.theme, sec.title, body, inner)
		sectionLines := strings.Split(section, "\n")
		sectionBase := len(lines)
		lines = append(lines, sectionLines...)
		if sec.focusIndex >= 0 {
			bodyOffset := sectionBase + 1
			if sec.focusIsFirst {
				focusStart = sectionBase
				focusEnd = bodyOffset + sec.focusLines
			} else {
				focusStart = bodyOffset + sec.focusIndex
				focusEnd = focusStart + sec.focusLines
			}
		}
	}
	return lines, focusStart, focusEnd
}

func (m *Model) rows() []row {
	configState := ui.UnavailableTitle
	if m.status.Config != nil {
		configState = fmt.Sprintf("%s · desired %d / observed %d", m.status.Config.Status, m.status.Config.DesiredRevision, m.status.Config.ObservedRevision)
	}
	daemon := fmt.Sprintf("Version %s\nUptime %s\nHealth %s\nRevision %d\nConfig %s", valueOr(m.status.DaemonVersion, ui.UnknownLabel), uptime(m.status.StartedAt), valueOr(m.status.Health, ui.UnknownLabel), m.status.Revision, configState)
	core := fmt.Sprintf("Status %s\nVersion %s\nPID %d\nRestarts %d", valueOr(m.core.Status, ui.UnknownLabel), valueOr(m.core.Version, ui.UnknownLabel), m.core.PID, m.core.Restarts)
	rows := m.portRows()
	rows = append(rows, row{id: rowDaemon, section: ui.DaemonSectionTitle, label: ui.DaemonLabel, value: daemonValue(m.theme, m.status, !m.mutationsEnabled), detail: daemon})
	rows = append(rows, m.panelRows()...)
	rows = append(rows, m.mihariChannelRow())
	rows = append(rows, m.mihariUpdateRow())
	rows = append(rows,
		row{id: rowRunSetup, section: ui.DaemonSectionTitle, label: ui.RunSetupLabel, detail: ui.RunSetupDetail},
		row{id: rowCore, section: ui.CoreSectionTitle, label: ui.MihomoCoreLabel, value: coreValue(m.theme, m.core, !m.mutationsEnabled), detail: core},
		row{id: rowCoreChannel, section: ui.CoreSectionTitle, label: ui.CoreChannelLabel, value: coreChannelName(m.core.Channel), detail: ui.SwitchCoreChannelImpact},
		row{id: rowCoreUpdate, section: ui.CoreSectionTitle, label: m.coreActionLabel(), value: actionState(m.hasCapability(protocol.CapabilityCore), m.mutationsEnabled), detail: ui.UpdateCoreImpact},
		row{id: rowCoreRestart, section: ui.CoreSectionTitle, label: ui.RestartCoreLabel, value: actionState(m.hasCapability(protocol.CapabilityCore), m.mutationsEnabled), detail: ui.RestartCoreImpact},
	)
	rows = append(rows, m.serviceRows()...)
	rows = append(rows, m.networkRows()...)
	rows = append(rows, m.aboutRows()...)
	return rows
}

func (m *Model) aboutRows() []row {
	section := ui.AboutSectionTitle
	return []row{
		{
			id: rowAbout, section: section,
			label: ui.AboutNameLabel, value: ui.AboutDescriptionValue,
			detail: ui.AboutDescriptionDetail,
		},
		{
			id: rowGitHub, section: section,
			label: ui.AboutGitHubLabel, value: ui.AboutGitHubDisplay,
			detail: ui.AboutGitHubDisplay,
		},
	}
}

func (m *Model) mihariChannelRow() row {
	m.ensureChannelLoaded()
	value := update.ChannelMain
	if m.mihariChannelLoaded && m.mihariChannel != "" {
		value = m.mihariChannel
	}
	return row{
		id: rowMihariChannel, section: ui.DaemonSectionTitle,
		label: ui.MihariChannelLabel, value: value, detail: ui.SwitchMihariChannelTitle,
	}
}

func (m *Model) mihariUpdateRow() row {
	current := valueOr(m.currentVersion, ui.UnknownLabel)
	value := current + " · " + ui.UnavailableTitle
	if m.selfCheckLoaded {
		latest := valueOr(m.selfCheckResult.Latest, ui.UnknownLabel)
		switch {
		case m.selfCheckResult.Available:
			value = current + " · " + latest + " " + ui.UpdateMihariAvailable
		case m.selfCheckResult.Ahead:
			channel := valueOr(m.selfCheckResult.Channel, m.currentMihariChannel())
			value = current + " · " + fmt.Sprintf(ui.UpdateMihariAhead, channel, latest)
		default:
			value = current + " · " + ui.UpdateMihariUpToDate
		}
	}
	return row{
		id: rowMihariUpdate, section: ui.DaemonSectionTitle,
		label: ui.UpdateMihariLabel, value: value, detail: ui.UpdateMihariImpact,
	}
}

func (m *Model) portRows() []row {
	return []row{
		m.portRow(rowMixed, ui.MixedLabel, m.onboarding.MixedAddr, m.core.PID),
		m.portRow(rowController, ui.ControllerPortLabel, m.onboarding.ControllerAddr, m.core.PID),
		m.portRow(rowWeb, ui.WebPortLabel, m.onboarding.WebAddr, m.status.PID),
	}
}

func (m *Model) portRow(id, label, addr string, ownerPID int) row {
	hold := m.portHolds[id]
	status := ui.RenderPortHold(m.theme, hold)
	value := valueOr(addr, ui.MissingValue)
	if status != "" {
		value += "  " + status
	}
	detail := fmt.Sprintf("%s\n%s", valueOr(addr, ui.MissingValue), ui.FormatPortHoldLabel(hold))
	if hold.PID > 0 {
		detail += fmt.Sprintf("\nHolder PID %d", hold.PID)
	}
	_ = ownerPID
	return row{id: id, section: ui.PortsConfigSectionTitle, label: padEndpointLabel(label), value: value, detail: detail}
}

// panelRows renders installed Web GUI panel rows (open browser).
func (m *Model) panelRows() []row {
	if !m.hasCapability(protocol.CapabilityWebGUI) {
		return nil
	}
	var rows []row
	webAddr := m.onboarding.WebAddr
	if panelRow, ok := m.panelEndpointRow(panelIDZashboard, ui.ZashboardLabel, webAddr); ok {
		rows = append(rows, panelRow)
	}
	if panelRow, ok := m.panelEndpointRow(panelIDMetaCubeXD, ui.MetaCubeXDLabel, webAddr); ok {
		rows = append(rows, panelRow)
	}
	return rows
}

// panelEndpointRow builds one Web GUI panel endpoint row. A settled
// not-installed state hides the row (user decision); unknown state shows a
// Loading…/Unavailable placeholder instead.
func (m *Model) panelEndpointRow(id, label, webAddr string) (row, bool) {
	if m.webGUILoaded && !m.webGUIErr && !webGUIPanelInstalled(m.webGUI, id) {
		return row{}, false
	}
	var value string
	switch {
	case m.webGUIErr:
		value = ui.UnavailableTitle
	case !m.webGUILoaded:
		value = ui.LoadingLabel
	default:
		value = ui.TruncateVisible(valueOr(panelURL(webAddr, id), ui.MissingValue), 48)
	}
	return row{
		id: id, section: ui.DaemonSectionTitle, label: padEndpointLabel(label),
		value: value, detail: ui.PanelOpenDetail,
	}, true
}

// webGUIPanelInstalled reports whether the panel has a local build, using the
// same source of truth as the Web GUI page uninstall guard.
func webGUIPanelInstalled(status protocol.WebGUIStatus, id string) bool {
	for _, panel := range status.Panels {
		if panel.ID == id {
			return panel.InstalledBuild != "" || panel.Active
		}
	}
	return false
}

// panelURL builds the token-free mount URL for a panel at a Web GUI gateway
// address. The path anchors panel.UIPathPrefix (internal/panel/static_root.go);
// local assembly keeps the panel package out of the presentation layer.
func panelURL(webAddr, panelID string) string {
	addr := strings.TrimSpace(webAddr)
	if addr == "" {
		return addr
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	return strings.TrimRight(addr, "/") + "/__mihari/panels/" + panelID + "/"
}

// padEndpointLabel aligns endpoint row labels so values start at a fixed
// column; "Mihomo Core API" is the widest label at 15 runes.
func padEndpointLabel(label string) string {
	return fmt.Sprintf("%-15s", label)
}

func (m *Model) networkRows() []row {
	section := ui.NetworkSectionTitle
	var rows []row
	if m.hasCapability(protocol.CapabilitySystemProxy) {
		// Status row shows observed state. It is never overlaid by the
		// pending/outcome chips (those bind the action row below), so the live
		// status stays visible even right after a toggle.
		value := ui.LoadingLabel
		if m.systemProxyLoaded {
			value = systemProxySummary(m.theme, m.systemProxy)
		}
		if !m.mutationsEnabled {
			if m.systemProxyLoaded {
				value = systemProxySummary(m.theme, m.systemProxy) + " · " + ui.StaleLabel
			} else {
				value = ui.StaleLabel
			}
		}
		rows = append(rows, row{
			id: rowSystemProxy, section: section, label: ui.SystemProxyLabel,
			value: value, detail: systemProxyDetail(m.systemProxy),
		})
		// Action row carries the toggle verb; its badge (pending/Done/Failed)
		// binds here via rowProgressForAction / outcomeRowID.
		impact := ui.EnableSystemProxyImpact
		if m.systemProxy.Desired || m.systemProxy.Observed.Owned {
			impact = ui.DisableSystemProxyImpact
		}
		rows = append(rows, row{
			id: rowSystemProxyAction, section: section, label: m.systemProxyActionLabel(),
			value:  actionState(m.hasCapability(protocol.CapabilitySystemProxy), m.mutationsEnabled),
			detail: impact,
		})
	}
	// TUN status row is always listed; live status when capability is present.
	tunValue := ui.UnavailableTitle
	tunDetail := ui.TUNUnavailableDetail
	if m.hasCapability(protocol.CapabilityTUN) {
		tunValue = ui.LoadingLabel
		tunDetail = tunDetailText(m.tun)
		if m.tunLoaded {
			tunValue = tunSummary(m.theme, m.tun)
		}
		if !m.mutationsEnabled {
			if m.tunLoaded {
				tunValue = tunSummary(m.theme, m.tun) + " · " + ui.StaleLabel
			} else {
				tunValue = ui.StaleLabel
			}
		}
	}
	rows = append(rows, row{
		id: rowTUN, section: section, label: ui.TUNLabel,
		value: tunValue, detail: tunDetail,
	})
	if m.hasCapability(protocol.CapabilityTUN) {
		tunImpact := ui.EnableTunImpact
		if m.tun.DesiredEnable {
			tunImpact = ui.DisableTunImpact
		}
		rows = append(rows, row{
			id: rowTUNAction, section: section, label: m.tunActionLabel(),
			value:  actionState(m.hasCapability(protocol.CapabilityTUN), m.mutationsEnabled),
			detail: tunImpact,
		})
	}
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
	rows := []row{
		{id: rowServiceStatus, section: section, label: ui.ServiceStatusLabel, value: ui.StatusDot(m.theme, ui.ClassifyStatusTone(statusValue), statusValue) + "  " + m.theme.Muted.Render("· "+privilege), detail: statusDetail},
	}
	if !m.elevated {
		// Fold all six action rows into one elevation hint (design SY1).
		rows = append(rows, row{
			id: rowServiceHint, section: section,
			value:  "(" + ui.ServiceNeedsElevation + ")",
			detail: ui.ServiceElevationRequired,
		})
		return rows
	}
	// Elevated: render only the actions that apply right now (design SY1 —
	// unavailable actions are not shown instead of showing "Unavailable").
	actions := []struct {
		id     string
		kind   serviceActionKind
		label  string
		impact string
	}{
		{rowServiceInstall, serviceInstall, ui.ServiceInstallLabel, ui.ServiceInstallImpact},
		{rowServiceUninstall, serviceUninstall, ui.ServiceUninstallLabel, ui.ServiceUninstallImpact},
		{rowServiceReinstall, serviceReinstall, ui.ServiceReinstallLabel, ui.ServiceReinstallImpact},
		{rowServiceStart, serviceStart, ui.ServiceStartLabel, ui.ServiceStartImpact},
		{rowServiceStop, serviceStop, ui.ServiceStopLabel, ui.ServiceStopImpact},
		{rowServiceRestart, serviceRestart, ui.ServiceRestartLabel, ui.ServiceRestartImpact},
	}
	for _, action := range actions {
		if !m.serviceActionAllowed(action.kind) {
			continue
		}
		rows = append(rows, row{
			id: action.id, section: section, label: action.label,
			value: m.serviceActionState(action.kind), detail: action.impact,
		})
	}
	return rows
}

func (m *Model) serviceActionState(kind serviceActionKind) string {
	if m.service == nil {
		return ui.UnavailableTitle
	}
	// Do not mirror global pending here; View paints a chip only on pendingRow.
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
	// New run replaces any sticky Done/Failed on this page.
	m.outcomeRow = ""
	m.outcomeOK = false
	m.outcomeDetail = ""
	m.lastError = ""
}

func (m *Model) clearRowPending() {
	m.pending = false
	m.pendingRow = ""
	m.pendingNote = ""
}

// outcomeRowID prefers the in-flight pending row, then an explicit fallback, then focus.
func (m *Model) outcomeRowID(fallback string) string {
	if m.pendingRow != "" {
		return m.pendingRow
	}
	if fallback != "" {
		return fallback
	}
	return m.focusID
}

func (m *Model) markRowOutcome(rowID string, ok bool, detail string) {
	defer m.ensureFocusVisible()
	if rowID == "" {
		return
	}
	m.outcomeRow = rowID
	m.outcomeOK = ok
	if ok {
		m.outcomeDetail = ""
		m.lastError = ""
		return
	}
	m.outcomeDetail = strings.TrimSpace(detail)
	m.lastError = m.outcomeDetail
}

// ClearDone drops sticky Done/Failed badges (call when leaving the System page).
func (m *Model) ClearDone() {
	m.outcomeRow = ""
	m.outcomeOK = false
	m.outcomeDetail = ""
	m.lastError = ""
}

func (m *Model) visibleErrorDetail() string {
	if m.outcomeRow != "" && !m.outcomeOK && m.outcomeDetail != "" {
		return m.outcomeDetail
	}
	return strings.TrimSpace(m.lastError)
}

func (m *Model) errorChromeLines() []string {
	detail := strings.TrimSpace(m.visibleErrorDetail())
	if detail == "" {
		return nil
	}
	detail = strings.ReplaceAll(detail, "\n", " ")
	rendered := m.theme.Danger.Render(detail)
	if m.width > 0 {
		rendered = lipgloss.NewStyle().MaxWidth(max(1, m.width-2)).Render(rendered)
	}
	if i := strings.Index(rendered, "\n"); i >= 0 {
		rendered = rendered[:i]
	}
	return []string{rendered}
}

func (m *Model) ensureFocusVisible() {
	lines, focusStart, focusEnd := m.buildSectionContent()
	avail := m.height - len(m.errorChromeLines())
	m.scrollY = ui.EnsureLineVisible(m.scrollY, avail, len(lines), focusStart, focusEnd)
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if max <= 0 || len(runes) <= max {
		return value
	}
	if max == 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

func serviceRowForKind(kind serviceActionKind) string {
	switch kind {
	case serviceInstall:
		return rowServiceInstall
	case serviceUninstall:
		return rowServiceUninstall
	case serviceReinstall:
		return rowServiceReinstall
	case serviceStart:
		return rowServiceStart
	case serviceStop:
		return rowServiceStop
	case serviceRestart:
		return rowServiceRestart
	default:
		return ""
	}
}

func coreRowForKind(kind actionKind) string {
	switch kind {
	case actionUpdate:
		return rowCoreUpdate
	case actionRestart:
		return rowCoreRestart
	case actionSwitchChannel:
		return rowCoreChannel
	default:
		return ""
	}
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
	return func() tea.Msg {
		return ui.PageResultMsg{Page: ui.PageSystem, Result: startRowSpinMsg{gen: gen}}
	}
}

// scheduleOutcomeFade arms a one-shot timer that clears a successful outcome
// badge after outcomeFadeInterval, so the live status row (not a stale Done)
// carries the result. Called only on the success path; failures stay sticky.
func (m *Model) scheduleOutcomeFade(row string) tea.Cmd {
	m.outcomeFadeGen++
	gen := m.outcomeFadeGen
	return func() tea.Msg {
		return ui.PageResultMsg{Page: ui.PageSystem, Result: startOutcomeFadeMsg{gen: gen, row: row}}
	}
}

func rowProgressForAction(action ui.Action, coreMissing bool) (rowID, note string) {
	switch action {
	case ui.ActionSwitchMihariChannel:
		return rowMihariChannel, ui.MihariProgressSwitching
	case ui.ActionUpdateMihari:
		return rowMihariUpdate, ui.MihariProgressUpdating
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
	case ui.ActionSwitchCoreChannel:
		return rowCoreChannel, ui.CoreProgressSwitching
	case ui.ActionRestartCore:
		return rowCoreRestart, ui.CoreProgressRestarting
	case ui.ActionEnableSystemProxy, ui.ActionForceSystemProxy:
		return rowSystemProxyAction, ui.ProxyProgressEnabling
	case ui.ActionDisableSystemProxy:
		return rowSystemProxyAction, ui.ProxyProgressDisabling
	case ui.ActionEnableTun, ui.ActionForceTun:
		return rowTUNAction, ui.TunProgressEnabling
	case ui.ActionDisableTun:
		return rowTUNAction, ui.TunProgressDisabling
	default:
		return "", ""
	}
}

// actionErrorDetail prefers a redacted API message, else the domain fallback.
func actionErrorDetail(err error, fallback string) string {
	var apiError protocol.APIError
	if errors.As(err, &apiError) {
		if msg := strings.TrimSpace(apiError.Message); msg != "" {
			return msg
		}
	}
	return fallback
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
	// 前置：status 已携带其他 TUN 网卡证据（信号 A）且当前非 enable → 直接走 force 确认。
	if !m.tun.DesiredEnable && m.tun.Conflict != nil && len(m.tun.Conflict.OtherTunInterfaces) > 0 {
		return m.confirmForceTunFromStatus()
	}
	if m.tun.DesiredEnable {
		return m.confirmTunAction(tunDisable)
	}
	return m.confirmTunAction(tunEnable)
}

func (m *Model) confirmTunAction(kind tunActionKind) tea.Cmd {
	revision, operationID := m.tunRevision(), m.newOperationID()
	title, impact, rollback, action := ui.EnableTunTitle, ui.EnableTunImpact, ui.EnableTunRollback, ui.ActionEnableTun
	force := false
	switch kind {
	case tunForceEnable:
		title, impact, rollback, action = ui.ForceTunTitle, forceTunImpactFromStatus(m.tun), ui.ForceTunRollback, ui.ActionForceTun
		force = true
	case tunDisable:
		title, impact, rollback, action = ui.DisableTunTitle, ui.DisableTunImpact, ui.DisableTunRollback, ui.ActionDisableTun
	}
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: action, Page: ui.PageSystem, Capability: protocol.CapabilityTUN,
			Key: "system:" + string(action), Title: title, Object: ui.TUNLabel,
			Impact: impact, Rollback: rollback,
			Execute: m.runTunAction(kind, operationID, revision, force),
		}
	}
}

func (m *Model) confirmForceTunFromStatus() tea.Cmd {
	return m.confirmTunAction(tunForceEnable)
}

// confirmForceTun 从后置 CodeTunConflict 错误的 Details 取证据构造分情形 force 确认
// （对称 confirmForceSystemProxy；与 disable 不对称——TUN disable 永不门控，见设计决策 1）。
func (m *Model) confirmForceTun(apiError protocol.APIError) tea.Cmd {
	impact := forceTunImpactFromDetails(apiError.Details)
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionForceTun, Page: ui.PageSystem, Capability: protocol.CapabilityTUN,
			Key: "system:" + string(ui.ActionForceTun), Title: ui.ForceTunTitle, Object: ui.TUNLabel,
			Impact: impact, Rollback: ui.ForceTunRollback,
			Execute: m.runTunAction(tunForceEnable, m.newOperationID(), m.tunRevision(), true),
		}
	}
}

func (m *Model) runTunAction(kind tunActionKind, operationID string, revision uint64, force bool) tea.Cmd {
	return func() tea.Msg {
		request := protocol.TunMutationRequest{OperationID: operationID, Force: force}
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

// openPanelBrowser returns the no-confirm open-browser intent for an installed
// panel row. RequiresDaemon gating (mutationsEnabled) is applied by the root
// shell; the one-shot token stays inside Execute — the view only ever renders
// token-free mount URLs (webgui page constraint).
func (m *Model) openPanelBrowser(panelID string) tea.Cmd {
	if m.client == nil || !m.mutationsEnabled || !m.hasCapability(protocol.CapabilityWebGUI) {
		return nil
	}
	// Rows only render when installed; do not open placeholder rows whose
	// install state is unknown (loading) or failed.
	if !m.webGUILoaded || m.webGUIErr || !webGUIPanelInstalled(m.webGUI, panelID) {
		return nil
	}
	openBrowser := m.openBrowser
	if openBrowser == nil {
		openBrowser = platform.OpenBrowser
	}
	object := ui.ZashboardLabel
	if panelID == panelIDMetaCubeXD {
		object = ui.MetaCubeXDLabel
	}
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionOpenWebGUI, Page: ui.PageSystem, Capability: protocol.CapabilityWebGUI,
			Key: "system:web-gui:open:" + panelID, Title: ui.OpenWebGUITitle, Object: object,
			Impact: ui.OpenWebGUIImpact, Rollback: ui.OpenWebGUIRollback,
			Execute: func() tea.Msg {
				result, err := m.client.OpenWebGUI(m.ctx, panelID)
				if err != nil {
					return webGUIOpenResultMsg{rowID: panelID, err: err}
				}
				if err := openBrowser(result.OpenURL); err != nil {
					return webGUIOpenResultMsg{rowID: panelID, err: err}
				}
				return webGUIOpenResultMsg{rowID: panelID}
			},
		}
	}
}

func (m *Model) probePortHolds() tea.Cmd {
	listen := m.listenFree
	if listen == nil {
		listen = ui.ListenFree
	}
	lookup := m.lookupOccupant
	if lookup == nil {
		lookup = platform.LookupTCPOccupant
	}
	addrs := map[string]string{
		rowMixed:      m.onboarding.MixedAddr,
		rowController: m.onboarding.ControllerAddr,
		rowWeb:        m.onboarding.WebAddr,
	}
	owners := map[string]int{
		rowMixed:      m.core.PID,
		rowController: m.core.PID,
		rowWeb:        m.status.PID,
	}
	return func() tea.Msg {
		holds := make(map[string]ui.PortHold, 3)
		for id, addr := range addrs {
			if strings.TrimSpace(addr) == "" {
				holds[id] = ui.PortHold{Kind: ui.PortHoldUnknown}
				continue
			}
			free := listen(addr)
			var occ platform.TCPOccupant
			if !free {
				occ, _ = lookup(addr)
			}
			holds[id] = ui.ClassifyPortHold(free, occ.PID, occ.Process, owners[id])
		}
		return portHoldsMsg{holds: holds}
	}
}

func (m *Model) beginPortEdit(id string) tea.Cmd {
	if !m.mutationsEnabled || m.client == nil || !m.hasCapability(protocol.CapabilityOnboarding) {
		return nil
	}
	addr := m.portAddr(id)
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = 64
	input.SetWidth(28)
	input.SetValue(addr)
	_ = input.Focus()
	m.editID = id
	m.editInput = input
	return func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }
}

func (m *Model) cancelPortEdit() tea.Cmd {
	m.editID = ""
	m.editInput = textinput.Model{}
	return func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
}

func (m *Model) updatePortEdit(message tea.Msg) (ui.Page, tea.Cmd) {
	key, ok := message.(tea.KeyPressMsg)
	if ok {
		switch key.String() {
		case "esc", "up", "down":
			return m, m.cancelPortEdit()
		case "enter":
			return m, m.confirmPortEdit()
		}
	}
	updated, cmd := m.editInput.Update(message)
	m.editInput = updated
	return m, cmd
}

func (m *Model) confirmPortEdit() tea.Cmd {
	mixed, controller, web := m.onboarding.MixedAddr, m.onboarding.ControllerAddr, m.onboarding.WebAddr
	next := strings.TrimSpace(m.editInput.Value())
	switch m.editID {
	case rowMixed:
		mixed = next
	case rowController:
		controller = next
	case rowWeb:
		web = next
	}
	if err := ui.ValidateManagedEndpoints(mixed, controller, web); err != nil {
		m.lastError = ui.InvalidPortEndpoint
		return nil
	}
	occupied := [3]bool{
		m.portHolds[rowMixed].Kind == ui.PortHoldOccupied,
		m.portHolds[rowController].Kind == ui.PortHoldOccupied,
		m.portHolds[rowWeb].Kind == ui.PortHoldOccupied,
	}
	probe := m.listenFree
	if probe == nil {
		probe = ui.ListenFree
	}
	fixed := ui.FindAvailablePorts([3]string{mixed, controller, web}, occupied, probe)
	mixed, controller, web = fixed[0], fixed[1], fixed[2]
	if mixed == m.onboarding.MixedAddr && controller == m.onboarding.ControllerAddr && web == m.onboarding.WebAddr {
		return m.cancelPortEdit()
	}
	_ = m.cancelPortEdit()
	revision, operationID := m.onboarding.Revision, m.newOperationID()
	request := protocol.OnboardingUpdateRequest{
		OperationID: operationID, IfRevision: &revision,
		MixedAddr: &mixed, ControllerAddr: &controller, WebAddr: &web,
	}
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionApplyEndpointChange, Page: ui.PageSystem, Capability: protocol.CapabilityOnboarding,
			Key: "system:ports", Title: ui.ReplaceConfigurationTitle, Object: ui.PortsConfigSectionTitle,
			Impact: ui.ReplaceConfigurationImpact, Rollback: ui.ReplaceConfigurationRollback,
			Execute: func() tea.Msg {
				status, err := m.client.UpdateOnboarding(m.ctx, request)
				return portsApplyResultMsg{status: status, err: err}
			},
		}
	}
}

func (m *Model) portAddr(id string) string {
	switch id {
	case rowMixed:
		return m.onboarding.MixedAddr
	case rowController:
		return m.onboarding.ControllerAddr
	case rowWeb:
		return m.onboarding.WebAddr
	default:
		return ""
	}
}

func (m *Model) openGitHub() tea.Cmd {
	openBrowser := m.openBrowser
	if openBrowser == nil {
		openBrowser = platform.OpenBrowser
	}
	if err := openBrowser(ui.AboutGitHubURL); err != nil {
		m.lastError = ui.AboutGitHubOpenFailed
		return nil
	}
	m.lastError = ""
	return nil
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

func (m *Model) confirmSwitchMihariChannel() tea.Cmd {
	target := update.ChannelDev
	if m.currentMihariChannel() == update.ChannelDev {
		target = update.ChannelMain
	}
	impact := ui.SwitchMihariChannelToDevImpact
	if target == update.ChannelMain {
		impact = ui.SwitchMihariChannelToMainImpact
	}
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionSwitchMihariChannel, Page: ui.PageSystem, Key: "mihari:channel",
			Title: fmt.Sprintf("%s to %s", ui.SwitchMihariChannelTitle, target), Object: ui.MihariChannelLabel,
			Impact: impact, Rollback: ui.SwitchMihariChannelRollback,
			Execute: m.switchMihariChannel(target),
		}
	}
}

func (m *Model) switchMihariChannel(target string) tea.Cmd {
	return func() tea.Msg {
		path, err := m.channelFilePath()
		if err != nil {
			return mihariChannelResultMsg{err: err}
		}
		if err := m.writeChannel(path, target); err != nil {
			return mihariChannelResultMsg{err: err}
		}
		return mihariChannelResultMsg{channel: target}
	}
}

func (m *Model) currentMihariChannel() string {
	m.ensureChannelLoaded()
	if m.mihariChannelLoaded && m.mihariChannel != "" {
		return m.mihariChannel
	}
	return update.ChannelMain
}

func (m *Model) ensureChannelLoaded() {
	if m.mihariChannelLoaded || m.mihariChannelFailed {
		return
	}
	path, err := m.channelFilePath()
	if err != nil {
		m.mihariChannelFailed = true
		return
	}
	channel, err := m.readChannel(path)
	if err != nil {
		m.mihariChannelFailed = true
		return
	}
	m.mihariChannel = channel
	m.mihariChannelLoaded = true
}

func (m *Model) channelFilePath() (string, error) {
	fn := m.channelPath
	if fn == nil {
		fn = platform.ChannelPath
	}
	return fn()
}

func (m *Model) readChannel(path string) (string, error) {
	fn := m.loadChannel
	if fn == nil {
		fn = update.LoadChannel
	}
	return fn(path)
}

func (m *Model) writeChannel(path, channel string) error {
	fn := m.saveChannel
	if fn == nil {
		fn = update.SaveChannel
	}
	return fn(path, channel)
}

func (m *Model) confirmSwitchCoreChannel(target string) tea.Cmd {
	if coreChannelName(m.core.Channel) == target {
		return nil
	}
	revision, operationID := m.currentRevision(), m.newOperationID()
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionSwitchCoreChannel, Page: ui.PageSystem, Capability: protocol.CapabilityCore,
			Key:   "system:" + string(ui.ActionSwitchCoreChannel),
			Title: fmt.Sprintf("%s to %s", ui.SwitchCoreChannelTitle, target), Object: ui.MihomoCoreLabel,
			Impact: ui.SwitchCoreChannelImpact, Rollback: ui.SwitchCoreChannelRollback,
			Execute: m.runAction(actionStartMsg{
				kind: actionSwitchChannel, operationID: operationID, revision: revision,
				channel: target, source: "channel-switch",
			}),
		}
	}
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

// systemProxyActionLabel is the Network action-row label: the verb for what
// pressing enter will do next. Mirrors the decision in confirmSystemProxyToggle
// so the label always matches the action that fires.
func (m *Model) systemProxyActionLabel() string {
	switch {
	case m.systemProxy.Observed.Foreign:
		return ui.ForceEnableSystemProxyLabel
	case m.systemProxy.Desired || m.systemProxy.Observed.Owned:
		return ui.DisableSystemProxyLabel
	default:
		return ui.EnableSystemProxyLabel
	}
}

// tunActionLabel is the Network action-row label, mirroring confirmTunToggle.
func (m *Model) tunActionLabel() string {
	if m.tun.DesiredEnable {
		return ui.DisableTunLabel
	}
	return ui.EnableTunLabel
}

func (m *Model) runAction(start actionStartMsg) tea.Cmd {
	return func() tea.Msg {
		revision := start.revision
		request := protocol.MutationRequest{OperationID: start.operationID, IfRevision: &revision, Source: start.source}
		if start.channel != "" {
			channel := start.channel
			request.Channel = &channel
		}
		if start.kind == actionUpdate || start.kind == actionSwitchChannel {
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
		return coreLoadResultMsg{core: core, err: err}
	}
}

// actionState is the idle suffix for action rows. In-flight/outcome chips are
// applied in View and must not depend on a global pending flag.
func actionState(available, enabled bool) string {
	if !available {
		return ui.UnavailableTitle
	}
	if !enabled {
		return ui.StaleLabel
	}
	return ""
}

// daemonValue renders the daemon row value: a status dot for health (tone from
// the health word) followed by the version as muted context. When the control
// connection is stale the dot degrades to caution yellow and the health word is
// marked stale (design G2), mirroring the Overview core card.
func daemonValue(theme ui.Theme, status protocol.Status, stale bool) string {
	health := valueOr(status.Health, ui.UnknownLabel)
	tone := ui.ClassifyStatusTone(health)
	if stale {
		tone = ui.ToneCaution
		health += " · " + ui.StaleLabel
	}
	return ui.StatusDot(theme, tone, health) + "  " +
		theme.Muted.Render(valueOr(status.DaemonVersion, ui.UnknownLabel))
}

// coreTone maps the mihomo core status to a tone. "running" is Positive; an
// explicit "missing" (not installed yet) is Neutral rather than a fault; any
// other word falls through to the shared classifier.
func coreTone(status string) ui.StatusTone {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return ui.TonePositive
	case "missing":
		return ui.ToneNeutral
	default:
		return ui.ClassifyStatusTone(status)
	}
}

// coreValue renders the core row value: a status dot (tone from coreTone) and
// the version as muted context. When the control connection is stale the dot
// degrades to caution yellow and the status word is marked stale (design G2),
// mirroring the Overview core card.
func coreValue(theme ui.Theme, core protocol.CoreStatus, stale bool) string {
	status := valueOr(core.Status, ui.UnknownLabel)
	tone := coreTone(core.Status)
	if stale {
		tone = ui.ToneCaution
		status += " · " + ui.StaleLabel
	}
	return ui.StatusDot(theme, tone, status) + "  " +
		theme.Muted.Render(valueOr(core.Version, ui.UnknownLabel)+"  "+coreChannelName(core.Channel))
}

func coreChannelName(channel string) string {
	if strings.TrimSpace(channel) == "" {
		return "stable"
	}
	return channel
}

func otherCoreChannel(channel string) string {
	if coreChannelName(channel) == "alpha" {
		return "stable"
	}
	return "alpha"
}

// proxyTone derives the system-proxy status tone: a foreign observer or a
// desired/owned drift needs attention (Caution); an owned proxy is Positive
// (Owned already implies Enabled, per sysproxy.IsOwned); otherwise (off) Neutral.
func proxyTone(status protocol.SystemProxyStatus) ui.StatusTone {
	switch {
	case status.Observed.Foreign:
		return ui.ToneCaution
	case status.Desired != status.Observed.Owned:
		return ui.ToneCaution
	case status.Observed.Owned:
		return ui.TonePositive
	default:
		return ui.ToneNeutral
	}
}

// tunTone derives the TUN status tone: unknown live state is Neutral; a
// desired/live drift is Caution; live-on is Positive.
func tunTone(status protocol.TunStatus) ui.StatusTone {
	if status.Conflict != nil && len(status.Conflict.OtherTunInterfaces) > 0 {
		return ui.ToneCaution
	}
	liveOn := status.LiveEnable != nil && *status.LiveEnable
	switch {
	case status.LiveEnable == nil:
		return ui.ToneNeutral
	case status.DesiredEnable != liveOn:
		return ui.ToneCaution
	case liveOn:
		return ui.TonePositive
	default:
		return ui.ToneNeutral
	}
}

func systemProxySummary(theme ui.Theme, status protocol.SystemProxyStatus) string {
	desired := ui.OffLabel
	if status.Desired {
		desired = ui.OnLabel
	}
	observed := ui.OffLabel
	if status.Observed.Enabled {
		observed = valueOr(status.Observed.Server, ui.OnLabel)
	} else if status.Observed.Foreign {
		// Residual foreign config with ProxyEnable=0 still surfaces classification.
		observed = valueOr(status.Observed.Server, ui.OffLabel)
	}
	classification := ""
	switch {
	case status.Observed.Owned:
		classification = ui.OwnedLabel
	case status.Observed.Foreign:
		classification = ui.ForeignLabel
	}
	context := fmt.Sprintf("%s %s · %s %s", ui.DesiredLabel, desired, ui.ObservedLabel, observed)
	if classification != "" {
		context += " · " + classification
	}
	// Status dot carries the tone; the desired/observed detail stays muted.
	return ui.StatusDot(theme, proxyTone(status), "") + "  " + theme.Muted.Render(context)
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

func tunSummary(theme ui.Theme, status protocol.TunStatus) string {
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
	if label := tunConflictLabel(status.Conflict); label != "" {
		parts = append(parts, label)
	}
	// Status dot carries the tone; desired/live/stack detail stays muted.
	return ui.StatusDot(theme, tunTone(status), "") + "  " + theme.Muted.Render(strings.Join(parts, " · "))
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

// forceTunImpactFromStatus 用 live status 的冲突证据构造分情形 force impact（前置路径）。
func forceTunImpactFromStatus(status protocol.TunStatus) string {
	return forceTunImpact(status.Conflict)
}

// forceTunImpactFromDetails 从后置 CodeTunConflict 错误的 Details 取证据构造 impact。
func forceTunImpactFromDetails(details map[string]any) string {
	return forceTunImpact(&protocol.TunConflict{
		OtherTunInterfaces:   detailStrings(details, "other_tun_interfaces"),
		OtherMihomoProcesses: detailStrings(details, "other_mihomo_processes"),
	})
}

// forceTunImpact 按信号 A/B 的有无分情形选择 force 确认文案（设计决策 2）。
// 进入 force 路径要求 OtherTunInterfaces 非空；兜底返回标题以防空 impact。
func forceTunImpact(conflict *protocol.TunConflict) string {
	tunNames := []string(nil)
	mihomoCount := 0
	if conflict != nil {
		tunNames = conflict.OtherTunInterfaces
		mihomoCount = len(conflict.OtherMihomoProcesses)
	}
	if len(tunNames) == 0 {
		return ui.ForceTunTitle
	}
	joined := strings.Join(tunNames, ", ")
	if mihomoCount > 0 {
		return fmt.Sprintf(ui.ForceTunImpactAB, len(tunNames), joined, mihomoCount)
	}
	return fmt.Sprintf(ui.ForceTunImpactA, len(tunNames), joined)
}

// tunConflictLabel 把分情形证据压缩成 summary 行 label（仅 B 也要展示，设计决策 2）。
func tunConflictLabel(conflict *protocol.TunConflict) string {
	if conflict == nil {
		return ""
	}
	tunCount := len(conflict.OtherTunInterfaces)
	mihomoCount := len(conflict.OtherMihomoProcesses)
	switch {
	case tunCount > 0 && mihomoCount > 0:
		return fmt.Sprintf(ui.TunConflictLabelAB, tunCount, mihomoCount)
	case tunCount > 0:
		return fmt.Sprintf(ui.TunConflictLabelA, tunCount)
	case mihomoCount > 0:
		return fmt.Sprintf(ui.TunConflictLabelB, mihomoCount)
	}
	return ""
}

// detailStrings 从 error Details 取 []string 字段（对称 detailString 的单串版本）。
// 兼容两种来源：进程内构造的 APIError（[]string）与经 HTTP/JSON 解码的 APIError
// （JSON 数组解码为 []any）。后者正是后置 CodeTunConflict 路径——若只接受 []string，
// force 确认的 Impact 会丢失接口证据。
func detailStrings(details map[string]any, key string) []string {
	if details == nil {
		return nil
	}
	value, ok := details[key]
	if !ok || value == nil {
		return nil
	}
	switch slice := value.(type) {
	case []string:
		return slice
	case []any:
		out := make([]string, 0, len(slice))
		for _, item := range slice {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
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
