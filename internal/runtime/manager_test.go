package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/core"
	"github.com/LeeShunEE/mihari/internal/state"
	"github.com/LeeShunEE/mihari/internal/supervisor"
)

func TestInstallCommitAndRestartCannotOverlap(t *testing.T) {
	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	restartCalled := make(chan struct{})
	manager := newTestManager(Options{
		Installer: &fakeInstaller{candidate: &fakeCandidate{
			version: "v1.19.0",
			commit: func() (core.InstallResult, error) {
				close(commitEntered)
				<-releaseCommit
				return core.InstallResult{Version: "v1.19.0", Updated: true}, nil
			},
		}},
		Supervisor: &fakeSupervisor{restart: func(context.Context) error {
			close(restartCalled)
			return nil
		}},
	})
	installDone := make(chan error, 1)
	go func() {
		_, err := manager.Install(context.Background(), Operation{ID: "install-1", Source: "test"})
		installDone <- err
	}()
	select {
	case <-commitEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("install did not enter commit")
	}
	restartDone := make(chan error, 1)
	go func() {
		restartDone <- manager.Restart(context.Background(), Operation{ID: "restart-1", Source: "test"})
	}()
	select {
	case <-restartCalled:
		t.Fatal("restart overlapped install commit")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseCommit)
	if err := <-installDone; err != nil {
		t.Fatal(err)
	}
	if err := <-restartDone; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeMutationWaitsForRestart(t *testing.T) {
	restartEntered := make(chan struct{})
	releaseRestart := make(chan struct{})
	selectCalled := make(chan struct{})
	manager := newTestManager(Options{
		Supervisor: &fakeSupervisor{restart: func(context.Context) error {
			close(restartEntered)
			<-releaseRestart
			return nil
		}},
		Controller: &fakeController{selectProxy: func(context.Context, string, string) error {
			close(selectCalled)
			return nil
		}},
	})
	restartDone := make(chan error, 1)
	go func() { restartDone <- manager.Restart(context.Background(), Operation{ID: "restart", Source: "test"}) }()
	select {
	case <-restartEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not begin")
	}
	selectDone := make(chan error, 1)
	go func() {
		selectDone <- manager.SelectProxy(context.Background(), Operation{ID: "select", Source: "test"}, "GLOBAL", "DIRECT")
	}()
	select {
	case <-selectCalled:
		t.Fatal("proxy selection overlapped restart")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseRestart)
	if err := <-restartDone; err != nil {
		t.Fatal(err)
	}
	if err := <-selectDone; err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateOperationIDExecutesOnce(t *testing.T) {
	prepareEntered := make(chan struct{})
	releasePrepare := make(chan struct{})
	installer := &fakeInstaller{candidate: &fakeCandidate{version: "v1.19.0"}}
	installer.prepare = func(context.Context, core.InstallRequest) (PreparedCore, error) {
		close(prepareEntered)
		<-releasePrepare
		return installer.candidate, nil
	}
	manager := newTestManager(Options{Installer: installer, Supervisor: &fakeSupervisor{}})
	results := make(chan core.InstallResult, 2)
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			result, err := manager.Install(context.Background(), Operation{ID: "same-operation", Source: "test"})
			results <- result
			errors <- err
		}()
	}
	select {
	case <-prepareEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("prepare did not begin")
	}
	close(releasePrepare)
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		if result := <-results; result.Version != "v1.19.0" {
			t.Fatalf("result=%#v", result)
		}
	}
	if got := installer.calls.Load(); got != 1 {
		t.Fatalf("prepare calls=%d", got)
	}
}

func TestInstallRevisionConflictDoesNotCommitCandidate(t *testing.T) {
	var commits atomic.Int64
	manager := newTestManager(Options{
		Installer: &fakeInstaller{candidate: &fakeCandidate{
			version: "v1.19.0",
			commit: func() (core.InstallResult, error) {
				commits.Add(1)
				return core.InstallResult{Version: "v1.19.0", Updated: true}, nil
			},
		}},
		Supervisor: &fakeSupervisor{},
	})
	stale := uint64(99)
	_, err := manager.Install(context.Background(), Operation{ID: "stale-install", Source: "test", IfRevision: &stale})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v", err)
	}
	if commits.Load() != 0 {
		t.Fatal("stale install committed its candidate")
	}
}

