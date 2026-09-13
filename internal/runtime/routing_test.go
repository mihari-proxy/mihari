package runtime

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/state"
)

type routingController struct {
	fakeController
	routingMu          sync.Mutex
	mode               string
	selected           string
	candidates         []string
	patchError         error
	patchReplyError    error
	configsError       error
	failReadAfterPatch bool
	patches            int
	selections         int
}

func TestRouting_RemoveInactiveSubscriptionPrunesSavedExit(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "save failure"}[failSave], func(t *testing.T) {
			m, service, _, url := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("proxies: [{name: A, type: direct}]\n"))
			}))
			ctx := context.Background()
			if _, err := m.AddSubscription(ctx, Operation{ID: "a"}, AddSubscriptionInput{Name: "A", URL: url + "?a"}); err != nil {
				t.Fatal(err)
			}
			b, err := m.AddSubscription(ctx, Operation{ID: "b"}, AddSubscriptionInput{Name: "B", URL: url + "?b"})
			if err != nil {
				t.Fatal(err)
			}
			m.settings.SetGlobalSelection(b.ID, "DIRECT")
			m.settingsPath = filepath.Join(t.TempDir(), "settings.yaml")
			if failSave {
				m.saveSettings = func(string, config.Settings) (config.CommitResult, error) {
					return config.CommitResult{}, errors.New("save failed")
				}
			}
			err = m.RemoveSubscription(ctx, Operation{ID: "remove"}, b.ID)
			if failSave {
				if err == nil || service.Snapshot().Index(b.ID) < 0 || m.settingsSnapshot().GlobalSelection(b.ID) != "DIRECT" {
					t.Fatal("catalog/settings rollback was lost")
				}
			} else if err != nil || m.settingsSnapshot().GlobalSelection(b.ID) != "" {
				t.Fatalf("saved exit was not pruned: %v", err)
			}
		})
	}
}

func (c *routingController) Configs(context.Context) (map[string]any, error) {
	c.routingMu.Lock()
	defer c.routingMu.Unlock()
	return map[string]any{"mode": c.mode}, c.configsError
}
func (c *routingController) PatchConfigs(_ context.Context, p map[string]any) error {
	c.routingMu.Lock()
	defer c.routingMu.Unlock()
	c.patches++
	if c.patchError != nil {
		return c.patchError
	}
	if len(p) != 1 {
		return errors.New("unexpected fields in mode patch")
	}
	c.mode, _ = p["mode"].(string)
	if c.failReadAfterPatch {
		c.configsError = errors.New("private controller failure")
	}
	return c.patchReplyError
}

func TestRouting_UnconfirmedRecoveryDegradesAndBlocksMutations(t *testing.T) {
	m, c := routingFixture(t)
	c.failReadAfterPatch = true
	_, err := m.UpdateRouting(context.Background(), Operation{ID: "uncertain"}, "direct")
	if err == nil || m.Snapshot().Health != "degraded" || m.settingsSnapshot().RoutingMode() != "rule" {
		t.Fatalf("uncertain recovery was not degraded: %+v %v", m.Snapshot(), err)
	}
	status, err := m.RoutingStatus(context.Background())
	if err != nil || status.State != "unknown" || status.LiveMode != "" {
		t.Fatalf("invented live state: %+v %v", status, err)
	}
	c.configsError = nil
	_, err = m.UpdateRouting(context.Background(), Operation{ID: "blocked"}, "rule")
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("degraded mutation accepted: %v", err)
	}
}

func TestRouting_CommittedSettingsWarningKeepsSuccess(t *testing.T) {
	m, c := routingFixture(t)
	m.saveSettings = func(string, config.Settings) (config.CommitResult, error) {
		return config.CommitResult{Committed: true, Warning: errors.New("sync warning")}, nil
	}
	status, err := m.UpdateRouting(context.Background(), Operation{ID: "warning"}, "direct")
	if err != nil || status.State != "applied" || status.Revision == 0 || c.mode != "direct" || m.settingsSnapshot().RoutingMode() != "direct" {
		t.Fatalf("committed warning rolled back: %+v %v", status, err)
	}
}

