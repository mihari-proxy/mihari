package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/buildinfo"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	setuppage "github.com/mihari-proxy/mihari/internal/tui/pages/setup"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type endpointRestartClient struct {
	status      protocol.Status
	ports       protocol.OnboardingStatus
	managedPID  int
	restarts    int
	failure     error
	keepPending bool
}

func (f *endpointRestartClient) Status(context.Context) (protocol.Status, error) {
	return f.status, nil
}
func (f *endpointRestartClient) Onboarding(context.Context) (protocol.OnboardingStatus, error) {
	return f.ports, nil
}

func (f *endpointRestartClient) Core(context.Context) (protocol.CoreStatus, error) {
	return protocol.CoreStatus{}, nil
}
func (f *endpointRestartClient) ManagedDaemonPID(context.Context) (int, error) {
	return f.managedPID, nil
}
func (f *endpointRestartClient) Restart() error {
	f.restarts++
	if f.failure != nil {
		return f.failure
	}
	f.status.PID++
	f.status.StartedAt = f.status.StartedAt.Add(time.Second)
	f.status.SetupRequired = false
	if !f.keepPending {
		f.ports.RestartRequired = false
	}
	return nil
}

func endpointRestartModel() (Model, *endpointRestartClient, ui.ApplySavedEndpointsMsg) {
	f := &endpointRestartClient{status: protocol.Status{PID: 42, StartedAt: time.Unix(100, 0), Health: "ok", SetupRequired: true}, ports: protocol.OnboardingStatus{MixedAddr: "127.0.0.1:19190", ControllerAddr: "127.0.0.1:9090", WebAddr: "127.0.0.1:9191", RestartRequired: true}, managedPID: 42}
	m := NewModel()
	f.status.DaemonVersion = buildinfo.Version
	m.status = f.status
	m.connected = true
	m.active = ui.PageSystem
	opts := app.EndpointRestartOptions{Client: f, Service: f, Elevated: func() bool { return true }}
	m.endpointRestartApply = func(ctx context.Context, target protocol.OnboardingStatus) (app.EndpointRestartResult, error) {
		return app.RestartSavedEndpoints(ctx, target, opts)
	}
	return m, f, ui.ApplySavedEndpointsMsg{Page: ui.PageSystem, Status: f.ports}
}

func TestEndpointRestart_QueuesDifferentSavedTargets(t *testing.T) {
	m, f, request := endpointRestartModel()
	next, first := m.Update(request)
	m = next.(Model)
	f.ports.MixedAddr = "127.0.0.1:29190"
	request.Status = f.ports
	next, concurrent := m.Update(request)
	m = next.(Model)
	if concurrent != nil {
		t.Fatal("started overlapping application")
	}
	next, queued := m.Update(first())
	m = next.(Model)
	if queued == nil || !m.endpointRestart.pending {
		t.Fatal("newer saved ports were silently dropped")
	}
	next, _ = m.Update(queued())
	m = next.(Model)
	if f.restarts != 1 || m.endpointRestart.pending || m.endpointRestart.result.Ports.MixedAddr != request.Status.MixedAddr {
		t.Fatal("newer saved ports were not applied")
	}
}

func TestEndpointRestart_FailedSystemApplicationCanRetryWithKey(t *testing.T) {
	m, f, request := endpointRestartModel()
	f.failure = protocol.APIError{Code: protocol.CodeInvalidState, Message: "specific restart failure"}
	next, cmd := m.Update(request)
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	view := m.modal.body
	if !strings.Contains(view, "could not be restarted") || !strings.Contains(view, "F2") || !strings.Contains(view, "mihari service restart") {
		t.Fatalf("failure dropped cause or recovery instructions: %s", view)
	}
	f.failure = nil
	next, retry := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = next.(Model)
	if retry == nil || !m.endpointRestart.pending {
		t.Fatal("System has no explicit retry")
	}
	next, _ = m.Update(retry())
	m = next.(Model)
	if f.restarts != 2 || m.endpointRestart.pending {
		t.Fatal("retry did not complete")
	}
}

func TestEndpointRestart_SetupFailureDoesNotBlockRecoveryKeys(t *testing.T) {
	m, f, request := endpointRestartModel()
	m.active = ui.PageSetup
	request.Page = ui.PageSetup
	f.failure = errors.New("service restart failed")
	next, cmd := m.Update(request)
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.modal != nil {
		t.Fatal("Setup failure modal blocks the page's retry and recheck keys")
	}
	if m.endpointRestart.pending || !m.endpointRestart.failed {
		t.Fatal("Setup failure did not release application state")
	}
}

func TestEndpointRestart_DropsOldResourceSnapshots(t *testing.T) {
	m, _, request := endpointRestartModel()
	old := m.status
	next, cmd := m.Update(request)
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	m.core = protocol.CoreStatus{Status: "running", PID: 900}
	m.applySessionEvent(session.Event{Kind: session.EventCore, Status: old, Core: protocol.CoreStatus{Status: "stopped", PID: 800}})
	if m.core.PID != 900 {
		t.Fatal("old core snapshot replaced restarted state")
	}
	m.applySessionEvent(session.Event{Kind: session.EventCore, Status: m.status, Core: protocol.CoreStatus{Status: "running", PID: 901}})
	if m.core.PID != 901 {
		t.Fatal("new daemon snapshot was rejected")
	}
}

