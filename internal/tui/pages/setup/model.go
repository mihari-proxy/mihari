package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type Client interface {
	Onboarding(context.Context) (protocol.OnboardingStatus, error)
	UpdateOnboarding(context.Context, protocol.OnboardingUpdateRequest) (protocol.OnboardingStatus, error)
	Core(context.Context) (protocol.CoreStatus, error)
	GeoIPStatus(context.Context) (protocol.GeoIPStatus, error)
	InstallCore(context.Context, protocol.MutationRequest) (protocol.CoreInstallResult, error)
	AddSubscription(context.Context, protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error)
	UpdateGeoIP(context.Context, protocol.MutationRequest) (protocol.GeoIPUpdateResult, error)
	ServiceStatus(context.Context) (protocol.ServiceStatus, error)
}

type step uint8

const (
	stepEndpoints step = iota
	stepCore
	stepSubscription
	stepGeoIP
	stepReview
)

type onboardingResultMsg struct {
	core             *protocol.CoreStatus
	subscriptions    *protocol.SubscriptionList
	subscriptionsErr error
	status           protocol.OnboardingStatus
	err              error
}

type actionResultMsg struct {
	gen          uint64
	core         *protocol.CoreInstallResult
	subscription *protocol.Subscription
	geoip        *protocol.GeoIPUpdateResult
	next         step
	revision     uint64
	operation    logging.OperationMetadata
	err          error
}

// coreLocalResultMsg carries a read-only core readiness probe. Older generations
// are ignored; a failed read remains unknown, not proof of a missing core.
type coreLocalResultMsg struct {
	gen    uint64
	status protocol.CoreStatus
	err    error
}

// geoipLocalResultMsg carries an advisory local GeoIP database probe for stepGeoIP.
type geoipLocalResultMsg struct {
	gen    uint64
	status protocol.GeoIPStatus
	err    error
}

// serviceStatusMsg carries the daemon's service registration status for stepReview.
// A stale generation or non-nil err leaves the 服务 row on its fallback (design §5.3) —
// service detection never blocks the review step.
type serviceStatusMsg struct {
	gen    uint64
	status protocol.ServiceStatus
	err    error
}

// portState distinguishes bindable, foreign, unknown and owned endpoints.
type portState uint8

const (
	portFree     portState = iota // bindable — net.Listen succeeded
	portOccupied                  // EADDRINUSE — another process holds the port
	portUnknown                   // any other error (permissions) — not red, not blocking
	portOwned
)

// portProbeMsg carries generation-guarded endpoint port probe results for
// stepEndpoints. A stale generation is discarded so rapid edits honor only the
// latest probe.
type portProbeMsg struct {
	gen     uint64
	results [3]portState
}

type completeStartMsg struct{}

type completeResultMsg struct {
	gen    uint64
	status protocol.OnboardingStatus
	err    error
}

// Err implements the shell's action-outcome contract so Setup completion is
// classified Succeeded/Failed in the Recent operations ledger.
func (m completeResultMsg) Err() error { return m.err }

var _ interface{ Err() error } = completeResultMsg{}

// CompletedMsg tells the root model to leave the standalone Setup route.
type CompletedMsg struct{ Status protocol.OnboardingStatus }

// CancelledMsg requests leaving a manually launched Setup flow without persisting completion.
type CancelledMsg struct{}

type Model struct {
	ctx                context.Context
	client             Client
	newOperationID     func() string
	step               step
	status             protocol.OnboardingStatus
	initial            protocol.OnboardingStatus
	inputs             []textinput.Model
	subscriptionInputs []textinput.Model
	focusedField       int
	loading            bool
	lastError          string
	settlementNotice   string
	errorAdvice        string
	errorDetail        string
	operationID        string
	cancelExecution    context.CancelFunc
	cancelSettlement   context.CancelFunc
	executionGen       uint64
	executionStarted   time.Time
	executionNow       time.Time
	executionLabel     string
	cancelRequested    bool
	settling           bool
	resultUnknown      bool
	statusUnsupported  bool
	coreLocal          protocol.CoreStatus
	coreLocalLoaded    bool
	coreLocalGen       uint64
	geoipLocal         protocol.GeoIPStatus
	geoipLocalLoaded   bool
	geoipLocalGen      uint64
	coreResult         protocol.CoreInstallResult
	addedSubscription  *protocol.Subscription
	geoipResult        *protocol.GeoIPUpdateResult
	geoipSkipped       bool
	serviceStatus      protocol.ServiceStatus
	serviceLoaded      bool
	serviceErr         bool
	serviceGen         uint64
	portProbe          [3]portState
	portOwners         [3]int
	automatic          bool
	resumePending      bool
	refreshInPlace     bool
	portRecovery       bool
	waitingRestart     bool
	subscriptionsErr   error
	subscriptions      protocol.SubscriptionList
	portProbeLoaded    bool
	portProbeGen       uint64
	probe              func(string) portState
	width              int
	scroll             int
	height             int
	theme              ui.Theme
}

