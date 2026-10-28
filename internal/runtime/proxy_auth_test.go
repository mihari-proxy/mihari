package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
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
