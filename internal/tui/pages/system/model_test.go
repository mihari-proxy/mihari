package system

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/elevate"
	"github.com/LeeShunEE/mihari/internal/service"
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

	systemProxy       protocol.SystemProxyStatus
	systemProxyCalls  int
	enableProxyCalls  int
	disableProxyCalls int
	lastProxyMutation protocol.SystemProxyMutationRequest
	enableProxyErr    error
	disableProxyErr   error

	tun             protocol.TunStatus
	tunCalls        int
	enableTunCalls  int
	disableTunCalls int
	lastTunMutation protocol.TunMutationRequest
	enableTunErr    error
	disableTunErr   error
}

type fakeService struct {
	installs   int
	uninstalls int
	starts     int
	stops      int
	restarts   int
	reinstalls int
	status     service.StatusKind
	statusErr  error
	controlErr error
}

func (f *fakeService) Install() error {
	f.installs++
	return f.controlErr
}
func (f *fakeService) Uninstall() error {
	f.uninstalls++
	return f.controlErr
}
func (f *fakeService) Reinstall() error {
	f.reinstalls++
	return f.controlErr
}
func (f *fakeService) Start() error {
	f.starts++
	return f.controlErr
}
func (f *fakeService) Stop() error {
	f.stops++
	return f.controlErr
}
func (f *fakeService) Restart() error {
	f.restarts++
	return f.controlErr
}
func (f *fakeService) Status() (service.StatusKind, error) { return f.status, f.statusErr }

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
func (f *fakeClient) SystemProxy(context.Context) (protocol.SystemProxyStatus, error) {
	f.systemProxyCalls++
	return f.systemProxy, nil
}
func (f *fakeClient) EnableSystemProxy(_ context.Context, request protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error) {
	f.enableProxyCalls++
	f.lastProxyMutation = request
	if f.enableProxyErr != nil {
		return protocol.SystemProxyStatus{}, f.enableProxyErr
	}
	status := f.systemProxy
	status.Desired = true
	status.Observed = protocol.SystemProxyObserved{Enabled: true, Server: status.Target, Owned: true}
	if status.Revision == 0 {
		status.Revision = 1
	} else {
		status.Revision++
	}
	f.systemProxy = status
	return status, nil
}
func (f *fakeClient) DisableSystemProxy(_ context.Context, request protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error) {
	f.disableProxyCalls++
	f.lastProxyMutation = request
	if f.disableProxyErr != nil {
		return protocol.SystemProxyStatus{}, f.disableProxyErr
	}
	status := f.systemProxy
	status.Desired = false
	status.Observed = protocol.SystemProxyObserved{}
	if status.Revision == 0 {
		status.Revision = 1
	} else {
		status.Revision++
	}
	f.systemProxy = status
	return status, nil
}
func (f *fakeClient) Tun(context.Context) (protocol.TunStatus, error) {
	f.tunCalls++
	return f.tun, nil
}
func (f *fakeClient) EnableTun(_ context.Context, request protocol.TunMutationRequest) (protocol.TunStatus, error) {
	f.enableTunCalls++
	f.lastTunMutation = request
	if f.enableTunErr != nil {
		return protocol.TunStatus{}, f.enableTunErr
	}
	live := true
	status := f.tun
	status.DesiredEnable = true
	status.LiveEnable = &live
	status.Managed = true
	if status.Stack == "" {
		status.Stack = "gVisor"
	}
	if status.Revision == 0 {
		status.Revision = 1
	} else {
		status.Revision++
	}
	f.tun = status
	return status, nil
}
func (f *fakeClient) DisableTun(_ context.Context, request protocol.TunMutationRequest) (protocol.TunStatus, error) {
	f.disableTunCalls++
	f.lastTunMutation = request
	if f.disableTunErr != nil {
		return protocol.TunStatus{}, f.disableTunErr
	}
	live := false
	status := f.tun
	status.DesiredEnable = false
	status.LiveEnable = &live
	status.Managed = true
	if status.Revision == 0 {
		status.Revision = 1
	} else {
		status.Revision++
	}
	f.tun = status
	return status, nil
}

