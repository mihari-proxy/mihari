package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/core"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/supervisor"
)

func TestCoreInstallPhaseTimeout_DefaultIsOneMinute(t *testing.T) {
	if runtimeapi.CoreInstallPhaseTimeout != time.Minute {
		t.Fatalf("install phase = %s", runtimeapi.CoreInstallPhaseTimeout)
	}
}

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
}

func (d *deadlineSupervisor) Run(context.Context) error     { return nil }
func (d *deadlineSupervisor) Restart(context.Context) error { return nil }

func (d *deadlineSupervisor) Update(ctx context.Context, _ func(*supervisor.UpdateSession) error) error {
	d.updateDeadline, d.updateHasDeadline = ctx.Deadline()
	return nil
}

func (d *deadlineSupervisor) Reinstall(ctx context.Context, _ func(*supervisor.UpdateSession) error) error {
	d.reinstallDeadline, d.reinstallHasDeadline = ctx.Deadline()
	return nil
}

func TestCoreInstallPhase_BudgetStartsAfterPrepare(t *testing.T) {
	for _, reinstall := range []bool{false, true} {
		t.Run(map[bool]string{false: "install", true: "reinstall"}[reinstall], func(t *testing.T) {
			budget := 80 * time.Millisecond
			holder := &holdingInstaller{started: make(chan struct{}), release: make(chan struct{})}
			supervisor := &deadlineSupervisor{}
			manager, _, _, _ := seamManager(t, func(o *runtimeapi.Options) {
				holder.inner = o.Installer
				o.Installer = holder
				o.Supervisor = supervisor
				o.CoreInstallTimeout = budget
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
			timer := time.NewTimer(budget + 40*time.Millisecond)
			select {
			case <-timer.C:
			case err := <-errCh:
				t.Fatalf("install finished while prepare was held: %v", err)
			}
			if holder.prepareHasDeadline {
				until := time.Until(holder.prepareDeadline)
				if until <= budget {
					t.Fatalf("prepare deadline %s is inside the install budget %s", until, budget)
				}
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
			deadline, ok := supervisor.updateDeadline, supervisor.updateHasDeadline
			if reinstall {
				deadline, ok = supervisor.reinstallDeadline, supervisor.reinstallHasDeadline
				if supervisor.updateHasDeadline {
					t.Fatal("reinstall used the update session")
				}
			} else if supervisor.reinstallHasDeadline {
				t.Fatal("install used the reinstall session")
			}
			if !ok {
				t.Fatal("install phase has no deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= budget/2 || remaining > budget {
				t.Fatalf("install phase remaining = %s, budget = %s", remaining, budget)
			}
		})
	}
}
