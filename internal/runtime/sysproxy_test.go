package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	if backend.GetCalls != 0 {
		t.Fatalf("GetCalls=%d on revision conflict, want 0 before OS observation", backend.GetCalls)
	}
	if manager.settings.SystemProxyDesired {
		t.Fatal("desired set after stale revision")
	}
}

func TestSystemProxyPreCommitFailureSkipsOSWriteSettings(t *testing.T) {
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: false}}
	settings := defaultSysProxySettings(false)
	saves := 0
	manager := newTestManager(Options{
		SysProxy: backend, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves++
			return config.CommitResult{}, errors.New("replace failed")
		},
	})

	_, err := manager.EnableSystemProxy(context.Background(), Operation{ID: "settings-save-fail", Source: "test"}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
		t.Fatalf("err=%v want data failure", err)
	}
	if saves != 1 || backend.EnableCalls != 0 || backend.DisableCalls != 0 {
		t.Fatalf("saves=%d enable=%d disable=%d want 1/0/0", saves, backend.EnableCalls, backend.DisableCalls)
	}
	if manager.settingsSnapshot().SystemProxyDesired {
		t.Fatal("pre-commit failure changed desired state")
	}
	if revision := manager.Snapshot().Revision; revision != 0 {
		t.Fatalf("revision=%d want=0", revision)
	}
}

func TestSystemProxyConflictGatesBeforeSaveSettings(t *testing.T) {
	tests := []struct {
		name     string
		settings config.Settings
		state    sysproxy.State
		invoke   func(*Manager) error
		wantCode protocol.ErrorCode
	}{
		{
			name:     "enable foreign",
			settings: defaultSysProxySettings(false),
			state:    sysproxy.State{Enabled: true, Server: "10.0.0.1:8080"},
			invoke: func(manager *Manager) error {
				_, err := manager.EnableSystemProxy(context.Background(), Operation{ID: "foreign-enable", Source: "test"}, false)
				return err
			},
			wantCode: protocol.CodeSystemProxyConflict,
		},
		{
			name:     "disable foreign",
			settings: defaultSysProxySettings(true),
			state:    sysproxy.State{Enabled: true, Server: "10.0.0.1:8080"},
			invoke: func(manager *Manager) error {
				_, err := manager.DisableSystemProxy(context.Background(), Operation{ID: "foreign-disable", Source: "test"})
				return err
			},
			wantCode: protocol.CodeSystemProxyNotOwned,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &sysproxy.FakeBackend{State: test.state}
			saves := 0
			manager := newTestManager(Options{
				SysProxy: backend, Settings: test.settings, SettingsPath: "settings.yaml",
				SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
					saves++
					return config.CommitResult{Committed: true}, nil
				},
			})
			before := manager.settingsSnapshot()
			err := test.invoke(manager)
			var apiError protocol.APIError
			if !errors.As(err, &apiError) || apiError.Code != test.wantCode {
				t.Fatalf("err=%v want code %s", err, test.wantCode)
			}
			if saves != 0 || backend.EnableCalls != 0 || backend.DisableCalls != 0 {
				t.Fatalf("saves=%d enable=%d disable=%d want 0/0/0", saves, backend.EnableCalls, backend.DisableCalls)
			}
			if got := manager.settingsSnapshot(); !reflect.DeepEqual(got, before) {
				t.Fatalf("conflict changed desired settings: got=%#v want=%#v", got, before)
			}
		})
	}
}

