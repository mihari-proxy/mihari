package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/panel"
	"github.com/LeeShunEE/mihari/internal/state"
)

type fakePanels struct {
	mu       sync.Mutex
	active   panel.Active
	list     []panel.PanelInfo
	install  func(ctx context.Context, id, pin string) error
	update   func(ctx context.Context, id string) error
	activate func(ctx context.Context, id string) error
	rollback func(ctx context.Context, id string) error
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
func (f *fakePanels) Install(ctx context.Context, id, pin string) error {
	if f.install != nil {
		return f.install(ctx, id, pin)
	}
	return nil
}
func (f *fakePanels) Update(ctx context.Context, id string) error {
	if f.update != nil {
		return f.update(ctx, id)
	}
	return nil
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
