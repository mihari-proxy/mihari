package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/state"
)

type fakePanels struct {
	mu               sync.Mutex
	active           panel.Active
	list             []panel.PanelInfo
	install          func(ctx context.Context, id, pin string) error
	installCommit    func() error
	installCandidate panel.PreparedMutation
	update           func(ctx context.Context, id string) error
	activate         func(ctx context.Context, id string) error
	rollback         func(ctx context.Context, id string) error
	uninstall        func(ctx context.Context, id string) error
	reinstall        func(ctx context.Context, id string) error
	updateCommit     func() error
	reinstallCommit  func() error
	updateCalls      []string
	installCalls     []string
	uninstallCalls   []string
	reinstallCalls   []string
}

func (f *fakePanels) List() []panel.PanelInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]panel.PanelInfo(nil), f.list...)
}
func (f *fakePanels) Active() (panel.Active, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active, nil
}
func (f *fakePanels) ActiveDir() (string, error) { return "", nil }
func (f *fakePanels) PanelDir(id string) (string, error) {
	// Non-empty marks the panel as installed for OpenWebGUI tests.
	if id == "" {
		return "", nil
	}
	return "/tmp/mihari-panel-" + id, nil
}
func (f *fakePanels) SetupPath(string) string { return "/" }
func (f *fakePanels) SetupPathFor(string, string) string {
	return "/__mihari/panels/zashboard/#/setup"
}
func (f *fakePanels) PrepareInstall(ctx context.Context, id, pin string) (panel.PreparedMutation, error) {
	f.mu.Lock()
	f.installCalls = append(f.installCalls, id)
	install := f.install
	commit := f.installCommit
	candidate := f.installCandidate
	f.mu.Unlock()
	if install != nil {
		if err := install(ctx, id, pin); err != nil {
			return nil, err
		}
	}
	if candidate != nil {
		return candidate, nil
	}
	return &fakePreparedPanelMutation{commit: commit}, nil
}
func (f *fakePanels) PrepareUpdate(ctx context.Context, id string) (panel.PreparedMutation, error) {
	f.mu.Lock()
	f.updateCalls = append(f.updateCalls, id)
	update := f.update
	commit := f.updateCommit
	f.mu.Unlock()
	if update != nil {
		if err := update(ctx, id); err != nil {
			return nil, err
		}
	}
	return &fakePreparedPanelMutation{commit: commit}, nil
}
func (f *fakePanels) Activate(ctx context.Context, id string) error {
	if f.activate != nil {
		return f.activate(ctx, id)
	}
	f.mu.Lock()
	f.active = panel.Active{Panel: id, Build: "v1"}
	f.mu.Unlock()
	return nil
}
func (f *fakePanels) Rollback(ctx context.Context, id string) error {
	if f.rollback != nil {
		return f.rollback(ctx, id)
	}
	return nil
}
func (f *fakePanels) Uninstall(ctx context.Context, id string) error {
	f.mu.Lock()
	f.uninstallCalls = append(f.uninstallCalls, id)
	uninstall := f.uninstall
	if uninstall != nil {
		f.mu.Unlock()
		return uninstall(ctx, id)
	}
	if f.active.Panel == id {
		f.active = panel.Active{}
	}
	f.mu.Unlock()
	return nil
}
func (f *fakePanels) PrepareReinstall(ctx context.Context, id string) (panel.PreparedMutation, error) {
	f.mu.Lock()
	f.reinstallCalls = append(f.reinstallCalls, id)
	reinstall := f.reinstall
	commit := f.reinstallCommit
	if reinstall != nil {
		f.mu.Unlock()
		if err := reinstall(ctx, id); err != nil {
			return nil, err
		}
		return &fakePreparedPanelMutation{commit: commit}, nil
	}
	f.mu.Unlock()
	return &fakePreparedPanelMutation{commit: commit}, nil
}

type fakePreparedPanelMutation struct {
	mu        sync.Mutex
	commit    func() error
	committed bool
	cleaned   bool
	identity  func() string
	valid     func() bool
}

