package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync/atomic"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
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
	status protocol.OnboardingStatus
	err    error
}

type actionResultMsg struct {
	next     step
	revision uint64
	err      error
}

// coreLocalResultMsg carries an advisory local-core readiness probe for stepCore.
// A stale generation (step re-entered) or non-nil err leaves the step on its static
// copy (design §4.4) — local detection never blocks onboarding.
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

type completeStartMsg struct{}

type completeResultMsg struct {
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
	coreLocal          protocol.CoreStatus
	coreLocalLoaded    bool
	coreLocalGen       uint64
	geoipLocal         protocol.GeoIPStatus
	geoipLocalLoaded   bool
	geoipLocalGen      uint64
	width              int
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

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }

func (m *Model) FocusFirst() {
	if len(m.inputs) > 0 {
		m.focusEndpoint(0)
	}
}

func (m *Model) Load() tea.Cmd {
	if m.client == nil {
		return nil
	}
	return func() tea.Msg {
		status, err := m.client.Onboarding(m.ctx)
		return onboardingResultMsg{status: status, err: err}
	}
}

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	switch typed := message.(type) {
	case onboardingResultMsg:
		m.loading = false
		if typed.err != nil {
			m.lastError = ui.SetupUnavailable
			return m, nil
		}
		m.status, m.initial = typed.status, typed.status
		m.inputs = endpointInputs(typed.status)
		m.subscriptionInputs = subscriptionInputs()
		m.focusEndpoint(0)
		return m, nil
	case actionResultMsg:
		m.loading = false
		if typed.err != nil {
			var apiError protocol.APIError
			if errors.As(typed.err, &apiError) && apiError.Code == protocol.CodeRevisionConflict {
				m.lastError = ui.SetupChangedMessage
				m.loading = true
				return m, m.Load()
			}
			m.lastError = ui.SetupActionFailed
			return m, nil
		}
		m.lastError = ""
		if typed.revision > 0 {
			m.status.Revision = typed.revision
		}
		m.step = typed.next
		switch m.step {
		case stepCore:
			return m, m.fetchCoreLocal()
		case stepSubscription:
			m.focusSubscription(0)
		case stepGeoIP:
			return m, m.fetchGeoIPLocal()
		}
		return m, nil
	case coreLocalResultMsg:
		if typed.gen != m.coreLocalGen {
			return m, nil
		}
		if typed.err == nil {
			m.coreLocal = typed.status
			m.coreLocalLoaded = true
		}
		return m, nil
	case geoipLocalResultMsg:
		if typed.gen != m.geoipLocalGen {
			return m, nil
		}
		if typed.err == nil {
			m.geoipLocal = typed.status
			m.geoipLocalLoaded = true
		}
		return m, nil
	case completeStartMsg:
		m.loading = true
		return m, m.complete()
	case completeResultMsg:
		m.loading = false
		if typed.err != nil {
			var apiError protocol.APIError
			if errors.As(typed.err, &apiError) && apiError.Code == protocol.CodeRevisionConflict {
				m.lastError = ui.SetupChangedMessage
				m.loading = true
				return m, m.Load()
			}
			m.lastError = ui.SetupCompletionFailed
			return m, nil
		}
		m.status = typed.status
		return m, func() tea.Msg { return CompletedMsg{Status: typed.status} }
	}

	if m.loading {
		return m, nil
	}
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		// Bracketed paste, clipboard results, and textinput blink updates must reach focused fields.
		return m.forwardTextInput(message)
	}
	if len(m.inputs) == 0 && m.step != stepSubscription {
		return m, nil
	}
	if key.String() == "esc" {
		if m.step > stepEndpoints {
			m.step--
			return m, nil
		}
		return m, func() tea.Msg { return CancelledMsg{} }
	}
	if key.String() == "q" && m.step != stepEndpoints && m.step != stepSubscription {
		return m, tea.Quit
	}
	switch m.step {
	case stepEndpoints:
		return m.updateEndpoints(message, key)
	case stepCore:
		if key.String() == "enter" {
			m.loading = true
			return m, m.installCore()
		}
	case stepSubscription:
		return m.updateSubscription(message, key)
	case stepGeoIP:
		if key.String() == "s" {
			m.step = stepReview
			return m, nil
		}
		if key.String() == "enter" {
			m.loading = true
			return m, m.updateGeoIP()
		}
	case stepReview:
		if key.String() == "enter" {
			if m.endpointsChanged() {
				return m, func() tea.Msg {
					return ui.ActionIntentMsg{
						Action: ui.ActionApplyEndpointChange, Page: ui.PageSetup, Capability: protocol.CapabilityOnboarding, Key: "setup:endpoints",
						Title: ui.ReplaceConfigurationTitle, Object: ui.LocalEndpointsLabel,
						Impact: ui.ReplaceConfigurationImpact, Rollback: ui.ReplaceConfigurationRollback,
						Execute: m.complete(),
					}
				}
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
		m.lastError = ""
		m.step = stepCore
		return m, m.fetchCoreLocal()
	}
	updated, command := m.inputs[m.focusedField].Update(message)
	m.inputs[m.focusedField] = updated
	return m, command
}

func (m *Model) updateSubscription(message tea.Msg, key tea.KeyPressMsg) (ui.Page, tea.Cmd) {
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
	if m.loading && len(m.inputs) == 0 {
		return m.theme.Title.Render(ui.SetupTitle) + "\n\n" + ui.LoadingLabel
	}
	lines := []string{m.theme.Title.Render(ui.SetupTitle), m.theme.Muted.Render(fmt.Sprintf(ui.SetupProgress, int(m.step)+1, 5)), ""}
	switch m.step {
	case stepEndpoints:
		lines = append(lines, ui.LocalEndpointsLabel)
		lines = append(lines, renderInputs([]string{"Mixed", "Controller", "Web"}, m.inputs, m.focusedField)...)
		lines = append(lines, "", ui.SetupEndpointHelp)
	case stepCore:
		lines = append(lines, ui.SetupCoreTitle, ui.SetupCoreBody)
		if m.coreLocalLoaded {
			if m.coreLocal.LocalReady {
				version := m.coreLocal.LocalVersion
				if version == "" {
					version = "unknown"
				}
				lines = append(lines, "", fmt.Sprintf(ui.SetupCoreLocalReady, version))
			} else {
				lines = append(lines, "", ui.SetupCoreWillDownload)
			}
		}
		lines = append(lines, "", ui.SetupEnterInstall)
	case stepSubscription:
		lines = append(lines, ui.SetupSubscriptionTitle, ui.SetupSubscriptionBody)
		lines = append(lines, renderInputs([]string{"Name", "URL"}, m.subscriptionInputs, m.focusedField)...)
		lines = append(lines, "", ui.SetupSubscriptionHelp)
	case stepGeoIP:
		lines = append(lines, ui.SetupGeoIPTitle, ui.SetupGeoIPBody)
		if m.geoipLocalLoaded {
			if m.geoipLocal.Country.Available && m.geoipLocal.ASN.Available {
				lines = append(lines, "", ui.SetupGeoIPLocalReady)
			} else {
				lines = append(lines, "", ui.SetupGeoIPWillDownload)
			}
		}
		lines = append(lines, "", ui.SetupEnterOrSkip)
	case stepReview:
		mixed, controller, web := m.endpointValues()
		lines = append(lines, ui.SetupReviewTitle,
			"Mixed       "+mixed, "Controller  "+controller, "Web         "+web, "",
			ui.SetupCompleteHelp)
	}
	if m.loading {
		lines = append(lines, "", ui.LoadingLabel)
	}
	if m.lastError != "" {
		lines = append(lines, "", m.lastError)
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

func (m *Model) installCore() tea.Cmd {
	revision, operationID := m.status.Revision, m.newOperationID()
	return func() tea.Msg {
		result, err := m.client.InstallCore(m.ctx, protocol.MutationRequest{OperationID: operationID, IfRevision: &revision, Source: "setup"})
		return actionResultMsg{next: stepSubscription, revision: result.Revision, err: err}
	}
}

func (m *Model) addSubscription(name, url string) tea.Cmd {
	revision, operationID := m.status.Revision, m.newOperationID()
	return func() tea.Msg {
		result, err := m.client.AddSubscription(m.ctx, protocol.SubscriptionAddRequest{OperationID: operationID, IfRevision: &revision, Name: name, URL: url})
		return actionResultMsg{next: stepGeoIP, revision: result.Revision, err: err}
	}
}

func (m *Model) updateGeoIP() tea.Cmd {
	revision, operationID := m.status.Revision, m.newOperationID()
	return func() tea.Msg {
		result, err := m.client.UpdateGeoIP(m.ctx, protocol.MutationRequest{OperationID: operationID, IfRevision: &revision, Source: "setup"})
		return actionResultMsg{next: stepReview, revision: result.Revision, err: err}
	}
}

// fetchCoreLocal probes GET /v1/core for the stepCore "use existing" hint. Advisory
// only: failures leave coreLocalLoaded=false and the step renders its static copy.
// The generation guard rejects results from a prior step entry.
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

func (m *Model) complete() tea.Cmd {
	mixed, controller, web := m.endpointValues()
	revision, operationID, complete := m.status.Revision, m.newOperationID(), true
	request := protocol.OnboardingUpdateRequest{
		OperationID: operationID, IfRevision: &revision, Complete: &complete,
		MixedAddr: &mixed, ControllerAddr: &controller, WebAddr: &web,
	}
	return func() tea.Msg {
		status, err := m.client.UpdateOnboarding(m.ctx, request)
		return completeResultMsg{status: status, err: err}
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
