package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/supervisor"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
	"path/filepath"
	"strings"
	"testing"
)

type resetFailureStore struct {
	fail    bool
	pending bool
}

func (s *resetFailureStore) Preview(context.Context) (protocol.ResetPreview, error) {
	return protocol.ResetPreview{Digest: "fixture"}, nil
}
func (s *resetFailureStore) Reset(context.Context, string, func(error)) (config.Settings, string, error) {
	s.pending = true
	if s.fail {
		return config.Settings{}, "", errors.New("partial reset failed")
	}
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	return settings, "fixture-web", nil
}
func (s *resetFailureStore) Pending() (bool, error) { return s.pending, nil }
func (s *resetFailureStore) Complete() error        { s.pending = false; return nil }
func TestDataReset_PartialFailureBlocksStartsAndAllowsExplicitRetry(t *testing.T) {
	reset := &resetFailureStore{fail: true}
	m := newTestManager(Options{BusinessReset: reset})
	m.sysProxy = nil
	m.store.Store(state.Snapshot{Health: "ok", Core: state.CoreState{Status: "stopped"}})
	request := protocol.DataResetRequest{Digest: "fixture", Confirm: true, ConfirmAgain: true}
	if _, err := m.ResetBusinessData(t.Context(), Operation{ID: "partial"}, request); err == nil {
		t.Fatal("partial reset reported success")
	}
	if !m.dataResetPending.Load() || !m.mutationDegraded.Load() {
		t.Fatal("partial reset did not fence the runtime")
	}
	var api protocol.APIError
	if _, err := m.PrepareCoreStart(t.Context()); !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatal("partially reset core could start")
	}
	reset.fail = false
	result, err := m.ResetBusinessData(t.Context(), Operation{ID: "retry"}, request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SetupRequired || m.mutationDegraded.Load() {
		t.Fatal("explicit reset retry failed to converge")
	}
}

type resetCompletionFailure struct{ resetFailureStore }

func (s *resetCompletionFailure) Complete() error { return errors.New("fixture marker removal failed") }
func TestDataReset_SetupCommittedBeforeCleanupFailureReturnsWarning(t *testing.T) {
	onboard, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json"), InitialSetupRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	m := newTestManager(Options{BusinessReset: &resetCompletionFailure{}, ResetSetupRequired: true, Onboarding: onboard})
	ctx, outcome := diagnostics.WithResult(t.Context())
	complete := true
	result, err := m.UpdateOnboarding(ctx, Operation{ID: "finish"}, onboarding.Update{Complete: &complete})
	if err != nil || result.Status.Complete || len(outcome.Warnings().Warnings) == 0 {
		t.Fatalf("committed Setup cleanup failure: complete=%v warnings=%v err=%v", result.Status.Complete, outcome.Warnings(), err)
	}
}
func TestDataReset_CombinedEndpointAndCompletionRejectsBeforeSaving(t *testing.T) {
	onboard, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json"), InitialSetupRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	m := newTestManager(Options{BusinessReset: &resetFailureStore{}, ResetSetupRequired: true, Onboarding: onboard})
	old := m.settingsSnapshot().MixedAddr
	next := "127.0.0.1:19390"
	complete := true
	_, err = m.UpdateOnboarding(t.Context(), Operation{ID: "combined"}, onboarding.Update{MixedAddr: &next, Complete: &complete})
	if err == nil || m.settingsSnapshot().MixedAddr != old || onboard.State().Complete {
		t.Fatalf("rejected Setup completion changed state: %v", err)
	}
}

type refusingResetSupervisor struct{ CoreSupervisor }

func (s refusingResetSupervisor) Update(context.Context, func(*supervisor.UpdateSession) error) error {
	return errors.New("fixture supervisor refused maintenance")
}
func TestDataReset_RefusedMaintenanceRestoresOwnedProxy(t *testing.T) {
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: true, Server: sysproxy.NormalizeServer("127.0.0.1", 9190)}}
	m := newTestManager(Options{BusinessReset: &resetFailureStore{}, Supervisor: refusingResetSupervisor{}, SysProxy: backend})
	m.store.Store(state.Snapshot{Health: "ok", Core: state.CoreState{Status: "running", PID: 42}})
	_, err := m.ResetBusinessData(t.Context(), Operation{ID: "refused"}, protocol.DataResetRequest{Digest: "fixture", Confirm: true, ConfirmAgain: true})
	if err == nil || !backend.State.Enabled || m.store.Load().Core.PID != 42 {
		t.Fatalf("refused maintenance changed running service/proxy: enabled=%v err=%v", backend.State.Enabled, err)
	}
}
