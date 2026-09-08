package subscription

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func baselineProxyInput(t *testing.T, kind, extra string) PolicyInput {
	t.Helper()
	content, err := os.ReadFile("testdata/rootpolicy/proxy-baselines.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Proxies []yaml.Node `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(content, &source); err != nil {
		t.Fatal(err)
	}
	var selected *yaml.Node
	for i := range source.Proxies {
		node := &source.Proxies[i]
		for j := 0; j < len(node.Content); j += 2 {
			if node.Content[j].Value == "type" && node.Content[j+1].Value == kind {
				selected = node
			}
		}
	}
	if selected == nil {
		t.Fatal("missing protocol fixture")
	}
	if extra != "" {
		var changes yaml.Node
		if err := yaml.Unmarshal([]byte(extra), &changes); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(changes.Content[0].Content); i += 2 {
			key, value := changes.Content[0].Content[i], changes.Content[0].Content[i+1]
			found := false
			for j := 0; j < len(selected.Content); j += 2 {
				if selected.Content[j].Value == key.Value {
					selected.Content[j+1] = value
					found = true
					break
				}
			}
			if !found {
				selected.Content = append(selected.Content, key, value)
			}
		}
	}
	encoded, err := yaml.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	input := rootPolicyInput()
	input.YAML = []byte("proxies:\n  - " + strings.ReplaceAll(strings.TrimSuffix(string(encoded), "\n"), "\n", "\n    ") + "\nrules: ['MATCH,DIRECT']\n")
	return input
}

func TestRootPolicy_MasqueFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"ip", "10.0.0.2/24", "malformed"}, {"ipv6", "'2001:db8::2/64'", "malformed"},
		{"uri", "https://example.test/connect", "'{host}'"}, {"sni", "example.test", "[]"},
		{"mtu", "4294967295", "4294967296"}, {"udp", "true", "1"},
		{"handshake-timeout", "9223372036", "9223372037"}, {"skip-cert-verify", "true", "1"},
		{"name-cert-verify", "unused-name", "[]"}, {"network", "h2", "[]"},
		{"congestion-controller", "bbr", "[]"}, {"cwnd", "32", "[]"}, {"bbr-profile", "aggressive", "[]"},
		{"ip-stack", "{mode: MIPS, congestion-controller: BBR3}", "{mode: kernel}"},
		{"remote-dns-resolve", "true", "1"}, {"dns", "[tls://1.1.1.1, 'https://example.test/dns-query']", "[file:///tmp/resolv]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "masque", tc.field+": "+tc.good))
			if err != nil {
				t.Fatalf("valid MASQUE field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("MASQUE field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "masque", tc.field+": "+tc.bad)); err == nil {
				t.Fatal("invalid MASQUE field accepted")
			}
		})
	}
	for _, field := range []string{"private-key", "public-key"} {
		for _, bad := range []string{"[]", "'/tmp/key'", "'AAAA'"} {
			if _, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "masque", field+": "+bad)); err == nil {
				t.Fatal("invalid inline MASQUE key accepted")
			}
		}
	}
}

func TestRootPolicy_MasqueBranches(t *testing.T) {
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"network: h3-l4proxy\nip: malformed\nmtu: -1\nuri: '{unused}'", true},
		{"ip: ''\nipv6: ''", false}, {"handshake-timeout: -1", false},
		{"ip: '2001:db8::2/64'", true},
		{"ip-stack: {mode: mips}\nmtu: 68", true}, {"ip-stack: {mode: mips}\nmtu: 67", false},
		{"ip-stack: {mode: mips}\nmtu: 65535", true}, {"ip-stack: {mode: mips}\nmtu: 65536", false},
		{"ip-stack: {mode: mips}\nipv6: '2001:db8::2'\nmtu: 1279", false},
		{"ip-stack: {mode: mips}\nip: 10.0.0.255/24", false},
		{"ip-stack: {mode: mips}\nip: 10.0.0.255/32", true},
		{"ip-stack: {mode: mips}\nip: 224.0.0.1", false},
		{"ip-stack: {mode: mips}\nip: 0.0.0.0", false},
		{"ip-stack: {mode: gvisor, congestion-controller: unknown}", false},
		{"network: h2\ncongestion-controller: bbr\ncwnd: 9223372036854775807", true},
		{fmt.Sprintf("congestion-controller: bbr\ncwnd: %d", int64(math.MaxInt64/1242)), true},
		{fmt.Sprintf("congestion-controller: bbr\ncwnd: %d", int64(math.MaxInt64/1242+1)), false},
		{fmt.Sprintf("congestion-controller: bbr_meta_v1\ncwnd: %d", int64(math.MaxInt64/1280)), true},
		{fmt.Sprintf("congestion-controller: bbr_meta_v1\ncwnd: %d", int64(math.MaxInt64/1280+1)), false},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "masque", tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("MASQUE branch mismatch: %v", err)
		}
	}
}