func (p *fakePreparedPanelMutation) Valid() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.valid != nil {
		return p.valid()
	}
	return !p.cleaned
}

func (p *fakePreparedPanelMutation) Identity() string {
	if p.identity != nil {
		return p.identity()
	}
	return "fake-panel-candidate"
}

func (p *fakePreparedPanelMutation) Commit() error {
	p.mu.Lock()
	if p.committed {
		p.mu.Unlock()
		return nil
	}
	if p.cleaned {
		p.mu.Unlock()
		return errors.New("prepared panel candidate was cleaned")
	}
	commit := p.commit
	p.mu.Unlock()
	if commit != nil {
		if err := commit(); err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.committed = true
	p.mu.Unlock()
	return nil
}

func (p *fakePreparedPanelMutation) Cleanup() {
	p.mu.Lock()
	p.cleaned = true
	p.mu.Unlock()
}

func (f *fakePanels) mutationCallCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch name {
	case "install":
		return len(f.installCalls)
	case "update":
		return len(f.updateCalls)
	case "uninstall":
		return len(f.uninstallCalls)
	case "reinstall":
		return len(f.reinstallCalls)
	default:
		return 0
	}
}

func TestActivatePanelCommitsThroughCoordinator(t *testing.T) {
	panels := &fakePanels{}
	manager := newTestManager(Options{Panels: panels})
	if err := manager.ActivatePanel(context.Background(), Operation{ID: "act-1", Source: "test"}, panel.IDZashboard); err != nil {
		t.Fatal(err)
	}
	if manager.Snapshot().Revision != 1 {
		t.Fatalf("revision=%d", manager.Snapshot().Revision)
	}
	active, err := manager.ActivePanel(context.Background())
	if err != nil || active.Panel != panel.IDZashboard {
		t.Fatalf("active=%#v err=%v", active, err)
	}
}

func TestInstallPanelDownloadsOutsideLockThenPublishesRevision(t *testing.T) {
	installEntered := make(chan struct{})
	releaseInstall := make(chan struct{})
	panels := &fakePanels{
		install: func(ctx context.Context, id, pin string) error {
			close(installEntered)
			select {
			case <-releaseInstall:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	manager := newTestManager(Options{Panels: panels})
	done := make(chan error, 1)
	go func() {
		done <- manager.InstallPanel(context.Background(), Operation{ID: "inst-1", Source: "test"}, panel.IDZashboard, "")
	}()
	select {
	case <-installEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("install did not start outside lock")
	}
	// Concurrent activate must not be blocked by long download forever; it takes the lock after download.
	// Restart/select-style check: another mutation waiting on maintenance is OK after install finishes.
	close(releaseInstall)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if manager.Snapshot().Revision != 1 {
		t.Fatalf("revision=%d", manager.Snapshot().Revision)
	}
}

func TestInstallPanelRejectsPreflightFailuresBeforePrepare(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Manager) Operation
		wantCode  protocol.ErrorCode
	}{
		{
			name: "stale revision",
			configure: func(manager *Manager) Operation {
				manager.store.Store(state.Snapshot{Revision: 5})
				stale := uint64(4)
				return Operation{ID: "stale-install", Source: "test", IfRevision: &stale}
			},
			wantCode: protocol.CodeRevisionConflict,
		},
		{
			name: "degraded mutation state",
			configure: func(manager *Manager) Operation {
				manager.mutationDegraded.Store(true)
				return Operation{ID: "degraded-install", Source: "test"}
			},
			wantCode: protocol.CodeInvalidState,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			panels := &fakePanels{}
			manager := newTestManager(Options{Panels: panels})
			operation := test.configure(manager)

			err := manager.InstallPanel(context.Background(), operation, panel.IDZashboard, "")
			var apiError protocol.APIError
			if !errors.As(err, &apiError) || apiError.Code != test.wantCode {
				t.Fatalf("err=%v want code %s", err, test.wantCode)
			}
			if calls := panels.mutationCallCount("install"); calls != 0 {
				t.Fatalf("prepare calls=%d want=0", calls)
			}
		})
	}
}

