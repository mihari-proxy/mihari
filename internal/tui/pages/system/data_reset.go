package system

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"time"
)

type resetClient interface {
	DataResetPreview(context.Context) (protocol.ResetPreview, error)
	ResetBusinessData(context.Context, protocol.DataResetRequest) (protocol.DataResetResult, error)
}
type resetPreviewMsg struct {
	preview protocol.ResetPreview
	err     error
}

func (m resetPreviewMsg) Err() error { return m.err }

type resetResultMsg struct {
	result protocol.DataResetResult
	err    error
}

func (m resetResultMsg) Err() error                        { return m.err }
func (m resetResultMsg) Warnings() protocol.WarningOutcome { return m.result.WarningOutcome }
func (m *Model) previewDataReset() tea.Cmd {
	api, ok := m.client.(resetClient)
	if !ok || m.pending || !m.mutationsEnabled {
		return nil
	}
	ctx := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		p, err := api.DataResetPreview(ctx)
		return ui.PageResultMsg{Page: ui.PageSystem, Result: resetPreviewMsg{preview: p, err: err}}
	}
}
func (m *Model) confirmDataReset(p protocol.ResetPreview) tea.Cmd {
	return func() tea.Msg {
		return ui.ConfirmationRequestMsg{
			Title: "Reset Mihari Data", Object: diagnostics.EscapeTerminal(strings.Join(p.Targets, "\n")),
			Impact:   "Clear subscriptions, settings, proxy authentication and preferences. Keep program, service, downloads, logs and exports.",
			Rollback: "Business data cannot be restored by Mihari",
			OnConfirm: func() tea.Msg {
				return ui.ConfirmationRequestMsg{
					Title: "Confirm business data reset again", Object: "Proxy will stop. Web logins will expire.",
					Impact: "Restart the daemon after reset, then complete Setup.", Rollback: "Subscription sources and configuration must be added again",
					OnConfirm: func() tea.Msg { return resetConfirmedMsg{preview: p} },
				}
			},
		}
	}
}

type resetConfirmedMsg struct{ preview protocol.ResetPreview }

func (m *Model) runDataReset(p protocol.ResetPreview) tea.Cmd {
	api, ok := m.client.(resetClient)
	if !ok || m.pending || !m.mutationsEnabled {
		return nil
	}
	m.pending = true
	m.pendingRow = "data-reset"
	ctx, id := m.ctx, m.newOperationID()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		v, err := api.ResetBusinessData(ctx, protocol.DataResetRequest{OperationID: id, IfRevision: &p.Revision, Digest: p.Digest, Confirm: true, ConfirmAgain: true})
		return ui.PageResultMsg{Page: ui.PageSystem, Result: resetResultMsg{result: v, err: err}}
	}
}