func TestEndpointRestart_SystemSaveRestartsOnceAndReturnsToSystem(t *testing.T) {
	m, f, request := endpointRestartModel()
	next, cmd := m.Update(request)
	m = next.(Model)
	if cmd == nil || m.endpointRestart == nil || !m.endpointRestart.pending {
		t.Fatal("saved ports did not start automatic application")
	}
	next, duplicate := m.Update(request)
	m = next.(Model)
	if duplicate != nil {
		t.Fatal("duplicate restart command")
	}
	m.applySessionEvent(session.Event{Kind: session.EventStatus, Status: f.status})
	if m.active != ui.PageSystem {
		t.Fatal("pending application forced setup")
	}
	result := cmd().(endpointRestartResultMsg)
	next, _ = m.Update(result)
	m = next.(Model)
	if result.err != nil || f.restarts != 1 || m.active != ui.PageSystem || m.status.SetupRequired || m.endpointRestart.pending || m.modal != nil {
		t.Fatalf("result=%+v restarts=%d page=%s", result, f.restarts, m.active)
	}
	// A status already queued by the old IPC session must not re-open setup.
	m.applySessionEvent(session.Event{Kind: session.EventStatus, Status: requestStatus()})
	if m.active != ui.PageSystem || m.status.SetupRequired {
		t.Fatal("stale daemon status re-entered setup")
	}
}

func requestStatus() protocol.Status {
	return protocol.Status{PID: 42, StartedAt: time.Unix(100, 0), Health: "ok", SetupRequired: true}
}

func TestEndpointRestart_ResultDoesNotReplaceNewerSessionStatus(t *testing.T) {
	m, _, request := endpointRestartModel()
	next, cmd := m.Update(request)
	m = next.(Model)
	result := cmd().(endpointRestartResultMsg)
	status := result.result.Status
	status.Revision = 9
	m.applySessionEvent(session.Event{Kind: session.EventStatus, Status: status, Epoch: 4})
	next, _ = m.Update(result)
	m = next.(Model)
	if m.status.Revision != 9 {
		t.Fatalf("newer session status replaced: revision=%d", m.status.Revision)
	}
}

func TestEndpointRestart_FailureKeepsSavedStateAndDoesNotLoop(t *testing.T) {
	m, f, request := endpointRestartModel()
	f.failure = errors.New("restart failed")
	next, cmd := m.Update(request)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("missing restart command")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if f.restarts != 1 || m.modal == nil || !strings.Contains(m.modal.View(100, 30), "Ports saved") || !f.ports.RestartRequired {
		t.Fatal("restart failure lost saved-state guidance")
	}
	_, duplicate := m.Update(request)
	if duplicate != nil {
		t.Fatal("failed restart retried without user intent")
	}
}

func TestEndpointRestart_ExplicitRetryAfterFailure(t *testing.T) {
	m, f, request := endpointRestartModel()
	f.failure = errors.New("restart failed")
	next, cmd := m.Update(request)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("missing restart command")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	f.failure = nil
	request.Retry = true
	next, cmd = m.Update(request)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("explicit retry ignored")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if f.restarts != 2 || m.endpointRestart.pending {
		t.Fatal("explicit retry did not finish")
	}
}

func TestEndpointRestart_AutomaticSetupDoesNotExitBeforeApplication(t *testing.T) {
	m, _, request := endpointRestartModel()
	m.active = ui.PageSetup
	m.pages[ui.PageSetup].(*setuppage.Model).SetAutomatic(true)
	request.Page = ui.PageSetup
	next, cmd := m.Update(request)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("missing automatic application")
	}
	next, _ = m.Update(setuppage.ReadyMsg{})
	m = next.(Model)
	if m.active != ui.PageSetup {
		t.Fatal("setup left before ports were applied")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	next, _ = m.Update(setuppage.ReadyMsg{})
	m = next.(Model)
	if m.active != ui.PageOverview {
		t.Fatal("ready setup did not leave after application")
	}
}

func TestEndpointRestart_SetupCommandReachesRoot(t *testing.T) {
	request := ui.ApplySavedEndpointsMsg{Page: ui.PageSetup}
	message := setupCommand(func() tea.Msg { return request })()
	if _, ok := message.(ui.ApplySavedEndpointsMsg); !ok {
		t.Fatalf("request trapped on setup page: %T", message)
	}
}

func TestEndpointRestart_RunWorkerCancelsAndJoins(t *testing.T) {
	_, f, _ := endpointRestartModel()
	f.keepPending = true
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	opts := app.EndpointRestartOptions{Client: f, Service: f, Elevated: func() bool { return true }, Wait: func(ctx context.Context, _ time.Duration) error {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release
		return ctx.Err()
	}}
	w, _ := newInstallationWorker(InstallationActions{})
	apply := endpointRestartTask(w, opts)
	target := f.ports
	done := make(chan error, 1)
	go func() { _, err := apply(context.Background(), target); done <- err }()
	<-entered
	joined := make(chan struct{})
	go func() { w.shutdown(); close(joined) }()
	<-cancelled
	select {
	case <-joined:
		t.Error("shutdown did not join the endpoint task")
	default:
	}
	close(release)
	<-joined
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if _, err := apply(context.Background(), target); !errors.Is(err, context.Canceled) {
		t.Fatal("accepted endpoint work after shutdown")
	}
}

func TestEndpointRestart_PendingApplicationBlocksAnotherMutation(t *testing.T) {
	m, _, request := endpointRestartModel()
	next, cmd := m.Update(request)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("missing application")
	}
	progress := m.modal
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.modal != progress {
		t.Fatal("application progress could be dismissed while running")
	}
	next, other := m.Update(ui.ActionIntentMsg{Action: ui.ActionServiceRestart, Page: ui.PageSystem, Title: "another restart"})
	m = next.(Model)
	if m.modal != progress || other != nil {
		t.Fatal("second service mutation accepted during application")
	}
}
