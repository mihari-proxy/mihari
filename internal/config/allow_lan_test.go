package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettings_NonLoopbackMixedRequiresAllowLAN(t *testing.T) {
	for _, address := range []string{"0.0.0.0:9190", "[::]:9190", "192.0.2.1:9190"} {
		t.Run(address, func(t *testing.T) {
			settings := Defaults()
			settings.MixedAddr = address
			err := settings.Validate()
			if err == nil || !strings.Contains(err.Error(), "allow-lan") {
				t.Fatalf("expected actionable LAN validation error, got %v", err)
			}
		})
	}
}

func TestSettings_AllowLANPersistenceAndValidation(t *testing.T) {
	for _, address := range []string{"127.0.0.1:9190", "0.0.0.0:9190", "[::]:9190", "192.0.2.1:9190"} {
		t.Run(address, func(t *testing.T) {
			settings := Defaults()
			settings.ControllerSecret = strings.Repeat("a", 64)
			settings.MixedAddr, settings.AllowLAN = address, true
			path := filepath.Join(t.TempDir(), "settings.yaml")
			if err := Save(path, settings); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path)
			if err != nil || !loaded.AllowLAN || loaded.MixedAddr != address {
				t.Fatalf("loaded=%+v err=%v", loaded, err)
			}
			loaded.AllowLAN, loaded.MixedAddr = false, "127.0.0.1:9190"
			if err := Save(path, loaded); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil || strings.Contains(string(raw), "allow-lan:") {
				t.Fatalf("default field not omitted: %s %v", raw, err)
			}
		})
	}
}
