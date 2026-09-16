package runtime

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
	"go.yaml.in/yaml/v3"
)

type loggingController struct {
	fakeController
	mu                 sync.Mutex
	level              string
	path               string
	patches, reloads   int
	patchErr, readErr  error
	failReload         bool
	failReadAfterPatch bool
	firstReloadLevel   string
}

func (c *loggingController) Configs(context.Context) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{"log-level": c.level, "mode": "rule"}, c.readErr
}

func (c *loggingController) PatchConfigs(_ context.Context, patch map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.patches++
	if level, ok := patch["log-level"].(string); ok {
		c.level = level
	}
	if c.failReadAfterPatch {
		c.readErr = errors.New("readback unavailable")
	}
	return c.patchErr
}

func (c *loggingController) Reload(context.Context, string, bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reloads++
	if c.failReload && c.reloads == 1 {
		return errors.New("reload failed")
	}
	content, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := yaml.Unmarshal(content, &document); err != nil {
		return err
	}
	c.level, _ = document["log-level"].(string)
	if c.reloads == 1 && c.firstReloadLevel != "" {
		c.level = c.firstReloadLevel
	}
	return nil
}

func onlineLoggingManager(t *testing.T) (*Manager, *loggingController) {
	t.Helper()
	m := startupLoggingManager(t)
	c := &loggingController{level: "info", path: m.runtimeConfig}
	m.controller = c
	m.logging = &recordingLoggingRuntime{}
	m.settingsPath = filepath.Join(t.TempDir(), "settings.yaml")
	m.store.Store(state.Snapshot{Health: "ok", Core: state.CoreState{Status: "running", PID: 42}})
	if err := os.WriteFile(m.runtimeConfig, []byte("log-level: info\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return m, c
}

func TestLogging_OnlineChangeConfirmsCoreBeforeSettingsPublish(t *testing.T) {
	m, c := onlineLoggingManager(t)
	m.saveSettings = func(_ string, settings config.Settings) (config.CommitResult, error) {
		if c.level != "debug" || c.reloads != 1 {
			t.Fatalf("saved before core confirmed: %+v", c)
		}
		if m.settingsSnapshot().EffectiveLogging().Level != "info" {
			t.Fatal("published before save")
		}
		return config.CommitResult{Committed: true}, nil
	}
	status, err := m.UpdateLogging(t.Context(), Operation{ID: "online"}, LoggingUpdate{Level: stringPointer("debug")})
	if err != nil {
		t.Fatal(err)
	}
	if status.Level != "debug" || c.level != "debug" || m.settingsSnapshot().EffectiveLogging().Level != "debug" {
		t.Fatalf("status=%+v core=%s", status, c.level)
	}
}

func TestLogging_OnlineFailureRestoresActualCoreAndRuntime(t *testing.T) {
	for _, failure := range []string{"validation", "reload", "save", "patch timeout", "reload mismatch"} {
		t.Run(failure, func(t *testing.T) {
			m, c := onlineLoggingManager(t)
			c.level = "warning" // Saved/runtime info and live warning deliberately differ.
			switch failure {
			case "validation":
				m.validateConfig = func(context.Context, string) error { return errors.New("invalid") }
			case "reload":
				c.failReload = true
			case "save":
				m.saveSettings = func(string, config.Settings) (config.CommitResult, error) {
					return config.CommitResult{}, errors.New("disk full")
				}
			case "patch timeout":
				c.patchErr = context.DeadlineExceeded
			case "reload mismatch":
				c.firstReloadLevel = "error"
			}
			_, err := m.UpdateLogging(t.Context(), Operation{ID: "failure"}, LoggingUpdate{Level: stringPointer("debug")})
			if err == nil {
				t.Fatal("failed operation succeeded")
			}
			content, readErr := os.ReadFile(m.runtimeConfig)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if c.level != "warning" || string(content) != "log-level: info\n" || m.settingsSnapshot().EffectiveLogging().Level != "info" {
				t.Fatalf("rollback: core=%s runtime=%s saved=%s", c.level, content, m.settingsSnapshot().EffectiveLogging().Level)
			}
		})
	}
}

func TestLogging_RejectsChangedCacheAfterValidation(t *testing.T) {
	m, service, _, url := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	profile, err := m.AddSubscription(t.Context(), Operation{ID: "add"}, AddSubscriptionInput{Name: "fixture", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UseSubscription(t.Context(), Operation{ID: "use"}, profile.ID); err != nil {
		t.Fatal(err)
	}
	c := &loggingController{level: "info", path: m.runtimeConfig}
	m.controller, m.logging = c, &recordingLoggingRuntime{}
	s := m.store.Load()
	s.Core = state.CoreState{Status: "running", PID: 42}
	m.store.Store(s)
	m.validateConfig = func(context.Context, string) error {
		return os.WriteFile(service.CachePath(profile.ID), []byte("proxies: []\nlog-level: error\n"), 0o600)
	}
	_, err = m.UpdateLogging(t.Context(), Operation{ID: "cache-changed"}, LoggingUpdate{Level: stringPointer("debug")})
	assertLoggingAPIError(t, err, protocol.CodeRevisionConflict)
	if c.patches != 0 {
		t.Fatal("stale cache candidate reached core")
	}
}

func TestLogging_FailedControlledChangePreservesUnsavedExternalProtection(t *testing.T) {
	m, c := onlineLoggingManager(t)
	c.level = "warning"
	m.saveSettings = func(string, config.Settings) (config.CommitResult, error) {
		return config.CommitResult{}, errors.New("disk full")
	}
	if err := m.SyncLogging(t.Context()); err == nil {
		t.Fatal("expected unsaved external change")
	}
	if _, err := m.UpdateLogging(t.Context(), Operation{ID: "failed-override"}, LoggingUpdate{Level: stringPointer("debug")}); err == nil {
		t.Fatal("expected controlled save failure")
	}
	if c.level != "warning" {
		t.Fatalf("rollback level=%s", c.level)
	}
	before := c.reloads
	if err := m.commitRuntimeConfig(t.Context(), configCandidate{content: []byte("log-level: info\n")}); err == nil {
		t.Fatal("failed controlled update removed unsaved protection")
	}
	if c.reloads != before {
		t.Fatal("stale runtime reload reached core after rollback")
	}
	// A successful explicit change can still supersede the external value.
	m.saveSettings = config.SaveWithCommit
	if _, err := m.UpdateLogging(t.Context(), Operation{ID: "successful-override"}, LoggingUpdate{Level: stringPointer("error")}); err != nil {
		t.Fatal(err)
	}
	if m.loggingUnsaved || c.level != "error" {
		t.Fatal("successful explicit update retained unsaved state")
	}
}