func New(client Client, newOperationID func() string) *Model {
	return NewWithContext(context.Background(), client, newOperationID)
}

// NewWithContext creates a Setup model whose operations end with the owning TUI lifecycle.
func NewWithContext(ctx context.Context, client Client, newOperationID func() string) *Model {
	if ctx == nil {
		ctx = context.Background()
	}
	if newOperationID == nil {
		newOperationID = defaultOperationID
	}
	return &Model{ctx: ctx, client: client, newOperationID: newOperationID, loading: client != nil, theme: ui.DefaultTheme()}
}

func (m *Model) ID() ui.PageID { return ui.PageSetup }

func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
	for i := range m.inputs {
		m.inputs[i].SetWidth(max(1, min(52, width-16)))
	}
	for i := range m.subscriptionInputs {
		m.subscriptionInputs[i].SetWidth(max(1, min(52, width-10)))
	}
}

func (m *Model) FocusFirst() {
	if len(m.inputs) > 0 {
		m.focusEndpoint(0)
	}
}

func (m *Model) Load() tea.Cmd {
	if m.client == nil {
		return nil
	}
	client, ctx := m.client, m.ctx
	m.loading = true
	m.executionLabel = "Reading saved setup state"
	m.executionStarted, m.executionNow = time.Now(), time.Now()
	return func() tea.Msg {
		status, err := client.Onboarding(ctx)
		result := onboardingResultMsg{status: status, err: err}
		if err != nil {
			return result
		}
		return loadResources(ctx, client, result)
	}
}

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	previousStep := m.step
	defer func() {
		if m.step != previousStep {
			m.scroll = 0
		}
	}()
	switch typed := message.(type) {
	case saveEndpointsStartMsg:
		return m, m.saveEndpoints()
	case endpointsSavedMsg:
		return m.handleEndpointsSaved(typed)
	case cancelExecutionMsg:
		if typed.gen == m.executionGen && m.loading && m.cancelExecution != nil {
			m.cancelRequested = true
			m.cancelExecution()
		}
		return m, nil
	case settlementMsg:
		if typed.gen != m.executionGen {
			return m, nil
		}
		m.loading, m.settling = false, false
		cancelled := m.cancelRequested
		m.cancelRequested = false
		if m.cancelSettlement != nil {
			m.cancelSettlement()
			m.cancelSettlement = nil
		}
		m.resultUnknown = !typed.confirmed || typed.err != nil
		if m.resultUnknown {
			message := "The daemon has not confirmed settlement. Recheck before starting another operation."
			if typed.unsupported {
				message = "The connected daemon does not support operation status. Reconnect to an updated daemon before rechecking."
			}
			m.fail("Result could not be confirmed", protocol.APIError{Code: protocol.CodeDaemonUnavailable, Message: message})
		} else {
			m.status = typed.status
			m.waitingRestart = typed.status.RestartRequired || m.portRecovery
			if typed.core != nil {
				m.coreLocal, m.coreLocalLoaded = *typed.core, true
			}
			if typed.geoip != nil {
				m.geoipLocal, m.geoipLocalLoaded = *typed.geoip, true
			}
			if m.step == stepSubscription {
				m.subscriptions = typed.subscriptions
				m.subscriptionsErr = nil
				if m.addedSubscription != nil {
					savedID := m.addedSubscription.ID
					m.addedSubscription = nil
					for _, profile := range typed.subscriptions.Subscriptions {
						if profile.ID == savedID {
							value := profile
							m.addedSubscription = &value
						}
					}
				}
			}
			m.clearFailure()
			m.settlementNotice = "Operation ended. Saved state rechecked; review before continuing."
			if cancelled {
				m.settlementNotice = "Cancellation requested · Operation ended. Saved work is kept."
			}
		}
		return m, nil
	case onboardingResultMsg:
		m.loading = false
		if typed.err != nil {
			m.fail(ui.SetupUnavailable, typed.err)
			return m, nil
		}
		m.status, m.initial = typed.status, typed.status
		preserve := m.refreshInPlace && len(m.inputs) > 0
		m.refreshInPlace = false
		if !preserve {
			m.inputs = endpointInputs(typed.status)
		}
		if len(m.subscriptionInputs) == 0 {
			m.subscriptionInputs = subscriptionInputs()
		}
		m.coreLocalLoaded = typed.core != nil
		if typed.core != nil {
			m.coreLocal = *typed.core
			m.coreLocalLoaded = true
			m.portOwners[0], m.portOwners[1] = typed.core.PID, typed.core.PID
		}
		m.subscriptionsErr = typed.subscriptionsErr
		if typed.subscriptions != nil {
			m.subscriptions = *typed.subscriptions
			if m.addedSubscription != nil {
				savedID := m.addedSubscription.ID
				m.addedSubscription = nil
				for _, profile := range m.subscriptions.Subscriptions {
					if profile.ID == savedID {
						value := profile
						m.addedSubscription = &value
					}
				}
			}
		}
		m.resumePending = !preserve && (m.automatic || m.waitingRestart)
		m.waitingRestart = typed.status.RestartRequired
		if m.width > 0 {
			m.SetSize(m.width, m.height)
		}
		if !preserve {
			m.focusEndpoint(0)
		}
		if preserve && m.step != stepEndpoints {
			return m, nil
		}
		return m, m.probePorts()
	case actionResultMsg:
		if typed.gen != 0 && typed.gen != m.executionGen {
			return m, nil
		}
		m.loading = false
		if typed.core != nil && typed.err == nil {
			m.coreResult = *typed.core
			if typed.core.Version != "" {
				m.coreLocal.LocalReady, m.coreLocalLoaded = true, true
				m.coreLocal.LocalVersion = typed.core.Version
			}
		}
		if typed.subscription != nil {
			value := *typed.subscription
			m.addedSubscription = &value
			m.rememberSubscription(value)
		}
		if typed.geoip != nil && typed.err == nil {
			value := *typed.geoip
			m.geoipResult = &value
			m.geoipLocal, m.geoipLocalLoaded = value.Status, true
		}
		if m.cancelExecution != nil {
			m.cancelExecution()
			m.cancelExecution = nil
		}
		if m.cancelRequested || uncertainOutcome(typed.err) {
			m.loading, m.settling = true, true
			return m, m.settle()
		}
		if typed.err != nil {
			var apiError protocol.APIError
			if errors.As(typed.err, &apiError) && apiError.Code == protocol.CodeRevisionConflict {
				m.fail(ui.SetupChangedMessage, typed.err)
				return m, m.reloadInPlace()
			}
			m.fail(ui.SetupActionFailed, typed.err)
			return m, nil
		}
		m.clearFailure()
		if typed.revision > 0 {
			m.status.Revision = typed.revision
		}
		if typed.subscription != nil && typed.subscription.ID != "" && (!typed.subscription.Cached || typed.subscription.LastError != "") {
			cause := typed.subscription.LastError
			if cause == "" {
				cause = "The first download did not produce a usable subscription."
			}
			m.fail("Subscription saved; first download failed", protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: cause})
			return m, nil
		}
		m.step = typed.next
		switch m.step {
		case stepCore:
			return m, m.fetchCoreLocal()
		case stepSubscription:
			if !m.hasSubscriptions() {
				m.focusSubscription(0)
			}
		case stepGeoIP:
			return m, m.fetchGeoIPLocal()
		case stepReview:
			return m, m.fetchServiceStatus()
		}
		return m, nil
	case coreLocalResultMsg:
		if typed.gen != m.coreLocalGen {
			return m, nil
		}
		if typed.err == nil {
			m.coreLocal = typed.status
			m.coreLocalLoaded = true
		} else {
			m.coreLocalLoaded = false
			m.fail("Read local core state", typed.err)
		}
		return m, nil
	case geoipLocalResultMsg:
		if typed.gen != m.geoipLocalGen {
			return m, nil
		}
		if typed.err == nil {
			m.geoipLocal = typed.status
			m.geoipLocalLoaded = true
		} else {
			m.geoipLocalLoaded = false
		}
		return m, nil
	case serviceStatusMsg:
		if typed.gen != m.serviceGen {
			return m, nil
		}
		if typed.err == nil {
			m.serviceStatus = typed.status
			m.serviceLoaded = true
		} else {
			m.serviceErr = true
		}
		return m, nil
	case portProbeMsg:
		if typed.gen != m.portProbeGen {
			return m, nil
		}
		m.portProbe = typed.results
		m.portProbeLoaded = true
		if m.resumePending {
			return m, m.resumeFromState()
		}
		return m, nil
	case completeStartMsg:
		m.loading = true
		return m, m.complete()
	case completeResultMsg:
		if typed.gen != 0 && typed.gen != m.executionGen {
			return m, nil
		}
		if m.cancelExecution != nil {
			m.cancelExecution()
			m.cancelExecution = nil
		}
		if m.cancelRequested || uncertainOutcome(typed.err) {
			m.loading, m.settling = true, true
			return m, m.settle()
		}
		m.loading = false
		if typed.err != nil {
			var apiError protocol.APIError
			if errors.As(typed.err, &apiError) && apiError.Code == protocol.CodeRevisionConflict {
				m.fail(ui.SetupChangedMessage, typed.err)
				return m, m.reloadInPlace()
			}
			m.fail(ui.SetupCompletionFailed, typed.err)
			return m, nil
		}
		m.status = typed.status
		return m, func() tea.Msg { return CompletedMsg{Status: typed.status} }
	}

	if m.loading {
		if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "esc" && m.cancelExecution != nil && !m.settling && !m.cancelRequested {
			return m, m.cancelPrompt()
		}
		return m, nil
	}
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		// Bracketed paste, clipboard results, and textinput blink updates must reach focused fields.
		return m.forwardTextInput(message)
	}
	if key.String() == "f2" && m.errorDetail != "" {
		body := m.errorDetail
		return m, func() tea.Msg { return ui.ErrorDetailMsg{Title: "Setup error details", Body: body} }
	}
	if key.String() == "ctrl+q" {
		return m, m.exitPrompt()
	}
	if key.String() == "pgdown" {
		m.scroll++
		return m, nil
	}
	if key.String() == "pgup" {
		m.scroll = max(0, m.scroll-1)
		return m, nil
	}
	if len(m.inputs) == 0 && m.step != stepSubscription {
		if key.String() == "enter" {
			m.loading = true
			return m, m.Load()
		}
		return m, nil
	}
	if m.resultUnknown {
		if key.String() == "enter" {
			m.loading, m.settling = true, true
			return m, m.settle()
		}
		return m, nil
	}
	if m.waitingRestart {
		switch key.String() {
		case "enter":
			m.resumePending = true
			return m, m.Load()
		case "esc":
			m.waitingRestart = false
			return m, nil
		}
		return m, nil
	}
	if key.String() == "esc" {
		if m.step > stepEndpoints {
			m.step--
			m.clearFailure()
			if m.step == stepEndpoints {
				m.focusEndpoint(0)
			}
			if m.step == stepCore {
				return m, m.fetchCoreLocal()
			}
			if m.step == stepGeoIP {
				return m, m.fetchGeoIPLocal()
			}
			return m, nil
		}
		if m.automatic {
			return m, m.exitPrompt()
		}
		return m, func() tea.Msg {
			return ui.ConfirmationRequestMsg{Title: "Leave setup?", Impact: "Saved ports, installed resources and registered subscriptions are kept. Unconfirmed edits are not saved.", Rollback: "Open setup again to continue.", OnConfirm: func() tea.Msg { return CancelledMsg{} }}
		}
	}
	if key.String() == "q" && m.step != stepEndpoints && m.step != stepSubscription {
		return m, m.exitPrompt()
	}
	if key.String() == "?" && m.step != stepEndpoints && m.step != stepSubscription {
		return m, func() tea.Msg { return ui.OpenHelpMsg{} }
	}
	switch m.step {
	case stepEndpoints:
		return m.updateEndpoints(message, key)
	case stepCore:
		if key.String() == "enter" {
			if !m.coreLocalLoaded {
				return m, m.fetchCoreLocal()
			}
			m.loading = true
			return m, m.installCore()
		}
	case stepSubscription:
		return m.updateSubscription(message, key)
	case stepGeoIP:
		if key.String() == "s" {
			m.step = stepReview
			m.geoipSkipped = true
			return m, m.fetchServiceStatus()
		}
		if key.String() == "enter" {
			m.loading = true
			return m, m.updateGeoIP()
		}
	case stepReview:
		if key.String() == "enter" {
			if m.endpointsChanged() {
				m.step = stepEndpoints
				return m, m.saveEndpointsPrompt()
			}
			m.loading = true
			return m, m.complete()
		}
	}
	return m, nil
}