func TestInstallUsesCurrentVersionAndSkipsRestartWhenUnchanged(t *testing.T) {
	requestSeen := make(chan core.InstallRequest, 1)
	var restarts atomic.Int64
	installer := &fakeInstaller{candidate: &fakeCandidate{version: "v1.19.0", notUpdated: true}}
	installer.prepare = func(_ context.Context, request core.InstallRequest) (PreparedCore, error) {
		requestSeen <- request
		return installer.candidate, nil
	}
	manager := newTestManager(Options{
		Installer: installer,
		Supervisor: &fakeSupervisor{restart: func(context.Context) error {
			restarts.Add(1)
			return nil
		}},
	})
	snapshot := manager.store.Load()
	snapshot.Core.Version = "v1.19.0"
	manager.store.Store(snapshot)
	manager.running.Store(true)
	result, err := manager.Install(context.Background(), Operation{ID: "up-to-date", Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if request := <-requestSeen; request.CurrentVersion != "v1.19.0" {
		t.Fatalf("current version=%q", request.CurrentVersion)
	}
	if result.Updated || restarts.Load() != 0 {
		t.Fatalf("result=%#v restarts=%d", result, restarts.Load())
	}
}

func TestMissingCoreStaysControllableAndStartsAfterInstall(t *testing.T) {
	var exists atomic.Bool
	supervisorStarted := make(chan struct{})
	manager := newTestManager(Options{
		BinaryExists: func() bool { return exists.Load() },
		Installer: &fakeInstaller{candidate: &fakeCandidate{
			version: "v1.19.0",
			commit: func() (core.InstallResult, error) {
				exists.Store(true)
				return core.InstallResult{Version: "v1.19.0", Updated: true}, nil
			},
		}},
		Supervisor: &fakeSupervisor{run: func(ctx context.Context) error {
			close(supervisorStarted)
			<-ctx.Done()
			return nil
		}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	requireCoreStatus(t, manager.store, string(supervisor.Status("missing")))
	if _, err := manager.Install(context.Background(), Operation{ID: "install", Source: "test"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-supervisorStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not start after install")
	}
	cancel()
	if err := waitManager(t, done); err != nil {
		t.Fatal(err)
	}
	err := manager.SelectProxy(context.Background(), Operation{ID: "after-stop", Source: "test"}, "GLOBAL", "DIRECT")
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidState {
		t.Fatalf("post-shutdown err=%v", err)
	}
}

func newTestManager(options Options) *Manager {
	store := state.NewStore(state.Snapshot{Health: "ok"})
	options.Store = store
	options.Coordinator = state.NewCoordinator(store)
	if options.BinaryExists == nil {
		options.BinaryExists = func() bool { return true }
	}
	manager := New(options)
	manager.store = store
	return manager
}

type fakeInstaller struct {
	calls     atomic.Int64
	candidate PreparedCore
	prepare   func(context.Context, core.InstallRequest) (PreparedCore, error)
}

func (i *fakeInstaller) Prepare(ctx context.Context, request core.InstallRequest) (PreparedCore, error) {
	i.calls.Add(1)
	if i.prepare != nil {
		return i.prepare(ctx, request)
	}
	return i.candidate, nil
}

type fakeCandidate struct {
	version    string
	notUpdated bool
	commit     func() (core.InstallResult, error)
	cleaned    atomic.Bool
}

func (c *fakeCandidate) Version() string { return c.version }

func (c *fakeCandidate) Updated() bool { return !c.notUpdated }

func (c *fakeCandidate) Commit() (core.InstallResult, error) {
	if c.commit != nil {
		return c.commit()
	}
	return core.InstallResult{Version: c.version, Updated: !c.notUpdated}, nil
}

func (c *fakeCandidate) Cleanup() { c.cleaned.Store(true) }

type fakeSupervisor struct {
	run     func(context.Context) error
	restart func(context.Context) error
}

func (s *fakeSupervisor) Run(ctx context.Context) error {
	if s.run != nil {
		return s.run(ctx)
	}
	<-ctx.Done()
	return nil
}

func (s *fakeSupervisor) Restart(ctx context.Context) error {
	if s.restart != nil {
		return s.restart(ctx)
	}
	return nil
}

type fakeController struct {
	selectProxy func(context.Context, string, string) error
}

func (c *fakeController) SelectProxy(ctx context.Context, group, name string) error {
	if c.selectProxy != nil {
		return c.selectProxy(ctx, group, name)
	}
	return nil
}

func requireCoreStatus(t *testing.T, store *state.Store, want string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if got := store.Load().Core.Status; got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("core status=%q want=%q", store.Load().Core.Status, want)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func waitManager(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("manager did not stop")
		return nil
	}
}
