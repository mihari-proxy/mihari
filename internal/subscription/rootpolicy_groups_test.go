package subscription

import (
	"context"
	"go.yaml.in/yaml/v3"
	"testing"
)

func groupPolicyInput(kind, extra string) PolicyInput {
	v := rootPolicyInput()
	v.YAML = []byte("proxies: []\nproxy-groups:\n  - name: choice\n    type: " + kind + "\n    proxies: [DIRECT]\n" + extra + "rules: ['MATCH,choice']\n")
	return v
}

func TestRootPolicy_GroupFields(t *testing.T) {
	for _, tc := range []struct{ kind, field, good, bad string }{
		{"select", "default-selected", "DIRECT", "[DIRECT]"},
		{"url-test", "tolerance", "65535", "65536"},
		{"load-balance", "strategy", "sticky-sessions", "random"},
		{"fallback", "url", "https://example.test/check", "file:///private/secret-value"},
		{"select", "interval", "300", "9223372037"},
		{"select", "timeout", "5000", "9223372036855"},
		{"select", "max-failed-times", "5", "-1"},
		{"select", "empty-fallback", "DIRECT", "[DIRECT]"},
		{"select", "lazy", "false", "'false'"},
		{"select", "disable-udp", "true", "1"},
		{"select", "filter", "'(?<=region)A'", "[bad]"},
		{"select", "exclude-filter", "'(?<!fast)slow'", "[bad]"},
		{"select", "exclude-type", "'ss|vmess'", "[bad]"},
		{"select", "expected-status", "'204,200-299/404'", "'65536'"},
		{"select", "include-all", "true", "1"},
		{"select", "include-all-proxies", "true", "1"},
		{"select", "include-all-providers", "true", "1"},
		{"select", "hidden", "true", "1"},
		{"select", "icon", "'https://example.test/icon.svg'", "[bad]"},
	} {
		t.Run(tc.kind+"/"+tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), groupPolicyInput(tc.kind, "    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("positive group field rejected: %v", err)
			}
			assertPolicyRootLeaf(t, out.YAML, []string{"proxy-groups", "[]", tc.field}, tc.good)
			_, err = NewRootConfigPolicy().Build(context.Background(), groupPolicyInput(tc.kind, "    "+tc.field+": "+tc.bad+"\n"))
			field := "proxy-groups[]." + tc.field
			if tc.field == "strategy" {
				field = "proxy-groups[]" // Existing object-level strategy contract.
			}
			assertPolicyDataFailure(t, err, field)
		})
	}
}

func TestRootPolicy_GroupKnownInactiveFields(t *testing.T) {
	for _, tc := range []struct{ kind, field, good, bad string }{
		{"fallback", "default-selected", "missing", "[]"},
		{"select", "tolerance", "65535", "65536"},
		{"select", "strategy", "native-unused-text", "{}"},
		{"select", "dialer-proxy", "missing", "[]"},
		{"select", "interface-name", "unused-interface", "{}"},
		{"select", "routing-mark", "-1", "'1'"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), groupPolicyInput(tc.kind, "    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("known inactive field rejected: %v", err)
			}
			var decoded struct {
				Groups []map[string]yaml.Node `yaml:"proxy-groups"`
			}
			if err := yaml.Unmarshal(out.YAML, &decoded); err != nil {
				t.Fatal(err)
			}
			if _, exists := decoded.Groups[0][tc.field]; exists {
				t.Fatal("inactive field emitted as effective setting")
			}
			_, err = NewRootConfigPolicy().Build(context.Background(), groupPolicyInput(tc.kind, "    "+tc.field+": "+tc.bad+"\n"))
			assertPolicyDataFailure(t, err, "proxy-groups[]."+tc.field)
		})
	}
	input := rootPolicyInput()
	input.YAML = []byte("proxy-groups: [{Name: g, type: select, proxies: [DIRECT]}]")
	if _, err := NewRootConfigPolicy().Build(context.Background(), input); err == nil {
		t.Fatal("native literal name precheck bypassed")
	}
	input.YAML = []byte("proxy-groups: [{name: g, TYPE: select, Proxies: [DIRECT]}]")
	if _, err := NewRootConfigPolicy().Build(context.Background(), input); err != nil {
		t.Fatalf("ordinary native field aliases rejected: %v", err)
	}
}
