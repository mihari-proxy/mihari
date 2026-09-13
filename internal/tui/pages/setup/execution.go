package setup

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type operationObserver interface {
	OperationStatus(context.Context, string) (protocol.OperationStatus, error)
}
type subscriptionReader interface {
	Subscriptions(context.Context) (protocol.SubscriptionList, error)
	RefreshSubscription(context.Context, string, protocol.MutationRequest) (protocol.SubscriptionResult, error)
}

type cancelExecutionMsg struct{ gen uint64 }
type settlementMsg struct {
	unsupported   bool
	gen           uint64
	confirmed     bool
	status        protocol.OnboardingStatus
	core          *protocol.CoreStatus
	geoip         *protocol.GeoIPStatus
	subscriptions protocol.SubscriptionList
	err           error
}

// beginExecution replaces the owned request and assigns a generation and operation ID.
func (m *Model) beginExecution(label string) (context.Context, uint64, string) {
	if m.cancelExecution != nil {
		m.cancelExecution()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancelExecution = cancel
	m.executionGen++
	m.operationID = m.newOperationID()
	m.executionLabel, m.executionStarted, m.executionNow = label, time.Now(), time.Now()
	m.cancelRequested, m.settling, m.resultUnknown = false, false, false
	m.clearFailure()
	m.loading = true
	return ctx, m.executionGen, m.operationID
}

// Busy reports work owned by this page, for the shared shell spinner.
func (m *Model) Busy() bool { return m.loading }

// ObserveTime advances presentation time without changing task ownership.
func (m *Model) ObserveTime(now time.Time) { m.executionNow = now }

// Stop cancels outstanding work when the owning TUI exits.
func (m *Model) Stop() {
	if m.cancelExecution != nil {
		m.cancelExecution()
	}
	if m.cancelSettlement != nil {
		m.cancelSettlement()
	}
}

// exitPrompt explains which committed resources survive leaving setup.
func (m *Model) exitPrompt() tea.Cmd {
	return func() tea.Msg {
		return ui.ConfirmationRequestMsg{Title: "Exit setup?", Impact: "Saved ports, installed core/databases and registered subscriptions are kept. Unconfirmed edits are not saved.", Rollback: "Next launch checks required resources and resumes missing setup.", OnConfirm: tea.Quit}
	}
}

// cancelPrompt binds cancellation confirmation to the current execution generation.
func (m *Model) cancelPrompt() tea.Cmd {
	gen := m.executionGen
	return func() tea.Msg {
		return ui.ConfirmationRequestMsg{
			Title: "Stop this operation?", Impact: "Request cancellation and check the saved result before continuing.",
			Rollback: "Already committed resources will be kept.", OnConfirm: func() tea.Msg { return cancelExecutionMsg{gen: gen} },
		}
	}
}

// settle waits for observed completion before reading saved state; unknown never proves failure.
func (m *Model) settle() tea.Cmd {
	client, owner, id, gen := m.client, m.ctx, m.operationID, m.executionGen
	if m.cancelSettlement != nil {
		m.cancelSettlement()
	}
	ctx, cancel := context.WithTimeout(owner, 15*time.Second)
	m.cancelSettlement = cancel
	current := m.step
	unsupported := m.statusUnsupported
	return func() tea.Msg {
		defer cancel()
		result := settlementMsg{gen: gen}
		observer, ok := client.(operationObserver)
		if !ok || unsupported {
			result.unsupported = true
			return result
		}
		for {
			status, err := observer.OperationStatus(ctx, id)
			if err != nil {
				result.err = err
				return result
			}
			switch status.State {
			case "finished":
				result.confirmed = true
			case "running":
				timer := time.NewTimer(150 * time.Millisecond)
				select {
				case <-timer.C:
					continue
				case <-ctx.Done():
					timer.Stop()
					result.err = ctx.Err()
					return result
				}
			default:
				return result
			}
			break
		}
		result.status, result.err = client.Onboarding(ctx)
		if result.err != nil {
			return result
		}
		switch current {
		case stepCore:
			value, err := client.Core(ctx)
			result.core, result.err = &value, err
		case stepGeoIP:
			value, err := client.GeoIPStatus(ctx)
			result.geoip, result.err = &value, err
		case stepSubscription:
			if reader, ok := client.(subscriptionReader); ok {
				result.subscriptions, result.err = reader.Subscriptions(ctx)
			} else {
				result.confirmed = false
			}
		}
		return result
	}
}

// executionText renders the current action, spinner and elapsed time without estimated progress.
func (m *Model) executionText() string {
	label := m.executionLabel
	if label == "" {
		label = "Reading setup state"
	}
	if m.settling {
		label = "Checking operation settlement"
	} else if m.cancelRequested {
		label = "Cancellation requested; waiting for response"
	}
	elapsed := max(time.Duration(0), m.executionNow.Sub(m.executionStarted))
	if m.executionStarted.IsZero() {
		elapsed = 0
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return fmt.Sprintf("%s %s  %02d:%02d", frames[int(elapsed/(100*time.Millisecond))%len(frames)], label, int(elapsed/time.Minute), int(elapsed/time.Second)%60)
}

// refreshSavedSubscription retries the saved profile through the daemon without adding a duplicate.
func (m *Model) refreshSavedSubscription() tea.Cmd {
	reader, ok := m.client.(subscriptionReader)
	if !ok {
		m.fail("Refresh unavailable", protocol.APIError{Code: protocol.CodeInvalidState, Message: "Reconnect to a compatible daemon to refresh this saved subscription."})
		return nil
	}
	id, revision := m.addedSubscription.ID, m.status.Revision
	ctx, gen, operationID := m.beginExecution("Refreshing saved subscription")
	return func() tea.Msg {
		result, err := reader.RefreshSubscription(ctx, id, protocol.MutationRequest{OperationID: operationID, IfRevision: &revision})
		var profile *protocol.Subscription
		if err == nil {
			profile = &result.Subscription
		}
		return actionResultMsg{gen: gen, next: stepGeoIP, revision: result.Revision, subscription: profile, err: err}
	}
}
