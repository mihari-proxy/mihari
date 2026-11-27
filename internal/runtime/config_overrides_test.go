package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
	"go.yaml.in/yaml/v3"
)

func TestConfigOverrides_NormalManagerAdvertisesEditor(t *testing.T) {
	m := startupLoggingManager(t)
	if !slices.Contains(m.Capabilities(), protocol.CapabilityConfigOverrides) {
		t.Fatal("normal daemon cannot serve the configuration overrides editor")
	}
	m.recoveryOnly = true
	if slices.Contains(m.Capabilities(), protocol.CapabilityConfigOverrides) {
		t.Fatal("recovery-only daemon advertises mutating override editor")
	}
}

type overridesEditor interface {
	ConfigOverrides(context.Context) (protocol.ConfigOverridesStatus, error)
	UpdateConfigOverrides(context.Context, Operation, string, protocol.ConfigOverridesUpdateRequest) (protocol.ConfigOverridesStatus, error)
}

func TestConfigOverrides_SaveFailureRestoresReloadedRuntime(t *testing.T) {
	m, core := onlineEgressManager(t)
	api, ok := any(m).(overridesEditor)
	if !ok {
		t.Fatal("override editing API is missing")
	}
	previous, err := os.ReadFile(core.path)
	if err != nil {
		t.Fatal(err)
	}
	m.saveSettings = func(string, config.Settings) (config.CommitResult, error) {
		return config.CommitResult{}, errors.New("fixture save failed")
	}
	req := protocol.ConfigOverridesUpdateRequest{Fragments: []protocol.OverrideFragment{{ID: "office", Name: "Office", Enabled: true, YAML: "rules: ['MATCH,DIRECT']"}}}
	_, err = api.UpdateConfigOverrides(t.Context(), Operation{ID: "failure"}, "fragments", req)
	after, readErr := os.ReadFile(core.path)
	if err == nil || readErr != nil || core.reloads != 2 || !bytes.Equal(previous, after) {
		t.Fatalf("err=%v read=%v reloads=%d restored=%v", err, readErr, core.reloads, bytes.Equal(previous, after))
	}
	status, err := api.ConfigOverrides(t.Context())
	if err != nil || len(status.Config.Fragments) != 0 {
		t.Fatalf("published failed override: %+v %v", status, err)
	}
}

func TestConfigOverrides_ExistingAuthenticationReadsFragmentCredentials(t *testing.T) {
	m := startupLoggingManager(t)
	m.settings.SetOverrides(protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{
		{ID: "office", Name: "Office", Enabled: true, YAML: "authentication+: ['bob:from-fragment']"},
	}})
	status, err := m.ProxyAuth(t.Context(), true)
	if err != nil || len(status.Users) != 1 || status.Users[0].Password != "from-fragment" || status.Users[0].BaseSource != "Office" {
		t.Fatalf("fragment authentication missing: %+v %v", status, err)
	}
	preview, err := m.PreviewProxyAuth(t.Context(), protocol.ProxyAuthPreviewRequest{Config: protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range preview.Lines {
		if line.Target == "bob" && line.Source == "Office" && !strings.Contains(line.Text, "from-fragment") && strings.Contains(line.Text, "********") {
			return
		}
	}
	t.Fatal("authentication preview omitted masked fragment credential")
}

func TestConfigOverrides_ClearIncompatibleFragmentOnStoppedCore(t *testing.T) {
	m := startupLoggingManager(t)
	m.settingsPath = filepath.Join(t.TempDir(), "settings.yaml")
	m.binaryExists = func() bool { return false }
	m.store.Store(state.Snapshot{Core: state.CoreState{Status: "missing"}})
	m.settings.SetOverrides(protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "retired", Name: "Retired", Enabled: true, YAML: "secret: old"}}})
	got, err := m.UpdateConfigOverrides(t.Context(), Operation{ID: "recover"}, "fragments", protocol.ConfigOverridesUpdateRequest{})
	if err != nil || got.Revision == 0 || len(got.Config.Fragments) != 0 {
		t.Fatalf("failed to clear unusable base: %+v %v", got, err)
	}
	saved, err := config.Load(m.settingsPath)
	if err != nil || saved.Overrides != nil {
		t.Fatalf("persisted stale block: %v %v", saved.Overrides, err)
	}
}