func TestRouting_FailedWriteRetainsOldIntent(t *testing.T) {
	m, c := routingFixture(t)
	c.patchError = errors.New("rejected")
	_, err := m.UpdateRouting(context.Background(), Operation{ID: "rejected"}, "global")
	if err == nil || c.mode != "rule" || c.selected != "Node A" || m.settingsSnapshot().RoutingMode() != "rule" || m.Snapshot().Revision != 0 {
		t.Fatalf("failed change was published: %v", err)
	}
}

func TestRouting_TimedOutWriteConfirmedByReadback(t *testing.T) {
	m, c := routingFixture(t)
	c.patchReplyError = context.DeadlineExceeded
	status, err := m.UpdateRouting(context.Background(), Operation{ID: "reply-lost"}, "direct")
	if err != nil || status.State != "applied" || m.settingsSnapshot().RoutingMode() != "direct" {
		t.Fatalf("confirmed write was discarded: %+v %v", status, err)
	}
}

func TestRouting_RevisionConflictHasNoSideEffects(t *testing.T) {
	m, c := routingFixture(t)
	revision := uint64(999)
	_, err := m.UpdateRouting(context.Background(), Operation{ID: "stale", IfRevision: &revision}, "global")
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("expected conflict: %v", err)
	}
	err = m.SelectProxy(context.Background(), Operation{ID: "stale-exit", IfRevision: &revision}, "GLOBAL", "Node B")
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict || c.patches != 0 || c.selections != 0 {
		t.Fatalf("stale selection changed the core: %v", err)
	}
}

func TestRouting_DuplicateOperationDoesNotApplyTwice(t *testing.T) {
	m, c := routingFixture(t)
	op := Operation{ID: "duplicate"}
	first, err := m.UpdateRouting(context.Background(), op, "global")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.UpdateRouting(context.Background(), op, "global")
	if err != nil || first.Revision != second.Revision || c.patches != 1 || c.selections != 1 {
		t.Fatalf("duplicate applied twice: %+v %v", second, err)
	}
}

func TestRouting_InvalidCandidateHasNoSideEffects(t *testing.T) {
	m, c := routingFixture(t)
	err := m.SelectProxy(context.Background(), Operation{ID: "unknown-exit"}, "GLOBAL", "unknown")
	if err == nil || c.selections != 0 || c.patches != 0 || m.settingsSnapshot().GlobalSelection("a") != "" {
		t.Fatal("invalid selection was applied")
	}
}
func (c *routingController) Proxies(context.Context) (mihomo.Proxies, error) {
	c.routingMu.Lock()
	defer c.routingMu.Unlock()
	return mihomo.Proxies{Proxies: map[string]mihomo.Proxy{"GLOBAL": {Name: "GLOBAL", Type: "Selector", Now: c.selected, All: append([]string(nil), c.candidates...)}}}, nil
}
func (c *routingController) SelectProxy(_ context.Context, group, name string) error {
	c.routingMu.Lock()
	defer c.routingMu.Unlock()
	if group != "GLOBAL" {
		return errors.New("unexpected group")
	}
	for _, candidate := range c.candidates {
		if candidate == name {
			c.selected = name
			c.selections++
			return nil
		}
	}
	return errors.New("unknown candidate")
}

func routingFixture(t *testing.T) (*Manager, *routingController) {
	t.Helper()
	c := &routingController{mode: "rule", selected: "Node A", candidates: []string{"DIRECT", "Node A", "Node B"}}
	s := defaultTunSettings(nil)
	m := newTestManager(Options{Settings: s, SettingsPath: filepath.Join(t.TempDir(), "settings.yaml"), Controller: c})
	m.store.Store(state.Snapshot{Core: state.CoreState{Status: "running", PID: 42}, ActiveSubscription: "a"})
	return m, c
}

