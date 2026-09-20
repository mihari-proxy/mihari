package proxies

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Result contracts retain original causes and committed warnings for the shell.
func (m delayResultMsg) Err() error { return m.err }

// DiagnosticErrors excludes only cancellation belonging to this test's context.
func (m delayResultMsg) DiagnosticErrors() []error {
	if m.cancelled || m.err == nil {
		return nil
	}
	return []error{m.err}
}

func (m selectionResultMsg) Warnings() protocol.WarningOutcome { return m.warnings }
func (m routingResultMsg) Warnings() protocol.WarningOutcome   { return m.status.WarningOutcome }
func (m selectionResultMsg) DiagnosticPage() ui.PageID         { return ui.PageProxies }
func (m routingResultMsg) DiagnosticPage() ui.PageID           { return ui.PageProxies }
func (m delayResultMsg) DiagnosticPage() ui.PageID             { return ui.PageProxies }
