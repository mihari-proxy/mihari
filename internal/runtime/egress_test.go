package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/state"
	"go.yaml.in/yaml/v3"
)

type egressRuntimeAPI interface {
	EgressStatus(context.Context) (protocol.EgressStatus, error)
	UpdateEgress(context.Context, Operation, protocol.EgressSelection) (protocol.EgressStatus, error)
}

type egressController struct {
	failReadback bool
	fakeController
	path, name                          string
	reloads, closed                     int
	failReload, ignoreReload, failClose bool
	ignoreRecovery                      bool
}

func (c *egressController) Configs(context.Context) (map[string]any, error) {
	if c.failReadback && c.reloads == 1 {
		return nil, errors.New("controller read failed")
	}
	return map[string]any{"interface-name": c.name, "mode": "rule"}, nil
}
func (c *egressController) Reload(context.Context, string, bool) error {
	c.reloads++
	if c.failReload && c.reloads == 1 {
		return errors.New("reload refused")
	}
	if (c.ignoreReload && c.reloads == 1) || (c.ignoreRecovery && c.reloads > 1) {
		return nil
	}
	content, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := yaml.Unmarshal(content, &document); err != nil {
		return err
	}
	c.name, _ = document["interface-name"].(string)
	return nil
}
func (c *egressController) CloseAllConnections(context.Context) error {
	c.closed++
	if c.failClose {
		return errors.New("close failed")
	}
	return nil
}

func onlineEgressManager(t *testing.T) (*Manager, *egressController) {
	t.Helper()
	m := startupLoggingManager(t)
	m.listInterfaces = func(context.Context) ([]platform.NetworkInterface, error) {
		return []platform.NetworkInterface{{Name: "Ethernet", Kind: "physical", Availability: "available"}}, nil
	}
	c := &egressController{path: m.runtimeConfig, name: "previous"}
	m.controller = c
	m.settingsPath = filepath.Join(t.TempDir(), "settings.yaml")
	m.store.Store(state.Snapshot{Core: state.CoreState{Status: "running", PID: 42}})
	if err := os.WriteFile(c.path, []byte("interface-name: previous\nproxies: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return m, c
}

func TestEgress_OnlineAppliesBeforeSavingAndIsIdempotent(t *testing.T) {
	m, c := onlineEgressManager(t)
	m.saveSettings = func(_ string, s config.Settings) (config.CommitResult, error) {
		if c.name != "Ethernet" || c.closed != 1 || m.settingsSnapshot().EgressInterface != "" {
			t.Fatal("incorrect commit ordering")
		}
		return config.CommitResult{Committed: true}, nil
	}
	for range 2 {
		got, err := m.UpdateEgress(t.Context(), Operation{ID: "same"}, protocol.EgressSelection{Mode: "manual", InterfaceName: "Ethernet"})
		if err != nil {
			t.Fatal(err)
		}
		if got.State != "applied" || got.Selection.InterfaceName != "Ethernet" {
			t.Fatalf("got=%+v", got)
		}
	}
	if c.closed != 1 || c.reloads != 1 {
		t.Fatalf("closed=%d reloads=%d", c.closed, c.reloads)
	}
}

func TestEgress_UnsavedLoggingRejectsWithoutReload(t *testing.T) {
	m, c := onlineEgressManager(t)
	m.loggingUnsaved = true
	_, err := m.UpdateEgress(t.Context(), Operation{ID: "unsaved"}, protocol.EgressSelection{Mode: "manual", InterfaceName: "Ethernet"})
	if err == nil || c.reloads != 0 || c.closed != 0 {
		t.Fatalf("err=%v reloads=%d closed=%d", err, c.reloads, c.closed)
	}
}

func TestEgress_AutomaticWithoutRuntimeIsUnknown(t *testing.T) {
	m, c := onlineEgressManager(t)
	c.name = ""
	if err := os.Remove(c.path); err != nil {
		t.Fatal(err)
	}
	got, err := m.EgressStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "unknown" {
		t.Fatalf("unconfirmed config reported %s", got.State)
	}
}

func TestEgress_OnlineFailuresRestorePrevious(t *testing.T) {
	for _, failure := range []string{"reload", "readback", "read error", "close", "save", "validation"} {
		t.Run(failure, func(t *testing.T) {
			m, c := onlineEgressManager(t)
			before, err := os.ReadFile(c.path)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "reload":
				c.failReload = true
			case "readback":
				c.ignoreReload = true
			case "read error":
				c.failReadback = true
			case "close":
				c.failClose = true
			case "save":
				m.saveSettings = func(string, config.Settings) (config.CommitResult, error) {
					return config.CommitResult{}, errors.New("disk full")
				}
			case "validation":
				m.validateConfig = func(context.Context, string) error { return errors.New("invalid candidate") }
			}
			_, err = m.UpdateEgress(t.Context(), Operation{ID: failure}, protocol.EgressSelection{Mode: "manual", InterfaceName: "Ethernet"})
			if err == nil {
				t.Fatal("expected apply failure")
			}
			after, err := os.ReadFile(c.path)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) || c.name != "previous" || m.settingsSnapshot().EgressInterface != "" {
				t.Fatalf("rollback failed: file=%s live=%s", after, c.name)
			}
		})
	}
}

