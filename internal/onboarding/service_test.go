package onboarding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LeeShunEE/mihari/internal/config"
)

func TestOpen_MigratesNewAndExistingInstallations(t *testing.T) {
	tests := []struct {
		name            string
		initialRequired bool
		wantComplete    bool
	}{
		{"new installation", true, false},
		{"existing installation", false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			service, err := Open(Options{StatePath: filepath.Join(dir, "onboarding.json"), SettingsPath: filepath.Join(dir, "mihari.yaml"), Settings: testSettings(), InitialSetupRequired: test.initialRequired})
			if err != nil {
				t.Fatal(err)
			}
			if service.Status().Complete != test.wantComplete {
				t.Fatalf("status=%#v", service.Status())
			}
			if _, err := os.Stat(filepath.Join(dir, "onboarding.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOpen_ExplicitStateIsAuthoritative(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "onboarding.json")
	first, err := Open(Options{StatePath: path, SettingsPath: filepath.Join(dir, "mihari.yaml"), Settings: testSettings(), InitialSetupRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Update(Update{Complete: boolPointer(true)}); err != nil {
		t.Fatal(err)
	}
	second, err := Open(Options{StatePath: path, SettingsPath: filepath.Join(dir, "mihari.yaml"), Settings: testSettings(), InitialSetupRequired: true})
	if err != nil || !second.Status().Complete {
		t.Fatalf("status=%#v err=%v", second.Status(), err)
	}
}

func TestUpdate_ValidatesEndpointsPreservesSecretAndReportsRestart(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "mihari.yaml")
	settings := testSettings()
	if err := config.Save(settingsPath, settings); err != nil {
		t.Fatal(err)
	}
	service, err := Open(Options{StatePath: filepath.Join(dir, "onboarding.json"), SettingsPath: settingsPath, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	controller, web := "127.0.0.1:19090", "127.0.0.1:19191"
	status, err := service.Update(Update{ControllerAddr: &controller, WebAddr: &web})
	if err != nil || !status.RestartRequired {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	persisted, err := config.Load(settingsPath)
	if err != nil || persisted.ControllerSecret != settings.ControllerSecret || persisted.ControllerAddr != controller || persisted.WebAddr != web {
		t.Fatalf("settings=%#v err=%v", persisted, err)
	}
	invalid := "0.0.0.0:9090"
	if _, err := service.Update(Update{ControllerAddr: &invalid}); err == nil {
		t.Fatal("non-loopback controller was accepted")
	}
}

func TestUpdate_RollsBackSettingsWhenCompletionStateCannotCommit(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "mihari.yaml")
	statePath := filepath.Join(dir, "onboarding.json")
	settings := testSettings()
	if err := config.Save(settingsPath, settings); err != nil {
		t.Fatal(err)
	}
	service, err := Open(Options{StatePath: statePath, SettingsPath: settingsPath, Settings: settings, InitialSetupRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0o700); err != nil {
		t.Fatal(err)
	}
	web, complete := "127.0.0.1:9292", true
	if _, err := service.Update(Update{WebAddr: &web, Complete: &complete}); err == nil {
		t.Fatal("update succeeded despite an uncommittable onboarding state")
	}
	persisted, err := config.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.WebAddr != settings.WebAddr || service.Status().Complete || service.Status().RestartRequired {
		t.Fatalf("settings=%#v status=%#v", persisted, service.Status())
	}
}

func testSettings() config.Settings {
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	return settings
}

func boolPointer(value bool) *bool { return &value }