func TestSystemProxyCommittedWarningPublishesBeforeOSApplySettings(t *testing.T) {
	events := make([]string, 0, 4)
	backend := &hookSysProxyBackend{state: sysproxy.State{Enabled: false}}
	backend.onGet = func() { events = append(events, "get") }
	settings := defaultSysProxySettings(false)
	warnings := 0
	manager := newTestManager(Options{
		SysProxy: backend, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, saved config.Settings) (config.CommitResult, error) {
			events = append(events, "save")
			if !saved.SystemProxyDesired {
				t.Fatalf("saved desired=%v want true", saved.SystemProxyDesired)
			}
			return config.CommitResult{Committed: true, Warning: errors.New("sensitive path")}, nil
		},
		OnBackgroundError: func(component string, err error) {
			if component != "settings" || err.Error() != "parent directory sync failed after commit" {
				t.Fatalf("warning component=%q err=%v", component, err)
			}
			warnings++
		},
	})
	backend.onEnable = func() {
		events = append(events, "enable")
		if !manager.settingsSnapshot().SystemProxyDesired {
			t.Fatal("OS apply ran before committed settings were published")
		}
	}

	status, err := manager.EnableSystemProxy(context.Background(), Operation{ID: "warning", Source: "test"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Desired || warnings != 1 {
		t.Fatalf("status=%#v warnings=%d", status, warnings)
	}
	wantPrefix := []string{"get", "save", "enable"}
	if len(events) < len(wantPrefix) || !reflect.DeepEqual(events[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("events=%v want prefix=%v", events, wantPrefix)
	}
}

func TestSystemProxyApplyFailureWithUnprovableDisabledSnapshotDegrades(t *testing.T) {
	backend := &hookSysProxyBackend{
		state:                  sysproxy.State{Enabled: false},
		enableErr:              errors.New(`enable failed at C:\secret\proxy`),
		enableStateBeforeError: true,
	}
	settings := defaultSysProxySettings(false)
	var saved []bool
	var restarts int
	manager := newTestManager(Options{
		SysProxy: backend, Settings: settings, SettingsPath: "settings.yaml",
		Supervisor: &fakeSupervisor{restart: func(context.Context) error {
			restarts++
			return nil
		}},
		SaveSettings: func(_ string, candidate config.Settings) (config.CommitResult, error) {
			saved = append(saved, candidate.SystemProxyDesired)
			return config.CommitResult{Committed: true}, nil
		},
	})

	op := Operation{ID: "apply-fail", Source: "test"}
	_, err := manager.EnableSystemProxy(context.Background(), op, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "mutation compensation failed" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err=%v want sanitized compensation failure", err)
	}
	if !reflect.DeepEqual(saved, []bool{true, false}) {
		t.Fatalf("saved desired sequence=%v want [true false]", saved)
	}
	if backend.enableCalls != 1 || backend.disableCalls != 0 || !backend.state.Enabled {
		t.Fatalf("enable=%d disable=%d state=%#v", backend.enableCalls, backend.disableCalls, backend.state)
	}
	if manager.settingsSnapshot().SystemProxyDesired {
		t.Fatal("committed rollback did not publish before settings")
	}
	if snapshot := manager.Snapshot(); snapshot.Revision != 1 || snapshot.Health != "degraded" || snapshot.LastError != "mutation compensation failed; restart required" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if _, retryErr := manager.EnableSystemProxy(context.Background(), op, false); retryErr == nil || retryErr.Error() != err.Error() {
		t.Fatalf("retry err=%v want cached %v", retryErr, err)
	}
	if !reflect.DeepEqual(saved, []bool{true, false}) || backend.enableCalls != 1 || backend.disableCalls != 0 || manager.Snapshot().Revision != 1 {
		t.Fatalf("retry repeated side effects: saved=%v enable=%d disable=%d revision=%d", saved, backend.enableCalls, backend.disableCalls, manager.Snapshot().Revision)
	}
	if nextErr := manager.Restart(context.Background(), Operation{ID: "after-degraded", Source: "test"}); !errors.As(nextErr, &apiError) || apiError.Code != protocol.CodeInvalidState || restarts != 0 {
		t.Fatalf("next mutation err=%v restarts=%d", nextErr, restarts)
	}
	status, statusErr := manager.SystemProxyStatus(context.Background())
	if statusErr != nil || status.Revision != 1 || status.Desired || !status.Observed.Enabled {
		t.Fatalf("read-only status=%#v err=%v", status, statusErr)
	}
}

func TestSystemProxyDisableFailureRestoresEnabledLiveSettings(t *testing.T) {
	target := sysproxy.NormalizeServer("127.0.0.1", 9190)
	backend := &hookSysProxyBackend{
		state:                   sysproxy.State{Enabled: true, Server: target},
		disableErr:              errors.New("disable failed"),
		disableStateBeforeError: true,
	}
	settings := defaultSysProxySettings(true)
	var saved []bool
	manager := newTestManager(Options{
		SysProxy: backend, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, candidate config.Settings) (config.CommitResult, error) {
			saved = append(saved, candidate.SystemProxyDesired)
			return config.CommitResult{Committed: true}, nil
		},
	})

	_, err := manager.DisableSystemProxy(context.Background(), Operation{ID: "disable-partial", Source: "test"})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeUpstreamFailure {
		t.Fatalf("err=%v want original upstream failure", err)
	}
	if !reflect.DeepEqual(saved, []bool{false, true}) {
		t.Fatalf("saved desired sequence=%v want [false true]", saved)
	}
	if backend.disableCalls != 1 || backend.enableCalls != 1 || !backend.state.Enabled || backend.state.Server != target {
		t.Fatalf("disable=%d enable=%d state=%#v", backend.disableCalls, backend.enableCalls, backend.state)
	}
	if !manager.settingsSnapshot().SystemProxyDesired {
		t.Fatal("committed rollback did not restore desired state")
	}
	if snapshot := manager.Snapshot(); snapshot.Revision != 0 || snapshot.Health != "ok" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestSystemProxyRollbackPreCommitFailureCommitsDegradedSettings(t *testing.T) {
	backend := &hookSysProxyBackend{
		state:                  sysproxy.State{Enabled: false},
		enableErr:              errors.New("enable failed"),
		enableStateBeforeError: true,
	}
	settings := defaultSysProxySettings(false)
	saves := 0
	manager := newTestManager(Options{
		SysProxy: backend, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, candidate config.Settings) (config.CommitResult, error) {
			saves++
			if saves == 1 && candidate.SystemProxyDesired {
				return config.CommitResult{Committed: true}, nil
			}
			return config.CommitResult{}, errors.New("rollback replace failed at C:\\secret")
		},
	})

	_, err := manager.EnableSystemProxy(context.Background(), Operation{ID: "rollback-fail", Source: "test"}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "mutation compensation failed" {
		t.Fatalf("err=%v want stable compensation data failure", err)
	}
	if !manager.settingsSnapshot().SystemProxyDesired {
		t.Fatal("uncommitted rollback must keep memory aligned with committed next settings")
	}
	snapshot := manager.Snapshot()
	if snapshot.Revision != 1 || snapshot.Health != "degraded" || snapshot.LastError != "mutation compensation failed; restart required" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if !backend.state.Enabled || backend.disableCalls != 0 {
		t.Fatalf("unprovable prior state was destructively restored: state=%#v disable=%d", backend.state, backend.disableCalls)
	}
	_, nextErr := manager.DisableSystemProxy(context.Background(), Operation{ID: "after-degraded", Source: "test"})
	var nextAPIError protocol.APIError
	if !errors.As(nextErr, &nextAPIError) || nextAPIError.Code != protocol.CodeInvalidState {
		t.Fatalf("next mutation err=%v want invalid state", nextErr)
	}
	status, statusErr := manager.SystemProxyStatus(context.Background())
	if statusErr != nil || !status.Desired || status.Revision != 1 {
		t.Fatalf("read-only status=%#v err=%v", status, statusErr)
	}
}

func TestSystemProxyLiveRestoreFailureCommitsDegradedSettings(t *testing.T) {
	target := sysproxy.NormalizeServer("127.0.0.1", 9190)
	backend := &hookSysProxyBackend{
		state:                  sysproxy.State{Enabled: true, Server: target},
		enableErr:              errors.New("enable failed"),
		enableStateBeforeError: true,
	}
	settings := defaultSysProxySettings(false)
	manager := newTestManager(Options{
		SysProxy: backend, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			return config.CommitResult{Committed: true}, nil
		},
	})

	_, err := manager.EnableSystemProxy(context.Background(), Operation{ID: "live-restore-fail", Source: "test"}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "mutation compensation failed" {
		t.Fatalf("err=%v want stable compensation data failure", err)
	}
	if manager.settingsSnapshot().SystemProxyDesired {
		t.Fatal("committed settings rollback must publish before state")
	}
	if snapshot := manager.Snapshot(); snapshot.Revision != 1 || snapshot.Health != "degraded" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if backend.enableCalls != 2 || backend.disableCalls != 0 {
		t.Fatalf("enable=%d disable=%d want 2/0", backend.enableCalls, backend.disableCalls)
	}
}

func TestSystemProxyCommittedApplyIgnoresLateCancellationSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &hookSysProxyBackend{state: sysproxy.State{Enabled: false}}
	backend.onGet = func() {
		if backend.getCalls == 2 {
			cancel()
		}
	}
	settings := defaultSysProxySettings(false)
	saves := 0
	manager := newTestManager(Options{
		SysProxy: backend, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves++
			return config.CommitResult{Committed: true}, nil
		},
	})
	op := Operation{ID: "cancel-after-apply", Source: "test"}
	status, err := manager.EnableSystemProxy(ctx, op, false)
	if err != nil {
		t.Fatalf("committed mutation returned cancellation: %v", err)
	}
	if !status.Desired || status.Revision != 1 {
		t.Fatalf("status=%#v", status)
	}
	retry, err := manager.EnableSystemProxy(context.Background(), op, false)
	if err != nil || retry.Revision != 1 || saves != 1 || backend.enableCalls != 1 {
		t.Fatalf("retry=%#v err=%v saves=%d enable=%d", retry, err, saves, backend.enableCalls)
	}
}

