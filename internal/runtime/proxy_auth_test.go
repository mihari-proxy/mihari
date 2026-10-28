package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"go.yaml.in/yaml/v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type proxyAuthAPI interface {
	ProxyAuth(context.Context, bool) (protocol.ProxyAuthStatus, error)
	UpdateProxyAuth(context.Context, Operation, protocol.ProxyAuthUpdateRequest) (protocol.ProxyAuthStatus, error)
}

func TestProxyAuth_OnlineReloadsWithoutClosingAndHidesPasswords(t *testing.T) {
	m, c := onlineEgressManager(t)
	api, ok := any(m).(proxyAuthAPI)
	if !ok {
		t.Fatal("proxy auth management is missing")
	}
	c.failReload = false
	request := protocol.ProxyAuthUpdateRequest{Config: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "required", Users: []protocol.ProxyAuthUser{{Username: "fixture", Password: "fixture-secret"}}}}
	got, err := api.UpdateProxyAuth(t.Context(), Operation{ID: "auth"}, request)
	if err != nil {
		t.Fatal(err)
	}
	if c.reloads != 1 || c.closed != 0 || len(got.Users) != 1 || got.Users[0].Password != "" || got.Config.Users[0].Password != "" {
		t.Fatalf("reloads=%d closed=%d status=%+v", c.reloads, c.closed, got)
	}
	revealed, err := api.ProxyAuth(t.Context(), true)
	if err != nil || revealed.Users[0].Password != "fixture-secret" {
		t.Fatalf("reveal=%+v err=%v", revealed, err)
	}
}

func TestProxyAuth_SaveFailureRestoresRuntimeAndSettings(t *testing.T) {
	m, c := onlineEgressManager(t)
	api, ok := any(m).(proxyAuthAPI)
	if !ok {
		t.Fatal("proxy auth management is missing")
	}
	m.saveSettings = func(string, config.Settings) (config.CommitResult, error) {
		return config.CommitResult{}, errors.New("disk failure")
	}
	_, err := api.UpdateProxyAuth(t.Context(), Operation{ID: "failure"}, protocol.ProxyAuthUpdateRequest{Config: protocol.ProxyAuthConfig{Mode: "enabled", Users: []protocol.ProxyAuthUser{{Username: "fixture", Password: "secret"}}}})
	if err == nil || c.reloads != 2 || m.settingsSnapshot().ProxyAuth != nil {
		t.Fatalf("err=%v reloads=%d", err, c.reloads)
	}
}

func TestProxyAuth_MissingCoreSavesWithoutInvokingValidator(t *testing.T) {
	m := startupLoggingManager(t)
	m.store.Store(state.Snapshot{Core: state.CoreState{Status: "missing"}})
	m.binaryExists = func() bool { return false }
	m.validateConfig = func(context.Context, string) error { t.Fatal("missing core must not be executed"); return nil }
	_, err := m.UpdateProxyAuth(t.Context(), Operation{ID: "offline"}, protocol.ProxyAuthUpdateRequest{Config: protocol.ProxyAuthConfig{Mode: "enabled", Users: []protocol.ProxyAuthUser{{Username: "u", Password: "p"}}}})
	if err != nil {
		t.Fatal(err)
	}
}
func TestProxyAuth_StaleGenerationCannotPublishValidatedCandidate(t *testing.T) {
	m, c := onlineEgressManager(t)
	m.validateConfig = func(context.Context, string) error {
		m.settingsMu.Lock()
		m.configGeneration++
		m.settingsMu.Unlock()
		return nil
	}
	_, err := m.UpdateProxyAuth(t.Context(), Operation{ID: "stale"}, protocol.ProxyAuthUpdateRequest{Config: protocol.ProxyAuthConfig{Mode: "enabled", Users: []protocol.ProxyAuthUser{{Username: "u", Password: "p"}}}})
	if err == nil || c.reloads != 0 || m.settingsSnapshot().ProxyAuth != nil {
		t.Fatalf("stale auth committed: err=%v reloads=%d", err, c.reloads)
	}
}