func TestConfigOverrides_CategoryIsolationAndRevision(t *testing.T) {
	m, core := onlineEgressManager(t)
	f := protocol.OverrideFragment{ID: "office", Name: "Office", Enabled: true, YAML: "dns: {nameserver: [1.1.1.1]}"}
	req := protocol.ConfigOverridesUpdateRequest{Fragments: []protocol.OverrideFragment{f}}
	initial, err := m.ConfigOverrides(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := m.UpdateConfigOverrides(t.Context(), Operation{ID: "fragment", IfRevision: &initial.Revision}, "fragments", req)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	dns := protocol.ConfigOverridesUpdateRequest{DNS: map[string]protocol.DNSFieldOverride{"enable": {Operation: "set", Bool: &enabled}}}
	_, err = m.UpdateConfigOverrides(t.Context(), Operation{ID: "stale", IfRevision: &initial.Revision}, "dns", dns)
	var conflict protocol.APIError
	if !errors.As(err, &conflict) || conflict.Code != protocol.CodeRevisionConflict {
		t.Fatalf("lost revision conflict: %v", err)
	}
	applied, err := m.UpdateConfigOverrides(t.Context(), Operation{ID: "dns", IfRevision: &saved.Revision}, "dns", dns)
	if err != nil || core.reloads != 2 || !slices.Equal(applied.Config.Fragments, req.Fragments) {
		t.Fatalf("category commit: %+v reloads=%d err=%v", applied, core.reloads, err)
	}
	raw, err := os.ReadFile(core.path)
	if err != nil {
		t.Fatal(err)
	}
	var effective struct {
		DNS struct {
			Enable     bool
			Nameserver []string
		}
	}
	if err := yaml.Unmarshal(raw, &effective); err != nil || !effective.DNS.Enable || !slices.Equal(effective.DNS.Nameserver, []string{"1.1.1.1"}) {
		t.Fatalf("effective DNS=%+v err=%v", effective, err)
	}
}

func TestConfigOverrides_ReplayedEditOwnsItsDraft(t *testing.T) {
	m := startupLoggingManager(t)
	m.store.Store(state.Snapshot{Core: state.CoreState{Status: "stopped"}})
	m.binaryExists = func() bool { return false }
	req := protocol.ConfigOverridesUpdateRequest{DNS: map[string]protocol.DNSFieldOverride{"nameserver": {Operation: "set", Servers: []string{"1.1.1.1"}}}}
	first, err := m.UpdateConfigOverrides(t.Context(), Operation{ID: "retry"}, "dns", req)
	if err != nil {
		t.Fatal(err)
	}
	first.Config.DNS["nameserver"].Servers[0] = "mutated.local"
	repeated, err := m.UpdateConfigOverrides(t.Context(), Operation{ID: "retry"}, "dns", req)
	if err != nil || !slices.Equal(repeated.Config.DNS["nameserver"].Servers, []string{"1.1.1.1"}) {
		t.Fatalf("cached operation data changed: %+v %v", repeated, err)
	}
}

func TestConfigOverrides_RecoveryModesRejectMutations(t *testing.T) {
	for _, mode := range []string{"settings recovery", "core recovery"} {
		t.Run(mode, func(t *testing.T) {
			m, core := onlineEgressManager(t)
			if mode == "settings recovery" {
				m.recoveryOnly = true
			} else {
				m.coreRecovery.Store(true)
			}
			req := protocol.ConfigOverridesUpdateRequest{Fragments: []protocol.OverrideFragment{{ID: "office", Name: "Office", YAML: "rules: []"}}}
			_, err := m.UpdateConfigOverrides(t.Context(), Operation{ID: "recovery-edit"}, "fragments", req)
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState || core.reloads != 0 {
				t.Fatalf("recovery write: err=%v reloads=%d", err, core.reloads)
			}
			if slices.Contains(m.Capabilities(), protocol.CapabilityConfigOverrides) {
				t.Fatal("recovery advertises editing")
			}
		})
	}
}