func (m *Model) forwardTextInput(message tea.Msg) (ui.Page, tea.Cmd) {
	switch m.step {
	case stepEndpoints:
		if len(m.inputs) == 0 || m.focusedField < 0 || m.focusedField >= len(m.inputs) {
			return m, nil
		}
		updated, command := m.inputs[m.focusedField].Update(message)
		m.inputs[m.focusedField] = updated
		return m, command
	case stepSubscription:
		if m.hasSubscriptions() || m.subscriptionsErr != nil {
			return m, nil
		}
		if len(m.subscriptionInputs) == 0 || m.focusedField < 0 || m.focusedField >= len(m.subscriptionInputs) {
			return m, nil
		}
		updated, command := m.subscriptionInputs[m.focusedField].Update(message)
		m.subscriptionInputs[m.focusedField] = updated
		return m, command
	default:
		return m, nil
	}
}

func (m *Model) updateEndpoints(message tea.Msg, key tea.KeyPressMsg) (ui.Page, tea.Cmd) {
	switch key.String() {
	case "tab":
		m.focusEndpoint((m.focusedField + 1) % len(m.inputs))
		return m, m.inputs[m.focusedField].Focus()
	case "shift+tab":
		m.focusEndpoint((m.focusedField - 1 + len(m.inputs)) % len(m.inputs))
		return m, m.inputs[m.focusedField].Focus()
	case "enter":
		if err := validateEndpoints(m.endpointValues()); err != nil {
			m.lastError = err.Error()
			return m, nil
		}
		if m.portProbeLoaded && m.anyPortOccupied() {
			fixed := findAvailablePortsForStates(m.endpointValuesArray(), m.portProbe)
			m.writeEndpoints(fixed)
			m.lastError = ui.SetupPortAutoFixHint
			return m, m.probePorts()
		}
		m.lastError = ""
		if m.endpointsChanged() || m.portRecovery {
			return m, m.saveEndpointsPrompt()
		}
		if m.status.RestartRequired {
			m.waitingRestart = true
			return m, nil
		}
		m.step = stepCore
		return m, m.fetchCoreLocal()
	}
	updated, command := m.inputs[m.focusedField].Update(message)
	m.inputs[m.focusedField] = updated
	return m, tea.Batch(command, m.probePorts())
}

