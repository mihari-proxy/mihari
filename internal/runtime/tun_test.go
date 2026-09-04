package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"github.com/mihari-proxy/mihari/internal/tundetect"
)

func TestTunStatusUnmanagedByDefault(t *testing.T) {
	controller := &fakeController{configs: map[string]any{
		"tun": map[string]any{"enable": false, "stack": "system"},
	}}
	manager := newTunManager(t, controller, defaultTunSettings(nil))

	status, err := manager.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Managed || status.DesiredEnable || status.Stack != "" {
		t.Fatalf("status=%#v", status)
	}
	if status.LiveEnable == nil || *status.LiveEnable {
		t.Fatalf("live_enable=%v", status.LiveEnable)
	}
	if status.Schema != "mihari/v1" {
		t.Fatalf("schema=%q", status.Schema)
	}
}

func TestTunStatusLiveEnableFromConfigs(t *testing.T) {
	controller := &fakeController{configs: map[string]any{
		"tun": map[string]any{"enable": true, "stack": "gVisor"},
	}}
	manager := newTunManager(t, controller, defaultTunSettings(map[string]any{
		"enable": true, "stack": "gVisor",
	}))

	status, err := manager.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Managed || !status.DesiredEnable || status.Stack != "gVisor" {
		t.Fatalf("status=%#v", status)
	}
	if status.LiveEnable == nil || !*status.LiveEnable {
		t.Fatalf("live_enable=%v", status.LiveEnable)
	}
}

func TestTunStatusOmitsLiveWhenCoreUnavailable(t *testing.T) {
	controller := &fakeController{
		configsErr: protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo controller is unavailable"},
	}
	manager := newTunManager(t, controller, defaultTunSettings(map[string]any{
		"enable": true, "stack": "gVisor",
	}))

	status, err := manager.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LiveEnable != nil {
		t.Fatalf("live_enable=%v, want nil when core unavailable", status.LiveEnable)
	}
	if !status.DesiredEnable || !status.Managed {
		t.Fatalf("status=%#v", status)
	}
}

