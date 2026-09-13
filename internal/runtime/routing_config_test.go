package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type routingReloadController struct {
	*routingController
	reloads   int
	failFirst bool
}

func (c *routingReloadController) Reload(context.Context, string, bool) error {
	c.reloads++
	c.routingMu.Lock()
	c.selected = "DIRECT"
	c.routingMu.Unlock()
	if c.failFirst && c.reloads == 1 {
		return errors.New("injected reload failure")
	}
	return nil
}

func TestRouting_FailedConfigReloadRestoresPreviousExit(t *testing.T) {
	m, base := routingFixture(t)
	m.settings.SetRoutingMode("global")
	m.settings.SetGlobalSelection("a", "Node B")
	base.mode, base.selected = "global", "Node B"
	c := &routingReloadController{routingController: base, failFirst: true}
	m.controller = c
	m.runtimeConfig = filepath.Join(t.TempDir(), "runtime.yaml")
	before := []byte("mode: global\n")
	if err := os.WriteFile(m.runtimeConfig, before, 0o600); err != nil {
		t.Fatal(err)
	}
	err := m.commitRuntimeConfig(context.Background(), configCandidate{content: []byte("mode: direct\n")})
	if err == nil {
		t.Fatal("reload failure ignored")
	}
	if base.mode != "global" || base.selected != "Node B" {
		t.Fatalf("old routing not restored: mode=%s exit=%s", base.mode, base.selected)
	}
	got, readErr := os.ReadFile(m.runtimeConfig)
	if readErr != nil || string(got) != string(before) {
		t.Fatal("old config not restored")
	}
}

func TestRouting_ConfigCandidateRejectsChangedSettings(t *testing.T) {
	m, _ := routingFixture(t)
	err := m.commitRuntimeConfig(context.Background(), configCandidate{generationBound: true, generation: m.currentConfigGeneration() + 1})
	if err == nil {
		t.Fatal("stale generated mode accepted")
	}
}