func withElevation(t *testing.T, elevated bool) {
	t.Helper()
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return elevated }
}

func boolPtr(v bool) *bool { return &v }

func TestView_FocusedRowHighlightOnlyWhenContentFocused(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 24)
	model.SetSnapshot(protocol.Status{DaemonVersion: "1.0.0"}, protocol.CoreStatus{Status: "running"})
	model.focusID = rowDaemon

	model.SetContentFocused(false)
	railView := model.View()
	if !strings.Contains(railView, ui.FocusMarker) {
		t.Fatalf("row marker missing while rail-focused:\n%s", railView)
	}

	model.SetContentFocused(true)
	contentView := model.View()
	if railView == contentView {
		t.Fatal("content focus should add row focus styling")
	}
	if !strings.Contains(contentView, ui.FocusMarker) || !strings.Contains(contentView, "\x1b[") {
		t.Fatalf("focused content row missing highlight:\n%s", contentView)
	}
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

func TestSystemRendersCategorizedRowsWithoutStopDaemon(t *testing.T) {
	client := &fakeClient{onboarding: protocol.OnboardingStatus{Revision: 7, MixedAddr: "127.0.0.1:9190", ControllerAddr: "127.0.0.1:9090", WebAddr: "127.0.0.1:9191"}}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{DaemonVersion: "v0.4.0", Health: "ok", Revision: 7, Capabilities: []string{protocol.CapabilityCore, protocol.CapabilityOnboarding}}, protocol.CoreStatus{Status: "running", Version: "v1.19.0", PID: 42, Restarts: 2})
	updated, _ := model.Update(onboardingResultMsg{status: client.onboarding})
	model = updated.(*Model)
	view := model.View()
	for _, want := range []string{"Daemon", "v0.4.0", "mihomo core", "v1.19.0", "Local endpoints", "127.0.0.1:9190", "Maintenance", "Run Setup", "TUN", "Unavailable"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view=%s", want, view)
		}
	}
	if strings.Contains(view, "Stop Daemon") {
		t.Fatalf("system page offered destructive self-stop: %s", view)
	}
}

func TestSystemServiceRendersStatusAndActionsWhenControllerPresent(t *testing.T) {
	withElevation(t, false)
	svc := &fakeService{status: service.StatusRunning}
	model := NewWithService(&fakeClient{}, svc, func() string { return "system-op" })
	updated, _ := model.Update(serviceStatusMsg{status: service.StatusRunning, elevated: false})
	model = updated.(*Model)
	view := model.View()
	for _, want := range []string{ui.SystemServiceSectionTitle, ui.ServiceStatusLabel, string(service.StatusRunning), ui.ServiceInstallLabel, ui.ServiceUninstallLabel, ui.ServiceReinstallLabel, ui.ServiceStartLabel, ui.ServiceStopLabel, ui.ServiceRestartLabel, ui.ServiceNeedsElevation} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view=%s", want, view)
		}
	}
	if strings.Contains(view, ui.ServiceUnavailableDetail) {
		t.Fatalf("service still unavailable: %s", view)
	}
}

func TestSystemServiceWithoutControllerShowsUnavailable(t *testing.T) {
	model := New(nil, nil)
	view := model.View()
	if !strings.Contains(view, ui.SystemServiceSectionTitle) || !strings.Contains(view, ui.UnavailableTitle) {
		t.Fatalf("view=%s", view)
	}
	if strings.Contains(view, ui.ServiceInstallLabel) {
		t.Fatalf("actions offered without controller: %s", view)
	}
}