func (m *Model) updateSubscription(message tea.Msg, key tea.KeyPressMsg) (ui.Page, tea.Cmd) {
	if key.String() == "ctrl+s" {
		m.step = stepGeoIP
		m.clearFailure()
		return m, m.fetchGeoIPLocal()
	}
	if m.hasSubscriptions() || m.subscriptionsErr != nil {
		if key.String() != "enter" {
			return m, nil
		}
		if m.subscriptionsErr != nil {
			m.fail("Read subscriptions", m.subscriptionsErr)
			return m, m.reloadInPlace()
		}
		if m.subscriptionNeedsRetry() {
			return m, m.refreshSavedSubscription()
		}
		m.step = stepGeoIP
		m.clearFailure()
		return m, m.fetchGeoIPLocal()
	}
	switch key.String() {
	case "tab":
		m.focusSubscription((m.focusedField + 1) % len(m.subscriptionInputs))
		return m, m.subscriptionInputs[m.focusedField].Focus()
	case "shift+tab":
		m.focusSubscription((m.focusedField - 1 + len(m.subscriptionInputs)) % len(m.subscriptionInputs))
		return m, m.subscriptionInputs[m.focusedField].Focus()
	case "enter":
		name := strings.TrimSpace(m.subscriptionInputs[0].Value())
		url := strings.TrimSpace(m.subscriptionInputs[1].Value())
		if name == "" && url == "" {
			m.step = stepGeoIP
			return m, m.fetchGeoIPLocal()
		}
		if name == "" || url == "" {
			m.lastError = ui.InvalidSubscriptionForm
			return m, nil
		}
		m.loading = true
		return m, m.addSubscription(name, url)
	}
	updated, command := m.subscriptionInputs[m.focusedField].Update(message)
	m.subscriptionInputs[m.focusedField] = updated
	return m, command
}

