package system

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"github.com/mihari-proxy/mihari/internal/update"
	"strings"
)

type preparedMihariResultMsg struct {
	generation uint64
	channel    string
	prepared   update.PreparedUpdate
	err        error
}
type preparedMihariConfirmedMsg struct {
	generation uint64
	prepared   update.PreparedUpdate
}

func (preparedMihariConfirmedMsg) Err() error { return nil }

type preparedMihariCanceledMsg struct {
	generation uint64
	prepared   update.PreparedUpdate
}

func discardMihari(p update.PreparedUpdate) tea.Cmd {
	return func() tea.Msg { return ui.DiscardPreparedUpdateMsg{Prepared: p} }
}

// CancelMihariPreparation invalidates queued results and cancels inert downloads.
// Run remains the owner that releases any completed candidate.
func (m *Model) CancelMihariPreparation() tea.Cmd {
	m.preparationGeneration++
	if m.preparationCancel != nil {
		m.preparationCancel()
		m.preparationCancel = nil
	}
	p := m.pendingPrepared
	m.pendingPrepared = nil
	if p != nil || (m.pendingRow == rowMihariUpdate && m.pendingNote != ui.MihariProgressChecking) {
		m.clearRowPending()
		m.markRowOutcome(rowMihariUpdate, false, "Update cancelled")
	}
	if p != nil {
		return discardMihari(*p)
	}
	return nil
}

// AcceptsMihariPreparation checks queued shell intents on the model goroutine.
func (m *Model) AcceptsMihariPreparation(key string) bool {
	return m.pendingPrepared != nil && key == fmt.Sprintf("mihari:update:%d", m.preparationGeneration)
}

func (m *Model) startMihariPreparation() tea.Cmd {
	if m.pending || m.pendingPrepared != nil || m.selfUpdater == nil {
		return nil
	}
	m.preparationGeneration++
	generation := m.preparationGeneration
	m.selfCheckGeneration++ // A queued display check must not clear Preparing.
	ctx, cancel := context.WithCancel(m.ctx)
	m.preparationCancel = cancel
	updater, binary, current, channel, elevated := m.selfUpdater, m.binaryPath, m.currentVersion, m.currentMihariChannel(), m.isElevated
	m.pending = true
	m.pendingRow = rowMihariUpdate
	m.pendingNote = ui.MihariProgressPreparing
	m.outcomeRow = ""
	m.outcomeDetail = ""
	m.lastError = ""
	return func() tea.Msg {
		var p update.PreparedUpdate
		var err error
		if elevated == nil || !elevated() {
			err = protocol.APIError{Code: protocol.CodePermissionDenied, Message: "administrator privileges are required; re-run Mihari from an elevated shell"}
		} else {
			p, err = updater.Prepare(ctx, binary, current, channel)
		}
		return ui.PageResultMsg{Page: ui.PageSystem, Result: preparedMihariResultMsg{generation: generation, channel: channel, prepared: p, err: err}}
	}
}
func (m *Model) handlePreparedMihariResult(msg preparedMihariResultMsg) (ui.Page, tea.Cmd) {
	if msg.generation != m.preparationGeneration || msg.channel != m.currentMihariChannel() {
		return m, discardMihari(msg.prepared)
	}
	if m.preparationCancel != nil {
		m.preparationCancel()
		m.preparationCancel = nil
	}
	m.clearRowPending()
	if msg.err != nil {
		m.markRowOutcome(rowMihariUpdate, false, actionErrorDetail(msg.err, ui.UpdateMihariActionFailed))
		return m, discardMihari(msg.prepared)
	}
	if !msg.prepared.Available {
		m.selfCheckLoaded = true
		m.selfCheckResult = update.CheckResult{Current: m.currentVersion, Latest: msg.prepared.Version, Ahead: msg.prepared.Ahead, Channel: msg.prepared.Channel}
		return m, discardMihari(msg.prepared)
	}
	m.pendingPrepared = &msg.prepared
	return m, m.confirmPreparedMihariUpdate(msg.prepared)
}
func (m *Model) confirmPreparedMihariUpdate(p update.PreparedUpdate) tea.Cmd {
	generation := m.preparationGeneration
	impact := ui.UpdateMihariImpact
	if p.Preview.Risk != update.ReplacementNone {
		impact = update.ReplacementWarning(p.Preview) + "\n" + impact
	}
	versions := make([]string, 0, len(p.Preview.Snapshot.Targets))
	for _, target := range p.Preview.Snapshot.Targets {
		roles := make([]string, 0, len(target.Roles))
		for _, role := range target.Roles {
			switch role {
			case "binary", "path", "service", "managed":
				roles = append(roles, role)
			}
		}
		versions = append(versions, fmt.Sprintf("%s: %s", valueOr(strings.Join(roles, "/"), "Mihari"), valueOr(target.Version, ui.UnknownLabel)))
	}
	object := fmt.Sprintf("Mihari %s → %s", strings.Join(versions, "; "), valueOr(p.Preview.Candidate.Version, ui.UnknownLabel))
	return func() tea.Msg {
		return ui.ActionIntentMsg{Action: ui.ActionUpdateMihari, Page: ui.PageSystem, Key: fmt.Sprintf("mihari:update:%d", generation), Title: ui.UpdateMihariTitle, Object: object, Impact: impact, Rollback: ui.UpdateMihariRollback,
			Execute: func() tea.Msg {
				p.Consent = update.ReplacementConsent{Yes: true, ExpectedPreview: p.Preview.ID}
				return preparedMihariConfirmedMsg{generation: generation, prepared: p}
			},
			Cancel: func() tea.Msg {
				return ui.PageResultMsg{Page: ui.PageSystem, Result: preparedMihariCanceledMsg{generation: generation, prepared: p}}
			}}
	}
}