func TestProxyAuth_CommittedUpdateReturnsWarningWhenStatusReadFails(t *testing.T) {
	m, _ := onlineEgressManager(t)
	root := t.TempDir()
	service, err := subscription.Open(subscription.ServiceOptions{CatalogPath: filepath.Join(root, "catalog.yaml"), CacheDir: filepath.Join(root, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	catalog := subscription.Defaults()
	id := strings.Repeat("a", 32)
	catalog.ActiveID = id
	catalog.Profiles = []subscription.Profile{{ID: id, Name: "fixture", URL: "https://example.invalid/sub", Enabled: true, Generation: 1}}
	if err := service.Restore(catalog); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(service.CachePath(id), []byte("proxies: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m.subscriptions = service
	m.saveSettings = func(path string, s config.Settings) (config.CommitResult, error) {
		result, err := config.SaveWithCommit(path, s)
		if err == nil {
			if err := os.Remove(service.CachePath(id)); err != nil {
				t.Fatal(err)
			}
		}
		return result, err
	}
	ctx, outcome := diagnostics.WithResult(t.Context())
	got, err := m.UpdateProxyAuth(ctx, Operation{ID: "commit-status"}, protocol.ProxyAuthUpdateRequest{Config: protocol.ProxyAuthConfig{Mode: "enabled", Users: []protocol.ProxyAuthUser{{Username: "alice", Password: "fixture"}}}})
	if err != nil || got.Config.Mode != "enabled" || len(outcome.Warnings().Warnings) == 0 || got.Config.Users[0].Password != "" {
		t.Fatalf("committed auth misreported as failure: warnings=%v err=%v", outcome.Warnings(), err)
	}
}

func TestProxyAuth_ResetRemovesPersistedDefaultBlock(t *testing.T) {
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	settings.ProxyAuth = &config.ProxyAuthSettings{Mode: "follow", BypassMode: "follow"}
	path := filepath.Join(t.TempDir(), "settings.yaml")
	raw, err := yaml.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(Options{Settings: settings, SettingsPath: path, BinaryExists: func() bool { return false }})
	m.store.Store(state.Snapshot{Core: state.CoreState{Status: "missing"}})
	if _, err := m.UpdateProxyAuth(t.Context(), Operation{ID: "remove-default"}, protocol.ProxyAuthUpdateRequest{Reset: true}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil || strings.Contains(string(raw), "proxy-auth:") {
		t.Fatalf("reset left a default block behind: %v", err)
	}
}

func TestProxyAuth_RecoveryResetFinishesAfterCommitCancellation(t *testing.T) {
	m := startupLoggingManager(t)
	m.settingsPath = filepath.Join(t.TempDir(), "settings.yaml")
	m.recoveryOnly, m.authResetAllowed = true, true
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.saveSettings = func(path string, settings config.Settings) (config.CommitResult, error) {
		result, err := config.SaveWithCommit(path, settings)
		if result.Committed {
			cancel()
		}
		return result, err
	}
	before := m.store.Load().Revision
	status, err := m.UpdateProxyAuth(ctx, Operation{ID: "recovery-cancel"}, protocol.ProxyAuthUpdateRequest{Reset: true})
	if err != nil || ctx.Err() == nil || status.Revision <= before || !m.onboardingRestartRequired.Load() {
		t.Fatalf("committed recovery reset did not finish: revision=%d err=%v", status.Revision, err)
	}
}

func TestProxyAuth_UnchangedConfigurationDoesNotReload(t *testing.T) {
	m, core := onlineEgressManager(t)
	request := protocol.ProxyAuthUpdateRequest{Config: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "follow", Users: []protocol.ProxyAuthUser{{Username: "alice", Password: "fixture"}}}}
	if _, err := m.UpdateProxyAuth(t.Context(), Operation{ID: "first"}, request); err != nil {
		t.Fatal(err)
	}
	before := m.store.Load().Revision
	if _, err := m.UpdateProxyAuth(t.Context(), Operation{ID: "unchanged"}, request); err != nil || core.reloads != 1 || m.store.Load().Revision != before {
		t.Fatalf("unchanged config reloaded: reloads=%d err=%v", core.reloads, err)
	}
}