func TestSystemServiceLoadRefreshesStatusAndElevation(t *testing.T) {
	withElevation(t, true)
	svc := &fakeService{status: service.StatusStopped}
	model := NewWithService(nil, svc, nil)
	cmd := model.Load()
	if cmd == nil {
		t.Fatal("Load should query service status")
	}
	msg := cmd()
	// may be batch or single
	switch typed := msg.(type) {
	case serviceStatusMsg:
		if typed.status != service.StatusStopped || !typed.elevated {
			t.Fatalf("msg=%#v", typed)
		}
	case tea.BatchMsg:
		found := false
		for _, item := range typed {
			if status, ok := item().(serviceStatusMsg); ok {
				found = true
				if status.status != service.StatusStopped || !status.elevated {
					t.Fatalf("status=%#v", status)
				}
			}
		}
		if !found {
			t.Fatalf("batch missing service status: %#v", typed)
		}
	default:
		t.Fatalf("unexpected Load msg %T %#v", msg, msg)
	}
}

func TestSystemServiceMutationRequiresElevation(t *testing.T) {
	withElevation(t, false)
	svc := &fakeService{status: service.StatusNotInstalled}
	model := NewWithService(nil, svc, func() string { return "system-op" })
	updated, _ := model.Update(serviceStatusMsg{status: service.StatusNotInstalled, elevated: false})
	model = updated.(*Model)
	model.focusID = rowServiceInstall
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if cmd != nil || svc.installs != 0 {
		t.Fatalf("cmd=%v installs=%d view=%s", cmd != nil, svc.installs, model.View())
	}
	if !strings.Contains(model.View(), ui.ServiceElevationRequired) {
		t.Fatalf("view=%s", model.View())
	}
}

func TestSystemServiceReinstallConfirmsWhenElevated(t *testing.T) {
	withElevation(t, true)
	svc := &fakeService{status: service.StatusRunning}
	model := NewWithService(nil, svc, func() string { return "op" })
	updated, _ := model.Update(serviceStatusMsg{status: service.StatusRunning, elevated: true})
	model = updated.(*Model)
	model.focusID = rowServiceReinstall
	model.SetContentFocused(true)
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil {
		t.Fatal("missing confirmation intent")
	}
	intent, ok := command().(ui.ActionIntentMsg)
	if !ok || intent.Action != ui.ActionServiceReinstall || intent.Title != ui.ServiceReinstallTitle {
		t.Fatalf("intent=%#v", intent)
	}
	// Simulate root ActionPending then execute.
	updated, spin := model.Update(ui.ActionPendingMsg{Action: ui.ActionServiceReinstall})
	model = updated.(*Model)
	if !model.pending || model.pendingRow != rowServiceReinstall || model.pendingNote != ui.ServiceProgressReinstalling {
		t.Fatalf("pending row=%q note=%q pending=%v", model.pendingRow, model.pendingNote, model.pending)
	}
	if spin == nil {
		t.Fatal("expected row spin cmd")
	}
	model.rowSpinClock = time.Unix(0, 0)
	view := model.View()
	if !strings.Contains(view, ui.ServiceProgressReinstalling) || !strings.Contains(view, "⠋") {
		t.Fatalf("view missing in-row braille progress:\n%s", view)
	}
	updated, _ = model.Update(intent.Execute())
	model = updated.(*Model)
	if svc.reinstalls != 1 || model.pending {
		t.Fatalf("reinstalls=%d pending=%v", svc.reinstalls, model.pending)
	}
	if model.outcomeRow != rowServiceReinstall || !model.outcomeOK {
		t.Fatalf("outcome row=%q ok=%v", model.outcomeRow, model.outcomeOK)
	}
	view = model.View()
	if !strings.Contains(view, ui.DoneLabel) {
		t.Fatalf("view missing Done badge:\n%s", view)
	}
	model.ClearDone()
	if model.outcomeRow != "" || strings.Contains(model.View(), ui.DoneLabel) {
		t.Fatalf("ClearDone left sticky badge: outcomeRow=%q", model.outcomeRow)
	}
}

