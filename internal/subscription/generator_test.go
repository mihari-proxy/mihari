package subscription

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"go.yaml.in/yaml/v3"
)

func testSettings() config.Settings {
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	return settings
}

func TestGenerateAppliesOverridesThenManagedInvariants(t *testing.T) {
	base, err := ParseDocument([]byte(`mixed-port: 1
bind-address: 0.0.0.0
allow-lan: true
external-controller: 0.0.0.0:9999
secret: fixture-only
external-ui: fixture-panel
external-ui-name: fixture-panel
external-ui-url: https://example.invalid/panel
proxies:
  - name: fixture-node
    type: ss
    server: 127.0.0.1
    port: 443
    cipher: aes-128-gcm
    password: fixture-only
    x-client-metadata:
      labels: [fixture]
`))
	if err != nil {
		t.Fatal(err)
	}
	before, err := yaml.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	content, err := Generate(base, map[string]any{
		"mode":                "global",
		"mixed-port":          2,
		"external-controller": "192.0.2.1:1234",
		"secret":              "override-only",
	}, testSettings())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	if got["mixed-port"] != 9190 || got["bind-address"] != "127.0.0.1" || got["allow-lan"] != false || got["external-controller"] != "127.0.0.1:9090" || got["secret"] != testSettings().ControllerSecret || got["mode"] != "global" {
		t.Fatalf("wrong merge result: %#v", got)
	}
	for _, field := range []string{"external-ui", "external-ui-name", "external-ui-url"} {
		if _, exists := got[field]; exists {
			t.Fatalf("%s was not removed", field)
		}
	}
	proxies := got["proxies"].([]any)
	metadata := proxies[0].(map[string]any)["x-client-metadata"].(map[string]any)
	if !reflect.DeepEqual(metadata["labels"], []any{"fixture"}) {
		t.Fatalf("metadata labels=%#v", metadata["labels"])
	}
	after, err := yaml.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("base mutated:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestGenerateMakesNodeOnlyDocumentRoutable(t *testing.T) {
	base, err := ParseDocument([]byte(`proxies:
  - {name: one, type: ss, server: 127.0.0.1, port: 443, cipher: aes-128-gcm, password: x}
`))
	if err != nil {
		t.Fatal(err)
	}
	content, err := Generate(base, nil, testSettings())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	groups, ok := got["proxy-groups"].([]any)
	if !ok || len(groups) == 0 {
		t.Fatalf("missing generated group: %#v", got)
	}
	rules, ok := got["rules"].([]any)
	if !ok || len(rules) == 0 {
		t.Fatalf("missing generated rules: %#v", got)
	}
}

func TestGenerateDoesNotMutateBase(t *testing.T) {
	base, _ := ParseDocument([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	_, err := Generate(base, nil, testSettings())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := base["secret"]; exists {
		t.Fatal("base was mutated")
	}
}

func TestGenerateTunChangesOnlyManagedEnable(t *testing.T) {
	baseYAML := []byte(`proxies: []
rules: [MATCH,DIRECT]
tun:
  enable: false
  stack: system
  device: sub-tun
  mtu: 1400
  dns-hijack: [any:53]
  auto-route: true
  route-exclude-address: [192.0.2.0/24]
  x-client-options:
    strict: true
`)
	wantNonEnable := map[string]any{
		"stack": "system", "device": "sub-tun", "mtu": 1400,
		"dns-hijack": []any{"any:53"}, "auto-route": true,
		"route-exclude-address": []any{"192.0.2.0/24"},
		"x-client-options":      map[string]any{"strict": true},
	}
	tests := []struct {
		name     string
		settings map[string]any
		want     bool
	}{
		{name: "enabled", settings: map[string]any{"enable": true, "stack": "gVisor"}, want: true},
		{name: "disabled", settings: map[string]any{"enable": false, "stack": "gVisor"}, want: false},
		{name: "unmanaged", settings: nil, want: false},
		{name: "legacy stack without enable is unmanaged", settings: map[string]any{"stack": "gVisor"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, err := ParseDocument(baseYAML)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := yaml.Marshal(base)
			settings := testSettings()
			settings.Tun = tt.settings
			content, err := Generate(base, nil, settings)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := yaml.Unmarshal(content, &got); err != nil {
				t.Fatal(err)
			}
			tun := got["tun"].(map[string]any)
			if tun["enable"] != tt.want {
				t.Fatalf("tun.enable=%#v want %v", tun["enable"], tt.want)
			}
			delete(tun, "enable")
			if !reflect.DeepEqual(tun, wantNonEnable) {
				t.Fatalf("non-enable tun=%#v want %#v", tun, wantNonEnable)
			}
			after, _ := yaml.Marshal(base)
			if !bytes.Equal(before, after) {
				t.Fatal("base TUN was mutated")
			}
		})
	}
}

func TestGenerateManagedTunPreservesOverrideFields(t *testing.T) {
	base, err := ParseDocument([]byte("proxies: []\nrules: [MATCH,DIRECT]\ntun: {enable: false, stack: system}\n"))
	if err != nil {
		t.Fatal(err)
	}
	overrideTun := map[string]any{
		"enable": false, "stack": "mixed", "device": "override-tun",
		"x-client-options": map[string]any{"labels": []any{"override"}},
	}
	overrides := map[string]any{"tun": overrideTun}
	settings := testSettings()
	settings.Tun = map[string]any{"enable": true, "stack": "gVisor"}
	content, err := Generate(base, overrides, settings)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"enable": true, "stack": "mixed", "device": "override-tun",
		"x-client-options": map[string]any{"labels": []any{"override"}},
	}
	if !reflect.DeepEqual(got["tun"], want) {
		t.Fatalf("tun=%#v want %#v", got["tun"], want)
	}
	if overrideTun["enable"] != false || overrideTun["stack"] != "mixed" {
		t.Fatalf("override TUN mutated: %#v", overrideTun)
	}
}

func TestGenerateManagedTunCreatesEnableOnlyBlockWhenMissing(t *testing.T) {
	base, err := ParseDocument([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	if err != nil {
		t.Fatal(err)
	}
	settings := testSettings()
	settings.Tun = map[string]any{"enable": true, "stack": "gVisor"}
	content, err := Generate(base, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"enable": true}; !reflect.DeepEqual(got["tun"], want) {
		t.Fatalf("tun=%#v want %#v", got["tun"], want)
	}
}

func TestGenerateManagedTunRejectsInvalidEffectiveBlock(t *testing.T) {
	tests := []struct {
		name      string
		baseYAML  string
		overrides map[string]any
	}{
		{name: "base", baseYAML: "proxies: []\nrules: [MATCH,DIRECT]\ntun: invalid\n"},
		{name: "override", baseYAML: "proxies: []\nrules: [MATCH,DIRECT]\n", overrides: map[string]any{"tun": "invalid"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, err := ParseDocument([]byte(tt.baseYAML))
			if err != nil {
				t.Fatal(err)
			}
			settings := testSettings()
			settings.Tun = map[string]any{"enable": true}
			if _, err := Generate(base, tt.overrides, settings); err == nil {
				t.Fatal("expected invalid TUN block to be rejected")
			}
		})
	}
}

func TestGenerateEmptyTunLeavesSubscriptionTun(t *testing.T) {
	base, err := ParseDocument([]byte(`proxies: []
rules: [MATCH,DIRECT]
tun:
  enable: true
  stack: system
`))
	if err != nil {
		t.Fatal(err)
	}
	settings := testSettings()
	// Tun nil / empty = unmanaged: subscription tun is left alone.
	if settings.Tun != nil {
		t.Fatalf("default Tun=%#v, want nil", settings.Tun)
	}
	content, err := Generate(base, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	tun, ok := got["tun"].(map[string]any)
	if !ok {
		t.Fatalf("subscription tun should remain: %#v", got["tun"])
	}
	if tun["enable"] != true || tun["stack"] != "system" {
		t.Fatalf("subscription tun changed: %#v", tun)
	}

	settings.Tun = map[string]any{}
	content, err = Generate(base, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	tun, ok = got["tun"].(map[string]any)
	if !ok {
		t.Fatalf("empty managed Tun should leave subscription tun: %#v", got["tun"])
	}
	if tun["enable"] != true || tun["stack"] != "system" {
		t.Fatalf("subscription tun changed with empty managed: %#v", tun)
	}
}

func TestGenerateDoesNotMutateSettingsTun(t *testing.T) {
	base, err := ParseDocument([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	if err != nil {
		t.Fatal(err)
	}
	settings := testSettings()
	settings.Tun = map[string]any{"enable": true, "stack": "gVisor"}
	content, err := Generate(base, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	tun := got["tun"].(map[string]any)
	tun["enable"] = false
	if settings.Tun["enable"] != true {
		t.Fatal("settings.Tun was mutated through generated document")
	}
}
