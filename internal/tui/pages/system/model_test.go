package system

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"github.com/mihari-proxy/mihari/internal/update"
)

type fakeSelfUpdater struct {
	checkResult  update.CheckResult
	checkErr     error
	updateResult update.Result
	updateErr    error
	checkCalls   int
	updateCalls  int
	lastBinary   string
	lastCurrent  string
}

func (f *fakeSelfUpdater) Check(context.Context, string) (update.CheckResult, error) {
	f.checkCalls++
	return f.checkResult, f.checkErr
}

func (f *fakeSelfUpdater) Update(_ context.Context, binaryPath, currentVersion string) (update.Result, error) {
	f.updateCalls++
	f.lastBinary = binaryPath
	f.lastCurrent = currentVersion
	return f.updateResult, f.updateErr
}

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

	webGUI          protocol.WebGUIStatus
	webGUICalls     int
	webGUIErr       error
	openWebGUICalls int
	lastOpenPanel   string
	openWebGUIErr   error
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
func (f *fakeClient) WebGUI(context.Context) (protocol.WebGUIStatus, error) {
	f.webGUICalls++
	if f.webGUIErr != nil {
		return protocol.WebGUIStatus{}, f.webGUIErr
	}
	return f.webGUI, nil
}
func (f *fakeClient) OpenWebGUI(_ context.Context, panelID string) (protocol.WebGUIOpenResult, error) {
	f.openWebGUICalls++
	f.lastOpenPanel = panelID
	if f.openWebGUIErr != nil {
		return protocol.WebGUIOpenResult{}, f.openWebGUIErr
	}
	return protocol.WebGUIOpenResult{
		Schema:  "mihari/v1",
		OpenURL: "http://127.0.0.1:9191/__mihari/panels/" + panelID + "/?token=test-token",
		Panel:   panelID,
	}, nil
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
	for _, want := range []string{"Daemon", "v0.4.0", "mihomo core", "v1.19.0", "Proxy endpoint", "127.0.0.1:9190", "Run Setup", "TUN", "Unavailable"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view=%s", want, view)
		}
	}
	// Maintenance was a single-row section; it is now merged into Daemon.
	if strings.Contains(view, ui.MaintenanceSectionTitle) {
		t.Fatalf("Maintenance section should be merged into Daemon: %s", view)
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
	// Not elevated: status row + a single elevation hint, action rows folded
	// away entirely (design SY1).
	for _, want := range []string{ui.SystemServiceSectionTitle, ui.ServiceStatusLabel, string(service.StatusRunning), "(" + ui.ServiceNeedsElevation + ")"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view=%s", want, view)
		}
	}
	for _, banned := range []string{ui.ServiceInstallLabel, ui.ServiceUninstallLabel, ui.ServiceReinstallLabel, ui.ServiceStartLabel, ui.ServiceStopLabel, ui.ServiceRestartLabel} {
		if strings.Contains(view, banned) {
			t.Fatalf("action rows should be folded when not elevated, found %q: %s", banned, view)
		}
	}
	if strings.Contains(view, ui.ServiceUnavailableDetail) {
		t.Fatalf("service still unavailable: %s", view)
	}
}

