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
	"go.yaml.in/yaml/v3"
)

func startupLoggingManager(t *testing.T) *Manager {
	t.Helper()
	root := t.TempDir()
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	m := newTestManager(Options{Settings: settings, RuntimeConfig: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging")})
	if err := os.WriteFile(m.runtimeConfig, []byte("log-level: error\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLoggingStartup_RebuildsSavedLevelOnEveryStart(t *testing.T) {
	m := startupLoggingManager(t)
	for _, level := range []string{"debug", "silent", "info"} {
		_, err := m.updateSettings(t.Context(), func(settings *config.Settings) error {
			log := settings.EffectiveLogging()
			log.Level = level
			settings.SetLogging(log)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		release, err := m.PrepareCoreStart(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		release()
		content, err := os.ReadFile(m.runtimeConfig)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(content, &doc); err != nil {
			t.Fatal(err)
		}
		if doc["log-level"] != level {
			t.Fatalf("config=%s; want saved %s", content, level)
		}
	}
}

func TestLoggingStartup_RejectsStaleValidatedCandidate(t *testing.T) {
	m := startupLoggingManager(t)
	m.validateConfig = func(context.Context, string) error {
		m.settingsMu.Lock()
		m.configGeneration++
		m.settingsMu.Unlock()
		return nil
	}
	release, err := m.PrepareCoreStart(t.Context())
	if release != nil {
		release()
	}
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v", err)
	}
	content, err := os.ReadFile(m.runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "log-level: error\n" {
		t.Fatalf("stale configuration published: %s", content)
	}
}

func TestLoggingStartup_ReassembledManagerUsesLastSuccessfulSave(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		m, c := onlineLoggingManager(t)
		if _, err := config.SaveWithCommit(m.settingsPath, m.settingsSnapshot()); err != nil {
			t.Fatal(err)
		}
		if failSave {
			m.saveSettings = func(string, config.Settings) (config.CommitResult, error) {
				return config.CommitResult{}, errors.New("save failed")
			}
		}
		c.level = "debug"
		err := m.SyncLogging(t.Context())
		if (err != nil) != failSave {
			t.Fatalf("sync error=%v failSave=%t", err, failSave)
		}
		saved, err := config.Load(m.settingsPath)
		if err != nil {
			t.Fatal(err)
		}
		reassembled := newTestManager(Options{Settings: saved, RuntimeConfig: m.runtimeConfig, StagingDir: m.stagingDir})
		release, err := reassembled.PrepareCoreStart(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		release()
		content, err := os.ReadFile(m.runtimeConfig)
		if err != nil {
			t.Fatal(err)
		}
		want := "debug"
		if failSave {
			want = "info"
		}
		if !strings.Contains(string(content), "log-level: "+want) {
			t.Fatalf("reassembled config ignored durable state: %s", content)
		}
	}
}
