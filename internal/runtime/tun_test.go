package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
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

	status, err := manager.EnableTun(context.Background(), Operation{ID: "tun-en", Source: "test"})
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

	status, err := manager.EnableTun(context.Background(), Operation{ID: "tun-stack", Source: "test"})
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
	})
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

func TestEnableTunRollsBackSettingsWhenApplyFails(t *testing.T) {
	controller := &fakeController{
		patchConfigs: func(context.Context, map[string]any) error {
			return protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "patch failed"}
		},
	}
	manager := newTunManager(t, controller, defaultTunSettings(nil))

	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-fail", Source: "test"})
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

func TestEnableTunMapsPermissionErrors(t *testing.T) {
	controller := &fakeController{
		patchConfigs: func(context.Context, map[string]any) error {
			return errors.New("operation not permitted")
		},
	}
	manager := newTunManager(t, controller, defaultTunSettings(nil))

	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-perm", Source: "test"})
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
	settingsPath := filepath.Join(t.TempDir(), "settings.yaml")
	if err := config.Save(settingsPath, settings); err != nil {
		t.Fatal(err)
	}
	return newTestManager(Options{
		Controller:   controller,
		SettingsPath: settingsPath,
		Settings:     settings,
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