func TestRouting_ModeIsAppliedAndPersisted(t *testing.T) {
	m, c := routingFixture(t)
	status, err := m.UpdateRouting(context.Background(), Operation{ID: "mode-global", Source: "test"}, "global")
	if err != nil {
		t.Fatal(err)
	}
	if status.DesiredMode != "global" || status.LiveMode != "global" || status.State != "applied" || c.selected != "DIRECT" {
		t.Fatalf("status=%+v selected=%q", status, c.selected)
	}
	s, err := config.Load(m.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if s.RoutingMode() != "global" || s.GlobalSelection("a") != "DIRECT" {
		t.Fatal("mode or exit was not persisted")
	}
	if len(c.callsFor("close")) != 0 || len(c.callsFor("close-all")) != 0 {
		t.Fatal("mode change closed connections")
	}
}

func TestRouting_PersistFailureRestoresLiveState(t *testing.T) {
	m, c := routingFixture(t)
	m.saveSettings = func(string, config.Settings) (config.CommitResult, error) {
		return config.CommitResult{}, errors.New("injected persistence failure")
	}
	_, err := m.UpdateRouting(context.Background(), Operation{ID: "save-fails", Source: "test"}, "global")
	if err == nil {
		t.Fatal("persistence failure was ignored")
	}
	if c.mode != "rule" || c.selected != "Node A" || m.settingsSnapshot().RoutingMode() != "rule" || m.Snapshot().Revision != 0 {
		t.Fatal("old state was not restored")
	}
}

func TestRouting_StoppedCoreSavesPending(t *testing.T) {
	m, c := routingFixture(t)
	m.store.Store(state.Snapshot{Core: state.CoreState{Status: "stopped"}})
	status, err := m.UpdateRouting(context.Background(), Operation{ID: "offline", Source: "test"}, "direct")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "pending" || status.LiveMode != "" || c.patches != 0 || m.settingsSnapshot().RoutingMode() != "direct" {
		t.Fatalf("status=%+v patches=%d", status, c.patches)
	}
}

func TestRouting_GLOBALSelectionsAreSubscriptionScoped(t *testing.T) {
	m, c := routingFixture(t)
	ctx := context.Background()
	if err := m.SelectProxy(ctx, Operation{ID: "a-select", Source: "test"}, "GLOBAL", "Node A"); err != nil {
		t.Fatal(err)
	}
	snapshot := m.store.Load()
	snapshot.ActiveSubscription = "b"
	m.store.Store(snapshot)
	if err := m.SelectProxy(ctx, Operation{ID: "b-select", Source: "test"}, "GLOBAL", "Node B"); err != nil {
		t.Fatal(err)
	}
	snapshot = m.store.Load()
	snapshot.ActiveSubscription = "a"
	m.store.Store(snapshot)
	if err := m.RestoreRouting(ctx); err != nil {
		t.Fatal(err)
	}
	if c.selected != "Node A" || c.mode != "rule" || m.settingsSnapshot().GlobalSelection("b") != "Node B" {
		t.Fatal("GLOBAL selection did not restore independently of mode and subscription")
	}
}

func TestRouting_FallbackPersistsAndDoesNotAutomaticallyReturn(t *testing.T) {
	for _, direct := range []bool{true, false} {
		m, c := routingFixture(t)
		m.settings.SetRoutingMode("global")
		m.settings.SetGlobalSelection("a", "Missing")
		if !direct {
			c.candidates = []string{"Node A"}
		}
		if err := m.RestoreRouting(context.Background()); err != nil {
			t.Fatal(err)
		}
		wantMode, wantExit := "global", "DIRECT"
		if !direct {
			wantMode, wantExit = "rule", ""
		}
		if c.mode != wantMode || m.settingsSnapshot().GlobalSelection("a") != wantExit {
			t.Fatal("incorrect persisted fallback")
		}
		c.candidates = append(c.candidates, "Missing")
		if err := m.RestoreRouting(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c.mode != wantMode || (direct && c.selected != "DIRECT") {
			t.Fatal("returned to a previously lost exit")
		}
	}
}