func TestSystemServiceElevatedFiltersUnavailableActions(t *testing.T) {
	withElevation(t, true)
	svc := &fakeService{status: service.StatusRunning}
	model := NewWithService(&fakeClient{}, svc, func() string { return "system-op" })
	updated, _ := model.Update(serviceStatusMsg{status: service.StatusRunning, elevated: true})
	model = updated.(*Model)
	view := model.View()
	// Running: start/install are not available and must not render.
	for _, want := range []string{ui.ServiceUninstallLabel, ui.ServiceReinstallLabel, ui.ServiceStopLabel, ui.ServiceRestartLabel} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view=%s", want, view)
		}
	}
	for _, banned := range []string{ui.ServiceInstallLabel, ui.ServiceStartLabel} {
		if strings.Contains(view, banned) {
			t.Fatalf("unavailable action should not render, found %q: %s", banned, view)
		}
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
	// Action rows are folded away; the hint row's detail carries the
	// elevation requirement when Entered.
	model.focusID = rowServiceHint
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
	// Reason must appear near the top (before the first bordered section), not only below the fold.
	detailIdx := strings.Index(view, model.outcomeDetail)
	sectionIdx := strings.Index(view, "╭")
	if detailIdx < 0 || sectionIdx < 0 || detailIdx > sectionIdx {
		t.Fatalf("failure reason not pinned above sections:\n%s", view)
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
	_ = updated.(*Model)
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
	_ = updated.(*Model)
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

func TestSystemEnterInspectsRowsAndRoutesSetupToStandaloneSetup(t *testing.T) {
	model := New(&fakeClient{}, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{DaemonVersion: "v0.4.0", Health: "ok", StartedAt: time.Now().Add(-5 * time.Minute), Capabilities: []string{protocol.CapabilityOnboarding}}, protocol.CoreStatus{Status: "running"})
	model.SetMutationsEnabled(true)
	model.focusID = rowDaemon
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(model.View(), "Daemon details") || !strings.Contains(model.View(), "Uptime") {
		t.Fatalf("detail view=%s", model.View())
	}
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEscape})
	model.focusID = rowRunSetup
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = updated.(*Model)
	if command == nil {
		t.Fatal("row=run-setup missing route command")
	}
	message, ok := command().(ui.RouteRequestMsg)
	if !ok || message.Page != ui.PageSetup {
		t.Fatalf("row=run-setup message=%T %#v", command(), command())
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
		_ = updated.(*Model)
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
	_ = updated.(*Model)
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
	for _, id := range []string{rowCoreUpdate, rowCoreRestart, rowRunSetup} {
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
	model.focusID = rowSystemProxyAction
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
	model.focusID = rowSystemProxyAction
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = updated.(*Model)
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
	model.focusID = rowSystemProxyAction
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = updated.(*Model)
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
	model.focusID = rowTUNAction
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

	model.focusID = rowTUNAction
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
	for _, id := range []string{rowSystemProxyAction, rowTUNAction} {
		model.focusID = id
		updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		model = updated.(*Model)
		if cmd != nil {
			t.Fatalf("row=%s offered mutation while disconnected", id)
		}
	}
}

func TestSystemCoreUpdateStickyDoneAndFailedWithAPIMessage(t *testing.T) {
	model := New(&fakeClient{}, func() string { return "op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{Version: "v1.19.0", Status: "running"})
	model.SetMutationsEnabled(true)

	updated, _ := model.Update(ui.ActionPendingMsg{Action: ui.ActionUpdateCore})
	model = updated.(*Model)
	if model.pendingRow != rowCoreUpdate || model.pendingNote != ui.CoreProgressUpdating {
		t.Fatalf("pending row=%q note=%q", model.pendingRow, model.pendingNote)
	}
	model.rowSpinClock = time.Unix(0, 0)
	view := model.View()
	if !strings.Contains(view, ui.CoreProgressUpdating) || !strings.Contains(view, "\x1b[") {
		t.Fatalf("expected solid pending chip:\n%s", view)
	}

	updated, _ = model.Update(actionResultMsg{
		kind:    actionUpdate,
		install: protocol.CoreInstallResult{Revision: 3, Version: "v1.20.0"},
	})
	model = updated.(*Model)
	if model.outcomeRow != rowCoreUpdate || !model.outcomeOK {
		t.Fatalf("outcome row=%q ok=%v", model.outcomeRow, model.outcomeOK)
	}
	if !strings.Contains(model.View(), ui.DoneLabel) {
		t.Fatalf("missing Done:\n%s", model.View())
	}

	updated, _ = model.Update(ui.ActionPendingMsg{Action: ui.ActionUpdateCore})
	model = updated.(*Model)
	updated, _ = model.Update(actionResultMsg{
		kind: actionUpdate,
		err:  protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download core asset failed"},
	})
	model = updated.(*Model)
	if model.outcomeOK || model.outcomeDetail != "download core asset failed" {
		t.Fatalf("outcome ok=%v detail=%q", model.outcomeOK, model.outcomeDetail)
	}
	view = model.View()
	if !strings.Contains(view, ui.FailedLabel) || !strings.Contains(view, "download core asset failed") {
		t.Fatalf("failed view:\n%s", view)
	}
}

func TestSystemCoreOutcomeDoesNotStartMihariCheck(t *testing.T) {
	updater := &fakeSelfUpdater{}
	model := New(&fakeClient{}, func() string { return "op" })
	model.SetSelfUpdater(updater, "v0.3.1", "mihari", func() bool { return true })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{Version: "v1.19.0", Status: "running"})
	updated, _ := model.Update(ui.ActionPendingMsg{Action: ui.ActionUpdateCore})
	model = updated.(*Model)

	updated, _ = model.Update(actionResultMsg{
		kind: actionUpdate,
		install: protocol.CoreInstallResult{
			Revision: 2, Version: "v1.20.0", Updated: true,
		},
	})
	model = updated.(*Model)
	if model.outcomeRow != rowCoreUpdate || !model.outcomeOK {
		t.Fatalf("outcome row=%q ok=%v", model.outcomeRow, model.outcomeOK)
	}
	if model.pending || model.selfCheckGeneration != 0 || updater.checkCalls != 0 {
		t.Fatalf("pending=%v generation=%d checks=%d", model.pending, model.selfCheckGeneration, updater.checkCalls)
	}
}

func TestSystemPendingDoesNotPolluteSiblingRows(t *testing.T) {
	withElevation(t, true)
	client := &fakeClient{
		systemProxy: protocol.SystemProxyStatus{
			Revision: 1, Desired: false, Target: "127.0.0.1:9190",
			Observed: protocol.SystemProxyObserved{Enabled: false},
		},
	}
	svc := &fakeService{status: service.StatusRunning}
	model := NewWithService(client, svc, func() string { return "op" })
	model.SetSnapshot(protocol.Status{
		Capabilities: []string{protocol.CapabilityCore, protocol.CapabilitySystemProxy, protocol.CapabilityTUN},
	}, protocol.CoreStatus{Version: "v1", Status: "running"})
	model.SetMutationsEnabled(true)
	model.SetSystemProxy(client.systemProxy)
	model.SetTun(protocol.TunStatus{DesiredEnable: false, LiveEnable: boolPtr(false)})
	updated, _ := model.Update(serviceStatusMsg{status: service.StatusRunning, elevated: true})
	model = updated.(*Model)

	updated, _ = model.Update(ui.ActionPendingMsg{Action: ui.ActionServiceReinstall})
	model = updated.(*Model)
	view := model.View()
	// Only the active service row shows progress; proxy must keep its summary, not "Pending".
	if strings.Count(view, ui.PendingLabel) > 0 {
		t.Fatalf("sibling rows should not show Pending label:\n%s", view)
	}
	if !strings.Contains(view, ui.ServiceProgressReinstalling) {
		t.Fatalf("missing reinstall progress:\n%s", view)
	}
	if !strings.Contains(view, ui.DesiredLabel) {
		t.Fatalf("proxy idle summary missing:\n%s", view)
	}
}

func TestSystemProxyFailedStickyAndTunDoneFades(t *testing.T) {
	client := &fakeClient{
		systemProxy: protocol.SystemProxyStatus{Revision: 2, Desired: true, Target: "127.0.0.1:9190"},
		tun:         protocol.TunStatus{Revision: 2, DesiredEnable: true, LiveEnable: boolPtr(true), Managed: true},
	}
	model := New(client, func() string { return "op" })
	model.SetSnapshot(protocol.Status{
		Capabilities: []string{protocol.CapabilitySystemProxy, protocol.CapabilityTUN},
	}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	model.SetSystemProxy(client.systemProxy)
	model.SetTun(client.tun)

	// System proxy disable fails: the Failed badge is sticky — the fade guard
	// (!outcomeOK) must keep it even if a stray fade tick fires.
	updated, _ := model.Update(ui.ActionPendingMsg{Action: ui.ActionDisableSystemProxy})
	model = updated.(*Model)
	updated, _ = model.Update(systemProxyActionResultMsg{
		kind: proxyDisable,
		err:  protocol.APIError{Code: protocol.CodePermissionDenied, Message: "elevation required for system proxy"},
	})
	model = updated.(*Model)
	if model.outcomeRow != rowSystemProxyAction || model.outcomeOK || model.outcomeDetail != "elevation required for system proxy" {
		t.Fatalf("proxy outcome row=%q ok=%v detail=%q", model.outcomeRow, model.outcomeOK, model.outcomeDetail)
	}
	if !strings.Contains(model.View(), ui.FailedLabel) {
		t.Fatalf("proxy failed view:\n%s", model.View())
	}
	updated, _ = model.Update(outcomeFadeMsg{gen: model.outcomeFadeGen, row: rowSystemProxyAction})
	model = updated.(*Model)
	if model.outcomeRow != rowSystemProxyAction || model.outcomeOK {
		t.Fatalf("failed outcome must stay sticky row=%q ok=%v", model.outcomeRow, model.outcomeOK)
	}
	if !strings.Contains(model.View(), ui.FailedLabel) {
		t.Fatalf("proxy failed view after fade:\n%s", model.View())
	}

	// TUN enable succeeds: Done badge shows, then the armed fade clears it while
	// the status row keeps carrying the live result.
	updated, _ = model.Update(ui.ActionPendingMsg{Action: ui.ActionEnableTun})
	model = updated.(*Model)
	if model.outcomeRow != "" {
		t.Fatal("new action should clear sticky proxy outcome")
	}
	updated, _ = model.Update(tunActionResultMsg{
		kind:   tunEnable,
		status: protocol.TunStatus{Revision: 3, DesiredEnable: true, Managed: true},
	})
	model = updated.(*Model)
	if model.outcomeRow != rowTUNAction || !model.outcomeOK {
		t.Fatalf("tun outcome row=%q ok=%v", model.outcomeRow, model.outcomeOK)
	}
	if !strings.Contains(model.View(), ui.DoneLabel) {
		t.Fatalf("tun done view:\n%s", model.View())
	}
	// The success path armed a fade; firing it clears the Done badge.
	updated, _ = model.Update(outcomeFadeMsg{gen: model.outcomeFadeGen, row: rowTUNAction})
	model = updated.(*Model)
	if model.outcomeRow != "" || model.outcomeOK {
		t.Fatalf("done outcome should fade row=%q ok=%v", model.outcomeRow, model.outcomeOK)
	}
	if strings.Contains(model.View(), ui.DoneLabel) {
		t.Fatalf("tun done view should not show Done after fade:\n%s", model.View())
	}
}

func TestSystemNetworkActionLabelFlipsWithState(t *testing.T) {
	tests := []struct {
		name      string
		proxy     protocol.SystemProxyStatus
		tun       protocol.TunStatus
		wantProxy string
		wantTun   string
	}{
		{
			name:      "both idle",
			proxy:     protocol.SystemProxyStatus{Target: "127.0.0.1:9190"},
			tun:       protocol.TunStatus{},
			wantProxy: ui.EnableSystemProxyLabel,
			wantTun:   ui.EnableTunLabel,
		},
		{
			name: "proxy owned and desired, tun desired on",
			proxy: protocol.SystemProxyStatus{
				Desired: true, Target: "127.0.0.1:9190",
				Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:9190", Owned: true},
			},
			tun:       protocol.TunStatus{DesiredEnable: true},
			wantProxy: ui.DisableSystemProxyLabel,
			wantTun:   ui.DisableTunLabel,
		},
		{
			name: "proxy foreign forces enable",
			proxy: protocol.SystemProxyStatus{
				Target:   "127.0.0.1:9190",
				Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:7890", Foreign: true},
			},
			tun:       protocol.TunStatus{},
			wantProxy: ui.ForceEnableSystemProxyLabel,
			wantTun:   ui.EnableTunLabel,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeClient{systemProxy: tt.proxy, tun: tt.tun}
			model := New(client, func() string { return "op" })
			model.SetSnapshot(protocol.Status{
				Capabilities: []string{protocol.CapabilitySystemProxy, protocol.CapabilityTUN},
			}, protocol.CoreStatus{})
			model.SetMutationsEnabled(true)
			model.SetSystemProxy(tt.proxy)
			model.SetTun(tt.tun)
			view := model.View()
			if !strings.Contains(view, tt.wantProxy) {
				t.Fatalf("proxy action label %q missing:\n%s", tt.wantProxy, view)
			}
			if !strings.Contains(view, tt.wantTun) {
				t.Fatalf("tun action label %q missing:\n%s", tt.wantTun, view)
			}
		})
	}
}

func TestSystemNetworkStatusAndActionRowsCoexist(t *testing.T) {
	client := &fakeClient{
		systemProxy: protocol.SystemProxyStatus{
			Desired: true, Target: "127.0.0.1:9190",
			Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:9190", Owned: true},
		},
		tun: protocol.TunStatus{Revision: 1, DesiredEnable: true, LiveEnable: boolPtr(true), Managed: true},
	}
	model := New(client, func() string { return "op" })
	model.SetSnapshot(protocol.Status{
		Capabilities: []string{protocol.CapabilitySystemProxy, protocol.CapabilityTUN},
	}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	model.SetSystemProxy(client.systemProxy)
	model.SetTun(client.tun)
	view := model.View()
	// Status rows carry the live observed state.
	for _, want := range []string{"127.0.0.1:9190", ui.OwnedLabel, ui.LiveLabel} {
		if !strings.Contains(view, want) {
			t.Fatalf("status row missing %q:\n%s", want, view)
		}
	}
	// Action rows carry the toggle verbs (both desired-on → Disable).
	if strings.Count(view, ui.DisableSystemProxyLabel) < 2 {
		t.Fatalf("expected both proxy and tun action rows to show Disable:\n%s", view)
	}
}

func TestSystemNetworkStatusRowUpdatesDynamically(t *testing.T) {
	client := &fakeClient{systemProxy: protocol.SystemProxyStatus{Target: "127.0.0.1:9190"}}
	model := New(client, func() string { return "op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilitySystemProxy}}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	model.SetSystemProxy(client.systemProxy)

	push := func(server string) {
		model.ApplyRootNetworkStatus(protocol.SystemProxyStatus{
			Desired: true, Target: "127.0.0.1:9190",
			Observed: protocol.SystemProxyObserved{Enabled: true, Server: server, Owned: true},
		}, true, protocol.TunStatus{}, false)
	}

	push("127.0.0.1:9190")
	if view := model.View(); !strings.Contains(view, "127.0.0.1:9190") {
		t.Fatalf("status row missing first server:\n%s", view)
	}
	push("127.0.0.1:9191")
	view := model.View()
	if !strings.Contains(view, "127.0.0.1:9191") {
		t.Fatalf("status row did not update to new server:\n%s", view)
	}
	if strings.Contains(view, "127.0.0.1:9190") {
		t.Fatalf("status row kept stale server after update:\n%s", view)
	}

	// While a toggle is pending on the action row, the status row must keep
	// showing the live summary — the pending chip binds the action row only.
	model.Update(ui.ActionPendingMsg{Action: ui.ActionEnableSystemProxy})
	if view := model.View(); !strings.Contains(view, "127.0.0.1:9191") {
		t.Fatalf("status row lost summary while action pending:\n%s", view)
	}
}

func TestSystemNetworkStatusRowEnterShowsObserveDetail(t *testing.T) {
	client := &fakeClient{
		systemProxy: protocol.SystemProxyStatus{
			Revision: 3, Desired: true, Target: "127.0.0.1:9190",
			Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:9190", Owned: true},
		},
	}
	model := New(client, func() string { return "op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilitySystemProxy}}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	model.SetSystemProxy(client.systemProxy)
	model.focusID = rowSystemProxy // status row, not the action row
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if cmd != nil {
		t.Fatalf("status row enter must not fire a command, got %v", cmd)
	}
	if model.detail == nil {
		t.Fatal("status row enter should open the observe detail panel")
	}
	view := model.View()
	for _, want := range []string{"Target", "127.0.0.1:9190"} {
		if !strings.Contains(view, want) {
			t.Fatalf("observe detail missing %q:\n%s", want, view)
		}
	}
}

func TestSystemRendersPanelEndpointRowsWhenInstalled(t *testing.T) {
	client := &fakeClient{
		onboarding: protocol.OnboardingStatus{MixedAddr: "127.0.0.1:9190", ControllerAddr: "127.0.0.1:9090", WebAddr: "127.0.0.1:9191"},
		webGUI: protocol.WebGUIStatus{
			Panels: []protocol.PanelStatus{
				{ID: "zashboard", Name: "Zashboard", InstalledBuild: "v1"},
				{ID: "metacubexd", Name: "MetaCubeXD", InstalledBuild: "abc123"},
			},
		},
	}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityWebGUI}}, protocol.CoreStatus{})
	updated, _ := model.Update(onboardingResultMsg{status: client.onboarding})
	model = updated.(*Model)
	updated, _ = model.Update(webGUIStatusMsg{status: client.webGUI})
	model = updated.(*Model)
	view := model.View()
	for _, want := range []string{
		ui.ProxyEndpointLabel, "127.0.0.1:9190",
		ui.MihomoCoreAPILabel, "127.0.0.1:9090",
		ui.ZashboardLabel, ui.MetaCubeXDLabel, "/__mihari/panels/",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view=%s", want, view)
		}
	}
	// URLs are token-free; an exactly-48-column URL renders whole, a longer
	// one truncates at 48 with an ellipsis.
	if strings.Contains(view, "token=") {
		t.Fatalf("panel URL must be token-free:\n%s", view)
	}
	if !strings.Contains(view, "http://127.0.0.1:9191/__mihari/panels/zashboard/") {
		t.Fatalf("48-column panel URL must render whole:\n%s", view)
	}
	client.onboarding.WebAddr = "http://192.168.1.100:9191"
	updated, _ = model.Update(onboardingResultMsg{status: client.onboarding})
	model = updated.(*Model)
	view = model.View()
	if strings.Contains(view, "http://192.168.1.100:9191/__mihari/panels/zashboard/") || !strings.Contains(view, "…") {
		t.Fatalf("longer panel URL must truncate at 48 columns:\n%s", view)
	}
}

func TestSystemHidesUninstalledPanelRow(t *testing.T) {
	client := &fakeClient{
		onboarding: protocol.OnboardingStatus{WebAddr: "127.0.0.1:9191"},
		webGUI: protocol.WebGUIStatus{
			Panels: []protocol.PanelStatus{
				{ID: "zashboard", Name: "Zashboard", InstalledBuild: "v1"},
				{ID: "metacubexd", Name: "MetaCubeXD"},
			},
		},
	}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityWebGUI}}, protocol.CoreStatus{})
	updated, _ := model.Update(webGUIStatusMsg{status: client.webGUI})
	model = updated.(*Model)
	view := model.View()
	if !strings.Contains(view, ui.ZashboardLabel) || strings.Contains(view, ui.MetaCubeXDLabel) {
		t.Fatalf("uninstalled panel row must hide (zashboard=%v metacubexd=%v):\n%s",
			strings.Contains(view, ui.ZashboardLabel), strings.Contains(view, ui.MetaCubeXDLabel), view)
	}
}

func TestSystemHidesPanelRowsWithoutWebGUICapability(t *testing.T) {
	model := New(&fakeClient{onboarding: protocol.OnboardingStatus{WebAddr: "127.0.0.1:9191"}}, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityOnboarding}}, protocol.CoreStatus{})
	view := model.View()
	for _, banned := range []string{ui.ZashboardLabel, ui.MetaCubeXDLabel} {
		if strings.Contains(view, banned) {
			t.Fatalf("panel row %q without web-gui capability: %s", banned, view)
		}
	}
	if !strings.Contains(view, ui.ProxyEndpointLabel) || !strings.Contains(view, ui.MihomoCoreAPILabel) {
		t.Fatalf("proxy/core endpoint rows must always render:\n%s", view)
	}
}

