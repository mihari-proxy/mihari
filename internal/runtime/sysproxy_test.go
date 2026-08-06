package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

func TestSystemProxyStatusClassifiesOwnedAndForeign(t *testing.T) {
	const mixed = "127.0.0.1:9190"
	target := sysproxy.NormalizeServer("127.0.0.1", 9190)

	tests := []struct {
		name     string
		desired  bool
		observed sysproxy.State
		want     protocol.SystemProxyObserved
	}{
		{
			name:     "owned",
			desired:  true,
			observed: sysproxy.State{Enabled: true, Server: target},
			want:     protocol.SystemProxyObserved{Enabled: true, Server: target, Owned: true, Foreign: false},
		},
		{
			name:     "foreign",
			desired:  false,
			observed: sysproxy.State{Enabled: true, Server: "127.0.0.1:7890"},
			want:     protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:7890", Owned: false, Foreign: true},
		},
		{
			name:     "disabled",
			desired:  false,
			observed: sysproxy.State{Enabled: false, Server: target},
			want:     protocol.SystemProxyObserved{Enabled: false, Server: target, Owned: false, Foreign: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &sysproxy.FakeBackend{State: tt.observed}
			manager := newSysProxyManager(t, backend, config.Settings{
				Schema: "mihari.settings/v1", MixedAddr: mixed,
				ControllerAddr: "127.0.0.1:9090", WebAddr: "127.0.0.1:9191",
				ControllerSecret:   strings.Repeat("a", 64),
				SystemProxyDesired: tt.desired,
			})
			status, err := manager.SystemProxyStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status.Schema != "mihari/v1" || status.Target != target || status.Desired != tt.desired {
				t.Fatalf("status=%#v", status)
			}
			if status.Observed != tt.want {
				t.Fatalf("observed=%#v want=%#v", status.Observed, tt.want)
			}
			if backend.GetCalls != 1 {
				t.Fatalf("GetCalls=%d", backend.GetCalls)
			}
		})
	}
}

func TestEnableSystemProxyWithoutForceOnForeignConflicts(t *testing.T) {
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: true, Server: "127.0.0.1:7890"}}
	manager := newSysProxyManager(t, backend, defaultSysProxySettings(false))

	_, err := manager.EnableSystemProxy(context.Background(), Operation{ID: "en-foreign", Source: "test"}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeSystemProxyConflict {
		t.Fatalf("err=%v", err)
	}
	if backend.EnableCalls != 0 {
		t.Fatalf("EnableCalls=%d, want 0", backend.EnableCalls)
	}
	if manager.settings.SystemProxyDesired {
		t.Fatal("desired flipped on conflict")
	}
	if apiError.Details["current_server"] != "127.0.0.1:7890" {
		t.Fatalf("details=%v", apiError.Details)
	}
}

func TestEnableSystemProxyWithForceOnForeignOverwrites(t *testing.T) {
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: true, Server: "127.0.0.1:7890"}}
	manager := newSysProxyManager(t, backend, defaultSysProxySettings(false))

	status, err := manager.EnableSystemProxy(context.Background(), Operation{ID: "en-force", Source: "test"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if backend.EnableCalls != 1 || backend.LastEnableHost != "127.0.0.1" || backend.LastEnablePort != 9190 {
		t.Fatalf("enable host=%s port=%d calls=%d", backend.LastEnableHost, backend.LastEnablePort, backend.EnableCalls)
	}
	if !status.Desired || !status.Observed.Owned || !manager.settings.SystemProxyDesired {
		t.Fatalf("status=%#v desired=%v", status, manager.settings.SystemProxyDesired)
	}
	assertSettingsDesired(t, manager.settingsPath, true)
}

func TestEnableSystemProxyWhenFree(t *testing.T) {
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: false}}
	manager := newSysProxyManager(t, backend, defaultSysProxySettings(false))

	status, err := manager.EnableSystemProxy(context.Background(), Operation{ID: "en-free", Source: "test"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if backend.EnableCalls != 1 {
		t.Fatalf("EnableCalls=%d", backend.EnableCalls)
	}
	if !status.Desired || !status.Observed.Owned || status.Revision != 1 {
		t.Fatalf("status=%#v", status)
	}
	assertSettingsDesired(t, manager.settingsPath, true)
}

func TestDisableSystemProxyWhenOwned(t *testing.T) {
	target := sysproxy.NormalizeServer("127.0.0.1", 9190)
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: true, Server: target}}
	manager := newSysProxyManager(t, backend, defaultSysProxySettings(true))

	status, err := manager.DisableSystemProxy(context.Background(), Operation{ID: "dis-owned", Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if backend.DisableCalls != 1 {
		t.Fatalf("DisableCalls=%d", backend.DisableCalls)
	}
	if status.Desired || status.Observed.Enabled || manager.settings.SystemProxyDesired {
		t.Fatalf("status=%#v desired=%v", status, manager.settings.SystemProxyDesired)
	}
	assertSettingsDesired(t, manager.settingsPath, false)
}

func TestDisableSystemProxyWhenForeignRefuses(t *testing.T) {
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: true, Server: "10.0.0.1:8080"}}
	manager := newSysProxyManager(t, backend, defaultSysProxySettings(true))

	_, err := manager.DisableSystemProxy(context.Background(), Operation{ID: "dis-foreign", Source: "test"})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeSystemProxyNotOwned {
		t.Fatalf("err=%v", err)
	}
	if backend.DisableCalls != 0 {
		t.Fatalf("DisableCalls=%d, want 0", backend.DisableCalls)
	}
	if !manager.settings.SystemProxyDesired {
		t.Fatal("desired cleared on foreign refuse")
	}
}

