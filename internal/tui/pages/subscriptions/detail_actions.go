package subscriptions

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

// Detail actions apply to committed catalog state, independently of the settings draft.
func (m *Model) updateDetailActionLabels() {
	f := m.form
	i := m.index(m.formID)
	if i < 0 {
		f.inputs[5].SetValue("Unavailable")
		f.inputs[6].SetValue("Unavailable")
		return
	}
	p := m.subscriptions[i]
	enabled := "[ Enable ]"
	if p.Enabled {
		enabled = "[ Disable ]"
	}
	inUse := "[ Use this subscription ]"
	switch {
	case p.ID == m.activeID:
		inUse = "In use"
	case !p.Enabled:
		inUse = "Enable first"
	case !p.Cached:
		inUse = "Refresh first"
	}
	f.inputs[5].SetValue(enabled)
	f.inputs[6].SetValue(inUse)
	if f.actionOperation != "" && f.isAction() {
		f.inputs[f.index].SetValue("Applying...")
	}
}

func (m *Model) submitDetailAction() tea.Cmd {
	i := m.index(m.formID)
	if m.client == nil || i < 0 || m.disconnected || m.pending[m.formID] != "" {
		return nil
	}
	p := m.subscriptions[i]
	kind, name, pending := mutationToggle, "subscription.enabled", "toggle"
	if m.form.labels[m.form.index] == "InUse" {
		if p.ID == m.activeID || !p.Enabled || !p.Cached {
			return nil
		}
		kind, name, pending = mutationUse, "subscription.use", "use"
	}
	op := logging.OperationMetadata{ID: m.newOperationID(), Name: name}
	epoch, revision, client := m.dialogEpoch, m.revision, m.client
	owner, cancelOwner := m.newContext()
	ctx, cancelTimeout := context.WithTimeout(owner, 15*time.Second)
	cancel := func() { cancelTimeout(); cancelOwner() }
	m.requestCancels[op.ID] = cancel
	m.form.actionOperation = op.ID
	m.form.errorText = ""
	m.pending[p.ID] = pending
	m.ensureFormFocus()
	return tea.Batch(func() tea.Msg {
		defer cancel()
		ctx := logging.WithOperation(ctx, op)
		var result protocol.SubscriptionResult
		var err error
		if kind == mutationUse {
			result, err = client.UseSubscription(ctx, p.ID, protocol.MutationRequest{OperationID: op.ID, IfRevision: &revision})
		} else {
			result, err = client.SetSubscriptionEnabled(ctx, p.ID, protocol.SubscriptionEnabledRequest{OperationID: op.ID, IfRevision: &revision, Enabled: !p.Enabled})
		}
		return mutationResultMsg{detailEpoch: epoch, requestRevision: revision, cancelled: diagnostics.NormalCancellation(ctx, err), kind: kind, id: p.ID, result: result, operation: op, err: err}
	}, m.loadSpinCmdIfNeeded())
}

func (m *Model) finishDetailAction(msg mutationResultMsg) tea.Cmd {
	delete(m.pending, msg.id)
	currentDialog := m.form != nil && m.dialogEpoch == msg.detailEpoch && m.form.actionOperation == msg.operation.ID
	if currentDialog {
		m.form.actionOperation = ""
	}
	if msg.err != nil {
		m.lastError = subscriptionErrorMessage(msg.err)
		var api protocol.APIError
		unknown := !errors.As(msg.err, &api) || api.Code == protocol.CodeDaemonUnavailable || errors.Is(msg.err, context.DeadlineExceeded)
		var outcome interface{ OutcomeUnknown() bool }
		if errors.As(msg.err, &outcome) {
			unknown = outcome.OutcomeUnknown()
		}
		if currentDialog {
			m.form.errorText = m.lastError
			if unknown {
				m.form.actionUncertain = true
				m.form.errorText = "Action outcome unknown. Close and reopen details to check the current state before retrying. " + m.lastError
			}
			m.ensureFormFocus()
			// Keep the start of failure feedback visible even in a short window.
			layout := m.formLayout()
			lastField := layout.fields[len(layout.fields)-1]
			m.dialogScroll = max(0, min(lastField.last+2, len(layout.lines)-layout.bodyHeight))
			m.dialogManualScroll = true
		}
		if unknown || api.Code == protocol.CodeRevisionConflict {
			return tea.Batch(m.reload(), m.loadSpinCmdIfNeeded())
		}
		return m.loadSpinCmdIfNeeded()
	}
	// A newer catalog observation wins over a late action response.
	if msg.result.Revision >= m.revision {
		m.revision = msg.result.Revision
		m.upsert(msg.result.Subscription)
		if msg.kind == mutationUse {
			m.activeID = msg.id
		} else if !msg.result.Subscription.Enabled && m.activeID == msg.id {
			m.activeID = ""
		}
		// Only our own action may advance the draft revision. An intervening
		// external change must still trigger the existing Save conflict flow.
		if currentDialog && m.formRevision == msg.requestRevision {
			m.formRevision = msg.result.Revision
		}
	}
	m.lastError = ""
	if currentDialog {
		m.ensureFormFocus()
	}
	return m.loadSpinCmdIfNeeded()
}