func TestSystemEndpointRowsOpenDetailPopup(t *testing.T) {
	model := New(&fakeClient{onboarding: protocol.OnboardingStatus{MixedAddr: "127.0.0.1:9190", ControllerAddr: "127.0.0.1:9090"}}, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{}, protocol.CoreStatus{})
	for _, test := range []struct {
		id         string
		detailText string
	}{
		{rowProxyEndpoint, ui.ProxyEndpointDetail},
		{rowCoreAPI, ui.MihomoCoreAPIDetail},
	} {
		model.focusID = test.id
		model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
		view := model.View()
		if !strings.Contains(view, " details") || !strings.Contains(view, test.detailText) {
			t.Fatalf("row=%s detail view=%s", test.id, view)
		}
		model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEscape})
	}
}

func TestSystemPanelEnterOpensBrowser(t *testing.T) {
	client := &fakeClient{
		onboarding: protocol.OnboardingStatus{WebAddr: "127.0.0.1:9191"},
		webGUI: protocol.WebGUIStatus{
			Panels: []protocol.PanelStatus{{ID: "zashboard", Name: "Zashboard", InstalledBuild: "v1"}},
		},
	}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityWebGUI}}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	var opened []string
	model.SetOpenBrowser(func(url string) error { opened = append(opened, url); return nil })
	updated, _ := model.Update(webGUIStatusMsg{status: client.webGUI})
	model = updated.(*Model)
	model.focusID = rowZashboard
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("missing open intent")
	}
	intent, ok := cmd().(ui.ActionIntentMsg)
	if !ok || intent.Action != ui.ActionOpenWebGUI || intent.Page != ui.PageSystem || intent.Capability != protocol.CapabilityWebGUI || intent.Execute == nil {
		t.Fatalf("intent=%#v", intent)
	}
	if len(opened) != 0 {
		t.Fatalf("browser opened before Execute: %v", opened)
	}
	updated, _ = model.Update(intent.Execute())
	model = updated.(*Model)
	if client.openWebGUICalls != 1 || client.lastOpenPanel != "zashboard" {
		t.Fatalf("open calls=%d panel=%q", client.openWebGUICalls, client.lastOpenPanel)
	}
	if len(opened) != 1 || !strings.Contains(opened[0], "/__mihari/panels/zashboard/") || !strings.Contains(opened[0], "token=") {
		t.Fatalf("opened=%v", opened)
	}
	view := model.View()
	if strings.Contains(view, ui.FailedLabel) || strings.Contains(view, ui.DoneLabel) {
		t.Fatalf("successful open must stay silent (no sticky badge):\n%s", view)
	}
}

