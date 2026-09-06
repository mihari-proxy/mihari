package subscription

import (
	"context"
	"go.yaml.in/yaml/v3"
	"os"
	"strings"
	"testing"
)

func simpleProxyInput(typ, extra string) PolicyInput {
	input := rootPolicyInput()
	input.YAML = []byte("proxies:\n  - name: fixture-node\n    type: " + typ + "\n" + extra + "proxy-groups: []\nrules: ['MATCH,fixture-node']\n")
	return input
}

func TestRootPolicy_InternalProxyTypes(t *testing.T) {
	for _, typ := range []string{"direct", "dns", "reject", "rematch"} {
		t.Run(typ, func(t *testing.T) {
			extra := ""
			if typ == "rematch" {
				extra = "    target-rematch-name: ''\n"
			}
			out, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput(typ, extra))
			if err != nil {
				t.Fatalf("valid internal proxy rejected: %v", err)
			}
			var got struct {
				Proxies []struct{ Name, Type string } `yaml:"proxies"`
			}
			if err := yaml.Unmarshal(out.YAML, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Proxies) != 1 || got.Proxies[0].Name != "fixture-node" || got.Proxies[0].Type != typ {
				t.Fatal("internal proxy data lost")
			}
		})
	}
}

func TestRootPolicy_KnownUnsupportedProxyFamilies(t *testing.T) {
	for _, kind := range []string{"tailscale", "zerotier"} {
		t.Run(kind, func(t *testing.T) {
			_, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput(kind, ""))
			assertPolicyDataFailure(t, err, "proxies[].type")
		})
	}
}

func TestRootPolicy_BasicProxyFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"tfo", "true", "1"}, {"mptcp", "true", "1"}, {"interface-name", "en0", "[en0]"},
		{"routing-mark", "4294967295", "4294967296"}, {"ip-version", "ipv6-prefer", "[ipv6]"},
		{"dialer-proxy", "DIRECT", "[DIRECT]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("direct", "    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid basic proxy field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("basic proxy field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("direct", "    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid basic proxy field accepted")
			}
		})
	}
}

func TestRootPolicy_BasicFieldsAcrossEveryProtocol(t *testing.T) {
	for _, kind := range []string{"ss", "ssr", "socks5", "http", "vmess", "vless", "snell", "trojan", "hysteria", "hysteria2", "tuic", "shadowquic", "gost-relay", "direct", "dns", "reject", "rematch", "ssh", "mieru", "anytls", "sudoku", "masque", "trusttunnel", "openvpn", "wireguard"} {
		for _, tc := range []struct{ field, good, bad string }{
			{"tfo", "true", "1"}, {"mptcp", "true", "1"},
			{"interface-name", "fixture-interface", "[]"}, {"routing-mark", "4294967295", "4294967296"},
			{"routing-mark", "0", "-1"},
			{"ip-version", "ipv6-prefer", "[]"}, {"dialer-proxy", "DIRECT", "[]"},
			{"name", "fixture-exact-name", "null"},
		} {
			t.Run(kind+"/"+tc.field, func(t *testing.T) {
				out, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, kind, tc.field+": "+tc.good+"\n"))
				if err != nil {
					t.Fatalf("registered protocol basic field rejected: %v", err)
				}
				assertPolicyProxyLeaf(t, out.YAML, []string{tc.field}, tc.good)
				_, err = NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, kind, tc.field+": "+tc.bad+"\n"))
				assertPolicyDataFailure(t, err, "proxies[]."+tc.field)
			})
		}
	}
}

func TestRootPolicy_IPVersionCanonicalFallback(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"", "dual"}, {"native-unknown", "dual"}, {"IPV6-PREFER", "ipv6-prefer"}} {
		out, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("direct", "    ip-version: '"+tc.input+"'\n"))
		if err != nil {
			t.Fatalf("native IP preference fallback rejected: %v", err)
		}
		assertPolicyProxyLeaf(t, out.YAML, []string{"ip-version"}, tc.want)
	}
}

func TestRootPolicy_ProxyConstructorPositiveControls(t *testing.T) {
	content, err := os.ReadFile("testdata/rootpolicy/proxy-baselines.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var fixture yaml.Node
	if err := yaml.Unmarshal(content, &fixture); err != nil {
		t.Fatal(err)
	}
	var nodes []*yaml.Node
	for i := 0; i < len(fixture.Content[0].Content); i += 2 {
		if fixture.Content[0].Content[i].Value == "proxies" {
			nodes = fixture.Content[0].Content[i+1].Content
		}
	}
	if len(nodes) != 25 {
		t.Fatal("full protocol baseline census changed")
	}
	for _, node := range nodes {
		typ := ""
		name := ""
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value == "type" {
				typ = node.Content[i+1].Value
			}
			if node.Content[i].Value == "name" {
				name = node.Content[i+1].Value
			}
		}
		t.Run(typ, func(t *testing.T) {
			source := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "proxies"}, {Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{node}},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "rules"}, {Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "MATCH,DIRECT"}}},
			}}
			input := rootPolicyInput()
			input.YAML, err = yaml.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseDocument(input.YAML); err != nil {
				t.Fatalf("legacy positive control failed: %v", err)
			}
			out, err := NewRootConfigPolicy().Build(context.Background(), input)
			if err != nil {
				t.Fatalf("complete protocol baseline rejected: %v", err)
			}
			var got struct {
				Proxies []struct{ Name, Type string } `yaml:"proxies"`
			}
			if err := yaml.Unmarshal(out.YAML, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Proxies) != 1 || got.Proxies[0].Name != name || got.Proxies[0].Type != typ {
				t.Fatal("protocol discriminator/name lost")
			}
			for i := 0; i < len(node.Content); i += 2 {
				expected, err := yaml.Marshal(node.Content[i+1])
				if err != nil {
					t.Fatal(err)
				}
				assertPolicyProxyLeaf(t, out.YAML, []string{node.Content[i].Value}, string(expected))
			}
		})
	}
}