func TestSystemServiceUninstallFailureShowsStickyFailed(t *testing.T) {
	withElevation(t, true)
	svc := &fakeService{status: service.StatusRunning, controlErr: errors.New("access is denied")}
	model := NewWithService(nil, svc, func() string { return "op" })
	updated, _ := model.Update(serviceStatusMsg{status: service.StatusRunning, elevated: true})
	model = updated.(*Model)
	model.focusID = rowServiceUninstall
	updated, _ = model.Update(ui.ActionPendingMsg{Action: ui.ActionServiceUninstall})
	model = updated.(*Model)
	result := serviceResultMsg{kind: serviceUninstall, err: errors.New("access is denied")}
	updated, _ = model.Update(result)
	model = updated.(*Model)
	if model.outcomeRow != rowServiceUninstall || model.outcomeOK {
		t.Fatalf("outcome row=%q ok=%v", model.outcomeRow, model.outcomeOK)
	}
	view := model.View()
	if !strings.Contains(view, ui.FailedLabel) {
		t.Fatalf("view missing Failed badge:\n%s", view)
	}
	if model.outcomeDetail == "" {
		t.Fatal("expected outcomeDetail reason")
	}
	// Reason must appear near the top (under title), not only below the fold.
	titleIdx := strings.Index(view, ui.SystemTitle)
	detailIdx := strings.Index(view, model.outcomeDetail)
	if titleIdx < 0 || detailIdx < 0 || detailIdx < titleIdx {
		t.Fatalf("failure reason not pinned under title:\n%s", view)
	}
}

func TestSystemServiceInstallConfirmsAndExecutesWhenElevated(t *testing.T) {
	withElevation(t, true)
	svc := &fakeService{status: service.StatusNotInstalled}
	model := NewWithService(nil, svc, func() string { return "system-op" })
	updated, _ := model.Update(serviceStatusMsg{status: service.StatusNotInstalled, elevated: true})
	model = updated.(*Model)
	model.focusID = rowServiceInstall
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if cmd == nil || svc.installs != 0 {
		t.Fatalf("cmd=%v installs=%d", cmd != nil, svc.installs)
	}
	intent, ok := cmd().(ui.ActionIntentMsg)
	if !ok || intent.Action != ui.ActionServiceInstall || intent.Title != ui.ServiceInstallTitle || intent.Execute == nil {
		t.Fatalf("intent=%#v", intent)
	}
	// Execute runs the install; feed result into the page.
	result := intent.Execute()
	updated, reload := model.Update(result)
	model = updated.(*Model)
	if svc.installs != 1 {
		t.Fatalf("installs=%d result=%#v", svc.installs, result)
	}
	if reload == nil {
		t.Fatal("expected status reload after install")
	}
	// Simulate successful status refresh.
	updated, _ = model.Update(serviceStatusMsg{status: service.StatusStopped, elevated: true})
	model = updated.(*Model)
	if !strings.Contains(model.View(), string(service.StatusStopped)) {
		t.Fatalf("view=%s", model.View())
	}
}

func TestSystemServiceStopRequiresConfirmationWithDisconnectImpact(t *testing.T) {
	withElevation(t, true)
	svc := &fakeService{status: service.StatusRunning}
	model := NewWithService(nil, svc, func() string { return "system-op" })
	updated, _ := model.Update(serviceStatusMsg{status: service.StatusRunning, elevated: true})
	model = updated.(*Model)
	model.focusID = rowServiceStop
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("missing confirmation")
	}
	intent := cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionServiceStop {
		t.Fatalf("intent=%#v", intent)
	}
	lower := strings.ToLower(intent.Impact)
	if !strings.Contains(lower, "disconnect") && !strings.Contains(lower, "control") {
		t.Fatalf("stop impact missing control-channel warning: %q", intent.Impact)
	}
}

