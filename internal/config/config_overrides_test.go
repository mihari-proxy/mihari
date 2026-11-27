package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
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
	const extra = "config-overrides:\n  fragments:\n    - id: lab\n      name: Lab DNS\n      enabled: true\n      yaml: |\n        # preserve this comment\n        dns: {enable: true}\n    - id: personal\n      name: Personal\n      enabled: false\n      yaml: 'dns: {ipv6: true}'\n"
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
	restored, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []protocol.OverrideFragment{{ID: "lab", Name: "Lab DNS", Enabled: true, YAML: "# preserve this comment\ndns: {enable: true}\n"}, {ID: "personal", Name: "Personal", Enabled: false, YAML: "dns: {ipv6: true}"}}
	if !reflect.DeepEqual(restored.EffectiveOverrides().Fragments, want) {
		t.Fatalf("fragment order/metadata/text changed: %#v", restored.EffectiveOverrides().Fragments)
	}
	if !strings.Contains(string(saved), "# preserve this comment") {
		t.Fatal("lost fragment text")
	}
}

func TestSettingsValidateExplicitBootstrapValues(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		servers  []string
		valid    bool
	}{
		{"set invalid hostname", "set", []string{"dns.example"}, false},
		{"set empty", "set", nil, false},
		{"append invalid hostname", "append", []string{"dns.example"}, false},
		{"remove inherited invalid hostname", "remove", []string{"dns.example"}, true},
		{"append empty", "append", nil, true},
		{"use native system URI", "set", []string{"system://resolver.example"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Defaults()
			s.ControllerSecret = strings.Repeat("a", 64)
			s.SetOverrides(protocol.ConfigOverrides{DNS: map[string]protocol.DNSFieldOverride{"default-nameserver": {Operation: tc.op, Servers: tc.servers}}})
			if err := Save(filepath.Join(t.TempDir(), "mihari.yaml"), s); (err == nil) != tc.valid {
				t.Fatalf("save valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
