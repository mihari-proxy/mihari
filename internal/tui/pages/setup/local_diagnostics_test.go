package setup

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"testing"
)

func TestSetupValidation_PublishesGlobalDiagnostic(t *testing.T) {
	for _, kind := range []string{"endpoint", "subscription"} {
		t.Run(kind, func(t *testing.T) {
			m := loadedModel(&fakeClient{status: defaultStatus(false)})
			if kind == "endpoint" {
				m.inputs[0].SetValue("invalid token=fixture-port")
			} else {
				m.step = stepSubscription
				m.subscriptionInputs = subscriptionInputs()
				m.subscriptionInputs[0].SetValue("fixture")
			}
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("validation failure has no global diagnostic")
			}
			msg, ok := cmd().(ui.DiagnosticMsg)
			if !ok || msg.Page != ui.PageSetup || msg.Err == nil {
				t.Fatalf("diagnostic = %#v", msg)
			}
			if m.lastError == "" || m.errorDetail == "" {
				t.Fatal("inline summary or detail missing")
			}
		})
	}
}

func TestSetupCancellation_OnlySuppressesOwnedCancellation(t *testing.T) {
	for _, joined := range []bool{false, true} {
		cause := error(context.Canceled)
		if joined {
			cause = errors.Join(cause, errors.New("cleanup token=fixture-cleanup"))
		}
		client := &fakeClient{status: defaultStatus(false), installErr: cause}
		m := loadedModel(client)
		cmd := m.installCore()
		m.cancelExecution()
		result := cmd().(actionResultMsg)
		var reported []error
		if outcome, ok := any(result).(interface{ DiagnosticErrors() []error }); ok {
			reported = outcome.DiagnosticErrors()
		} else {
			reported = []error{result.Err()}
		}
		count := 0
		for _, err := range reported {
			if err != nil {
				count++
			}
		}
		if !joined && count != 0 {
			t.Fatal("owned cancellation became an error record")
		}
		if joined && (count != 1 || !strings.Contains(reported[0].Error(), "fixture-cleanup")) {
			t.Fatal("cleanup failure was suppressed")
		}
		if !errors.Is(result.Err(), context.Canceled) {
			t.Fatal("settlement lost its cancellation cause")
		}
	}
}

func TestSetupSettlement_PreservesReadbackWarnings(t *testing.T) {
	warning := protocol.Warning{Message: "readback token=fixture-warning"}
	result := settlementMsg{status: protocol.OnboardingStatus{WarningOutcome: protocol.WarningOutcome{Warnings: []protocol.Warning{warning}}}, subscriptions: protocol.SubscriptionList{WarningOutcome: protocol.WarningOutcome{Warnings: []protocol.Warning{warning}}}}
	outcome, ok := any(result).(interface {
		Warnings() protocol.WarningOutcome
	})
	if !ok || len(outcome.Warnings().Warnings) != 2 {
		t.Fatal("settlement dropped readback warnings")
	}
}

func TestSetupPortProbe_RetainsOriginalFailure(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.probe = nil
	m.inputs[0].SetValue("127.0.0.1:fixture-invalid-port")
	m.inputs[1].SetValue("127.0.0.1:0")
	m.inputs[2].SetValue("127.0.0.1:0")
	result := m.probePorts()()
	outcome, ok := result.(interface{ DiagnosticErrors() []error })
	if !ok {
		t.Fatal("port probe discarded bind failure")
	}
	failures := outcome.DiagnosticErrors()
	if len(failures) != 1 || !strings.Contains(failures[0].Error(), "fixture-invalid-port") {
		t.Fatalf("failures = %v", failures)
	}
	if result.(portProbeMsg).results[0] != portUnknown {
		t.Fatal("probe failure changed availability classification")
	}
}

func TestSetupSavedSubscription_PublishesPartialFailure(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.step = stepSubscription
	_, cmd := m.Update(actionResultMsg{subscription: &protocol.Subscription{ID: "fixture-saved", LastError: "fetch token=fixture-partial"}, revision: 42})
	if cmd == nil {
		t.Fatal("saved subscription download failure missing from global history")
	}
	result, ok := cmd().(ui.DiagnosticMsg)
	if !ok || result.Diagnostic == nil || result.Diagnostic.Severity != "warning" || !strings.Contains(result.Diagnostic.Detail, "fixture-partial") {
		t.Fatalf("partial failure = %#v", result)
	}
	if m.addedSubscription == nil || m.addedSubscription.ID != "fixture-saved" || m.status.Revision != 42 {
		t.Fatal("diagnostic discarded successful registration")
	}
}
