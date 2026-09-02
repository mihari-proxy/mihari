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
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/preferences"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

func TestResetUserDataClearsUserStateKeepsCachesAndToken(t *testing.T) {
	root := t.TempDir()
	paths := platform.NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	secret := strings.Repeat("c", 64)
	settings := config.Defaults()
	settings.ControllerSecret = secret
	settings.MixedAddr = "127.0.0.1:9290"
	settings.SystemProxyDesired = true
	settings.Tun = map[string]any{"enable": true, "stack": "gVisor"}
	if err := config.Save(paths.Settings, settings); err != nil {
		t.Fatal(err)
	}

	onboardingService, err := onboarding.Open(onboarding.Options{
		StatePath: paths.Onboarding, SettingsPath: paths.Settings, Settings: settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	complete := true
	if _, err := onboardingService.Update(onboarding.Update{Complete: &complete}); err != nil {
		t.Fatal(err)
	}

	catalog := subscription.Defaults()
	catalog.Profiles = []subscription.Profile{{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "home", URL: "https://example.test/sub", Enabled: true,
	}}
	if err := subscription.Save(paths.SubscriptionCatalog, catalog); err != nil {
		t.Fatal(err)
	}
	subs, err := subscription.Open(subscription.ServiceOptions{CatalogPath: paths.SubscriptionCatalog, CacheDir: paths.SubscriptionCache})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subs.CachePath(catalog.Profiles[0].ID), []byte("proxies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	prefs, err := preferences.Open(paths.TUIPreferences)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prefs.Update(context.Background(), preferences.Update{ConnectionsColumns: []string{"host", "rule"}}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(paths.ControlToken, []byte("keep-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.CoreBinary, []byte("mihomo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.GeoIPCountry, []byte("country"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Log, []byte("old log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Staging, "tmp.bin"), []byte("stage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RuntimeConfig, []byte("proxies: [old]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := panel.SaveActive(paths.WebActive, panel.Active{Panel: panel.IDZashboard, Build: "v1"}); err != nil {
		t.Fatal(err)
	}
	panelBuild := filepath.Join(paths.WebRoot, panel.IDZashboard, "v1", "index.html")
	if err := os.MkdirAll(filepath.Dir(panelBuild), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(panelBuild, []byte("<html>"), 0o600); err != nil {
		t.Fatal(err)
	}

	target := sysproxy.NormalizeServer("127.0.0.1", 9290)
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: true, Server: target}}
	restarts := 0
	manager := newTestManager(Options{
		Paths:         paths,
		Settings:      settings,
		SettingsPath:  paths.Settings,
		RuntimeConfig: paths.RuntimeConfig,
		StagingDir:    paths.SubscriptionStaging,
		Onboarding:    onboardingService,
		Subscriptions: subs,
		Preferences:   prefs,
		SysProxy:      backend,
		Supervisor:    &fakeSupervisor{restart: func(context.Context) error { restarts++; return nil }},
	})
	manager.store.Store(state.Snapshot{Revision: 4, Health: "ok"})

	result, err := manager.ResetUserData(context.Background(), Operation{ID: "reset-1", Source: "control"})
	if err != nil {
		t.Fatal(err)
	}
	if result.OperationID != "reset-1" || !result.SetupRequired || result.Revision != 5 {
		t.Fatalf("result=%#v", result)
	}
	if backend.DisableCalls != 1 {
		t.Fatalf("DisableCalls=%d", backend.DisableCalls)
	}
	if restarts != 1 {
		t.Fatalf("restarts=%d", restarts)
	}

	loaded, err := config.Load(paths.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MixedAddr != config.Defaults().MixedAddr || loaded.SystemProxyDesired || len(loaded.Tun) != 0 || loaded.ControllerSecret != secret {
		t.Fatalf("settings=%#v", loaded)
	}
	if onboardingService.Status().Complete || onboardingService.Status().MixedAddr != config.Defaults().MixedAddr {
		t.Fatalf("onboarding=%#v", onboardingService.Status())
	}
	if got := manager.Subscriptions(); len(got.Profiles) != 0 {
		t.Fatalf("subscriptions=%#v", got)
	}
	if _, err := os.Stat(subs.CachePath(catalog.Profiles[0].ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache still present: %v", err)
	}
	if cols := manager.TUIPreferences().ConnectionsColumns; len(cols) == 2 && cols[1] == "rule" {
		t.Fatalf("preferences were not reset: %v", cols)
	}
	active, err := panel.LoadActive(paths.WebActive)
	if err != nil || active.Panel != "" {
		t.Fatalf("active=%#v err=%v", active, err)
	}
	if raw, err := os.ReadFile(paths.Log); err != nil || len(raw) != 0 {
		t.Fatalf("log=%q err=%v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(paths.Staging, "tmp.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging file still present: %v", err)
	}
	if raw, err := os.ReadFile(paths.ControlToken); err != nil || string(raw) != "keep-token\n" {
		t.Fatalf("token=%q err=%v", raw, err)
	}
	if _, err := os.Stat(paths.CoreBinary); err != nil {
		t.Fatalf("core binary missing: %v", err)
	}
	if _, err := os.Stat(paths.GeoIPCountry); err != nil {
		t.Fatalf("geoip missing: %v", err)
	}
	if _, err := os.Stat(panelBuild); err != nil {
		t.Fatalf("panel build missing: %v", err)
	}
	runtimeRaw, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil || !strings.Contains(string(runtimeRaw), "DIRECT") {
		t.Fatalf("runtime config=%q err=%v", runtimeRaw, err)
	}
}

func TestResetUserDataRejectsStaleRevision(t *testing.T) {
	manager := newTestManager(Options{})
	manager.store.Store(state.Snapshot{Revision: 3})
	stale := uint64(2)
	_, err := manager.ResetUserData(context.Background(), Operation{ID: "reset-stale", Source: "test", IfRevision: &stale})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v", err)
	}
}