func TestSystemPanelOpenBlockedWhenNotInstalledOrDisabled(t *testing.T) {
	client := &fakeClient{
		onboarding: protocol.OnboardingStatus{WebAddr: "127.0.0.1:9191"},
		webGUI: protocol.WebGUIStatus{
			Panels: []protocol.PanelStatus{{ID: "zashboard", Name: "Zashboard", InstalledBuild: "v1"}},
		},
	}
	// Not installed: the metacubexd row is hidden entirely, enter stays silent.
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityWebGUI}}, protocol.CoreStatus{})
	updated, _ := model.Update(webGUIStatusMsg{status: client.webGUI})
	model = updated.(*Model)
	model.focusID = rowMetaCubeXD
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = updated.(*Model)
	if cmd != nil {
		t.Fatalf("enter on uninstalled panel row must be silent, got %T", cmd())
	}
	// Mutations disabled: no intent even when the panel is installed.
	model = New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityWebGUI}}, protocol.CoreStatus{})
	model.SetMutationsEnabled(false)
	updated, _ = model.Update(webGUIStatusMsg{status: client.webGUI})
	model = updated.(*Model)
	model.focusID = rowZashboard
	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = updated.(*Model)
	if cmd != nil {
		t.Fatalf("enter with mutations disabled must be silent, got %T", cmd())
	}
	// No capability: rows are hidden, enter stays silent.
	model = New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityOnboarding}}, protocol.CoreStatus{})
	model.focusID = rowZashboard
	updated, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = updated.(*Model)
	if cmd != nil {
		t.Fatalf("enter without web-gui capability must be silent, got %T", cmd())
	}
}

