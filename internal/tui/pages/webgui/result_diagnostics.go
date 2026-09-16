package webgui

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Result contracts retain original causes and committed warnings for the shell.
func (m statusResultMsg) Err() error                        { return m.err }
func (m statusResultMsg) Warnings() protocol.WarningOutcome { return m.status.WarningOutcome }

func (m mutationDoneMsg) Warnings() protocol.WarningOutcome { return m.warnings }

// DiagnosticPage preserves the producer when results arrive after navigation.
func (m statusResultMsg) DiagnosticPage() ui.PageID { return ui.PageWebGUI }
func (m mutationDoneMsg) DiagnosticPage() ui.PageID { return ui.PageWebGUI }