func (m *Model) View() string {
	if m.waitingRestart {
		lines := []string{ui.RestartRequiredTitle, "Ports are saved. Restart the Mihari daemon to apply them.", "For an installed service: restart Mihari with administrator/root privileges.", "For a foreground daemon: stop it and run mihari daemon again.", "Enter recheck connection and saved state"}
		if m.width > 0 {
			return m.renderFrame(lines)
		}
		return strings.Join(lines, "\n")
	}
	if m.loading && len(m.inputs) == 0 {
		if m.width > 0 {
			return m.renderFrame(nil)
		}
		return m.theme.Title.Render(ui.SetupTitle) + "\n\n" + m.executionText()
	}
	lines := []string{m.theme.Title.Render(ui.SetupTitle), m.theme.Muted.Render(fmt.Sprintf(ui.SetupProgress, int(m.step)+1, 5)), ""}
	switch m.step {
	case stepEndpoints:
		lines = append(lines, ui.LocalEndpointsLabel)
		lines = append(lines, m.renderEndpoints()...)
		lines = append(lines, "", ui.SetupEndpointHelp)
	case stepCore:
		lines = append(lines, m.coreStatusLines()...)
	case stepSubscription:
		lines = append(lines, m.subscriptionStatusLines()...)
	case stepGeoIP:
		lines = append(lines, m.geoipStatusLines()...)
	case stepReview:
		mixed, controller, web := m.endpointValues()
		lines = append(lines, ui.SetupReviewTitle,
			"Mixed        "+mixed,
			"Controller   "+controller,
			"Web          "+web+m.restartSuffix(),
			"Core         "+m.coreSummary(),
			"Subscription "+m.subscriptionSummary(),
			"GeoIP        "+m.geoipSummary(),
			"Service      "+m.serviceSummary(),
			"", ui.SetupCompleteHelp)
	}
	if m.width > 0 {
		return m.renderFrame(lines[3:])
	}
	if m.loading {
		lines = append(lines, "", m.executionText())
	}
	if m.lastError != "" {
		lines = append(lines, "", m.lastError)
	}
	if m.settlementNotice != "" {
		lines = append(lines, "", m.theme.Info.Render(m.settlementNotice))
	}
	return strings.Join(lines, "\n")
}

func endpointInputs(status protocol.OnboardingStatus) []textinput.Model {
	return makeInputs([]string{status.MixedAddr, status.ControllerAddr, status.WebAddr}, []string{"127.0.0.1:9190", "127.0.0.1:9090", "127.0.0.1:9191"})
}

