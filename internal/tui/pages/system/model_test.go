package system

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

type fakeClient struct {
	onboarding      protocol.OnboardingStatus
	installCalls    int
	restartCalls    int
	lastMutation    protocol.MutationRequest
	onboardingCalls int
	coreCalls       int
	coreStatus      protocol.CoreStatus
}

func (f *fakeClient) Onboarding(context.Context) (protocol.OnboardingStatus, error) {
	f.onboardingCalls++
	return f.onboarding, nil
}
func (f *fakeClient) Core(context.Context) (protocol.CoreStatus, error) {
	f.coreCalls++
	if f.coreStatus.Schema != "" {
		return f.coreStatus, nil
	}
	return protocol.CoreStatus{Schema: "mihari/v1", Revision: f.onboarding.Revision, Status: "running", Version: "v1.19.0"}, nil
}

func TestSystemRevisionConflictReloadsWithoutLosingStableFocus(t *testing.T) {
	client := &fakeClient{onboarding: protocol.OnboardingStatus{Revision: 9}}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore, protocol.CapabilityOnboarding}}, protocol.CoreStatus{})
	model.focusID = rowCoreUpdate
	model.pending = true
	updated, command := model.Update(actionResultMsg{kind: actionUpdate, err: protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "changed"}})
	model = updated.(*Model)
	if command == nil || model.pending || model.focusID != rowCoreUpdate || !strings.Contains(model.View(), ui.SystemChangedMessage) {
		t.Fatalf("command=%v pending=%v focus=%q view=%s", command != nil, model.pending, model.focusID, model.View())
	}
	batch, ok := command().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("reload message=%T %#v", command(), command())
	}
	for _, reload := range batch {
		updated, _ = model.Update(reload())
		model = updated.(*Model)
	}
	if client.onboardingCalls != 1 || client.coreCalls != 1 || model.focusID != rowCoreUpdate {
		t.Fatalf("onboarding=%d core=%d focus=%q", client.onboardingCalls, client.coreCalls, model.focusID)
	}
}
func (f *fakeClient) InstallCore(_ context.Context, request protocol.MutationRequest) (protocol.CoreInstallResult, error) {
	f.installCalls++
	f.lastMutation = request
	return protocol.CoreInstallResult{Schema: "mihari/v1", Revision: f.onboarding.Revision + 1, Version: "v1.20.0", Updated: true}, nil
}
func (f *fakeClient) RestartCore(_ context.Context, request protocol.MutationRequest) (protocol.MutationResult, error) {
	f.restartCalls++
	f.lastMutation = request
	return protocol.MutationResult{Schema: "mihari/v1", Revision: f.onboarding.Revision + 1}, nil
}

func TestSystemRendersCategorizedRowsWithoutStopDaemon(t *testing.T) {
	client := &fakeClient{onboarding: protocol.OnboardingStatus{Revision: 7, MixedAddr: "127.0.0.1:9190", ControllerAddr: "127.0.0.1:9090", WebAddr: "127.0.0.1:9191"}}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{DaemonVersion: "v0.4.0", Health: "ok", Revision: 7, Capabilities: []string{protocol.CapabilityCore, protocol.CapabilityOnboarding}}, protocol.CoreStatus{Status: "running", Version: "v1.19.0", PID: 42, Restarts: 2})
	updated, _ := model.Update(onboardingResultMsg{status: client.onboarding})
	model = updated.(*Model)
	view := model.View()
	for _, want := range []string{"Daemon", "v0.4.0", "mihomo core", "v1.19.0", "Local endpoints", "127.0.0.1:9190", "Maintenance", "Run Setup", "TUN", "System service", "Unavailable"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view=%s", want, view)
		}
	}
	if strings.Contains(view, "Stop Daemon") {
		t.Fatalf("system page offered destructive self-stop: %s", view)
	}
}

func TestSystemEnterInspectsRowsAndRoutesEndpointAndSetupToStandaloneSetup(t *testing.T) {
	model := New(&fakeClient{}, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{DaemonVersion: "v0.4.0", Health: "ok", StartedAt: time.Now().Add(-5 * time.Minute), Capabilities: []string{protocol.CapabilityOnboarding}}, protocol.CoreStatus{Status: "running"})
	model.SetMutationsEnabled(true)
	model.focusID = rowDaemon
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(model.View(), "Daemon details") || !strings.Contains(model.View(), "Uptime") {
		t.Fatalf("detail view=%s", model.View())
	}
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEscape})
	for _, id := range []string{rowEndpoints, rowRunSetup} {
		model.focusID = id
		updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		model = updated.(*Model)
		if command == nil {
			t.Fatalf("row=%s missing route command", id)
		}
		message, ok := command().(ui.RouteRequestMsg)
		if !ok || message.Page != ui.PageSetup {
			t.Fatalf("row=%s message=%T %#v", id, command(), command())
		}
	}
}

