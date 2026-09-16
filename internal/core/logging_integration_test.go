package core_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/logging"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/supervisor"
)

func TestRootManager_LoggingTransactionUsesTrustedConfigAndCompensates(t *testing.T) {
	for _, failure := range []string{"", "reload", "save", "publication"} {
		t.Run(failure, func(t *testing.T) {
			settingsPath := filepath.Join(t.TempDir(), "settings.yaml")
			m, fixture, controller, _ := seamManager(t, func(o *runtimeapi.Options) {
				o.Logging = logging.NewGroup("fixture-logs", logging.DefaultConfig())
				o.SettingsPath = settingsPath
				if failure == "save" {
					o.SaveSettings = func(string, config.Settings) (config.CommitResult, error) {
						return config.CommitResult{}, errors.New("save failed")
					}
				}
			})
			controller.logLevel = "warning"
			m.Observe(supervisor.Observation{Status: supervisor.StatusRunning, PID: 42})
			before := fixture.Content()
			if failure == "reload" {
				controller.failReloads = 1
			}
			if failure == "publication" {
				fixture.FailConfigWriteAfter(1, errors.New("publish failed after replace"))
			}
			level := "debug"
			status, err := m.UpdateLogging(t.Context(), runtimeapi.Operation{ID: "trusted-logging"}, runtimeapi.LoggingUpdate{Level: &level})
			if failure == "" {
				if err != nil || status.Level != "debug" || controller.logLevel != "debug" || !bytes.Contains(fixture.Content(), []byte("log-level: debug")) {
					t.Fatalf("status=%+v err=%v level=%s", status, err, controller.logLevel)
				}
			} else {
				if err == nil || controller.logLevel != "warning" || !bytes.Equal(before, fixture.Content()) {
					t.Fatalf("rollback failed: err=%v live=%s", err, controller.logLevel)
				}
			}
			capability, err := fixture.Trusted.CommittedConfig(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := capability.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRootManager_LoggingAdoptionRebuildsBeforeEveryStart(t *testing.T) {
	m, fixture, controller, _ := seamManager(t, func(o *runtimeapi.Options) { o.Logging = logging.NewGroup("fixture", logging.DefaultConfig()) })
	for _, level := range []string{"debug", "silent"} {
		m.Observe(supervisor.Observation{Status: supervisor.StatusRunning, PID: 42})
		controller.logLevel = level
		if err := m.SyncLogging(t.Context()); err != nil {
			t.Fatal(err)
		}
		patches, reloads := controller.patches, controller.reloads
		release, err := m.PrepareCoreStart(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		release()
		if !bytes.Contains(fixture.Content(), []byte("log-level: "+level)) {
			t.Fatalf("startup level missing: %s", fixture.Content())
		}
		if controller.patches != patches || controller.reloads != reloads {
			t.Fatal("adoption/startup sent live mutation")
		}
	}
}

func TestRootManager_StartupPublicationRecoveryFailureFencesMutation(t *testing.T) {
	m, fixture, _, _ := seamManager(t, func(o *runtimeapi.Options) { o.Logging = logging.NewGroup("fixture", logging.DefaultConfig()) })
	fixture.FailPublicationAndRecovery()
	release, err := m.PrepareCoreStart(t.Context())
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("publication recovery failure lost")
	}
	count := int64(4)
	if _, err := m.UpdateLogging(t.Context(), runtimeapi.Operation{ID: "fenced"}, runtimeapi.LoggingUpdate{MaxFiles: &count}); err == nil {
		t.Fatal("mutation allowed after unknown startup publication")
	}
}