func subscriptionInputs() []textinput.Model {
	return makeInputs([]string{"", ""}, []string{"Optional subscription name", "https://example.test/subscription"})
}

func makeInputs(values, placeholders []string) []textinput.Model {
	inputs := make([]textinput.Model, len(values))
	for index := range values {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = placeholders[index]
		input.CharLimit = 2048
		input.SetWidth(52)
		input.SetValue(values[index])
		inputs[index] = input
	}
	return inputs
}

func renderInputs(labels []string, inputs []textinput.Model, focused int) []string {
	lines := make([]string, 0, len(inputs)*2)
	for index := range inputs {
		marker := "  "
		if index == focused {
			marker = ui.FocusMarker
		}
		lines = append(lines, marker+labels[index], "  "+inputs[index].View())
	}
	return lines
}

func (m *Model) focusEndpoint(index int) {
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	m.focusedField = index
	if len(m.inputs) > 0 {
		_ = m.inputs[index].Focus()
	}
}

func (m *Model) focusSubscription(index int) {
	for i := range m.subscriptionInputs {
		m.subscriptionInputs[i].Blur()
	}
	m.focusedField = index
	if len(m.subscriptionInputs) > 0 {
		_ = m.subscriptionInputs[index].Focus()
	}
}

func (m *Model) endpointValues() (string, string, string) {
	return strings.TrimSpace(m.inputs[0].Value()), strings.TrimSpace(m.inputs[1].Value()), strings.TrimSpace(m.inputs[2].Value())
}

func validateEndpoints(mixedValue, controllerValue, webValue string) error {
	mixed, err := netip.ParseAddrPort(mixedValue)
	if err != nil || mixed.Port() == 0 {
		return fmt.Errorf("mixed endpoint must be an IP address and valid port")
	}
	controller, err := netip.ParseAddrPort(controllerValue)
	if err != nil || controller.Port() == 0 || !controller.Addr().IsLoopback() {
		return fmt.Errorf("controller endpoint must use a loopback address and valid port")
	}
	web, err := netip.ParseAddrPort(webValue)
	if err != nil || web.Port() == 0 || !web.Addr().IsLoopback() {
		return fmt.Errorf("web endpoint must use a loopback address and valid port")
	}
	if mixed.Port() == controller.Port() || mixed.Port() == web.Port() || controller.Port() == web.Port() {
		return fmt.Errorf("managed ports must be distinct")
	}
	return nil
}

func (m *Model) endpointsChanged() bool {
	mixed, controller, web := m.endpointValues()
	return mixed != m.initial.MixedAddr || controller != m.initial.ControllerAddr || web != m.initial.WebAddr
}

func (m *Model) endpointValuesArray() [3]string {
	mixed, controller, web := m.endpointValues()
	return [3]string{mixed, controller, web}
}

func (m *Model) writeEndpoints(values [3]string) {
	for index, value := range values {
		if index < len(m.inputs) {
			m.inputs[index].SetValue(value)
		}
	}
}

// anyPortOccupied reports whether any probed endpoint is held by another process.
// portUnknown is intentionally excluded (design §8): permission failures must not
// block onboarding or render red — the daemon's startup check remains the backstop.
func (m *Model) anyPortOccupied() bool {
	for _, state := range m.portProbe {
		if state == portOccupied {
			return true
		}
	}
	return false
}

// probePorts schedules a generation-guarded endpoint probe. Each call bumps
// portProbeGen so only the most recently scheduled probe's result lands; earlier
// probes are discarded by the gen guard in the portProbeMsg case. This is the
// debounce (design §7.2) — a race-free override instead of a wall-clock timer.
func (m *Model) probePorts() tea.Cmd {
	m.portProbeGen++
	gen := m.portProbeGen
	values := m.endpointValuesArray()
	owners := m.portOwners
	probe := m.probe
	return func() tea.Msg {
		var results [3]portState
		for i, address := range values {
			if probe != nil {
				results[i] = probe(address)
			} else {
				results[i] = classifySetupPort(address, owners[i], platform.LookupTCPOccupant)
			}
		}
		return portProbeMsg{gen: gen, results: results}
	}
}

// renderEndpoints renders the three endpoint inputs with focus markers and, once a
// probe has landed, a trailing ✓ for free ports or ✗ in use for occupied ones; the
// occupied value is colored Danger. Unknown ports get no marker (design §8).
func (m *Model) renderEndpoints() []string {
	labels := []string{"Mixed", "Controller", "Web"}
	lines := make([]string, 0, len(labels)*2)
	for index := range m.inputs {
		marker := "  "
		if index == m.focusedField {
			marker = ui.FocusMarker
		}
		value := m.inputs[index].View()
		suffix := ""
		if m.portProbeLoaded {
			switch m.portProbe[index] {
			case portOwned:
				suffix = "  " + ui.RenderPortHold(m.theme, ui.PortHold{Kind: ui.PortHoldOwned})
			case portFree:
				suffix = "  " + m.theme.Success.Render("✓")
			case portOccupied:
				value = m.theme.Danger.Render(value)
				suffix = "  " + m.theme.Danger.Render(ui.SetupPortInUse)
			}
		}
		lines = append(lines, marker+labels[index], "  "+value+suffix)
	}
	return lines
}

