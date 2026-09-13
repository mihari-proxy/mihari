package runtime

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/config"
	"testing"
)

func TestRouting_StagedTunCommitRetainsReloadFallback(t *testing.T) {
	m, _ := routingFixture(t)
	m.settings.SetRoutingMode("global")
	m.settings.SetGlobalSelection("a", "Missing")
	candidate, err := m.prepareSettings(func(s *config.Settings) error { s.Tun = map[string]any{"enable": true}; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.saveSettingsCandidate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	// Reload has persistently replaced the missing exit before the TUN owner
	// publishes its staged settings. Both committed intents must survive.
	if _, err := m.updateSettings(context.Background(), func(s *config.Settings) error { s.SetGlobalSelection("a", "DIRECT"); return nil }); err != nil {
		t.Fatal(err)
	}
	candidate, err = m.settleRoutingSettings(context.Background(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	m.publishSettings(candidate)
	saved, err := config.Load(m.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.GlobalSelection("a") != "DIRECT" || saved.Tun["enable"] != true || m.settingsSnapshot().GlobalSelection("a") != "DIRECT" {
		t.Fatal("TUN commit lost fallback or settings diverged")
	}
}

func TestRouting_StagedTunRollbackRestoresPublishedFallback(t *testing.T) {
	m, _ := routingFixture(t)
	m.settings.SetRoutingMode("global")
	m.settings.SetGlobalSelection("a", "Node B")
	candidate, err := m.prepareSettings(func(s *config.Settings) error { s.Tun = map[string]any{"enable": true}; return nil })
	if err != nil {
		t.Fatal(err)
	}
	m.settings.SetGlobalSelection("a", "DIRECT")
	_ = m.rollbackTrustedTun(context.Background(), Operation{ID: "rollback"}, candidate, nil, context.Canceled)
	if m.settingsSnapshot().GlobalSelection("a") != "Node B" {
		t.Fatal("rollback left published fallback in memory")
	}
}