func TestSystemServiceActionsWorkWhileDaemonDisconnected(t *testing.T) {
	withElevation(t, true)
	svc := &fakeService{status: service.StatusStopped}
	model := NewWithService(nil, svc, func() string { return "system-op" })
	model.SetMutationsEnabled(false)
	updated, _ := model.Update(serviceStatusMsg{status: service.StatusStopped, elevated: true})
	model = updated.(*Model)
	model.focusID = rowServiceStart
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("service start should work without daemon mutations")
	}
	intent := cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionServiceStart || intent.Capability != "" {
		t.Fatalf("intent=%#v", intent)
	}
	_ = intent.Execute()
	if svc.starts != 1 {
		t.Fatalf("starts=%d", svc.starts)
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

func TestSystemRendersSystemProxyAndTunStatusWhenCapabilitiesPresent(t *testing.T) {
	client := &fakeClient{
		systemProxy: protocol.SystemProxyStatus{
			Revision: 3, Desired: true, Target: "127.0.0.1:9190",
			Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:9190", Owned: true},
		},
		tun: protocol.TunStatus{
			Revision: 3, DesiredEnable: true, LiveEnable: boolPtr(true), Stack: "gVisor", Managed: true,
		},
	}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{
		Capabilities: []string{protocol.CapabilitySystemProxy, protocol.CapabilityTUN},
	}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	model.SetSystemProxy(client.systemProxy)
	model.SetTun(client.tun)
	view := model.View()
	for _, want := range []string{
		ui.NetworkSectionTitle, ui.SystemProxyLabel, ui.TUNLabel,
		ui.DesiredLabel, ui.OnLabel, "127.0.0.1:9190", ui.OwnedLabel,
		ui.LiveLabel, "gVisor",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view=%s", want, view)
		}
	}
	if strings.Contains(view, ui.UnavailableTitle) && strings.Contains(view, ui.TUNLabel+"  "+ui.UnavailableTitle) {
		t.Fatalf("TUN still unavailable with capability: %s", view)
	}
}

func TestSystemProxyRowHiddenWithoutCapability(t *testing.T) {
	model := New(&fakeClient{}, nil)
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityTUN}}, protocol.CoreStatus{})
	model.SetTun(protocol.TunStatus{DesiredEnable: false, Managed: false})
	view := model.View()
	if strings.Contains(view, ui.SystemProxyLabel) {
		t.Fatalf("system proxy row without capability: %s", view)
	}
	if !strings.Contains(view, ui.TUNLabel) {
		t.Fatalf("TUN row missing: %s", view)
	}
}

func TestSystemLoadFetchesSystemProxyAndTun(t *testing.T) {
	client := &fakeClient{
		systemProxy: protocol.SystemProxyStatus{Revision: 2, Target: "127.0.0.1:9190"},
		tun:         protocol.TunStatus{Revision: 2, Stack: "gVisor"},
	}
	model := New(client, nil)
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilitySystemProxy, protocol.CapabilityTUN}}, protocol.CoreStatus{})
	cmd := model.Load()
	if cmd == nil {
		t.Fatal("Load should fetch proxy and tun")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("want batch, got %T", msg)
	}
	var sawProxy, sawTun bool
	for _, item := range batch {
		switch typed := item().(type) {
		case systemProxyStatusMsg:
			sawProxy = true
			if typed.status.Target != "127.0.0.1:9190" || typed.err != nil {
				t.Fatalf("proxy=%#v", typed)
			}
		case tunStatusMsg:
			sawTun = true
			if typed.status.Stack != "gVisor" || typed.err != nil {
				t.Fatalf("tun=%#v", typed)
			}
		}
	}
	if !sawProxy || !sawTun || client.systemProxyCalls != 1 || client.tunCalls != 1 {
		t.Fatalf("proxy=%v tun=%v proxyCalls=%d tunCalls=%d", sawProxy, sawTun, client.systemProxyCalls, client.tunCalls)
	}
}