func TestInstallPanelRejectsRevisionChangeAfterPrepareWithoutCommit(t *testing.T) {
	prepareEntered := make(chan struct{})
	releasePrepare := make(chan struct{})
	committed := false
	panels := &fakePanels{
		install: func(ctx context.Context, _, _ string) error {
			close(prepareEntered)
			select {
			case <-releasePrepare:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		installCommit: func() error {
			committed = true
			return nil
		},
	}
	manager := newTestManager(Options{Panels: panels})
	current := uint64(0)
	done := make(chan error, 1)
	go func() {
		done <- manager.InstallPanel(context.Background(), Operation{
			ID: "install-after-revision-change", Source: "test", IfRevision: &current,
		}, panel.IDZashboard, "")
	}()
	<-prepareEntered
	if err := manager.ActivatePanel(context.Background(), Operation{ID: "newer-panel-state", Source: "test"}, panel.IDZashboard); err != nil {
		close(releasePrepare)
		t.Fatal(err)
	}
	close(releasePrepare)

	err := <-done
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v want revision conflict", err)
	}
	if committed {
		t.Fatal("stale prepared install committed")
	}
	if revision := manager.Snapshot().Revision; revision != 1 {
		t.Fatalf("revision=%d want=1", revision)
	}
}

func TestInstallPanelSettlesCanceledCommitAndCachesSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	prepareCalls := 0
	commitCalls := 0
	panels := &fakePanels{
		install: func(context.Context, string, string) error {
			prepareCalls++
			return nil
		},
		installCommit: func() error {
			commitCalls++
			cancel()
			return nil
		},
	}
	manager := newTestManager(Options{Panels: panels})
	operation := Operation{ID: "cancel-after-panel-commit", Source: "test"}

	if err := manager.InstallPanel(ctx, operation, panel.IDZashboard, ""); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if revision := manager.Snapshot().Revision; revision != 1 {
		t.Fatalf("revision=%d want=1", revision)
	}
	if err := manager.InstallPanel(context.Background(), operation, panel.IDZashboard, ""); err != nil {
		t.Fatalf("cached retry: %v", err)
	}
	if prepareCalls != 1 || commitCalls != 1 {
		t.Fatalf("prepare calls=%d commit calls=%d want 1 each", prepareCalls, commitCalls)
	}
}

func TestInstallPanelRejectsChangedOrInvalidCandidate(t *testing.T) {
	tests := []struct {
		name      string
		candidate *fakePreparedPanelMutation
	}{
		{
			name: "changed identity",
			candidate: func() *fakePreparedPanelMutation {
				calls := 0
				return &fakePreparedPanelMutation{identity: func() string {
					calls++
					if calls == 1 {
						return "candidate-a"
					}
					return "candidate-b"
				}}
			}(),
		},
		{
			name:      "invalid candidate",
			candidate: &fakePreparedPanelMutation{valid: func() bool { return false }},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			committed := false
			test.candidate.commit = func() error {
				committed = true
				return nil
			}
			panels := &fakePanels{installCandidate: test.candidate}
			manager := newTestManager(Options{Panels: panels})

			err := manager.InstallPanel(context.Background(), Operation{ID: test.name, Source: "test"}, panel.IDZashboard, "")
			var apiError protocol.APIError
			if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
				t.Fatalf("err=%v want data failure", err)
			}
			if committed {
				t.Fatal("rejected candidate committed")
			}
			if revision := manager.Snapshot().Revision; revision != 0 {
				t.Fatalf("revision=%d want=0", revision)
			}
		})
	}
}

func TestRollbackPanelRejectsStaleRevision(t *testing.T) {
	panels := &fakePanels{}
	manager := newTestManager(Options{Panels: panels})
	manager.store.Store(state.Snapshot{Revision: 5})
	stale := uint64(4)
	err := manager.RollbackPanel(context.Background(), Operation{ID: "rb-1", Source: "test", IfRevision: &stale}, panel.IDZashboard)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v", err)
	}
	if panels.active.Panel != "" {
		t.Fatal("stale rollback mutated panel state")
	}
}