func TestEnableTunPersistsAndPatches(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	manager := newTunManager(t, controller, defaultTunSettings(nil))

	status, err := manager.EnableTun(context.Background(), Operation{ID: "tun-en", Source: "test"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !status.DesiredEnable || !status.Managed || status.Stack != "gVisor" {
		t.Fatalf("status=%#v", status)
	}
	if status.LiveEnable == nil || !*status.LiveEnable {
		t.Fatalf("live_enable=%v", status.LiveEnable)
	}
	if controller.patchCalls != 1 {
		t.Fatalf("patchCalls=%d", controller.patchCalls)
	}
	tun, ok := controller.lastPatch["tun"].(map[string]any)
	if !ok || tun["enable"] != true || tun["stack"] != "gVisor" {
		t.Fatalf("lastPatch=%#v", controller.lastPatch)
	}
	assertPersistedTun(t, manager.settingsPath, true, "gVisor")
	if status.Revision != 1 {
		t.Fatalf("revision=%d", status.Revision)
	}
}

func TestDisableTunSetsEnableFalseKeepsManaged(t *testing.T) {
	controller := &fakeController{configs: map[string]any{
		"tun": map[string]any{"enable": true, "stack": "gVisor"},
	}}
	manager := newTunManager(t, controller, defaultTunSettings(map[string]any{
		"enable": true, "stack": "gVisor",
	}))

	status, err := manager.DisableTun(context.Background(), Operation{ID: "tun-dis", Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if status.DesiredEnable || !status.Managed || status.Stack != "gVisor" {
		t.Fatalf("status=%#v", status)
	}
	if status.LiveEnable == nil || *status.LiveEnable {
		t.Fatalf("live_enable=%v", status.LiveEnable)
	}
	tun, ok := controller.lastPatch["tun"].(map[string]any)
	if !ok || tun["enable"] != false || tun["stack"] != "gVisor" {
		t.Fatalf("lastPatch=%#v", controller.lastPatch)
	}
	assertPersistedTun(t, manager.settingsPath, false, "gVisor")
}

func TestEnableTunPreservesExistingStack(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	manager := newTunManager(t, controller, defaultTunSettings(map[string]any{
		"enable": false, "stack": "system",
	}))

	status, err := manager.EnableTun(context.Background(), Operation{ID: "tun-stack", Source: "test"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if status.Stack != "system" {
		t.Fatalf("stack=%q", status.Stack)
	}
	tun := controller.lastPatch["tun"].(map[string]any)
	if tun["stack"] != "system" {
		t.Fatalf("patched stack=%v", tun["stack"])
	}
}

func TestEnableTunRejectsStaleRevision(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	manager := newTunManager(t, controller, defaultTunSettings(nil))
	manager.store.Store(state.Snapshot{Revision: 5, Health: "ok"})

	stale := uint64(4)
	_, err := manager.EnableTun(context.Background(), Operation{
		ID: "tun-stale", Source: "test", IfRevision: &stale,
	}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v", err)
	}
	if controller.patchCalls != 0 {
		t.Fatalf("patchCalls=%d on revision conflict", controller.patchCalls)
	}
	if len(manager.settings.Tun) != 0 {
		t.Fatalf("tun mutated after stale revision: %#v", manager.settings.Tun)
	}
}

func TestTunPreCommitFailureSkipsControllerApplySettings(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	settings := defaultTunSettings(nil)
	saves := 0
	manager := newTestManager(Options{
		Controller: controller, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves++
			return config.CommitResult{}, errors.New("replace failed")
		},
	})

	_, err := manager.EnableTun(context.Background(), Operation{ID: "settings-save-fail", Source: "test"}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
		t.Fatalf("err=%v want data failure", err)
	}
	if saves != 1 || controller.patchCalls != 0 {
		t.Fatalf("saves=%d patchCalls=%d want 1/0", saves, controller.patchCalls)
	}
	if len(manager.settingsSnapshot().Tun) != 0 {
		t.Fatalf("pre-commit failure published tun=%#v", manager.settingsSnapshot().Tun)
	}
	if revision := manager.Snapshot().Revision; revision != 0 {
		t.Fatalf("revision=%d want=0", revision)
	}
}

func TestTunConflictRejectsBeforeSaveSettings(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	settings := defaultTunSettings(nil)
	saves := 0
	manager := newTestManager(Options{
		Controller: controller, Settings: settings, SettingsPath: "settings.yaml",
		TunDetect: &tundetect.FakeBackend{
			Detection: tundetect.Detection{TunInterfaces: []string{"foreign-tun"}},
		},
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves++
			return config.CommitResult{Committed: true}, nil
		},
	})

	_, err := manager.EnableTun(context.Background(), Operation{ID: "settings-conflict", Source: "test"}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeTunConflict {
		t.Fatalf("err=%v want tun conflict", err)
	}
	if saves != 0 || controller.patchCalls != 0 || len(manager.settingsSnapshot().Tun) != 0 {
		t.Fatalf("saves=%d patchCalls=%d tun=%#v", saves, controller.patchCalls, manager.settingsSnapshot().Tun)
	}
}

func TestTunApplyFailureRestoresCommittedBeforeSettings(t *testing.T) {
	controller := &fakeController{patchConfigs: func(context.Context, map[string]any) error {
		return errors.New("patch failed")
	}}
	settings := defaultTunSettings(nil)
	var saved []bool
	manager := newTestManager(Options{
		Controller: controller, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, candidate config.Settings) (config.CommitResult, error) {
			saved = append(saved, tunDesiredEnable(candidate.Tun))
			return config.CommitResult{Committed: true}, nil
		},
	})

	_, err := manager.EnableTun(context.Background(), Operation{ID: "apply-fail-settings", Source: "test"}, true)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeUpstreamFailure {
		t.Fatalf("err=%v want upstream failure", err)
	}
	if !slices.Equal(saved, []bool{true, false}) {
		t.Fatalf("saved enable sequence=%v want [true false]", saved)
	}
	if len(manager.settingsSnapshot().Tun) != 0 {
		t.Fatalf("committed rollback did not restore memory: %#v", manager.settingsSnapshot().Tun)
	}
	if snapshot := manager.Snapshot(); snapshot.Revision != 0 || snapshot.Health != "ok" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestTunRollbackPreCommitFailureCommitsDegradedSettings(t *testing.T) {
	controller := &fakeController{patchConfigs: func(context.Context, map[string]any) error {
		return errors.New("patch failed")
	}}
	settings := defaultTunSettings(nil)
	saves := 0
	manager := newTestManager(Options{
		Controller: controller, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, candidate config.Settings) (config.CommitResult, error) {
			saves++
			if saves == 1 && tunDesiredEnable(candidate.Tun) {
				return config.CommitResult{Committed: true}, nil
			}
			return config.CommitResult{}, errors.New("rollback failed at C:\\secret")
		},
	})

	_, err := manager.EnableTun(context.Background(), Operation{ID: "rollback-fail-settings", Source: "test"}, true)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "mutation compensation failed" {
		t.Fatalf("err=%v want stable compensation data failure", err)
	}
	if !tunDesiredEnable(manager.settingsSnapshot().Tun) {
		t.Fatalf("uncommitted rollback must keep next settings: %#v", manager.settingsSnapshot().Tun)
	}
	snapshot := manager.Snapshot()
	if snapshot.Revision != 1 || snapshot.Health != "degraded" || snapshot.LastError != "mutation compensation failed; restart required" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	_, nextErr := manager.DisableTun(context.Background(), Operation{ID: "after-degraded", Source: "test"})
	var nextAPIError protocol.APIError
	if !errors.As(nextErr, &nextAPIError) || nextAPIError.Code != protocol.CodeInvalidState {
		t.Fatalf("next mutation err=%v want invalid state", nextErr)
	}
	status, statusErr := manager.TunStatus(context.Background())
	if statusErr != nil || !status.DesiredEnable || status.Revision != 1 {
		t.Fatalf("read-only status=%#v err=%v", status, statusErr)
	}
}

func TestTunLiveRestoreFailureCommitsDegradedSettings(t *testing.T) {
	settings := defaultTunSettings(map[string]any{"enable": false, "stack": "system"})
	var patchCalls atomic.Int64
	controller := &fakeController{
		configs: map[string]any{"tun": map[string]any{"enable": false, "stack": "system"}},
		patchConfigs: func(context.Context, map[string]any) error {
			if patchCalls.Add(1) == 1 {
				return nil
			}
			return errors.New("restore live failed")
		},
	}
	manager := newTestManager(Options{
		Controller: controller, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			return config.CommitResult{Committed: true}, nil
		},
	})

	_, err := manager.EnableTun(context.Background(), Operation{ID: "live-restore-fail-settings", Source: "test"}, true)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "mutation compensation failed" {
		t.Fatalf("err=%v want stable compensation data failure", err)
	}
	if tunDesiredEnable(manager.settingsSnapshot().Tun) {
		t.Fatalf("committed settings rollback must publish before: %#v", manager.settingsSnapshot().Tun)
	}
	if snapshot := manager.Snapshot(); snapshot.Revision != 1 || snapshot.Health != "degraded" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestTunCancellationBeforeLiveConfirmationCompensatesSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	configsEntered := make(chan struct{})
	controller := &fakeController{patchConfigs: func(context.Context, map[string]any) error { return nil }}
	controller.configsFunc = func(ctx context.Context) (map[string]any, error) {
		if controller.patchCalls == 0 {
			return map[string]any{}, nil
		}
		close(configsEntered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	settings := defaultTunSettings(nil)
	var saved []bool
	manager := newTestManager(Options{
		Controller: controller, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, candidate config.Settings) (config.CommitResult, error) {
			saved = append(saved, tunDesiredEnable(candidate.Tun))
			return config.CommitResult{Committed: true}, nil
		},
	})
	done := make(chan error, 1)
	go func() {
		_, err := manager.EnableTun(ctx, Operation{ID: "cancel-before-live", Source: "test"}, true)
		done <- err
	}()
	select {
	case <-configsEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("live confirmation did not begin")
	}
	cancel()
	err := <-done
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeUpstreamFailure {
		t.Fatalf("err=%v want upstream failure", err)
	}
	if !slices.Equal(saved, []bool{true, false}) || len(manager.settingsSnapshot().Tun) != 0 {
		t.Fatalf("saved=%v tun=%#v", saved, manager.settingsSnapshot().Tun)
	}
	if revision := manager.Snapshot().Revision; revision != 0 {
		t.Fatalf("revision=%d want=0", revision)
	}
}

func TestTunCancellationAfterSaveBeforeControllerApplyCompensatesSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	controller := &fakeController{configs: map[string]any{}}
	settings := defaultTunSettings(nil)
	var saved []bool
	manager := newTestManager(Options{
		Controller: controller, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, candidate config.Settings) (config.CommitResult, error) {
			saved = append(saved, tunDesiredEnable(candidate.Tun))
			if len(saved) == 1 {
				cancel()
			}
			return config.CommitResult{Committed: true}, nil
		},
	})

	_, err := manager.EnableTun(ctx, Operation{ID: "cancel-after-save", Source: "test"}, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context canceled", err)
	}
	if !slices.Equal(saved, []bool{true, false}) {
		t.Fatalf("saved enable sequence=%v want [true false]", saved)
	}
	if controller.patchCalls != 0 {
		t.Fatalf("patchCalls=%d want=0 before external apply", controller.patchCalls)
	}
	if len(manager.settingsSnapshot().Tun) != 0 || manager.Snapshot().Revision != 0 {
		t.Fatalf("settings=%#v snapshot=%#v", manager.settingsSnapshot(), manager.Snapshot())
	}
}

func TestTunCommittedLiveIgnoresLateCancellationAndSerializesObservationSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backgroundStarted := make(chan struct{})
	backgroundDone := make(chan struct{})
	var liveChecks atomic.Int64
	var manager *Manager
	controller := &fakeController{patchConfigs: func(context.Context, map[string]any) error { return nil }}
	controller.configsFunc = func(context.Context) (map[string]any, error) {
		if controller.patchCalls == 0 {
			return map[string]any{}, nil
		}
		if liveChecks.Add(1) == 1 {
			cancel()
			go func() {
				close(backgroundStarted)
				manager.setCoreState(state.CoreState{Status: "running"})
				close(backgroundDone)
			}()
			<-backgroundStarted
		}
		return map[string]any{"tun": map[string]any{"enable": true, "stack": "gVisor"}}, nil
	}
	settings := defaultTunSettings(nil)
	saves := 0
	manager = newTestManager(Options{
		Controller: controller, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves++
			return config.CommitResult{Committed: true}, nil
		},
	})
	op := Operation{ID: "cancel-after-live", Source: "test"}
	status, err := manager.EnableTun(ctx, op, true)
	if err != nil {
		t.Fatalf("committed live mutation returned cancellation: %v", err)
	}
	if !status.DesiredEnable || status.Revision != 1 {
		t.Fatalf("status=%#v", status)
	}
	select {
	case <-backgroundDone:
	case <-time.After(3 * time.Second):
		t.Fatal("background observation did not resume after mutation commit")
	}
	if snapshot := manager.Snapshot(); snapshot.Revision != 2 || snapshot.Core.Status != "running" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	retry, err := manager.EnableTun(context.Background(), op, true)
	if err != nil || retry.Revision != 1 || saves != 1 || controller.patchCalls != 1 {
		t.Fatalf("retry=%#v err=%v saves=%d patchCalls=%d", retry, err, saves, controller.patchCalls)
	}
}

