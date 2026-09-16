package setup

import (
	tea "charm.land/bubbletea/v2"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Read and action results expose original causes to the shared shell history.
// Page readiness and settlement still use their individual result fields.
func (m onboardingResultMsg) Err() error { return errors.Join(m.err, m.coreErr, m.subscriptionsErr) }
func (m onboardingResultMsg) DiagnosticErrors() []error {
	return []error{m.err, m.coreErr, m.subscriptionsErr}
}
func (m actionResultMsg) Err() error     { return m.err }
func (m coreLocalResultMsg) Err() error  { return m.err }
func (m geoipLocalResultMsg) Err() error { return m.err }
func (m serviceStatusMsg) Err() error    { return m.err }
func (m endpointsSavedMsg) Err() error   { return m.err }
func (m settlementMsg) Err() error       { return m.err }

func (m onboardingResultMsg) Warnings() protocol.WarningOutcome {
	result := m.status.Clone()
	if m.subscriptions != nil {
		result.Append(m.subscriptions.WarningOutcome)
	}
	return result
}
func (m completeResultMsg) Warnings() protocol.WarningOutcome { return m.status.WarningOutcome }
func (m endpointsSavedMsg) Warnings() protocol.WarningOutcome { return m.status.WarningOutcome }

func (m actionResultMsg) Warnings() protocol.WarningOutcome { return m.warnings }

// DiagnosticPage preserves the producer when results arrive after navigation.
func (m onboardingResultMsg) DiagnosticPage() ui.PageID { return ui.PageSetup }
func (m actionResultMsg) DiagnosticPage() ui.PageID     { return ui.PageSetup }
func (m coreLocalResultMsg) DiagnosticPage() ui.PageID  { return ui.PageSetup }
func (m geoipLocalResultMsg) DiagnosticPage() ui.PageID { return ui.PageSetup }
func (m serviceStatusMsg) DiagnosticPage() ui.PageID    { return ui.PageSetup }
func (m endpointsSavedMsg) DiagnosticPage() ui.PageID   { return ui.PageSetup }
func (m settlementMsg) DiagnosticPage() ui.PageID       { return ui.PageSetup }
func (m completeResultMsg) DiagnosticPage() ui.PageID   { return ui.PageSetup }

// DiagnosticErrors preserves the business outcome for settlement while omitting
// cancellation confirmed by the operation's own context from error history.
func (m actionResultMsg) DiagnosticErrors() []error   { return executionErrors(m.err, m.cancelled) }
func (m endpointsSavedMsg) DiagnosticErrors() []error { return executionErrors(m.err, m.cancelled) }
func (m completeResultMsg) DiagnosticErrors() []error { return executionErrors(m.err, m.cancelled) }
func executionErrors(err error, cancelled bool) []error {
	if cancelled {
		return nil
	}
	return []error{err}
}

func (m settlementMsg) Warnings() protocol.WarningOutcome {
	result := m.status.Clone()
	result.Append(m.subscriptions.WarningOutcome)
	return result
}

// localFailure sends synchronous validation failures through the shared shell.
func (m *Model) localFailure(summary string, err error) tea.Cmd {
	m.fail(summary, err)
	return func() tea.Msg { return ui.DiagnosticMsg{Page: ui.PageSetup, Err: err} }
}

func (m portProbeMsg) DiagnosticErrors() []error { return m.failures }
func (m portProbeMsg) DiagnosticPage() ui.PageID { return ui.PageSetup }
