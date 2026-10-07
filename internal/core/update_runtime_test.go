package core_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/supervisor"
)

type updateInstaller struct{ *core.TestTrustedFixture }

func (i *updateInstaller) Prepare(ctx context.Context, request core.InstallRequest) (core.PreparedCore, error) {
	return i.PrepareUpdate(ctx, request)
}

func TestCoreUpdateRuntimeFirstInstallStartsButIdleUpdateStaysStopped(t *testing.T) {
	for _, firstInstall := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "first install"}[firstInstall], func(t *testing.T) {
			installer := &updateInstaller{}
			starter := &capabilityStarter{seamStarter: &seamStarter{children: make(chan *seamChild, 8)}}
			var manager *runtimeapi.Manager
			var healthChecks atomic.Int32
			ready := make(chan struct{})
			var readyOnce sync.Once
			sup := supervisor.New(supervisor.Options{
				Starter: starter, Waiter: updateTestWaiter{},
				Now: func() time.Time {
					// Run reads the clock after publishing its event-loop lifetime.
					readyOnce.Do(func() { close(ready) })
					return time.Now()
				},
				BeforeStart: func(ctx context.Context) (func(), error) { return manager.PrepareCoreStart(ctx) },
				Health: func(context.Context) error {
					healthChecks.Add(1)
					if firstInstall && !starter.fixture.UpdateStartsNewCore() {
						t.Error("first-install running intent was not persisted")
					}
					return nil
				},
				Observe: func(o supervisor.Observation) { manager.Observe(o) },
			})
			manager, starter.fixture, _, _ = seamManager(t, func(o *runtimeapi.Options) {
				o.Supervisor, o.Installer = sup, installer
				o.SysProxy = &seamSystemProxy{}
				o.BinaryExists = func() bool { return len(starter.fixture.Binary()) != 0 }
			})
			installer.TestTrustedFixture = starter.fixture
			starter.inspect = starter.fixture.Binary
			if firstInstall {
				starter.fixture.RemoveInstalledCore()
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				go func() { done <- manager.Run(ctx) }()
				t.Cleanup(func() {
					cancel()
					if err := awaitSeam(t, done); err != nil {
						t.Error(err)
					}
				})
				select {
				case <-ready:
				case <-time.After(5 * time.Second):
					t.Fatal("supervisor did not enter the waiting event loop")
				}
			}
			channel := "alpha"
			_, err := manager.Install(context.WithValue(t.Context(), updateTestContextKey{}, true), runtimeapi.Operation{ID: "first-or-stopped", Channel: &channel})
			if err != nil {
				t.Fatal(err)
			}
			want := int32(0)
			if firstInstall {
				want = 1
			}
			if starter.starts.Load() != want || healthChecks.Load() != want {
				t.Fatalf("starts=%d health=%d want=%d", starter.starts.Load(), healthChecks.Load(), want)
			}
		})
	}
}

type updateTestContextKey struct{}
type updateTestWaiter struct{}

func (updateTestWaiter) Wait(ctx context.Context, _ time.Duration) error {
	if ctx.Value(updateTestContextKey{}) != nil {
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}

type capabilityStarter struct {
	*seamStarter
	fixture *core.TestTrustedFixture
}

func (s *capabilityStarter) StartContext(ctx context.Context) (supervisor.Child, error) {
	_, release, err := s.fixture.Trusted.RunCommand(ctx)
	if err != nil {
		return nil, err
	}
	if err := release(); err != nil {
		return nil, err
	}
	return s.Start()
}

func TestCoreUpdateRuntimeCommitsAfterHealthAndRecoversSettingsFailure(t *testing.T) {
	for _, failSettings := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "settings failure"}[failSettings], func(t *testing.T) {
			starter := &capabilityStarter{seamStarter: &seamStarter{children: make(chan *seamChild, 8)}}
			installer := &updateInstaller{}
			var manager *runtimeapi.Manager
			var healthChecks atomic.Int32
			failure := errors.New("synthetic settings failure")
			disk := config.Defaults()
			disk.CoreChannelBundle = "existing-bundle-stamp"
			sup := supervisor.New(supervisor.Options{
				Starter: starter, Waiter: updateTestWaiter{},
				BeforeStart: func(ctx context.Context) (func(), error) { return manager.PrepareCoreStart(ctx) },
				Observe:     func(o supervisor.Observation) { manager.Observe(o) },
				Health: func(context.Context) error {
					healthChecks.Add(1)
					if manager.Snapshot().Core.Channel != "stable" || disk.CoreChannel != "stable" {
						t.Error("new channel visible before health/commit")
					}
					other, cancel := context.WithTimeout(t.Context(), time.Second)
					defer cancel()
					err := manager.Restart(other, runtimeapi.Operation{ID: "concurrent-restart"})
					var api protocol.APIError
					if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
						t.Errorf("reservation did not reject concurrent mutation promptly: %v", err)
					}
					return nil
				},
			})
			manager, starter.fixture, _, _ = seamManager(t, func(o *runtimeapi.Options) {
				o.Supervisor, o.Installer = sup, installer
				o.Settings.CoreChannelBundle = disk.CoreChannelBundle
				o.Store = state.NewStore(state.Snapshot{Health: "ok", Core: state.CoreState{Version: "v1.19.30", Channel: "stable", Status: "stopped"}})
				o.SettingsPath = filepath.Join(t.TempDir(), "settings.yaml")
				o.SaveSettings = func(_ string, settings config.Settings) (config.CommitResult, error) {
					if failSettings && settings.CoreChannel == "alpha" {
						return config.CommitResult{}, failure
					}
					disk = settings.Clone()
					return config.CommitResult{Committed: true}, nil
				}
			})
			installer.TestTrustedFixture = starter.fixture
			starter.inspect = starter.fixture.Binary
			startSeamSupervisor(t, manager, sup, starter.seamStarter)
			channel := "alpha"
			ctx := context.WithValue(t.Context(), updateTestContextKey{}, true)
			_, err := manager.Install(ctx, runtimeapi.Operation{ID: "official-update", Channel: &channel})
			wantChannel, wantBinary, wantStarts := "alpha", "new official alpha", int32(2)
			if failSettings {
				if !errors.Is(err, failure) {
					t.Fatalf("lost update failure: %v", err)
				}
				wantChannel, wantBinary, wantStarts = "stable", "old trusted binary", 3
			} else if err != nil {
				t.Fatal(err)
			}
			if disk.CoreChannel != wantChannel || disk.CoreChannelBundle != "existing-bundle-stamp" || manager.Snapshot().Core.Channel != wantChannel {
				t.Fatalf("settings/state inconsistent: disk=%s bundle=%s state=%+v", disk.CoreChannel, disk.CoreChannelBundle, manager.Snapshot().Core)
			}
			if got := string(starter.fixture.Binary()); got != wantBinary {
				t.Fatalf("core=%q want=%q", got, wantBinary)
			}
			if starter.starts.Load() != wantStarts || healthChecks.Load() != wantStarts-1 {
				t.Fatalf("starts=%d health=%d", starter.starts.Load(), healthChecks.Load())
			}
			if starter.fixture.UpdatePending() {
				t.Fatal("completed update retains blocking journal")
			}
			if !starter.fixture.HasDeferredUpdate() {
				t.Fatal("completed update did not retain cleanup authority until startup")
			}
		})
	}
}