func TestEnableTunRollsBackSettingsWhenApplyFails(t *testing.T) {
	controller := &fakeController{
		patchConfigs: func(context.Context, map[string]any) error {
			return protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "patch failed"}
		},
	}
	manager := newTunManager(t, controller, defaultTunSettings(nil))

	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-fail", Source: "test"}, false)
	if err == nil {
		t.Fatal("expected apply failure")
	}
	if len(manager.settings.Tun) != 0 {
		t.Fatalf("in-memory tun after rollback=%#v", manager.settings.Tun)
	}
	loaded, loadErr := config.Load(manager.settingsPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(loaded.Tun) != 0 {
		t.Fatalf("persisted tun after rollback=%#v", loaded.Tun)
	}
}

func TestApplyTunReturnsPatchErrorEvenIfReloadSucceeded(t *testing.T) {
	root := t.TempDir()
	controller := &fakeController{
		configs: map[string]any{},
		patchConfigs: func(context.Context, map[string]any) error {
			return protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "patch tun rejected"}
		},
	}
	subs, err := subscription.Open(subscription.ServiceOptions{
		CatalogPath: filepath.Join(root, "catalog.yaml"),
		CacheDir:    filepath.Join(root, "cache"),
		ProxyAddr:   "127.0.0.1:9190",
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := defaultTunSettings(nil)
	settingsPath := filepath.Join(root, "settings.yaml")
	if err := config.Save(settingsPath, settings); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(Options{
		Controller:    controller,
		SettingsPath:  settingsPath,
		Settings:      settings,
		Subscriptions: subs,
		RuntimeConfig: filepath.Join(root, "runtime", "config.yaml"),
		StagingDir:    filepath.Join(root, "staging"),
	})
	_, err = manager.EnableTun(context.Background(), Operation{ID: "tun-patch-fail", Source: "test"}, false)
	if err == nil {
		t.Fatal("expected patch error")
	}
	loaded, loadErr := config.Load(settingsPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if tunDesiredEnable(loaded.Tun) {
		t.Fatalf("desired must roll back, tun=%#v", loaded.Tun)
	}
}

func TestEnableTunRollsBackWhenLiveStaysOff(t *testing.T) {
	controller := &fakeController{
		configs: map[string]any{"tun": map[string]any{"enable": false, "stack": "gVisor"}},
		patchConfigs: func(context.Context, map[string]any) error {
			return nil
		},
	}
	manager := newTunManager(t, controller, defaultTunSettings(nil))
	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-live-off", Source: "test"}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeUpstreamFailure {
		t.Fatalf("err=%v", err)
	}
	if apiError.Message != "TUN did not become live after apply" {
		t.Fatalf("message=%q", apiError.Message)
	}
	loaded, err := config.Load(manager.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if tunDesiredEnable(loaded.Tun) {
		t.Fatalf("desired rolled back? tun=%#v", loaded.Tun)
	}
}

func TestEnableTunForceStillRequiresLive(t *testing.T) {
	controller := &fakeController{
		configs:      map[string]any{"tun": map[string]any{"enable": false}},
		patchConfigs: func(context.Context, map[string]any) error { return nil },
	}
	manager := newTunManagerWithDetect(t, controller, defaultTunSettings(nil), &tundetect.FakeBackend{
		Detection: tundetect.Detection{TunInterfaces: []string{"mihomo (Meta Tunnel)"}},
	})
	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-force-live", Source: "test"}, true)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeUpstreamFailure {
		t.Fatalf("force must not skip live check, err=%v", err)
	}
}

func TestEnableTunFailsWhenConfigsUnreadableAfterApply(t *testing.T) {
	controller := &fakeController{
		configs:    map[string]any{},
		configsErr: protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo controller is unavailable"},
	}
	// patchConfigs 默认会写 configs，但随后 Configs 返回 err
	manager := newTunManager(t, controller, defaultTunSettings(nil))
	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-unread", Source: "test"}, false)
	if err == nil {
		t.Fatal("unread live must not count as success")
	}
}

func TestTunStatusLastErrorWhenDesiredOnLiveOff(t *testing.T) {
	live := false
	controller := &fakeController{configs: map[string]any{
		"tun": map[string]any{"enable": live, "stack": "gVisor"},
	}}
	manager := newTunManager(t, controller, defaultTunSettings(map[string]any{
		"enable": true, "stack": "gVisor",
	}))
	status, err := manager.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LastError != "live TUN is off" {
		t.Fatalf("LastError=%q", status.LastError)
	}
}

func TestEnableTunFailurePersistsLastErrorOnStatus(t *testing.T) {
	controller := &fakeController{
		configs:      map[string]any{"tun": map[string]any{"enable": false}},
		patchConfigs: func(context.Context, map[string]any) error { return nil },
	}
	manager := newTunManager(t, controller, defaultTunSettings(nil))
	_, _ = manager.EnableTun(context.Background(), Operation{ID: "tun-err", Source: "test"}, false)
	status, err := manager.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LastError != "TUN did not become live after apply" {
		t.Fatalf("LastError=%q", status.LastError)
	}
	if status.DesiredEnable {
		t.Fatal("desired should stay off after failed enable")
	}
}

func TestEnableTunMapsPermissionErrors(t *testing.T) {
	controller := &fakeController{
		patchConfigs: func(context.Context, map[string]any) error {
			return errors.New("operation not permitted")
		},
	}
	manager := newTunManager(t, controller, defaultTunSettings(nil))

	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-perm", Source: "test"}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodePermissionDenied {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(strings.ToLower(apiError.Message), "elevat") &&
		!strings.Contains(strings.ToLower(apiError.Message), "service") {
		t.Fatalf("message=%q", apiError.Message)
	}
}

func TestCapabilitiesIncludeTUN(t *testing.T) {
	manager := newTunManager(t, &fakeController{}, defaultTunSettings(nil))
	if !slices.Contains(manager.Capabilities(), protocol.CapabilityTUN) {
		t.Fatalf("capabilities=%v missing tun", manager.Capabilities())
	}
	// CapabilityTUN is always present even without a controller.
	managerNoController := newTestManager(Options{Settings: defaultTunSettings(nil)})
	if !slices.Contains(managerNoController.Capabilities(), protocol.CapabilityTUN) {
		t.Fatalf("capabilities=%v", managerNoController.Capabilities())
	}
}

func defaultTunSettings(tun map[string]any) config.Settings {
	return config.Settings{
		Schema:           "mihari.settings/v1",
		MixedAddr:        "127.0.0.1:9190",
		ControllerAddr:   "127.0.0.1:9090",
		WebAddr:          "127.0.0.1:9191",
		ControllerSecret: strings.Repeat("c", 64),
		Tun:              tun,
	}
}

func newTunManager(t *testing.T, controller *fakeController, settings config.Settings) *Manager {
	t.Helper()
	return newTunManagerWithDetect(t, controller, settings, nil)
}

func newTunManagerWithDetect(t *testing.T, controller *fakeController, settings config.Settings, detect tundetect.Backend) *Manager {
	t.Helper()
	settingsPath := filepath.Join(t.TempDir(), "settings.yaml")
	if err := config.Save(settingsPath, settings); err != nil {
		t.Fatal(err)
	}
	return newTestManager(Options{
		Controller:   controller,
		SettingsPath: settingsPath,
		Settings:     settings,
		TunDetect:    detect,
	})
}

func assertPersistedTun(t *testing.T, path string, wantEnable bool, wantStack string) {
	t.Helper()
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tun) == 0 {
		t.Fatal("expected persisted tun block")
	}
	if loaded.Tun["enable"] != wantEnable {
		t.Fatalf("tun.enable=%#v want %v", loaded.Tun["enable"], wantEnable)
	}
	if loaded.Tun["stack"] != wantStack {
		t.Fatalf("tun.stack=%#v want %q", loaded.Tun["stack"], wantStack)
	}
}