type panelMutationTest struct {
	name    string
	setHook func(*fakePanels, func(context.Context, string) error)
	invoke  func(*Manager, context.Context, Operation) error
}

func TestUpdatePanelCoordinatesRevisionAndServiceFailures(t *testing.T) {
	testPanelMutationSymmetry(t, panelMutationTest{
		name: "update",
		setHook: func(panels *fakePanels, hook func(context.Context, string) error) {
			panels.update = hook
		},
		invoke: func(manager *Manager, ctx context.Context, operation Operation) error {
			return manager.UpdatePanel(ctx, operation, panel.IDZashboard)
		},
	})
}

func TestUninstallPanelCoordinatesRevisionAndServiceFailures(t *testing.T) {
	testPanelMutationSymmetry(t, panelMutationTest{
		name: "uninstall",
		setHook: func(panels *fakePanels, hook func(context.Context, string) error) {
			panels.uninstall = hook
		},
		invoke: func(manager *Manager, ctx context.Context, operation Operation) error {
			return manager.UninstallPanel(ctx, operation, panel.IDZashboard)
		},
	})
}

func TestReinstallPanelCoordinatesRevisionAndServiceFailures(t *testing.T) {
	testPanelMutationSymmetry(t, panelMutationTest{
		name: "reinstall",
		setHook: func(panels *fakePanels, hook func(context.Context, string) error) {
			panels.reinstall = hook
		},
		invoke: func(manager *Manager, ctx context.Context, operation Operation) error {
			return manager.ReinstallPanel(ctx, operation, panel.IDZashboard)
		},
	})
}

func testPanelMutationSymmetry(t *testing.T, mutation panelMutationTest) {
	t.Helper()
	serviceErr := errors.New("panel service failed")
	stale := uint64(6)
	tests := []struct {
		name         string
		ifRevision   *uint64
		serviceErr   error
		wantRevision uint64
		wantCalls    int
		wantConflict bool
	}{
		{name: "success publishes revision", wantRevision: 8, wantCalls: 1},
		{name: "stale revision skips service", ifRevision: &stale, wantRevision: 7, wantCalls: 0, wantConflict: true},
		{name: "service error does not publish revision", serviceErr: serviceErr, wantRevision: 7, wantCalls: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			panels := &fakePanels{}
			mutation.setHook(panels, func(context.Context, string) error {
				return test.serviceErr
			})
			manager := newTestManager(Options{Panels: panels})
			manager.store.Store(state.Snapshot{Revision: 7})

			err := mutation.invoke(manager, context.Background(), Operation{
				ID:         mutation.name + "-1",
				Source:     "test",
				IfRevision: test.ifRevision,
			})
			switch {
			case test.wantConflict:
				var apiError protocol.APIError
				if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
					t.Fatalf("err=%v", err)
				}
			case test.serviceErr != nil:
				if !errors.Is(err, serviceErr) {
					t.Fatalf("err=%v", err)
				}
			case err != nil:
				t.Fatal(err)
			}
			if revision := manager.Snapshot().Revision; revision != test.wantRevision {
				t.Fatalf("revision=%d, want %d", revision, test.wantRevision)
			}
			if calls := panels.mutationCallCount(mutation.name); calls != test.wantCalls {
				t.Fatalf("%s calls=%d, want %d", mutation.name, calls, test.wantCalls)
			}
		})
	}
}

