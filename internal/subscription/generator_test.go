package subscription

import (
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
allow-lan: true
external-controller: 0.0.0.0:9999
secret: leaked
external-ui: unsafe
proxies: []
rules: [MATCH,DIRECT]
`))
	if err != nil {
		t.Fatal(err)
	}
	content, err := Generate(base, map[string]any{"mode": "global", "mixed-port": 2}, testSettings())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	if got["mixed-port"] != 9190 || got["allow-lan"] != false || got["external-controller"] != "127.0.0.1:9090" || got["mode"] != "global" {
		t.Fatalf("wrong merge result: %#v", got)
	}
	if _, exists := got["external-ui"]; exists {
		t.Fatal("external-ui was not removed")
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

func TestGenerateInjectsManagedTun(t *testing.T) {
	base, err := ParseDocument([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	if err != nil {
		t.Fatal(err)
	}
	settings := testSettings()
	settings.Tun = map[string]any{
		"enable": true,
		"stack":  "gVisor",
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
		t.Fatalf("tun missing or wrong type: %#v", got["tun"])
	}
	if tun["enable"] != true {
		t.Fatalf("tun.enable=%#v, want true", tun["enable"])
	}
	if tun["stack"] != "gVisor" {
		t.Fatalf("tun.stack=%#v, want gVisor", tun["stack"])
	}
}

func TestGenerateManagedTunOverridesBaseAndOverrides(t *testing.T) {
	base, err := ParseDocument([]byte(`proxies: []
rules: [MATCH,DIRECT]
tun:
  enable: false
  stack: system
  device: sub-tun
`))
	if err != nil {
		t.Fatal(err)
	}
	settings := testSettings()
	settings.Tun = map[string]any{
		"enable": true,
		"stack":  "gVisor",
	}
	content, err := Generate(base, map[string]any{
		"tun": map[string]any{"enable": false, "stack": "mixed"},
	}, settings)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	tun, ok := got["tun"].(map[string]any)
	if !ok {
		t.Fatalf("tun missing or wrong type: %#v", got["tun"])
	}
	if tun["enable"] != true || tun["stack"] != "gVisor" {
		t.Fatalf("managed tun did not win: %#v", tun)
	}
	if _, exists := tun["device"]; exists {
		t.Fatalf("subscription tun keys should not remain: %#v", tun)
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