// TestEnableTunGatesOnOtherTunAdapters 验证核心门控：检测到其他 TUN 网卡且非 force
// 时拒绝 enable，且不触碰 controller（PatchConfigs 未被调用）。对应设计决策 1/2 的信号 A。
func TestEnableTunGatesOnOtherTunAdapters(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	manager := newTunManagerWithDetect(t, controller, defaultTunSettings(nil), &tundetect.FakeBackend{
		Detection: tundetect.Detection{TunInterfaces: []string{"Wintun0", "Wintun1"}},
	})

	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-conflict", Source: "test"}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeTunConflict {
		t.Fatalf("err=%v", err)
	}
	if controller.patchCalls != 0 {
		t.Fatalf("patchCalls=%d; gate must block apply", controller.patchCalls)
	}
	names, ok := apiError.Details["other_tun_interfaces"].([]string)
	if !ok || len(names) == 0 {
		t.Fatalf("details missing other_tun_interfaces: %#v", apiError.Details)
	}
}

// TestEnableTunForceOverridesConflict 验证 --force 绕过冲突门控，正常下发 PATCH。
func TestEnableTunForceOverridesConflict(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	manager := newTunManagerWithDetect(t, controller, defaultTunSettings(nil), &tundetect.FakeBackend{
		Detection: tundetect.Detection{TunInterfaces: []string{"Wintun0"}},
	})

	status, err := manager.EnableTun(context.Background(), Operation{ID: "tun-force", Source: "test"}, true)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !status.DesiredEnable {
		t.Fatalf("status=%#v", status)
	}
	if controller.patchCalls != 1 {
		t.Fatalf("patchCalls=%d", controller.patchCalls)
	}
	if status.Conflict == nil || !slices.Equal(status.Conflict.OtherTunInterfaces, []string{"Wintun0"}) {
		t.Fatalf("status conflict=%#v", status.Conflict)
	}
}