func TestSystemProxyCancellationAfterSaveBeforeOSApplyCompensatesSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &hookSysProxyBackend{state: sysproxy.State{Enabled: false}}
	settings := defaultSysProxySettings(false)
	var saved []bool
	manager := newTestManager(Options{
		SysProxy: backend, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, candidate config.Settings) (config.CommitResult, error) {
			saved = append(saved, candidate.SystemProxyDesired)
			if len(saved) == 1 {
				cancel()
			}
			return config.CommitResult{Committed: true}, nil
		},
	})

	_, err := manager.EnableSystemProxy(ctx, Operation{ID: "cancel-after-save", Source: "test"}, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context canceled", err)
	}
	if !reflect.DeepEqual(saved, []bool{true, false}) {
		t.Fatalf("saved desired sequence=%v want [true false]", saved)
	}
	if backend.enableCalls != 0 || backend.disableCalls != 0 {
		t.Fatalf("enable=%d disable=%d want 0/0 before external apply", backend.enableCalls, backend.disableCalls)
	}
	if manager.settingsSnapshot().SystemProxyDesired || manager.Snapshot().Revision != 0 {
		t.Fatalf("settings=%#v snapshot=%#v", manager.settingsSnapshot(), manager.Snapshot())
	}
}

func TestSystemProxyCancellationDuringObservationSkipsSaveSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &hookSysProxyBackend{state: sysproxy.State{Enabled: false}, onGet: cancel}
	settings := defaultSysProxySettings(false)
	saves := 0
	manager := newTestManager(Options{
		SysProxy: backend, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves++
			return config.CommitResult{Committed: true}, nil
		},
	})

	_, err := manager.EnableSystemProxy(ctx, Operation{ID: "cancel-during-get", Source: "test"}, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context canceled", err)
	}
	if saves != 0 || backend.enableCalls != 0 || backend.disableCalls != 0 {
		t.Fatalf("saves=%d enable=%d disable=%d want 0/0/0", saves, backend.enableCalls, backend.disableCalls)
	}
	if manager.settingsSnapshot().SystemProxyDesired || manager.Snapshot().Revision != 0 {
		t.Fatalf("settings=%#v snapshot=%#v", manager.settingsSnapshot(), manager.Snapshot())
	}
}

