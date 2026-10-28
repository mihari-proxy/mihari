package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
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
func (s *resetFailureStore) Reset(context.Context, string) (config.Settings, string, error) {
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
	if _, err := m.PrepareCoreStart(t.Context()); err == nil {
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