// TestEnableTunIgnoresOtherMihomoWithoutTun 验证信号 B 单独非空不触发门控：
// 另一个 mihomo 未开 TUN 时 enable 不冲突，正常成功。
func TestEnableTunIgnoresOtherMihomoWithoutTun(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	manager := newTunManagerWithDetect(t, controller, defaultTunSettings(nil), &tundetect.FakeBackend{
		Detection: tundetect.Detection{MihomoProcesses: []tundetect.Process{{Name: "mihomo", PID: 123}, {Name: "mihomo", PID: 456}}},
	})

	status, err := manager.EnableTun(context.Background(), Operation{ID: "tun-mihomo", Source: "test"}, false)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !status.DesiredEnable {
		t.Fatalf("status=%#v", status)
	}
	if controller.patchCalls != 1 {
		t.Fatalf("patchCalls=%d", controller.patchCalls)
	}
}

// TestDisableTunNotGatedByConflict 验证有意的不对称：disable 永不受冲突门控，
// 即使存在其他 TUN 网卡也正常下发（拆卸自身 tun 块对其他参与者非破坏性）。
func TestDisableTunNotGatedByConflict(t *testing.T) {
	controller := &fakeController{configs: map[string]any{
		"tun": map[string]any{"enable": true, "stack": "gVisor"},
	}}
	manager := newTunManagerWithDetect(t, controller, defaultTunSettings(map[string]any{
		"enable": true, "stack": "gVisor",
	}), &tundetect.FakeBackend{
		Detection: tundetect.Detection{TunInterfaces: []string{"Wintun0"}},
	})

	status, err := manager.DisableTun(context.Background(), Operation{ID: "tun-dis-conflict", Source: "test"})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if status.DesiredEnable {
		t.Fatalf("status=%#v", status)
	}
	if controller.patchCalls != 1 {
		t.Fatalf("patchCalls=%d", controller.patchCalls)
	}
}

