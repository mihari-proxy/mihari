package system

import (
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"log/slog"
)

// Result contracts retain original causes and committed warnings for the shell.
func (m portHoldsMsg) DiagnosticErrors() []error                 { return m.failures }
func (m portHoldsMsg) DiagnosticPage() ui.PageID                 { return ui.PageSystem }
func (m onboardingResultMsg) Err() error                         { return m.err }
func (m selfCheckResultMsg) Err() error                          { return m.err }
func (m serviceStatusMsg) Err() error                            { return m.err }
func (m systemProxyStatusMsg) Err() error                        { return m.err }
func (m tunStatusMsg) Err() error                                { return m.err }
func (m webGUIStatusMsg) Err() error                             { return m.err }
func (m coreLoadResultMsg) Err() error                           { return m.err }
func (m loggingUpdateResultMsg) Err() error                      { return m.err }
func (m loggingReloadResultMsg) Err() error                      { return m.err }
func (m preparedMihariResultMsg) Err() error                     { return m.err }
func (m onboardingResultMsg) Warnings() protocol.WarningOutcome  { return m.status.WarningOutcome }
func (m systemProxyStatusMsg) Warnings() protocol.WarningOutcome { return m.status.WarningOutcome }
func (m systemProxyActionResultMsg) Warnings() protocol.WarningOutcome {
	return m.status.WarningOutcome
}
func (m tunStatusMsg) Warnings() protocol.WarningOutcome        { return m.status.WarningOutcome }
func (m tunActionResultMsg) Warnings() protocol.WarningOutcome  { return m.status.WarningOutcome }
func (m webGUIStatusMsg) Warnings() protocol.WarningOutcome     { return m.status.WarningOutcome }
func (m portsApplyResultMsg) Warnings() protocol.WarningOutcome { return m.status.WarningOutcome }
func (m actionResultMsg) Warnings() protocol.WarningOutcome {
	result := m.install.Clone()
	result.Append(m.restart.WarningOutcome)
	return result
}

// DiagnosticPage preserves the producer when results arrive after navigation.
func (m onboardingResultMsg) DiagnosticPage() ui.PageID        { return ui.PageSystem }
func (m selfCheckResultMsg) DiagnosticPage() ui.PageID         { return ui.PageSystem }
func (m serviceStatusMsg) DiagnosticPage() ui.PageID           { return ui.PageSystem }
func (m systemProxyStatusMsg) DiagnosticPage() ui.PageID       { return ui.PageSystem }
func (m tunStatusMsg) DiagnosticPage() ui.PageID               { return ui.PageSystem }
func (m webGUIStatusMsg) DiagnosticPage() ui.PageID            { return ui.PageSystem }
func (m coreLoadResultMsg) DiagnosticPage() ui.PageID          { return ui.PageSystem }
func (m loggingUpdateResultMsg) DiagnosticPage() ui.PageID     { return ui.PageSystem }
func (m loggingReloadResultMsg) DiagnosticPage() ui.PageID     { return ui.PageSystem }
func (m preparedMihariResultMsg) DiagnosticPage() ui.PageID    { return ui.PageSystem }
func (m mihariChannelResultMsg) DiagnosticPage() ui.PageID     { return ui.PageSystem }
func (m uninstallPreviewMsg) DiagnosticPage() ui.PageID        { return ui.PageSystem }
func (m serviceResultMsg) DiagnosticPage() ui.PageID           { return ui.PageSystem }
func (m systemProxyActionResultMsg) DiagnosticPage() ui.PageID { return ui.PageSystem }
func (m webGUIOpenResultMsg) DiagnosticPage() ui.PageID        { return ui.PageSystem }
func (m tunActionResultMsg) DiagnosticPage() ui.PageID         { return ui.PageSystem }
func (m actionResultMsg) DiagnosticPage() ui.PageID            { return ui.PageSystem }
func (m portsApplyResultMsg) DiagnosticPage() ui.PageID        { return ui.PageSystem }

// localFailure publishes an actual synchronous failure and returns the same
// occurrence to the shell, including when no file logger is available.
func (m *Model) localFailure(operation, summary string, cause error) tea.Cmd {
	ctx := m.localTaskDiagnostics.NewContext(m.ctx, operation)
	failure := diagnostics.ReportError(ctx, m.localTaskDiagnostics.Reporter, diagnostics.Record{
		Component: "tui.system", Event: operation + ".failed", Level: slog.LevelError, Summary: summary, Err: cause,
	})
	return func() tea.Msg { return ui.DiagnosticMsg{Page: ui.PageSystem, Err: failure} }
}

func (m preparedMihariResultMsg) DiagnosticErrors() []error {
	if m.cancelled {
		return nil
	}
	return []error{m.err}
}