func TestCoreUpdateRuntimeRestartDoesNotRepairFailedUpdate(t *testing.T) {
	installer := &updateInstaller{}
	starter := &capabilityStarter{seamStarter: &seamStarter{children: make(chan *seamChild, 8)}}
	var manager *runtimeapi.Manager
	updateErr, restoreErr := errors.New("candidate unhealthy"), errors.New("old core health also failed")
	var repaired atomic.Bool
	sup := supervisor.New(supervisor.Options{
		Starter: starter, Waiter: updateTestWaiter{},
		BeforeStart: func(ctx context.Context) (func(), error) { return manager.PrepareCoreStart(ctx) },
		Observe:     func(o supervisor.Observation) { manager.Observe(o) },
		Health: func(context.Context) error {
			if repaired.Load() {
				return nil
			}
			if starter.starts.Load() == 2 {
				return updateErr
			}
			return restoreErr
		},
	})
	manager, starter.fixture, _, _ = seamManager(t, func(o *runtimeapi.Options) {
		o.Installer, o.Supervisor = installer, sup
		o.Store = state.NewStore(state.Snapshot{Health: "ok", Core: state.CoreState{Version: "v1.19.30", Channel: "stable"}})
	})
	installer.TestTrustedFixture = starter.fixture
	starter.inspect = starter.fixture.Binary
	startSeamSupervisor(t, manager, sup, starter.seamStarter)
	channel := "alpha"
	ctx := context.WithValue(t.Context(), updateTestContextKey{}, true)
	_, err := manager.Install(ctx, runtimeapi.Operation{ID: "fails-twice", Channel: &channel})
	if !errors.Is(err, updateErr) || !errors.Is(err, restoreErr) {
		t.Fatalf("lost failures: %v", err)
	}
	if starter.starts.Load() != 3 || manager.Snapshot().Health != "degraded" || !starter.fixture.UpdatePending() {
		t.Fatal("failed recovery did not preserve blocked transaction")
	}
	if err := sup.Restart(ctx); err == nil {
		t.Fatal("ordinary supervisor restart bypassed recovery")
	}
	if err := manager.Restart(ctx, runtimeapi.Operation{ID: "ordinary-restart"}); err == nil {
		t.Fatal("ordinary manager restart bypassed the reinstall requirement")
	}
	if starter.starts.Load() != 3 || manager.Snapshot().Health != "degraded" || !starter.fixture.UpdatePending() {
		t.Fatalf("restart changed blocked update: starts=%d health=%s pending=%v", starter.starts.Load(), manager.Snapshot().Health, starter.fixture.UpdatePending())
	}
	if got := string(starter.fixture.Binary()); got != "old trusted binary" {
		t.Fatalf("recovery installed another core: %q", got)
	}
	if _, err := manager.Reinstall(ctx, runtimeapi.Operation{ID: "failed-reinstall"}); !errors.Is(err, restoreErr) {
		t.Fatalf("failed reinstall lost its health failure: %v", err)
	}
	if manager.Snapshot().Health != "degraded" || !starter.fixture.UpdatePending() || starter.starts.Load() != 4 {
		t.Fatal("failed reinstall cleared blocking state or tried the uncertain old core")
	}
	repaired.Store(true)
	result, err := manager.Reinstall(ctx, runtimeapi.Operation{ID: "user-reinstall"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "v1.20.0" || manager.Snapshot().Core.Channel != "stable" || manager.Snapshot().Health != "ok" || starter.fixture.UpdatePending() {
		t.Fatalf("reinstall failed to restore original channel: result=%+v snapshot=%+v", result, manager.Snapshot())
	}
	if starter.starts.Load() != 5 || string(starter.fixture.Binary()) != "new official stable" {
		t.Fatalf("reinstall did not adopt fresh official core: starts=%d binary=%q", starter.starts.Load(), starter.fixture.Binary())
	}
}
