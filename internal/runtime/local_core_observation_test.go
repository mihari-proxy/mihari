package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestLocalCoreObservation_RepeatedQueriesDoNotProbe(t *testing.T) {
	for _, version := range []string{"v1.19.0", ""} {
		t.Run(version, func(t *testing.T) {
			installer := &fakeInstaller{detectVersion: func(context.Context, string) (string, error) {
				return "v9.99.0", nil // A later external replacement must not be silently observed.
			}}
			m := New(Options{Installer: installer, Store: state.NewStore(state.Snapshot{Core: state.CoreState{Version: version}})})
			var readers sync.WaitGroup
			for range 16 {
				readers.Go(func() {
					for range 10 {
						info, err := m.LocalCore(t.Context())
						if err != nil || info.Ready != (version != "") || info.Version != version {
							t.Errorf("observation=%+v err=%v, want version %q", info, err, version)
							return
						}
					}
				})
			}
			readers.Wait()
			if got := installer.detectCalls.Load(); got != 0 {
				t.Fatalf("ordinary queries executed %d version probes", got)
			}
		})
	}
}

func TestLocalCoreObservation_InstallPublishesWithoutAnotherProbe(t *testing.T) {
	installer := &fakeInstaller{candidate: &fakeCandidate{version: "v1.20.0"}}
	m := newTestManager(Options{Installer: installer})
	if info, err := m.LocalCore(t.Context()); err != nil || info.Ready {
		t.Fatalf("before install: %+v err=%v", info, err)
	}
	if _, err := m.Install(t.Context(), Operation{ID: "retry-core-install"}); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		info, err := m.LocalCore(t.Context())
		if err != nil || !info.Ready || info.Version != "v1.20.0" {
			t.Fatalf("after install: %+v err=%v", info, err)
		}
	}
	if installer.detectCalls.Load() != 0 || installer.calls.Load() != 1 {
		t.Fatalf("prepare=%d probes=%d", installer.calls.Load(), installer.detectCalls.Load())
	}
}

func TestLocalCoreObservation_FailedInstallKeepsConfirmedVersion(t *testing.T) {
	cause := errors.New("candidate preparation failed")
	installer := &fakeInstaller{prepare: func(context.Context, core.InstallRequest) (PreparedCore, error) { return nil, cause }}
	m := New(Options{Installer: installer, Store: state.NewStore(state.Snapshot{Core: state.CoreState{Version: "v1.19.0"}})})
	if _, err := m.Install(t.Context(), Operation{ID: "failed-update"}); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if local, err := m.LocalCore(t.Context()); err != nil || !local.Ready || local.Version != "v1.19.0" {
		t.Fatalf("failed preparation replaced observation: %+v err=%v", local, err)
	}
}

func TestLocalCoreObservation_CanceledQueryDoesNotChangeObservation(t *testing.T) {
	m := New(Options{Store: state.NewStore(state.Snapshot{Core: state.CoreState{Version: "v1.19.0"}})})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.LocalCore(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query err=%v", err)
	}
	if info, err := m.LocalCore(t.Context()); err != nil || !info.Ready || info.Version != "v1.19.0" {
		t.Fatalf("cancellation changed observation: %+v err=%v", info, err)
	}
}

func TestLocalCoreObservation_LateSetupProbeCannotReplaceNewInstall(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finished := make(chan struct{})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); <-finished })
	installer := &fakeInstaller{candidate: &fakeCandidate{version: "v1.20.0"}, detectVersion: func(ctx context.Context, _ string) (string, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-release:
			return "v1.19.0", nil
		}
	}}
	m := newTestManager(Options{Installer: installer})
	done := make(chan error, 1)
	go func() {
		defer close(finished)
		_, err := m.Install(t.Context(), Operation{ID: "old-setup", Source: "setup"})
		done <- err
	}()
	<-entered
	if _, err := m.Install(t.Context(), Operation{ID: "new-install"}); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	var api protocol.APIError
	if err := <-done; !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("late setup did not reject stale version: %v", err)
	}
	if info, err := m.LocalCore(t.Context()); err != nil || !info.Ready || info.Version != "v1.20.0" {
		t.Fatalf("late probe replaced installed version: %+v err=%v", info, err)
	}
}

func TestLocalCoreObservation_SetupQueriesDoNotCheckBinary(t *testing.T) {
	service, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json")})
	if err != nil {
		t.Fatal(err)
	}
	var checks atomic.Int64
	m := New(Options{
		Onboarding:   service,
		Store:        state.NewStore(state.Snapshot{Core: state.CoreState{Version: "v1.19.0", Status: "stopped"}}),
		BinaryExists: func() bool { checks.Add(1); return false },
	})
	for range 100 {
		if required, err := m.SetupRequired(t.Context()); err != nil || required {
			t.Fatalf("last confirmed local core should remain ready: required=%v err=%v", required, err)
		}
	}
	if got := checks.Load(); got != 0 {
		t.Fatalf("ordinary setup queries checked the binary %d times", got)
	}
}
