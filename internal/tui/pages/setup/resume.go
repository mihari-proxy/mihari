package setup

import (
	"context"
	"errors"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// ReadyMsg leaves an automatically resumed setup whose required resources are ready.
type ReadyMsg struct{}
type saveEndpointsStartMsg struct{}
type endpointsSavedMsg struct {
	gen    uint64
	status protocol.OnboardingStatus
	err    error
}

// SetAutomatic selects state-derived recovery versus explicitly opening the wizard.
func (m *Model) SetAutomatic(automatic bool) {
	m.automatic = automatic
	m.resumePending = automatic
	m.step = stepEndpoints
}

// Automatic reports whether setup was entered to repair missing required state.
func (m *Model) Automatic() bool { return m.automatic }

// WaitingRestart reports saved configuration awaiting a new daemon instance.
func (m *Model) WaitingRestart() bool { return m.waitingRestart }

// ObserveDaemon supplies owner identity already available on the authenticated session.
func (m *Model) ObserveDaemon(status protocol.Status, core protocol.CoreStatus) {
	m.statusUnsupported = !slices.Contains(status.Capabilities, protocol.OperationStatusCapability)
	m.portOwners = [3]int{core.PID, core.PID, status.PID}
	m.portRecovery = status.Health == "degraded" && slices.Contains(status.Capabilities, protocol.CapabilityOnboarding) && !slices.Contains(status.Capabilities, protocol.CapabilityCore)
}

func (m *Model) resumeFromState() tea.Cmd {
	m.resumePending = false
	if validateEndpoints(m.endpointValues()) != nil || m.anyPortOccupied() {
		m.step = stepEndpoints
		return nil
	}
	if m.status.RestartRequired {
		m.waitingRestart = true
		m.step = stepEndpoints
		return nil
	}
	if m.portRecovery {
		m.step = stepEndpoints
		return nil
	}
	if !m.coreLocalLoaded {
		m.step = stepCore
		m.fail("Read core state", protocol.APIError{Code: protocol.CodeDaemonUnavailable, Message: "Core readiness could not be confirmed. Recheck before installing."})
		return nil
	}
	if m.coreLocal.LocalReady && m.automatic {
		return func() tea.Msg { return ReadyMsg{} }
	}
	m.step = stepCore
	return nil
}

func (m *Model) saveEndpointsPrompt() tea.Cmd {
	return func() tea.Msg {
		return ui.ConfirmationRequestMsg{Title: "Save local endpoints?", Impact: "Save these ports now, then restart the daemon to apply them before continuing.", Rollback: "Saved ports remain in place if setup is interrupted.", OnConfirm: func() tea.Msg { return saveEndpointsStartMsg{} }}
	}
}

func (m *Model) saveEndpoints() tea.Cmd {
	mixed, controller, web := m.endpointValues()
	revision := m.status.Revision
	ctx, gen, id := m.beginExecution("Saving local endpoints")
	client := m.client
	request := protocol.OnboardingUpdateRequest{OperationID: id, IfRevision: &revision, MixedAddr: &mixed, ControllerAddr: &controller, WebAddr: &web}
	return func() tea.Msg {
		status, err := client.UpdateOnboarding(ctx, request)
		return endpointsSavedMsg{gen: gen, status: status, err: err}
	}
}

func (m *Model) handleEndpointsSaved(msg endpointsSavedMsg) (ui.Page, tea.Cmd) {
	if msg.gen != m.executionGen {
		return m, nil
	}
	if m.cancelExecution != nil {
		m.cancelExecution()
		m.cancelExecution = nil
	}
	if m.cancelRequested || uncertainOutcome(msg.err) {
		m.loading, m.settling = true, true
		return m, m.settle()
	}
	m.loading = false
	if msg.err != nil {
		var api protocol.APIError
		if errors.As(msg.err, &api) && api.Code == protocol.CodeRevisionConflict {
			m.fail(ui.SetupChangedMessage, msg.err)
			return m, m.reloadInPlace()
		}
		m.fail("Save endpoints", msg.err)
		return m, nil
	}
	m.status, m.initial = msg.status, msg.status
	m.waitingRestart = msg.status.RestartRequired || m.portRecovery
	m.clearFailure()
	if !m.waitingRestart {
		m.step = stepCore
		return m, m.fetchCoreLocal()
	}
	return m, nil
}

func (m *Model) reloadInPlace() tea.Cmd {
	m.refreshInPlace = true
	return m.Load()
}

func classifySetupPort(address string, owner int, lookup func(string) (platform.TCPOccupant, bool)) portState {
	state := probeEndpoint(address)
	if state != portOccupied {
		return state
	}
	occupant, ok := lookup(address)
	if !ok || occupant.PID <= 0 {
		return portUnknown
	}
	hold := ui.ClassifyPortHold(false, occupant.PID, occupant.Process, owner)
	if hold.Kind == ui.PortHoldOwned {
		return portOwned
	}
	return portOccupied
}

// loadResources returns values only; page fields remain owned by Update.
func loadResources(ctx context.Context, client Client, result onboardingResultMsg) onboardingResultMsg {
	if core, err := client.Core(ctx); err == nil {
		result.core = &core
	}
	if reader, ok := client.(subscriptionReader); ok {
		list, err := reader.Subscriptions(ctx)
		result.subscriptionsErr = err
		if err == nil {
			result.subscriptions = &list
		}
	}
	return result
}