func TestDisableSystemProxyWhenAlreadyOff(t *testing.T) {
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: false}}
	manager := newSysProxyManager(t, backend, defaultSysProxySettings(true))

	status, err := manager.DisableSystemProxy(context.Background(), Operation{ID: "dis-off", Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if backend.DisableCalls != 0 {
		t.Fatalf("DisableCalls=%d, want 0 when already off", backend.DisableCalls)
	}
	if status.Desired || manager.settings.SystemProxyDesired {
		t.Fatalf("status=%#v desired=%v", status, manager.settings.SystemProxyDesired)
	}
}

func TestEnableSystemProxyRejectsStaleRevision(t *testing.T) {
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: false}}
	manager := newSysProxyManager(t, backend, defaultSysProxySettings(false))
	manager.store.Store(state.Snapshot{Revision: 5, Health: "ok"})

	stale := uint64(4)
	_, err := manager.EnableSystemProxy(context.Background(), Operation{
		ID: "en-stale", Source: "test", IfRevision: &stale,
	}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v", err)
	}
	if backend.EnableCalls != 0 {
		t.Fatalf("EnableCalls=%d on revision conflict, want 0", backend.EnableCalls)
	}
	if manager.settings.SystemProxyDesired {
		t.Fatal("desired set after stale revision")
	}
}

func TestCapabilitiesIncludeSystemProxyWhenBackendPresent(t *testing.T) {
	backend := &sysproxy.FakeBackend{}
	manager := newSysProxyManager(t, backend, defaultSysProxySettings(false))
	found := false
	for _, cap := range manager.Capabilities() {
		if cap == protocol.CapabilitySystemProxy {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("capabilities=%v missing system-proxy", manager.Capabilities())
	}
}

func TestNewInstallsDefaultSysProxyBackend(t *testing.T) {
	manager := New(Options{})
	if manager.sysProxy == nil {
		t.Fatal("expected default platform sysproxy backend")
	}
	found := false
	for _, cap := range manager.Capabilities() {
		if cap == protocol.CapabilitySystemProxy {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("capabilities=%v", manager.Capabilities())
	}
}

func TestApplyDesiredAndClearOwnedSystemProxy(t *testing.T) {
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: false}}
	manager := newSysProxyManager(t, backend, defaultSysProxySettings(true))

	if err := manager.ApplyDesiredSystemProxy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if backend.EnableCalls != 1 {
		t.Fatalf("EnableCalls=%d after apply desired", backend.EnableCalls)
	}
	if !backend.State.Enabled {
		t.Fatal("proxy not enabled after apply")
	}

	if err := manager.ClearOwnedSystemProxy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if backend.DisableCalls != 1 {
		t.Fatalf("DisableCalls=%d after clear owned", backend.DisableCalls)
	}
}

func defaultSysProxySettings(desired bool) config.Settings {
	return config.Settings{
		Schema:             "mihari.settings/v1",
		MixedAddr:          "127.0.0.1:9190",
		ControllerAddr:     "127.0.0.1:9090",
		WebAddr:            "127.0.0.1:9191",
		ControllerSecret:   strings.Repeat("b", 64),
		SystemProxyDesired: desired,
	}
}

func newSysProxyManager(t *testing.T, backend *sysproxy.FakeBackend, settings config.Settings) *Manager {
	t.Helper()
	settingsPath := filepath.Join(t.TempDir(), "settings.yaml")
	if err := config.Save(settingsPath, settings); err != nil {
		t.Fatal(err)
	}
	return newTestManager(Options{
		SysProxy:     backend,
		SettingsPath: settingsPath,
		Settings:     settings,
	})
}

func assertSettingsDesired(t *testing.T, path string, want bool) {
	t.Helper()
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SystemProxyDesired != want {
		t.Fatalf("persisted desired=%v want=%v", loaded.SystemProxyDesired, want)
	}
}
