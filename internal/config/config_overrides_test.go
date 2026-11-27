package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsRoundTripOverrideIntent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mihari.yaml")
	s := Defaults()
	s.ControllerSecret = strings.Repeat("a", 64)
	if err := Save(p, s); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	const extra = "config-overrides:\n  fragments:\n    - id: lab\n      name: Lab DNS\n      enabled: true\n      yaml: |\n        # preserve this comment\n        dns: {enable: true}\n"
	if err := os.WriteFile(p, append(raw, extra...), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(p)
	if err != nil {
		t.Fatalf("load user's YAML fragment: %v", err)
	}
	if err := Save(p, loaded.Clone()); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "# preserve this comment") {
		t.Fatal("lost fragment text")
	}
}