func TestSystemProxyEnableConfirmsAndExecutes(t *testing.T) {
	client := &fakeClient{
		systemProxy: protocol.SystemProxyStatus{Revision: 4, Desired: false, Target: "127.0.0.1:9190"},
	}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilitySystemProxy}}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	model.SetSystemProxy(client.systemProxy)
	model.focusID = rowSystemProxy
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("missing enable confirmation")
	}
	intent := cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionEnableSystemProxy || intent.Title != ui.EnableSystemProxyTitle {
		t.Fatalf("intent=%#v", intent)
	}
	result := intent.Execute()
	updated, reload := model.Update(result)
	model = updated.(*Model)
	if client.enableProxyCalls != 1 || client.lastProxyMutation.Force || client.lastProxyMutation.OperationID != "system-op" {
		t.Fatalf("mutation=%#v calls=%d", client.lastProxyMutation, client.enableProxyCalls)
	}
	if client.lastProxyMutation.IfRevision == nil || *client.lastProxyMutation.IfRevision != 4 {
		t.Fatalf("revision=%v", client.lastProxyMutation.IfRevision)
	}
	if reload == nil || !model.systemProxy.Desired {
		t.Fatalf("desired=%v reload=%v", model.systemProxy.Desired, reload != nil)
	}
}

func TestSystemProxyForeignEnterRequestsForceOverwrite(t *testing.T) {
	client := &fakeClient{
		systemProxy: protocol.SystemProxyStatus{
			Revision: 5, Desired: false, Target: "127.0.0.1:9190",
			Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:7890", Foreign: true},
		},
	}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilitySystemProxy}}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	model.SetSystemProxy(client.systemProxy)
	model.focusID = rowSystemProxy
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("missing force confirmation")
	}
	intent := cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionForceSystemProxy {
		t.Fatalf("intent=%#v", intent)
	}
	if !strings.Contains(intent.Impact, "127.0.0.1:7890") || !strings.Contains(intent.Impact, "127.0.0.1:9190") {
		t.Fatalf("force impact=%q", intent.Impact)
	}
	_ = intent.Execute()
	if client.enableProxyCalls != 1 || !client.lastProxyMutation.Force {
		t.Fatalf("mutation=%#v", client.lastProxyMutation)
	}
}

func TestSystemProxyEnableConflictEntersForceConfirmPath(t *testing.T) {
	client := &fakeClient{
		systemProxy: protocol.SystemProxyStatus{Revision: 6, Desired: false, Target: "127.0.0.1:9190"},
		enableProxyErr: protocol.APIError{
			Code:    protocol.CodeSystemProxyConflict,
			Message: "system proxy is managed by another application",
			Details: map[string]any{"current_server": "127.0.0.1:7890", "target_server": "127.0.0.1:9190"},
		},
	}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilitySystemProxy}}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	model.SetSystemProxy(client.systemProxy)
	// Simulate enable without force returning conflict.
	updated, cmd := model.Update(systemProxyActionResultMsg{
		kind: proxyEnable,
		err: protocol.APIError{
			Code:    protocol.CodeSystemProxyConflict,
			Message: "system proxy is managed by another application",
			Details: map[string]any{"current_server": "127.0.0.1:7890", "target_server": "127.0.0.1:9190"},
		},
	})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("conflict should open force confirmation")
	}
	intent := cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionForceSystemProxy || intent.Title != ui.ForceSystemProxyTitle {
		t.Fatalf("intent=%#v", intent)
	}
	if !strings.Contains(intent.Impact, "127.0.0.1:7890") || !strings.Contains(intent.Impact, "127.0.0.1:9190") {
		t.Fatalf("impact=%q", intent.Impact)
	}
	// Clear error so force succeeds.
	client.enableProxyErr = nil
	result := intent.Execute()
	updated, _ = model.Update(result)
	model = updated.(*Model)
	if client.enableProxyCalls != 1 || !client.lastProxyMutation.Force {
		t.Fatalf("force mutation=%#v calls=%d", client.lastProxyMutation, client.enableProxyCalls)
	}
	if model.lastError != "" {
		t.Fatalf("lastError=%q", model.lastError)
	}
}

