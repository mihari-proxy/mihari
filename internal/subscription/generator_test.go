package subscription

import (
	"testing"

	"github.com/LeeShunEE/mihari/internal/config"
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