func TestUpdatePanelDownloadsOutsideMutationLock(t *testing.T) {
	updateEntered := make(chan struct{})
	releaseUpdate := make(chan struct{})
	activateEntered := make(chan struct{})
	// The activate hook parks until the test observed activateEntered, so
	// activateDone cannot become ready while the select below is choosing a
	// case. Without the handshake, both channels could be ready at once and
	// the select might report the nil result as a failure.
	activateProceed := make(chan struct{})
	panels := &fakePanels{
		update: func(ctx context.Context, _ string) error {
			close(updateEntered)
			select {
			case <-releaseUpdate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		activate: func(ctx context.Context, _ string) error {
			close(activateEntered)
			select {
			case <-activateProceed:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	manager := newTestManager(Options{Panels: panels})
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- manager.UpdatePanel(context.Background(), Operation{ID: "update-outside-lock", Source: "test"}, panel.IDZashboard)
	}()
	select {
	case <-updateEntered:
	case err := <-updateDone:
		t.Fatalf("update returned before download entered: %v", err)
	}

	activateCtx, cancelActivate := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelActivate()
	activateDone := make(chan error, 1)
	go func() {
		activateDone <- manager.ActivatePanel(activateCtx, Operation{ID: "activate-during-update", Source: "test"}, panel.IDZashboard)
	}()
	select {
	case <-activateEntered:
		close(activateProceed)
	case err := <-activateDone:
		close(releaseUpdate)
		t.Fatalf("activate could not enter while update download was blocked: %v", err)
	}
	if err := <-activateDone; err != nil {
		close(releaseUpdate)
		t.Fatal(err)
	}
	close(releaseUpdate)
	if err := <-updateDone; err != nil {
		t.Fatal(err)
	}
	if revision := manager.Snapshot().Revision; revision != 2 {
		t.Fatalf("revision=%d, want 2", revision)
	}
}

func TestPreparedPanelMutationDoesNotCommitAfterConcurrentUninstall(t *testing.T) {
	current := uint64(7)
	tests := []struct {
		name      string
		setHook   func(*fakePanels, func(context.Context, string) error)
		setCommit func(*fakePanels, func() error)
		invoke    func(*Manager, context.Context, Operation) error
	}{
		{
			name: "update",
			setHook: func(panels *fakePanels, hook func(context.Context, string) error) {
				panels.update = hook
			},
			setCommit: func(panels *fakePanels, commit func() error) {
				panels.updateCommit = commit
			},
			invoke: func(manager *Manager, ctx context.Context, operation Operation) error {
				return manager.UpdatePanel(ctx, operation, panel.IDZashboard)
			},
		},
		{
			name: "reinstall",
			setHook: func(panels *fakePanels, hook func(context.Context, string) error) {
				panels.reinstall = hook
			},
			setCommit: func(panels *fakePanels, commit func() error) {
				panels.reinstallCommit = commit
			},
			invoke: func(manager *Manager, ctx context.Context, operation Operation) error {
				return manager.ReinstallPanel(ctx, operation, panel.IDZashboard)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceEntered := make(chan struct{})
			releaseService := make(chan struct{})
			var promoted bool
			panels := &fakePanels{}
			test.setHook(panels, func(ctx context.Context, _ string) error {
				close(serviceEntered)
				select {
				case <-releaseService:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			test.setCommit(panels, func() error {
				promoted = true
				return nil
			})
			manager := newTestManager(Options{Panels: panels})
			manager.store.Store(state.Snapshot{Revision: current})
			mutationDone := make(chan error, 1)
			go func() {
				mutationDone <- test.invoke(manager, context.Background(), Operation{
					ID: test.name + "-concurrent-uninstall", Source: "test", IfRevision: &current,
				})
			}()
			select {
			case <-serviceEntered:
			case err := <-mutationDone:
				t.Fatalf("%s returned before service entered: %v", test.name, err)
			}

			if err := manager.UninstallPanel(context.Background(), Operation{
				ID: "newer-uninstall", Source: "test",
			}, panel.IDZashboard); err != nil {
				close(releaseService)
				t.Fatal(err)
			}
			close(releaseService)
			err := <-mutationDone
			var apiError protocol.APIError
			if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
				t.Fatalf("err=%v", err)
			}
			if promoted {
				t.Fatalf("stale %s promoted after the newer uninstall committed", test.name)
			}
			if revision := manager.Snapshot().Revision; revision != current+1 {
				t.Fatalf("revision=%d, want %d", revision, current+1)
			}
		})
	}
}