func TestSystemLoadFetchesWebGUI(t *testing.T) {
	client := &fakeClient{webGUI: protocol.WebGUIStatus{GatewayAddr: "127.0.0.1:9191"}}
	model := New(client, nil)
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityWebGUI}}, protocol.CoreStatus{})
	cmd := model.Load()
	if cmd == nil {
		t.Fatal("Load should fetch web gui status")
	}
	msg := cmd()
	found := false
	if typed, ok := msg.(webGUIStatusMsg); ok {
		found = typed.err == nil
	} else if batch, ok := msg.(tea.BatchMsg); ok {
		for _, item := range batch {
			if typed, ok := item().(webGUIStatusMsg); ok {
				found = typed.err == nil
			}
		}
	} else {
		t.Fatalf("unexpected Load msg %T", msg)
	}
	if !found || client.webGUICalls != 1 {
		t.Fatalf("found=%v calls=%d", found, client.webGUICalls)
	}
}

func TestSystemWebGUIErrorShowsUnavailablePanelPlaceholders(t *testing.T) {
	model := New(&fakeClient{onboarding: protocol.OnboardingStatus{WebAddr: "127.0.0.1:9191"}}, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityWebGUI}}, protocol.CoreStatus{})
	updated, _ := model.Update(webGUIStatusMsg{err: errors.New("boom")})
	model = updated.(*Model)
	view := model.View()
	if !strings.Contains(view, ui.ZashboardLabel) || !strings.Contains(view, ui.MetaCubeXDLabel) {
		t.Fatalf("panel placeholder rows missing on fetch error:\n%s", view)
	}
	if !strings.Contains(view, ui.UnavailableTitle) {
		t.Fatalf("placeholder value missing:\n%s", view)
	}
	if model.lastError != ui.WebGUIUnavailable {
		t.Fatalf("lastError=%q", model.lastError)
	}
	// Placeholder rows must not open anything.
	model.focusID = rowZashboard
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = updated.(*Model)
	if cmd != nil {
		t.Fatalf("placeholder row must not open a browser, got %T", cmd())
	}
}

