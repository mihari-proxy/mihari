package rules

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Result contracts retain original causes and committed warnings for the shell.
func (m rulesResultMsg) Err() error          { return m.err }
func (m providersResultMsg) Err() error      { return m.err }
func (m providerUpdateResultMsg) Err() error { return m.err }

func (m providerUpdateResultMsg) Warnings() protocol.WarningOutcome     { return m.warnings }
func (m providersUpdateAllResultMsg) Warnings() protocol.WarningOutcome { return m.warnings }

// DiagnosticPage preserves the producer when results arrive after navigation.
func (m rulesResultMsg) DiagnosticPage() ui.PageID              { return ui.PageRules }
func (m providersResultMsg) DiagnosticPage() ui.PageID          { return ui.PageRules }
func (m providerUpdateResultMsg) DiagnosticPage() ui.PageID     { return ui.PageRules }
func (m providersUpdateAllResultMsg) DiagnosticPage() ui.PageID { return ui.PageRules }