func TestSystemProxyDisableForeignShowsNotOwnedError(t *testing.T) {
	client := &fakeClient{
		systemProxy: protocol.SystemProxyStatus{
			Revision: 7, Desired: true, Target: "127.0.0.1:9190",
			Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:7890", Foreign: true},
		},
		disableProxyErr: protocol.APIError{
			Code:    protocol.CodeSystemProxyNotOwned,
			Message: "system proxy is managed by another application; Mihari will not clear it",
		},
	}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilitySystemProxy}}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	model.SetSystemProxy(client.systemProxy)
	// Simulate disable result against foreign proxy (stale client or race).
	updated, cmd := model.Update(systemProxyActionResultMsg{
		kind: proxyDisable,
		err: protocol.APIError{
			Code:    protocol.CodeSystemProxyNotOwned,
			Message: "system proxy is managed by another application; Mihari will not clear it",
		},
	})
	model = updated.(*Model)
	if !strings.Contains(model.View(), ui.SystemProxyNotOwnedMessage) {
		t.Fatalf("view=%s", model.View())
	}
	if client.disableProxyCalls != 0 {
		t.Fatalf("disable should not be reissued on error path, calls=%d", client.disableProxyCalls)
	}
	// Reload is optional; if present, drain it.
	if cmd != nil {
		_ = cmd()
	}
}

func TestSystemProxyOwnedEnterConfirmsDisable(t *testing.T) {
	client := &fakeClient{
		systemProxy: protocol.SystemProxyStatus{
			Revision: 8, Desired: true, Target: "127.0.0.1:9190",
			Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:9190", Owned: true},
		},
	}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilitySystemProxy}}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	model.SetSystemProxy(client.systemProxy)
	model.focusID = rowSystemProxy
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	intent := cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionDisableSystemProxy {
		t.Fatalf("intent=%#v", intent)
	}
	_ = intent.Execute()
	if client.disableProxyCalls != 1 {
		t.Fatalf("disableCalls=%d", client.disableProxyCalls)
	}
}

func TestSystemTunToggleEnableDisableWithConfirm(t *testing.T) {
	client := &fakeClient{
		tun: protocol.TunStatus{Revision: 9, DesiredEnable: false, LiveEnable: boolPtr(false), Managed: false},
	}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityTUN}}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	model.SetTun(client.tun)
	model.focusID = rowTUN
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	intent := cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionEnableTun || intent.Title != ui.EnableTunTitle {
		t.Fatalf("intent=%#v", intent)
	}
	result := intent.Execute()
	updated, _ = model.Update(result)
	model = updated.(*Model)
	if client.enableTunCalls != 1 || !model.tun.DesiredEnable {
		t.Fatalf("enable calls=%d desired=%v", client.enableTunCalls, model.tun.DesiredEnable)
	}

	model.focusID = rowTUN
	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	intent = cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionDisableTun {
		t.Fatalf("intent=%#v", intent)
	}
	result = intent.Execute()
	updated, _ = model.Update(result)
	model = updated.(*Model)
	if client.disableTunCalls != 1 || model.tun.DesiredEnable {
		t.Fatalf("disable calls=%d desired=%v", client.disableTunCalls, model.tun.DesiredEnable)
	}
}

func TestSystemProxyAndTunMutationsDisabledWhileDisconnected(t *testing.T) {
	client := &fakeClient{
		systemProxy: protocol.SystemProxyStatus{Target: "127.0.0.1:9190"},
		tun:         protocol.TunStatus{},
	}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilitySystemProxy, protocol.CapabilityTUN}}, protocol.CoreStatus{})
	model.SetMutationsEnabled(false)
	model.SetSystemProxy(client.systemProxy)
	model.SetTun(client.tun)
	for _, id := range []string{rowSystemProxy, rowTUN} {
		model.focusID = id
		updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		model = updated.(*Model)
		if cmd != nil {
			t.Fatalf("row=%s offered mutation while disconnected", id)
		}
	}
}

func updateKey(t *testing.T, model *Model, key tea.KeyPressMsg) *Model {
	t.Helper()
	updated, _ := model.Update(key)
	return updated.(*Model)
}