// TestEnableTunSubtractsSelfWhenLiveActive 验证决策 3 的端到端：mihomo 自身已开 TUN
// （live tun.enable=true）且检测到的唯一 TUN 网卡就是自身那一个时，Classify 按 live
// device 与 core PID 扣除自身后无其他 TUN → enable 不被门控。覆盖 selfFromLive。
func TestEnableTunSubtractsSelfWhenLiveActive(t *testing.T) {
	controller := &fakeController{configs: map[string]any{
		"tun": map[string]any{"enable": true, "device": "Wintun0", "stack": "gVisor"},
	}}
	manager := newTunManagerWithDetect(t, controller, defaultTunSettings(nil), &tundetect.FakeBackend{
		Detection: tundetect.Detection{
			TunInterfaces:   []string{"Wintun0"},
			MihomoProcesses: []tundetect.Process{{Name: "mihomo", PID: 13400}},
		},
	})
	manager.store.Store(state.Snapshot{Health: "ok", Core: state.CoreState{PID: 13400}})

	status, err := manager.EnableTun(context.Background(), Operation{ID: "tun-self", Source: "test"}, false)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !status.DesiredEnable {
		t.Fatalf("status=%#v", status)
	}
	if controller.patchCalls != 1 {
		t.Fatalf("patchCalls=%d", controller.patchCalls)
	}
}