func TestPanelURLNormalizesSchemeAndTrailingSlash(t *testing.T) {
	for _, test := range []struct{ addr, id, want string }{
		{"127.0.0.1:9191", "zashboard", "http://127.0.0.1:9191/__mihari/panels/zashboard/"},
		{"http://127.0.0.1:9191/", "metacubexd", "http://127.0.0.1:9191/__mihari/panels/metacubexd/"},
		{"https://127.0.0.1:9191//", "zashboard", "https://127.0.0.1:9191/__mihari/panels/zashboard/"},
		{"http://localhost:9191", "zashboard", "http://localhost:9191/__mihari/panels/zashboard/"},
		{"", "zashboard", ""},
	} {
		if got := panelURL(test.addr, test.id); got != test.want {
			t.Fatalf("panelURL(%q, %q)=%q want %q", test.addr, test.id, got, test.want)
		}
	}
}

func TestSystemPanelOpenFailureShowsStickyFailed(t *testing.T) {
	client := &fakeClient{
		onboarding: protocol.OnboardingStatus{WebAddr: "127.0.0.1:9191"},
		webGUI: protocol.WebGUIStatus{
			Panels: []protocol.PanelStatus{{ID: "zashboard", Name: "Zashboard", InstalledBuild: "v1"}},
		},
		openWebGUIErr: protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "open browser failed"},
	}
	model := New(client, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityWebGUI}}, protocol.CoreStatus{})
	model.SetMutationsEnabled(true)
	updated, _ := model.Update(webGUIStatusMsg{status: client.webGUI})
	model = updated.(*Model)
	model.focusID = rowZashboard
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	intent := cmd().(ui.ActionIntentMsg)
	updated, _ = model.Update(intent.Execute())
	model = updated.(*Model)
	if model.outcomeRow != rowZashboard || model.outcomeOK {
		t.Fatalf("outcome row=%q ok=%v", model.outcomeRow, model.outcomeOK)
	}
	view := model.View()
	if !strings.Contains(view, ui.FailedLabel) || !strings.Contains(view, "open browser failed") {
		t.Fatalf("failed view:\n%s", view)
	}
}

func TestSystemLoadStartsMihariVersionCheck(t *testing.T) {
	updater := &fakeSelfUpdater{checkResult: update.CheckResult{Current: "v0.3.1", Latest: "v0.4.0", Available: true}}
	model := New(nil, nil)
	model.SetSelfUpdater(updater, "v0.3.1", `C:\Program Files\Mihari\mihari.exe`, func() bool { return true })

	command := model.Load()
	if command == nil {
		t.Fatal("load did not start version check")
	}
	view := model.View()
	if !strings.Contains(view, ui.UpdateMihariLabel) || !strings.Contains(view, ui.MihariProgressChecking) {
		t.Fatalf("checking view:\n%s", view)
	}
	if updater.checkCalls != 0 {
		t.Fatalf("check ran synchronously: calls=%d", updater.checkCalls)
	}
}

func TestSystemMihariVersionCheckRendersAvailable(t *testing.T) {
	model := New(nil, nil)
	model.SetSelfUpdater(&fakeSelfUpdater{}, "v0.3.1", "mihari", func() bool { return true })
	model.selfCheckGeneration = 2

	updated, _ := model.Update(selfCheckResultMsg{
		generation: 2,
		result:     update.CheckResult{Current: "v0.3.1", Latest: "v0.4.0", Available: true},
	})
	model = updated.(*Model)
	if view := model.View(); !strings.Contains(view, "v0.3.1 · v0.4.0 available") {
		t.Fatalf("available view:\n%s", view)
	}
}

