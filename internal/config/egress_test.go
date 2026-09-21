package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsEgress_RoundTripAndAutomaticOmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	for _, name := range []string{"", "Work VPN", "\u5de5\u4f5c VPN", ""} {
		s := Defaults()
		s.ControllerSecret = strings.Repeat("ab", 32)
		s.EgressInterface = name
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.EgressInterface != name {
			t.Fatalf("name changed: %q", got.EgressInterface)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "egress-interface:") != (name != "") {
			t.Fatalf("unexpected override: %s", raw)
		}
	}
}