// TestEnableTunKeepsForeignAdapterWhenLiveDeviceDiffers 验证按 live device 名扣除自身：
// Sparkle 网卡排在前面时，盲删 [0] 会删错并放过 Enable；按名扣除 Meta 后必须留下
// mihomo (Meta Tunnel) 并返回 CodeTunConflict。
func TestEnableTunKeepsForeignAdapterWhenLiveDeviceDiffers(t *testing.T) {
	controller := &fakeController{configs: map[string]any{
		"tun": map[string]any{"enable": true, "device": "Meta", "stack": "gVisor"},
	}}
	manager := newTunManagerWithDetect(t, controller, defaultTunSettings(map[string]any{
		"enable": true, "stack": "gVisor",
	}), &tundetect.FakeBackend{
		Detection: tundetect.Detection{TunInterfaces: []string{"mihomo (Meta Tunnel)", "Meta"}},
	})
	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-foreign", Source: "test"}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeTunConflict {
		t.Fatalf("err=%v", err)
	}
	names, _ := apiError.Details["other_tun_interfaces"].([]string)
	if len(names) != 1 || names[0] != "mihomo (Meta Tunnel)" {
		t.Fatalf("details=%#v", apiError.Details)
	}
	if controller.patchCalls != 0 {
		t.Fatalf("patchCalls=%d", controller.patchCalls)
	}
}

// TestEnableTunNotGatedWhenDetectionFails 验证设计决策 3 的核心契约：tundetect 失败时
// detectTunConflict best-effort 返回 nil，enable 绝不被不透明的检测错误阻塞——即便 backend
// 返回 err，enable(!force) 仍正常成功下发，且 status.Conflict 为 nil（不构造证据）。
func TestEnableTunNotGatedWhenDetectionFails(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	manager := newTunManagerWithDetect(t, controller, defaultTunSettings(nil), &tundetect.FakeBackend{
		Err: errors.New("detection backend unavailable"),
	})

	status, err := manager.EnableTun(context.Background(), Operation{ID: "tun-detect-fail", Source: "test"}, false)
	if err != nil {
		t.Fatalf("best-effort: enable must not be blocked by detection error, got err=%v", err)
	}
	if !status.DesiredEnable {
		t.Fatalf("status=%#v", status)
	}
	if controller.patchCalls != 1 {
		t.Fatalf("patchCalls=%d; enable should apply despite detection failure", controller.patchCalls)
	}
	if status.Conflict != nil {
		t.Fatalf("conflict=%#v; detection failure must yield nil conflict evidence", status.Conflict)
	}
}

func TestTunStatusSubtractsSelfWhenCorePIDZeroButOccupantMatches(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	settings := defaultTunSettings(nil)
	manager := newTestManager(Options{
		Controller:   controller,
		SettingsPath: persistTunSettings(t, settings),
		Settings:     settings,
		TunDetect: &tundetect.FakeBackend{
			Detection: tundetect.Detection{MihomoProcesses: []tundetect.Process{{Name: "mihomo.exe", PID: 43560}}},
		},
		LookupTCPOccupant: func(addr string) (int, bool) {
			if addr != settings.ControllerAddr {
				t.Fatalf("occupant lookup addr=%q want %q", addr, settings.ControllerAddr)
			}
			return 43560, true
		},
	})

	status, err := manager.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Conflict != nil {
		t.Fatalf("conflict=%#v, want nil when controller occupant is our mihomo", status.Conflict)
	}
}