func TestSystemProxyStartupReconcileSerializesAndClearsOnlyOwnedSettings(t *testing.T) {
	t.Run("owned", func(t *testing.T) {
		target := sysproxy.NormalizeServer("127.0.0.1", 9190)
		backend := &hookSysProxyBackend{state: sysproxy.State{Enabled: true, Server: target}}
		manager := newTestManager(Options{SysProxy: backend, Settings: defaultSysProxySettings(false)})
		if err := manager.lockMaintenance(context.Background()); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- manager.ApplyDesiredSystemProxy(context.Background()) }()
		select {
		case err := <-done:
			manager.unlock()
			t.Fatalf("startup reconcile bypassed maintenance gate: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		manager.unlock()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if backend.getCalls != 1 || backend.disableCalls != 1 || backend.state.Enabled {
			t.Fatalf("get=%d disable=%d state=%#v", backend.getCalls, backend.disableCalls, backend.state)
		}
	})

	t.Run("foreign", func(t *testing.T) {
		backend := &hookSysProxyBackend{state: sysproxy.State{Enabled: true, Server: "10.0.0.1:8080"}}
		manager := newTestManager(Options{SysProxy: backend, Settings: defaultSysProxySettings(false)})
		if err := manager.ApplyDesiredSystemProxy(context.Background()); err != nil {
			t.Fatal(err)
		}
		if backend.getCalls != 1 || backend.disableCalls != 0 || !backend.state.Enabled {
			t.Fatalf("get=%d disable=%d state=%#v", backend.getCalls, backend.disableCalls, backend.state)
		}
	})
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

type hookSysProxyBackend struct {
	state                   sysproxy.State
	getErr                  error
	enableErr               error
	disableErr              error
	enableStateBeforeError  bool
	disableStateBeforeError bool
	getCalls                int
	enableCalls             int
	disableCalls            int
	onGet                   func()
	onEnable                func()
	onDisable               func()
}

func (b *hookSysProxyBackend) Get() (sysproxy.State, error) {
	b.getCalls++
	if b.onGet != nil {
		b.onGet()
	}
	if b.getErr != nil {
		return sysproxy.State{}, b.getErr
	}
	return b.state, nil
}

func (b *hookSysProxyBackend) Enable(host string, port int) error {
	b.enableCalls++
	if b.enableErr == nil || b.enableStateBeforeError {
		b.state = sysproxy.State{Enabled: true, Server: sysproxy.NormalizeServer(host, port)}
	}
	if b.onEnable != nil {
		b.onEnable()
	}
	return b.enableErr
}

func (b *hookSysProxyBackend) Disable() error {
	b.disableCalls++
	if b.disableErr == nil || b.disableStateBeforeError {
		b.state.Enabled = false
	}
	if b.onDisable != nil {
		b.onDisable()
	}
	return b.disableErr
}