func TestEgress_RejectsUnknownAndOwnTunKeepsMissingSaved(t *testing.T) {
	m, c := onlineEgressManager(t)
	if err := os.WriteFile(c.path, []byte("proxies: []\ntun: {device: Ethernet}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unknown", "Ethernet"} {
		_, err := m.UpdateEgress(t.Context(), Operation{ID: name}, protocol.EgressSelection{Mode: "manual", InterfaceName: name})
		if err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	m.settings.EgressInterface = "missing VPN"
	status, err := m.EgressStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range status.Interfaces {
		if item.Name == "missing VPN" {
			found = item.Selectable && item.Availability == "not_found"
		}
	}
	if !found {
		t.Fatal("missing saved adapter lost")
	}
	if c.reloads != 0 || c.closed != 0 {
		t.Fatal("rejected selection mutated core")
	}
}

func TestEgress_StoppedSavesWithoutStartingCore(t *testing.T) {
	m := startupLoggingManager(t)
	m.store.Store(state.Snapshot{Core: state.CoreState{Status: "stopped"}})
	m.listInterfaces = func(context.Context) ([]platform.NetworkInterface, error) {
		return []platform.NetworkInterface{{Name: "Ethernet", Availability: "disconnected"}}, nil
	}
	api, ok := any(m).(egressRuntimeAPI)
	if !ok {
		t.Fatal("egress capability is missing")
	}
	got, err := api.UpdateEgress(t.Context(), Operation{ID: "save"}, protocol.EgressSelection{Mode: "manual", InterfaceName: "Ethernet"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Selection.InterfaceName != "Ethernet" || got.State != "saved" || m.settingsSnapshot().EgressInterface != "Ethernet" {
		t.Fatalf("status=%+v", got)
	}
	if m.store.Load().Core.Status != "stopped" {
		t.Fatal("started core")
	}
}

func TestEgress_ValidationRechecksGenerationAndInterface(t *testing.T) {
	for _, change := range []string{"generation", "disappeared", "epoch"} {
		t.Run(change, func(t *testing.T) {
			m, c := onlineEgressManager(t)
			m.validateConfig = func(context.Context, string) error {
				switch change {
				case "generation":
					m.settingsMu.Lock()
					m.configGeneration++
					m.settingsMu.Unlock()
				case "disappeared":
					m.listInterfaces = func(context.Context) ([]platform.NetworkInterface, error) { return nil, nil }
				case "epoch":
					m.coreEpoch++
				}
				return nil
			}
			_, err := m.UpdateEgress(t.Context(), Operation{ID: change}, protocol.EgressSelection{Mode: "manual", InterfaceName: "Ethernet"})
			if err == nil || c.reloads != 0 || m.settingsSnapshot().EgressInterface != "" {
				t.Fatalf("stale candidate committed: %v", err)
			}
		})
	}
}
func TestEgress_RevisionConflictHasNoSideEffects(t *testing.T) {
	m, c := onlineEgressManager(t)
	revision := uint64(99)
	_, err := m.UpdateEgress(t.Context(), Operation{ID: "stale", IfRevision: &revision}, protocol.EgressSelection{Mode: "manual", InterfaceName: "Ethernet"})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict || c.reloads != 0 {
		t.Fatalf("err=%v reloads=%d", err, c.reloads)
	}
}
func TestEgress_StoppedSaveIsUsedOnNextStartAndAutoRemovesOverride(t *testing.T) {
	m, _ := onlineEgressManager(t)
	m.store.Store(state.Snapshot{Core: state.CoreState{Status: "stopped"}})
	for i, name := range []string{"Ethernet", ""} {
		m.store.Store(state.Snapshot{Core: state.CoreState{Status: "stopped"}})
		selection := egressSelection(name)
		if _, err := m.UpdateEgress(t.Context(), Operation{ID: string(rune('a' + i))}, selection); err != nil {
			t.Fatal(err)
		}
		saved, err := config.Load(m.settingsPath)
		if err != nil {
			t.Fatal(err)
		}
		if saved.EgressInterface != name {
			t.Fatalf("saved=%+v", saved)
		}
		release, err := m.PrepareCoreStart(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		release()
		raw, err := os.ReadFile(m.runtimeConfig)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "interface-name: Ethernet") != (name != "") {
			t.Fatalf("wrong startup config: %s", raw)
		}
	}
}

func TestEgress_UnconfirmedRecoveryFencesFurtherMutations(t *testing.T) {
	m, c := onlineEgressManager(t)
	c.ignoreRecovery = true
	c.failClose = true
	_, err := m.UpdateEgress(t.Context(), Operation{ID: "first"}, protocol.EgressSelection{Mode: "manual", InterfaceName: "Ethernet"})
	if err == nil || !m.mutationDegraded.Load() {
		t.Fatalf("unconfirmed recovery was accepted: %v", err)
	}
	_, err = m.UpdateEgress(t.Context(), Operation{ID: "second"}, protocol.EgressSelection{Mode: "automatic"})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("degraded mutation allowed: %v", err)
	}
}

func TestEgress_SameSelectionWithNewOperationDoesNotReload(t *testing.T) {
	m, c := onlineEgressManager(t)
	selection := protocol.EgressSelection{Mode: "manual", InterfaceName: "Ethernet"}
	first, err := m.UpdateEgress(t.Context(), Operation{ID: "first"}, selection)
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.UpdateEgress(t.Context(), Operation{ID: "different-operation"}, selection)
	if err != nil {
		t.Fatal(err)
	}
	if c.reloads != 1 || c.closed != 1 || again.Revision != first.Revision {
		t.Fatalf("unchanged selection mutated: reloads=%d closed=%d revision=%d", c.reloads, c.closed, again.Revision)
	}
}

func TestEgress_MalformedConfirmationPreservesCauseAndClassification(t *testing.T) {
	m, c := onlineEgressManager(t)
	err := m.confirmEgressConfig(t.Context(), []byte("[]"))
	var api protocol.APIError
	var cause *yaml.TypeError
	if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || !errors.As(err, &cause) {
		t.Fatalf("parse error lost context or cause: %v", err)
	}
	if c.reloads != 0 || c.closed != 0 {
		t.Fatal("malformed confirmation mutated the core")
	}
}