func TestSystemMihariVersionCheckRendersUpToDate(t *testing.T) {
	model := New(nil, nil)
	model.SetSelfUpdater(&fakeSelfUpdater{}, "v0.3.1", "mihari", func() bool { return true })
	model.selfCheckGeneration = 3

	updated, _ := model.Update(selfCheckResultMsg{
		generation: 3,
		result:     update.CheckResult{Current: "v0.3.1", Latest: "v0.3.1", Available: false},
	})
	model = updated.(*Model)
	if view := model.View(); !strings.Contains(view, "v0.3.1 · Up to date") {
		t.Fatalf("up-to-date view:\n%s", view)
	}
}

func TestSystemMihariVersionCheckFailureUsesFailedChip(t *testing.T) {
	model := New(nil, nil)
	model.SetSelfUpdater(&fakeSelfUpdater{}, "v0.3.1", "mihari", func() bool { return true })
	model.selfCheckGeneration = 4

	updated, _ := model.Update(selfCheckResultMsg{generation: 4, err: errors.New("secret transport detail")})
	model = updated.(*Model)
	view := model.View()
	if !strings.Contains(view, ui.FailedLabel) || !strings.Contains(view, ui.UpdateMihariCheckFailed) {
		t.Fatalf("failed view:\n%s", view)
	}
	if strings.Contains(view, "secret transport detail") {
		t.Fatalf("raw error leaked:\n%s", view)
	}
}

func TestSystemMihariVersionCheckRetryIgnoresStaleResult(t *testing.T) {
	model := New(nil, nil)
	model.SetSelfUpdater(&fakeSelfUpdater{}, "v0.3.1", "mihari", func() bool { return true })
	model.selfCheckGeneration = 1
	model.markRowOutcome(rowMihariUpdate, false, ui.UpdateMihariCheckFailed)
	model.focusID = rowMihariUpdate

	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if command == nil || model.selfCheckGeneration != 2 || !strings.Contains(model.View(), ui.MihariProgressChecking) {
		t.Fatalf("retry generation=%d command=%v view=\n%s", model.selfCheckGeneration, command != nil, model.View())
	}

	updated, _ = model.Update(selfCheckResultMsg{
		generation: 1,
		result:     update.CheckResult{Current: "v0.3.1", Latest: "v9.9.9", Available: true},
	})
	model = updated.(*Model)
	view := model.View()
	if !strings.Contains(view, ui.MihariProgressChecking) || strings.Contains(view, "v9.9.9") {
		t.Fatalf("stale result replaced pending check:\n%s", view)
	}
}

