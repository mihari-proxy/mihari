package subscription

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func decodeProviderTestValue(t *testing.T, raw string, schema *policySchema) (policyValue, error) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	return decodePolicyValue(doc.Content[0], schema, "proxy-providers.[entry]")
}

func assertPolicyTypedLeaf(t *testing.T, value policyValue, path []string, expected string) {
	t.Helper()
	encoded, err := yaml.Marshal(value.yamlNode())
	if err != nil {
		t.Fatal(err)
	}
	assertPolicyRootLeaf(t, encoded, path, expected)
}

func TestRootPolicy_ProviderOverrideFields(t *testing.T) {
	for _, tc := range []struct{ name, positive, negative string }{
		{"tfo", "true", "1"}, {"mptcp", "false", "'false'"}, {"udp", "false", "[]"},
		{"udp-over-tcp", "true", "{}"}, {"up", "'10 Mbps'", "10"}, {"down", "''", "false"},
		{"dialer-proxy", "DIRECT", "1"}, {"skip-cert-verify", "false", "0"},
		{"name-cert-verify", "example.test", "true"}, {"interface-name", "eth0", "[]"},
		{"routing-mark", "4294967295", "9223372036854775808"}, {"ip-version", "ipv6", "6"},
		{"additional-prefix", "'../network-name/'", "[]"}, {"additional-suffix", "'-suffix'", "{}"},
		{"proxy-name", "[{pattern: '(?<=node)1', target: '$0-new'}]", "[{pattern: 1, target: text}]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := decodeProviderTestValue(t, tc.name+": "+tc.positive, providerOverrideSchema())
			if err != nil {
				t.Fatalf("positive field: %v", err)
			}
			assertPolicyTypedLeaf(t, v, []string{tc.name}, tc.positive)
			_, err = decodeProviderTestValue(t, tc.name+": "+tc.negative, providerOverrideSchema())
			field := "proxy-providers.[entry]." + tc.name
			if tc.name == "proxy-name" {
				field += "[].pattern"
			}
			assertPolicyDataFailure(t, err, field)
		})
	}
	for _, raw := range []string{"override-expr: []", "override-expr: ['tfo = true']", "proxy-name: [{pattern: x}]", "proxy-name: [{pattern: null, target: x}]", "unknown-secret: true", "tfo: true\ntfo: false"} {
		if _, err := decodeProviderTestValue(t, raw, providerOverrideSchema()); err == nil {
			t.Fatal("closed override grammar bypassed")
		}
	}
}

func TestRootPolicy_ProviderOverrideMaterializesEffectiveProtocolFields(t *testing.T) {
	for _, tc := range []struct {
		name, override, node string
		valid                bool
	}{
		{"valid replaces invalid", "routing-mark: 3", "{name: node, type: direct, routing-mark: -1}", true},
		{"invalid replaces valid", "routing-mark: -1", "{name: node, type: direct, routing-mark: 3}", false},
		{"bandwidth required supplied", "up: '10 Mbps'\ndown: '20 Mbps'", "{name: node, type: hysteria, server: 192.0.2.1, port: 443}", true},
		{"undeclared bandwidth noop", "up: invalid-bandwidth", "{name: node, type: direct}", true},
		{"unknown source remains rejected", "udp: true", "{name: node, type: direct, secret-field: ignored}", false},
		{"duplicate source remains rejected", "routing-mark: 3", "{name: node, type: direct, routing-mark: 1, routing-mark: 2}", false},
		{"null overlay leaves source", "routing-mark: null", "{name: node, type: direct, routing-mark: -1}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			override, err := decodeProviderTestValue(t, tc.override, providerOverrideSchema())
			if err != nil {
				t.Fatalf("override grammar: %v", err)
			}
			got, err := decodeProviderTestValue(t, tc.node, providerPayloadProxySchema(override, ""))
			if (err == nil) != tc.valid {
				t.Fatalf("effective protocol validity = %v", err)
			}
			if tc.name == "undeclared bandwidth noop" {
				if _, exists := got.get("up"); exists {
					t.Fatal("protocol schema broadened")
				}
			}
		})
	}
	override, err := decodeProviderTestValue(t, "dialer-proxy: ''\nadditional-prefix: ../\nproxy-name: [{pattern: node, target: renamed}]", providerOverrideSchema())
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeProviderTestValue(t, "{name: node, type: direct}", providerPayloadProxySchema(override, "parent"))
	if err != nil {
		t.Fatal(err)
	}
	name, _ := got.get("name")
	dialer, _ := got.get("dialer-proxy")
	if name.text != "node" || dialer.text != "" {
		t.Fatal("name order or dialer override precedence changed")
	}
	encoded, err := yaml.Marshal(got.yamlNode())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "renamed") {
		t.Fatal("name-only transformation applied before native filter")
	}
}
