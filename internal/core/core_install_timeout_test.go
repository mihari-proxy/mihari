package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/core"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/supervisor"
)

type holdingInstaller struct {
	inner              runtimeapi.CoreInstaller
	started            chan struct{}
	release            chan struct{}
	prepareDeadline    time.Time
	prepareHasDeadline bool
}

func (h *holdingInstaller) DetectVersion(ctx context.Context, path string) (string, error) {
	return h.inner.DetectVersion(ctx, path)
}

func (h *holdingInstaller) Prepare(ctx context.Context, request core.InstallRequest) (core.PreparedCore, error) {
	h.prepareDeadline, h.prepareHasDeadline = ctx.Deadline()
	close(h.started)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.release:
	}
	if fixture, ok := h.inner.(*core.TestTrustedFixture); ok {
		return fixture.PrepareUpdate(ctx, request)
	}
	return h.inner.Prepare(ctx, request)
}

type deadlineSupervisor struct {
	updateDeadline       time.Time
	updateHasDeadline    bool
	reinstallDeadline    time.Time
	reinstallHasDeadline bool
	updateCalls          int
	reinstallCalls       int
}

func (d *deadlineSupervisor) Run(context.Context) error     { return nil }
func (d *deadlineSupervisor) Restart(context.Context) error { return nil }

func (d *deadlineSupervisor) Update(ctx context.Context, _ func(*supervisor.UpdateSession) error) error {
	d.updateCalls++
	d.updateDeadline, d.updateHasDeadline = ctx.Deadline()
	return nil
}

func (d *deadlineSupervisor) Reinstall(ctx context.Context, _ func(*supervisor.UpdateSession) error) error {
	d.reinstallCalls++
	d.reinstallDeadline, d.reinstallHasDeadline = ctx.Deadline()
	return nil
}

func (d *deadlineSupervisor) assertSelected(t *testing.T, reinstall bool) {
	t.Helper()
	if reinstall {
		if d.updateCalls != 0 || d.reinstallCalls != 1 {
			t.Fatalf("update calls = %d, reinstall calls = %d", d.updateCalls, d.reinstallCalls)
		}
		if d.reinstallHasDeadline {
			t.Fatalf("reinstall added a deadline at %s", d.reinstallDeadline)
		}
		return
	}
	if d.reinstallCalls != 0 || d.updateCalls != 1 {
		t.Fatalf("update calls = %d, reinstall calls = %d", d.updateCalls, d.reinstallCalls)
	}
	if d.updateHasDeadline {
		t.Fatalf("install added a deadline at %s", d.updateDeadline)
	}
}

type blockingSupervisor struct {
	started        chan struct{}
	updateCalls    int
	reinstallCalls int
}

func (b *blockingSupervisor) Run(context.Context) error     { return nil }
func (b *blockingSupervisor) Restart(context.Context) error { return nil }

func (b *blockingSupervisor) Update(ctx context.Context, _ func(*supervisor.UpdateSession) error) error {
	b.updateCalls++
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

func (b *blockingSupervisor) Reinstall(ctx context.Context, _ func(*supervisor.UpdateSession) error) error {
	b.reinstallCalls++
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

func (b *blockingSupervisor) assertSelected(t *testing.T, reinstall bool) {
	t.Helper()
	if reinstall {
		if b.updateCalls != 0 || b.reinstallCalls != 1 {
			t.Fatalf("update calls = %d, reinstall calls = %d", b.updateCalls, b.reinstallCalls)
		}
		return
	}
	if b.reinstallCalls != 0 || b.updateCalls != 1 {
		t.Fatalf("update calls = %d, reinstall calls = %d", b.updateCalls, b.reinstallCalls)
	}
}

func TestCoreInstall_DoesNotAddDeadline(t *testing.T) {
	for _, reinstall := range []bool{false, true} {
		t.Run(map[bool]string{false: "install", true: "reinstall"}[reinstall], func(t *testing.T) {
			holder := &holdingInstaller{started: make(chan struct{}), release: make(chan struct{})}
			supervisor := &deadlineSupervisor{}
			manager, _, _, _ := seamManager(t, func(o *runtimeapi.Options) {
				holder.inner = o.Installer
				o.Installer = holder
				o.Supervisor = supervisor
			})
			errCh := make(chan error, 1)
			go func() {
				var err error
				if reinstall {
					_, err = manager.Reinstall(context.Background(), runtimeapi.Operation{ID: "phase-reinstall"})
				} else {
					_, err = manager.Install(context.Background(), runtimeapi.Operation{ID: "phase-install"})
				}
				errCh <- err
			}()
			select {
			case <-holder.started:
			case <-time.After(2 * time.Second):
				t.Fatal("prepare did not start")
			}
			if holder.prepareHasDeadline {
				t.Fatalf("prepare added a deadline at %s", holder.prepareDeadline)
			}
			close(holder.release)
			select {
			case err := <-errCh:
				if err != nil {
					t.Fatalf("install: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("install did not finish after prepare")
			}
			supervisor.assertSelected(t, reinstall)
		})
	}
}

func TestCoreInstall_ParentCancelReachesInstall(t *testing.T) {
	for _, reinstall := range []bool{false, true} {
		t.Run(map[bool]string{false: "install", true: "reinstall"}[reinstall], func(t *testing.T) {
			holder := &holdingInstaller{started: make(chan struct{}), release: make(chan struct{})}
			supervisor := &blockingSupervisor{started: make(chan struct{})}
			manager, _, _, _ := seamManager(t, func(o *runtimeapi.Options) {
				holder.inner = o.Installer
				o.Installer = holder
				o.Supervisor = supervisor
			})
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			errCh := make(chan error, 1)
			go func() {
				var err error
				if reinstall {
					_, err = manager.Reinstall(parent, runtimeapi.Operation{ID: "cancel-reinstall"})
				} else {
					_, err = manager.Install(parent, runtimeapi.Operation{ID: "cancel-install"})
				}
				errCh <- err
			}()
			select {
			case <-holder.started:
			case <-time.After(2 * time.Second):
				t.Fatal("prepare did not start")
			}
			close(holder.release)
			select {
			case <-supervisor.started:
			case <-time.After(2 * time.Second):
				t.Fatal("install session did not start")
			}
			cancel()
			select {
			case err := <-errCh:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("parent cancel = %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("install did not observe parent cancel")
			}
			supervisor.assertSelected(t, reinstall)
		})
	}
}
