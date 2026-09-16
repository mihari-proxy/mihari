package connections

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Result contracts retain original causes and committed warnings for the shell.
func (m preferencesResultMsg) Err() error                        { return m.err }
func (m geoIPResultMsg) Err() error                              { return m.err }
func (m preferencesResultMsg) Warnings() protocol.WarningOutcome { return m.preferences.WarningOutcome }

func (m closeResultMsg) Warnings() protocol.WarningOutcome { return m.warnings }

// DiagnosticPage preserves the producer when results arrive after navigation.
func (m preferencesResultMsg) DiagnosticPage() ui.PageID { return ui.PageConnections }
func (m geoIPResultMsg) DiagnosticPage() ui.PageID       { return ui.PageConnections }
func (m closeResultMsg) DiagnosticPage() ui.PageID       { return ui.PageConnections }