func TestSystemCheckingMihariBlocksOtherRowActions(t *testing.T) {
	model := New(&fakeClient{}, func() string { return "system-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{Version: "v1.19.0"})
	model.SetMutationsEnabled(true)
	model.SetSelfUpdater(&fakeSelfUpdater{}, "v0.3.1", "mihari", func() bool { return true })
	if command := model.Load(); command == nil {
		t.Fatal("version check did not start")
	}
	model.focusID = rowCoreUpdate

	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if command != nil {
		t.Fatalf("core update was offered while Mihari check pending: %T", command())
	}
	if model.pendingRow != rowMihariUpdate {
		t.Fatalf("pending row=%q", model.pendingRow)
	}
}

func TestSystemMihariUpdateOffersConfirmationWhenAvailable(t *testing.T) {
	model, _ := availableMihariUpdateModel(t, true, update.Result{})

	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil {
		t.Fatal("available update did not offer confirmation")
	}
	intent, ok := command().(ui.ActionIntentMsg)
	if !ok || intent.Action != ui.ActionUpdateMihari || intent.Page != ui.PageSystem || intent.Capability != "" || intent.Execute == nil {
		t.Fatalf("intent=%#v", intent)
	}
	if !strings.Contains(intent.Object, "v0.3.1") || !strings.Contains(intent.Object, "v0.4.0") {
		t.Fatalf("confirmation object=%q", intent.Object)
	}
}

func TestSystemMihariUpdatePermissionFailureDoesNotCallUpdater(t *testing.T) {
	model, updater := availableMihariUpdateModel(t, false, update.Result{})
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	intent := command().(ui.ActionIntentMsg)
	updated, _ := model.Update(ui.ActionPendingMsg{Page: ui.PageSystem, Action: ui.ActionUpdateMihari})
	model = updated.(*Model)

	updated, relaunch := model.Update(intent.Execute())
	model = updated.(*Model)
	if updater.updateCalls != 0 || relaunch != nil || model.outcomeOK || model.outcomeRow != rowMihariUpdate {
		t.Fatalf("calls=%d relaunch=%v outcome=%q ok=%v", updater.updateCalls, relaunch != nil, model.outcomeRow, model.outcomeOK)
	}
	if view := model.View(); !strings.Contains(view, ui.FailedLabel) || !strings.Contains(strings.ToLower(view), "administrator") {
		t.Fatalf("permission view:\n%s", view)
	}
}

func TestSystemMihariUpdateFailureStaysInCurrentTUI(t *testing.T) {
	model, _ := availableMihariUpdateModel(t, true, update.Result{})
	model.selfUpdater.(*fakeSelfUpdater).updateErr = errors.New("raw replacement detail")
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	intent := command().(ui.ActionIntentMsg)
	updated, _ := model.Update(ui.ActionPendingMsg{Page: ui.PageSystem, Action: ui.ActionUpdateMihari})
	model = updated.(*Model)

	updated, relaunch := model.Update(intent.Execute())
	model = updated.(*Model)
	if relaunch != nil || model.outcomeOK {
		t.Fatalf("relaunch=%v outcomeOK=%v", relaunch != nil, model.outcomeOK)
	}
	view := model.View()
	if !strings.Contains(view, ui.UpdateMihariActionFailed) || strings.Contains(view, "raw replacement detail") {
		t.Fatalf("failure view:\n%s", view)
	}
}

func TestSystemMihariUpdateSuccessRequestsRelaunch(t *testing.T) {
	model, updater := availableMihariUpdateModel(t, true, update.Result{Version: "v0.4.0", Updated: true})
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	intent := command().(ui.ActionIntentMsg)
	updated, _ := model.Update(ui.ActionPendingMsg{Page: ui.PageSystem, Action: ui.ActionUpdateMihari})
	model = updated.(*Model)
	if view := model.View(); !strings.Contains(view, ui.MihariProgressUpdating) {
		t.Fatalf("updating view:\n%s", view)
	}

	result := intent.Execute()
	if outcome, ok := result.(interface{ Err() error }); !ok || outcome.Err() != nil {
		t.Fatalf("result=%T err=%v", result, outcome.Err())
	}
	updated, relaunch := model.Update(result)
	model = updated.(*Model)
	if updater.updateCalls != 1 || updater.lastBinary != `C:\Program Files\Mihari\mihari.exe` || updater.lastCurrent != "v0.3.1" {
		t.Fatalf("calls=%d binary=%q current=%q", updater.updateCalls, updater.lastBinary, updater.lastCurrent)
	}
	if !model.outcomeOK || !strings.Contains(model.View(), ui.DoneLabel) || relaunch == nil {
		t.Fatalf("outcomeOK=%v relaunch=%v view=\n%s", model.outcomeOK, relaunch != nil, model.View())
	}
	request, ok := relaunch().(ui.RelaunchRequestMsg)
	if !ok || request.Warning != "" {
		t.Fatalf("request=%#v", request)
	}
}

func TestSystemMihariUpdateCommittedWithServiceFailureStillRelaunches(t *testing.T) {
	model, updater := availableMihariUpdateModel(t, true, update.Result{Version: "v0.4.0", Updated: true})
	updater.updateErr = protocol.APIError{Code: protocol.CodeInvalidState, Message: "restart installed service failed"}
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	intent := command().(ui.ActionIntentMsg)
	updated, _ := model.Update(ui.ActionPendingMsg{Page: ui.PageSystem, Action: ui.ActionUpdateMihari})
	model = updated.(*Model)

	result := intent.Execute()
	if outcome := result.(interface{ Err() error }); outcome.Err() != nil {
		t.Fatalf("committed replacement classified as failed: %v", outcome.Err())
	}
	updated, relaunch := model.Update(result)
	model = updated.(*Model)
	if !model.outcomeOK || relaunch == nil {
		t.Fatalf("outcomeOK=%v relaunch=%v", model.outcomeOK, relaunch != nil)
	}
	request := relaunch().(ui.RelaunchRequestMsg)
	if request.Warning != "restart installed service failed" {
		t.Fatalf("warning=%q", request.Warning)
	}
}

func availableMihariUpdateModel(t *testing.T, elevated bool, result update.Result) (*Model, *fakeSelfUpdater) {
	t.Helper()
	updater := &fakeSelfUpdater{updateResult: result}
	model := New(nil, nil)
	model.SetSelfUpdater(updater, "v0.3.1", `C:\Program Files\Mihari\mihari.exe`, func() bool { return elevated })
	model.selfCheckGeneration = 1
	updated, _ := model.Update(selfCheckResultMsg{
		generation: 1,
		result:     update.CheckResult{Current: "v0.3.1", Latest: "v0.4.0", Available: true},
	})
	model = updated.(*Model)
	model.focusID = rowMihariUpdate
	return model, updater
}

func updateKey(t *testing.T, model *Model, key tea.KeyPressMsg) *Model {
	t.Helper()
	updated, _ := model.Update(key)
	return updated.(*Model)
}

func TestSystemDaemonCoreRowsDegradeWhenDisconnected(t *testing.T) {
	model := New(&fakeClient{}, func() string { return "system-op" })
	model.SetSnapshot(
		protocol.Status{DaemonVersion: "v0.4.0", Health: "ok", Capabilities: []string{protocol.CapabilityCore}},
		protocol.CoreStatus{Status: "running", Version: "v1.19.0"},
	)

	// Online: positive green dots (78), raw health/status words, no stale
	// marker and no caution color on either row.
	model.SetMutationsEnabled(true)
	online := model.View()
	for _, want := range []string{"v0.4.0", "v1.19.0", "38;5;78"} { // 78 = TonePositive (Success)
		if !strings.Contains(online, want) {
			t.Fatalf("online view missing %q:\n%s", want, online)
		}
	}
	for _, stale := range []string{"ok · " + ui.StaleLabel, "running · " + ui.StaleLabel, "38;5;214"} { // 214 = ToneCaution (Warning)
		if strings.Contains(online, stale) {
			t.Fatalf("online daemon/core rows should not be stale:\n%s", online)
		}
	}

	// Disconnected: keep the last value (version stays) but degrade the dot to
	// caution yellow and append " · Stale" (design G2), matching the Overview
	// core card and the rest of the System page.
	model.SetMutationsEnabled(false)
	disconnected := model.View()
	for _, want := range []string{"ok · " + ui.StaleLabel, "running · " + ui.StaleLabel, "v0.4.0", "v1.19.0", "38;5;214"} {
		if !strings.Contains(disconnected, want) {
			t.Fatalf("disconnected view missing %q:\n%s", want, disconnected)
		}
	}

	// Reconnect: stale marker and caution color clear, green dot returns — the
	// degradation must be reversible, not sticky.
	model.SetMutationsEnabled(true)
	reconnected := model.View()
	if strings.Contains(reconnected, " · "+ui.StaleLabel) || !strings.Contains(reconnected, "38;5;78") {
		t.Fatalf("reconnected view should drop stale and restore green:\n%s", reconnected)
	}
}