func TestTunStatusSubtractsSelfWhenCorePIDZeroButParentMatches(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	settings := defaultTunSettings(nil)
	manager := newTestManager(Options{
		Controller:   controller,
		SettingsPath: persistTunSettings(t, settings),
		Settings:     settings,
		TunDetect: &tundetect.FakeBackend{
			Detection: tundetect.Detection{MihomoProcesses: []tundetect.Process{
				{Name: "mihomo.exe", PID: 43560, ParentPID: os.Getpid()},
			}},
		},
	})

	status, err := manager.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Conflict != nil {
		t.Fatalf("conflict=%#v, want nil when parent is this daemon", status.Conflict)
	}
}

func TestTunStatusSubtractsSelfWhenCorePIDZeroButPathMatches(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	settings := defaultTunSettings(nil)
	corePath := filepath.Join(t.TempDir(), "mihomo.exe")
	manager := newTestManager(Options{
		Controller:     controller,
		SettingsPath:   persistTunSettings(t, settings),
		Settings:       settings,
		InstallRequest: core.InstallRequest{BinaryPath: corePath},
		TunDetect: &tundetect.FakeBackend{
			Detection: tundetect.Detection{MihomoProcesses: []tundetect.Process{
				{Name: "mihomo.exe", PID: 43560, Path: corePath},
			}},
		},
	})

	status, err := manager.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Conflict != nil {
		t.Fatalf("conflict=%#v, want nil when image path is the managed core", status.Conflict)
	}
}

func TestTunStatusStaleCorePIDUsesOccupant(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	settings := defaultTunSettings(nil)
	manager := newTestManager(Options{
		Controller:   controller,
		SettingsPath: persistTunSettings(t, settings),
		Settings:     settings,
		TunDetect: &tundetect.FakeBackend{
			Detection: tundetect.Detection{MihomoProcesses: []tundetect.Process{{Name: "mihomo.exe", PID: 43560}}},
		},
		LookupTCPOccupant: func(string) (int, bool) { return 43560, true },
	})
	manager.store.Store(state.Snapshot{Health: "ok", Core: state.CoreState{PID: 11111}})

	status, err := manager.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Conflict != nil {
		t.Fatalf("conflict=%#v, want nil when occupant matches live pid", status.Conflict)
	}
}

func TestTunStatusKeepsForeignMihomoWhenIdentityDoesNotMatch(t *testing.T) {
	controller := &fakeController{configs: map[string]any{}}
	settings := defaultTunSettings(nil)
	manager := newTestManager(Options{
		Controller:     controller,
		SettingsPath:   persistTunSettings(t, settings),
		Settings:       settings,
		InstallRequest: core.InstallRequest{BinaryPath: filepath.Join(t.TempDir(), "mihomo.exe")},
		TunDetect: &tundetect.FakeBackend{
			Detection: tundetect.Detection{MihomoProcesses: []tundetect.Process{
				{Name: "Sparkle-mihomo.exe", PID: 99, ParentPID: 8, Path: `C:\Sparkle\mihomo.exe`},
			}},
		},
		LookupTCPOccupant: func(string) (int, bool) { return 43560, true },
	})
	manager.store.Store(state.Snapshot{Health: "ok", Core: state.CoreState{PID: 43560}})

	status, err := manager.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Conflict == nil || len(status.Conflict.OtherMihomoProcesses) != 1 || status.Conflict.OtherMihomoProcesses[0] != "Sparkle-mihomo.exe (99)" {
		t.Fatalf("conflict=%#v", status.Conflict)
	}
}

func TestSelfFromLiveSkipsOccupantLookupWithoutController(t *testing.T) {
	calls := 0
	manager := newTestManager(Options{
		Settings: defaultTunSettings(nil),
		LookupTCPOccupant: func(string) (int, bool) {
			calls++
			return 123, true
		},
	})
	manager.controller = nil
	manager.selfFromLive(context.Background())
	if calls != 0 {
		t.Fatalf("occupant lookup calls=%d, want 0 without controller", calls)
	}
}

func TestSelfFromLiveSkipsOccupantLookupWhenContextCanceled(t *testing.T) {
	calls := 0
	manager := newTestManager(Options{
		Settings: defaultTunSettings(nil),
		LookupTCPOccupant: func(string) (int, bool) {
			calls++
			return 123, true
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager.selfFromLive(ctx)
	if calls != 0 {
		t.Fatalf("occupant lookup calls=%d, want 0 when context is canceled", calls)
	}
}

func persistTunSettings(t *testing.T, settings config.Settings) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if err := config.Save(path, settings); err != nil {
		t.Fatal(err)
	}
	return path
}
