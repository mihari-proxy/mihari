package subscription

import (
	"bytes"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestGenerateEgress_DNSCannotSilentlyBecomeProxySelection(t *testing.T) {
	for _, name := range []string{"node", "RULES", "DIRECT", "VPN&other", "VPN=value"} {
		t.Run(name, func(t *testing.T) {
			base := Document{"proxies": []any{map[string]any{"name": "node", "type": "direct"}}, "dns": map[string]any{"nameserver": []any{"https://dns.invalid/dns-query#old"}}}
			s := testSettings()
			s.EgressInterface = name
			if _, err := Generate(base, nil, s); err == nil || !strings.Contains(err.Error(), "DNS") {
				t.Fatalf("ambiguous DNS binding must fail before commit: %v", err)
			}
		})
	}
}

func TestGenerateEgress_DNSPoliciesPreserveProxyAndOptions(t *testing.T) {
	base := Document{"proxies": []any{map[string]any{"name": "node", "type": "direct"}}, "dns": map[string]any{"nameserver-policy": map[string]any{"+.example": []any{"https://dns.invalid/query#old%20VPN&h3=true", "https://dns.invalid/query#RULES", "https://dns.invalid/query#node", "https://dns.invalid/query#old&", "system://", "dhcp://eth0"}}}}
	s := testSettings()
	s.EgressInterface = "工作 VPN"
	got, err := Generate(base, nil, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"%E5%B7%A5%E4%BD%9C%20VPN&h3=true", "#RULES", "#node", "#old&", "system://", "dhcp://eth0"} {
		if !strings.Contains(string(got), text) {
			t.Fatalf("lost %q: %s", text, got)
		}
	}
}

func TestGenerateEgress_OverridesBindingsAndPreservesSource(t *testing.T) {
	base, err := ParseDocument([]byte(`interface-name: old
proxies:
  - {name: node, type: direct, interface-name: old, dialer-proxy: carrier}
  - {name: carrier, type: direct}
proxy-providers:
  remote:
    type: http
    url: https://example.invalid/proxies
    override: {interface-name: old, udp: true}
dns:
  nameserver: ["https://dns.example/dns-query#old&h3=true", "https://dns.example/dns-query#node"]
`))
	if err != nil {
		t.Fatal(err)
	}
	before, err := yaml.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	settings := testSettings()
	if err := yaml.Unmarshal([]byte("egress-interface: Ethernet\n"), &settings); err != nil {
		t.Fatal(err)
	}
	content, err := Generate(base, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	if got["interface-name"] != "Ethernet" {
		t.Fatalf("global interface=%v", got["interface-name"])
	}
	for _, raw := range got["proxies"].([]any) {
		if raw.(map[string]any)["interface-name"] != "Ethernet" {
			t.Fatalf("node binding=%v", raw)
		}
	}
	provider := got["proxy-providers"].(map[string]any)["remote"].(map[string]any)["override"].(map[string]any)
	if provider["interface-name"] != "Ethernet" || provider["udp"] != true {
		t.Fatalf("override=%v", provider)
	}
	ns := got["dns"].(map[string]any)["nameserver"].([]any)
	if ns[0] != "https://dns.example/dns-query#Ethernet&h3=true" || ns[1] != "https://dns.example/dns-query#node" {
		t.Fatalf("DNS=%v", ns)
	}
	after, err := yaml.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("source mutated")
	}
	content, err = Generate(base, nil, testSettings())
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	if got["interface-name"] != "old" {
		t.Fatal("automatic did not restore source")
	}
}

func TestGenerateEgress_DoesNotMutateCallerOverrides(t *testing.T) {
	overrides := map[string]any{"proxies": []any{map[string]any{"name": "node", "type": "direct", "interface-name": "original"}}}
	before, err := yaml.Marshal(overrides)
	if err != nil {
		t.Fatal(err)
	}
	s := testSettings()
	s.EgressInterface = "Ethernet"
	content, err := Generate(Document{}, overrides, s)
	if err != nil {
		t.Fatal(err)
	}
	var generated map[string]any
	if err := yaml.Unmarshal(content, &generated); err != nil {
		t.Fatal(err)
	}
	choices := generated["proxy-groups"].([]any)[0].(map[string]any)["proxies"].([]any)
	if len(choices) != 2 || choices[0] != "node" {
		t.Fatalf("generated group lost node: %v", choices)
	}
	after, err := yaml.Marshal(overrides)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("overrides changed: %s", after)
	}
}

func TestGenerateEgress_PreservesNativeDNSPolicies(t *testing.T) {
	for _, policy := range []string{"DIRECT", "REJECT", "REJECT-DROP", "COMPATIBLE", "PASS", "PASS-RULE", "GLOBAL", "RULES"} {
		s := testSettings()
		s.EgressInterface = "Ethernet"
		endpoint := "https://dns.invalid/query#" + policy
		base := Document{"dns": map[string]any{"nameserver": []any{endpoint}}}
		got, err := Generate(base, nil, s)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), endpoint) {
			t.Fatalf("native policy %s changed: %s", policy, got)
		}
	}
}
