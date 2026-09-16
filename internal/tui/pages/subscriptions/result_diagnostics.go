package subscriptions

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Result contracts retain original causes and committed warnings for the shell.
func (m subscriptionsResultMsg) Err() error                        { return m.err }
func (m revealResultMsg) Err() error                               { return m.err }
func (m saveCheckMsg) Err() error                                  { return m.err }
func (m subscriptionsResultMsg) Warnings() protocol.WarningOutcome { return m.result.WarningOutcome }
func (m saveCheckMsg) Warnings() protocol.WarningOutcome           { return m.list.WarningOutcome }
func (m mutationResultMsg) Warnings() protocol.WarningOutcome {
	result := m.result.Clone()
	result.Append(m.remove.WarningOutcome)
	return result
}

func (m refreshAllResultMsg) Warnings() protocol.WarningOutcome { return m.warnings }

// DiagnosticPage preserves the producer when results arrive after navigation.
func (m subscriptionsResultMsg) DiagnosticPage() ui.PageID { return ui.PageSubscriptions }
func (m revealResultMsg) DiagnosticPage() ui.PageID        { return ui.PageSubscriptions }
func (m saveCheckMsg) DiagnosticPage() ui.PageID           { return ui.PageSubscriptions }
func (m mutationResultMsg) DiagnosticPage() ui.PageID      { return ui.PageSubscriptions }
func (m refreshAllResultMsg) DiagnosticPage() ui.PageID    { return ui.PageSubscriptions }

func (m mutationResultMsg) DiagnosticErrors() []error {
	if m.cancelled || m.err == nil {
		return nil
	}
	return []error{m.err}
}

func (m refreshAllResultMsg) DiagnosticErrors() []error {
	if m.cancelled || m.err == nil {
		return nil
	}
	return []error{m.err}
}

func (m revealResultMsg) DiagnosticErrors() []error {
	if m.cancelled || m.err == nil {
		return nil
	}
	return []error{m.err}
}

func (m saveCheckMsg) DiagnosticErrors() []error {
	if m.cancelled || m.err == nil {
		return nil
	}
	return []error{m.err}
}