// probeEndpoint reports the bindability of a loopback address via a short-lived
// net.Listen that closes immediately (design §7.2). Success → free; EADDRINUSE →
// occupied; any other error (permissions) → unknown — the socket never contends
// with core startup.
func probeEndpoint(addr string) portState {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		if isAddrInUse(err) {
			return portOccupied
		}
		return portUnknown
	}
	_ = listener.Close()
	return portFree
}

// isAddrInUse reports whether a listen error means the address is already bound.
// Cross-platform: EADDRINUSE on Unix, WSAEADDRINUSE (10048) on Windows. Go's
// syscall.EADDRINUSE constant does not equal the raw Windows socket errno
// (10048 vs 536870914), so both values are checked explicitly.
func isAddrInUse(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == syscall.EADDRINUSE || errno == 10048
}

// findAvailablePorts rewrites occupied endpoints to the next free port (walking
// port+1 up to +1024), leaving free/unknown ports unchanged. Results stay mutually
// distinct so validateEndpoints' collision rule still holds. If no free port is
// found within the cap the input is left as-is (design §8) — never an invalid value.
func findAvailablePorts(current [3]string) [3]string {
	var states [3]portState
	for i, addr := range current {
		states[i] = probeEndpoint(addr)
	}
	return findAvailablePortsForStates(current, states)
}

func findAvailablePortsForStates(current [3]string, states [3]portState) [3]string {
	result := current
	used := make(map[uint16]bool)
	for _, addr := range current {
		if parsed, err := netip.ParseAddrPort(addr); err == nil {
			used[parsed.Port()] = true
		}
	}
	for index, addr := range current {
		parsed, err := netip.ParseAddrPort(addr)
		if err != nil {
			continue
		}
		if states[index] != portOccupied {
			continue
		}
		host := parsed.Addr()
		for offset := 1; offset <= 1024; offset++ {
			port := int(parsed.Port()) + offset
			if port > 65535 {
				break
			}
			candidate := uint16(port)
			if used[candidate] {
				continue
			}
			candidateAddr := netip.AddrPortFrom(host, candidate).String()
			if probeEndpoint(candidateAddr) == portFree {
				result[index] = candidateAddr
				used[candidate] = true
				break
			}
		}
	}
	return result
}

func (m *Model) installCore() tea.Cmd {
	revision := m.status.Revision
	executionCtx, gen, operationID := m.beginExecution("Installing mihomo core")
	operation := logging.OperationMetadata{ID: operationID, Name: "core.install"}
	return func() tea.Msg {
		ctx := logging.WithOperation(executionCtx, operation)
		result, err := m.client.InstallCore(ctx, protocol.MutationRequest{OperationID: operationID, IfRevision: &revision, Source: "setup"})
		// Capture the install outcome so stepReview can summarize "本地已有/新装/安装失败".
		// The cmd→channel→Update path provides the happens-before guarantee (design §7.4).
		return actionResultMsg{gen: gen, core: &result, next: stepSubscription, revision: result.Revision, operation: operation, err: err}
	}
}

func (m *Model) addSubscription(name, url string) tea.Cmd {
	revision := m.status.Revision
	ctx, gen, operationID := m.beginExecution("Saving and fetching subscription")
	return func() tea.Msg {
		result, err := m.client.AddSubscription(ctx, protocol.SubscriptionAddRequest{OperationID: operationID, IfRevision: &revision, Name: name, URL: url})
		// Capture the added subscription so stepReview shows its name, not the
		// "未添加（已跳过）" fallback. See installCore for the happens-before note.
		subscription := result.Subscription
		var saved *protocol.Subscription
		if err == nil {
			saved = &subscription
		}
		return actionResultMsg{gen: gen, subscription: saved, next: stepGeoIP, revision: result.Revision, err: err}
	}
}

func (m *Model) updateGeoIP() tea.Cmd {
	revision := m.status.Revision
	executionCtx, gen, operationID := m.beginExecution("Preparing Country and ASN databases")
	operation := logging.OperationMetadata{ID: operationID, Name: "geoip.update"}
	ctx := logging.WithOperation(executionCtx, operation)
	return func() tea.Msg {
		result, err := m.client.UpdateGeoIP(ctx, protocol.MutationRequest{OperationID: operationID, IfRevision: &revision, Source: "setup"})
		// Capture the update outcome so stepReview shows "Country ✓ ASN ✓" or "更新失败".
		// Copied by value; the runtime result is not retained. See installCore for the note.
		resultCopy := result
		return actionResultMsg{gen: gen, geoip: &resultCopy, operation: operation, next: stepReview, revision: result.Revision, err: err}
	}
}

