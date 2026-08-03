package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultSettingsUseManagedPortsAndLoopback(t *testing.T) {
	settings := Defaults()
	if settings.Schema != "mihari.settings/v1" || settings.MixedAddr != "127.0.0.1:9190" || settings.ControllerAddr != "127.0.0.1:9090" || settings.WebAddr != "127.0.0.1:9191" {
		t.Fatalf("settings=%#v", settings)
	}
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsValidationRejectsUnsafeOrConflictingAddresses(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Settings)
	}{
		{"public controller", func(s *Settings) { s.ControllerAddr = "0.0.0.0:9090" }},
		{"public web", func(s *Settings) { s.WebAddr = "192.0.2.1:9191" }},
		{"duplicate port", func(s *Settings) { s.WebAddr = "127.0.0.1:9090" }},
		{"invalid mixed port", func(s *Settings) { s.MixedAddr = "127.0.0.1:70000" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := Defaults()
			test.mutate(&settings)
			if err := settings.Validate(); err == nil {
				t.Fatalf("expected invalid settings: %#v", settings)
			}
		})
	}
}

func TestLoadOrCreateSettingsIsStablePrivateAndStrict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "mihari.yaml")
	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first.ControllerSecret) != 64 {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("mode=%v", info.Mode().Perm())
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "unknown-setting:") {
		t.Fatal("unexpected fixture collision")
	}
	raw = append(raw, []byte("unknown-setting: true\n")...)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown YAML field to fail")
	}
}

func TestSaveRejectsMissingControllerSecret(t *testing.T) {
	settings := Defaults()
	if err := Save(filepath.Join(t.TempDir(), "mihari.yaml"), settings); err == nil {
		t.Fatal("expected missing controller secret to fail")
	}
}