func TestSystemCoreUpdateAndRestartRequireConfirmationWithCapturedRevision(t *testing.T) {
	client := &fakeClient{onboarding: protocol.OnboardingStatus{Revision: 11}}
	for _, test := range []struct {
		id        string
		wantTitle string
		calls     func() int
	}{
		{rowCoreUpdate, "Update mihomo core", func() int { return client.installCalls }},
		{rowCoreRestart, "Restart mihomo core", func() int { return client.restartCalls }},
	} {
		model := New(client, func() string { return "system-op" })
		model.SetSnapshot(protocol.Status{Revision: 11, Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{Revision: 11, Status: "running", Version: "v1.19.0"})
		model.SetMutationsEnabled(true)
		model.focusID = test.id
		updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		model = updated.(*Model)
		if command == nil || test.calls() != 0 {
			t.Fatalf("row=%s command=%v calls=%d", test.id, command != nil, test.calls())
		}
		intent, ok := command().(ui.ActionIntentMsg)
		if !ok || intent.Title != test.wantTitle || intent.Execute == nil {
			t.Fatalf("row=%s intent=%#v", test.id, intent)
		}
		updated, reconcile := model.Update(intent.Execute())
		model = updated.(*Model)
		if test.calls() != 1 || client.lastMutation.IfRevision == nil || *client.lastMutation.IfRevision != 11 || reconcile == nil {
			t.Fatalf("row=%s calls=%d mutation=%#v", test.id, test.calls(), client.lastMutation)
		}
	}
}

func TestSystemOffersCoreInstallWhenNoVersionIsPresent(t *testing.T) {
	model := New(&fakeClient{}, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Revision: 2, Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{Revision: 2, Status: "missing"})
	model.SetMutationsEnabled(true)
	model.focusID = rowCoreUpdate
	if !strings.Contains(model.View(), ui.InstallCoreLabel) {
		t.Fatalf("view=%s", model.View())
	}
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if command == nil {
		t.Fatal("install confirmation missing")
	}
	intent := command().(ui.ActionIntentMsg)
	if intent.Title != ui.InstallCoreTitle || intent.Action != ui.ActionUpdateCore {
		t.Fatalf("intent=%#v", intent)
	}
}

func TestSystemDisablesMutationsAndSetupRouteWhileDisconnected(t *testing.T) {
	model := New(&fakeClient{}, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore, protocol.CapabilityOnboarding}}, protocol.CoreStatus{Version: "v1.19.0"})
	for _, id := range []string{rowCoreUpdate, rowCoreRestart, rowEndpoints, rowRunSetup} {
		model.focusID = id
		updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		model = updated.(*Model)
		if command != nil {
			t.Fatalf("row=%s remained active while disconnected", id)
		}
	}
}

func TestSystemSuccessfulRestartReconcilesAuthoritativeCore(t *testing.T) {
	client := &fakeClient{onboarding: protocol.OnboardingStatus{Revision: 5}, coreStatus: protocol.CoreStatus{Schema: "mihari/v1", Revision: 6, Status: "running", Version: "v1.19.0", PID: 99, Restarts: 4}}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Revision: 5, Capabilities: []string{protocol.CapabilityCore, protocol.CapabilityOnboarding}}, protocol.CoreStatus{Revision: 5, Status: "running", Version: "v1.19.0", PID: 42, Restarts: 3})
	model.SetMutationsEnabled(true)
	updated, command := model.Update(actionResultMsg{kind: actionRestart, restart: protocol.MutationResult{Revision: 6}})
	model = updated.(*Model)
	if command == nil {
		t.Fatal("successful restart did not request authoritative reconciliation")
	}
	batch := command().(tea.BatchMsg)
	for _, reconcile := range batch {
		updated, _ = model.Update(reconcile())
		model = updated.(*Model)
	}
	if model.core.PID != 99 || model.core.Restarts != 4 || client.coreCalls != 1 {
		t.Fatalf("core=%#v calls=%d", model.core, client.coreCalls)
	}
}

func TestSystemDoesNotOfferCoreMutationWithoutClientOrCapability(t *testing.T) {
	for _, model := range []*Model{New(nil, func() string { return "system-op" }), New(&fakeClient{}, func() string { return "system-op" })} {
		for _, id := range []string{rowCoreUpdate, rowCoreRestart} {
			model.focusID = id
			updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			model = updated.(*Model)
			if command != nil {
				t.Fatalf("row=%s offered mutation without client/capability", id)
			}
		}
	}
}

func updateKey(t *testing.T, model *Model, key tea.KeyPressMsg) *Model {
	t.Helper()
	updated, _ := model.Update(key)
	return updated.(*Model)
}