// fetchCoreLocal probes GET /v1/core before deciding whether setup can reuse or
// install a core. Failed reads require a read-only retry, not a blind install.
// The generation guard rejects older probe results.
func (m *Model) fetchCoreLocal() tea.Cmd {
	m.coreLocalGen++
	gen := m.coreLocalGen
	return func() tea.Msg {
		status, err := m.client.Core(m.ctx)
		return coreLocalResultMsg{gen: gen, status: status, err: err}
	}
}

// fetchGeoIPLocal probes GET /v1/geoip/status for the stepGeoIP reuse hint.
func (m *Model) fetchGeoIPLocal() tea.Cmd {
	m.geoipLocalGen++
	gen := m.geoipLocalGen
	return func() tea.Msg {
		status, err := m.client.GeoIPStatus(m.ctx)
		return geoipLocalResultMsg{gen: gen, status: status, err: err}
	}
}

// fetchServiceStatus probes GET /v1/service/status for the stepReview 服务 row.
// Advisory only: failures set serviceErr and the row renders "未知".
func (m *Model) fetchServiceStatus() tea.Cmd {
	m.serviceGen++
	gen := m.serviceGen
	return func() tea.Msg {
		status, err := m.client.ServiceStatus(m.ctx)
		return serviceStatusMsg{gen: gen, status: status, err: err}
	}
}

// coreSummary renders the Core review row: version plus source label, or the
// install-failed fallback when no version was recorded.
func (m *Model) coreSummary() string {
	if m.coreResult.Version == "" {
		return ui.SetupReviewCoreFailed
	}
	source := ui.SetupReviewCoreLocal
	if m.coreResult.Updated {
		source = ui.SetupReviewCoreFresh
	}
	return m.coreResult.Version + "  " + source
}

// subscriptionSummary renders the Subscription review row: the added
// subscription name, or the skipped fallback when none was added.
func (m *Model) subscriptionSummary() string {
	if len(m.subscriptions.Subscriptions) > 0 {
		return m.subscriptionCounts()
	}
	if m.addedSubscription != nil && m.addedSubscription.Name != "" {
		name := m.safeText(m.addedSubscription.Name)
		if m.addedSubscription.ID != "" && (!m.addedSubscription.Cached || m.addedSubscription.LastError != "") {
			return name + " · not ready; refresh in Subscriptions"
		}
		return name
	}
	return ui.SetupReviewSubscriptionNone
}

// geoipSummary renders the GeoIP review row: ready, skipped, or failed.
func (m *Model) geoipSummary() string {
	if m.geoipSkipped {
		return ui.SetupReviewGeoIPSkipped
	}
	if m.geoipResult == nil {
		return ui.SetupReviewGeoIPFailed
	}
	if m.geoipResult.Status.Country.Error != "" || m.geoipResult.Status.ASN.Error != "" {
		return ui.SetupReviewGeoIPFailed
	}
	if m.geoipResult.Status.Country.Available && m.geoipResult.Status.ASN.Available {
		return ui.SetupReviewGeoIPReady
	}
	return ui.SetupReviewGeoIPFailed
}

// serviceSummary renders the 服务 review row, mapping the daemon's literal
// service status to localized copy. Loading/unknown stay on fallbacks.
func (m *Model) serviceSummary() string {
	if !m.serviceLoaded {
		return ui.LoadingLabel
	}
	if m.serviceErr {
		return ui.SetupReviewServiceUnknown
	}
	switch m.serviceStatus.Status {
	case "running":
		return "running"
	case "stopped":
		return "stopped"
	case "not_installed":
		return ui.SetupReviewServiceNotRegistered
	default:
		return ui.SetupReviewServiceUnknown
	}
}

// restartSuffix appends the "（需重启生效）" hint to the Web endpoint row when
// the user changed endpoints and the daemon reports a restart is required.
func (m *Model) restartSuffix() string {
	if m.endpointsChanged() && m.status.RestartRequired {
		return "  " + ui.SetupReviewRestartRequired
	}
	return ""
}

func (m *Model) complete() tea.Cmd {
	revision, complete := m.status.Revision, true
	ctx, gen, operationID := m.beginExecution("Finishing setup")
	request := protocol.OnboardingUpdateRequest{
		OperationID: operationID, IfRevision: &revision, Complete: &complete,
	}
	return func() tea.Msg {
		status, err := m.client.UpdateOnboarding(ctx, request)
		return completeResultMsg{gen: gen, status: status, err: err}
	}
}

var fallbackOperationID atomic.Uint64

func defaultOperationID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "tui-setup-" + hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("tui-setup-%d", fallbackOperationID.Add(1))
}
