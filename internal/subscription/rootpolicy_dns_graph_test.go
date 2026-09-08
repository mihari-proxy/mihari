package subscription

import (
	"context"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_WireGuardActiveDNSReferences(t *testing.T) {
	testPolicyOutboundDNSReferences(t, "wireguard")
}

func TestRootPolicy_MasqueOpenVPNActiveDNSReferences(t *testing.T) {
	for _, kind := range []string{"masque", "openvpn"} {
		t.Run(kind, func(t *testing.T) { testPolicyOutboundDNSReferences(t, kind) })
	}
}

func testPolicyOutboundDNSReferences(t *testing.T, kind string) {
	t.Helper()
	for _, scope := range []string{"root", "provider", "filtered-provider"} {
		for _, tc := range []struct {
			name, extra string
			valid       bool
		}{
			{"active missing", "remote-dns-resolve: true\ndns: ['ts://missing']", false},
			{"active wrong kind", "remote-dns-resolve: true\ndns: ['tailscale://DIRECT']", false},
			{"inactive default", "dns: ['ts://missing']", true},
			{"inactive explicit", "remote-dns-resolve: false\ndns: ['ts://missing']", true},
			{"empty list", "remote-dns-resolve: true\ndns: []", true},
			{"UDP", "remote-dns-resolve: true\ndns: [192.0.2.1]", true},
			{"fixed system", "remote-dns-resolve: true\ndns: [system]", true},
			{"adapter overrides bare selector", "remote-dns-resolve: true\ndns: ['udp://192.0.2.1#nonexistent']", true},
		} {
			t.Run(scope+"/"+tc.name, func(t *testing.T) {
				input := baselineProxyInput(t, kind, tc.extra+"\n")
				if scope != "root" {
					var original map[string]any
					if err := yaml.Unmarshal(input.YAML, &original); err != nil {
						t.Fatal(err)
					}
					payload := original["proxies"].([]any)
					definition := map[string]any{"type": "inline", "payload": payload}
					if scope == "filtered-provider" {
						definition["payload"] = append(payload, map[string]any{"name": "safe", "type": "direct"})
						definition["filter"] = "safe"
					}
					var err error
					input.YAML, err = yaml.Marshal(map[string]any{"proxy-providers": map[string]any{"p": definition}})
					if err != nil {
						t.Fatal(err)
					}
				}
				for _, method := range []string{"Build", "Inspect"} {
					t.Run(method, func(t *testing.T) {
						var err error
						if method == "Inspect" {
							_, err = NewRootConfigPolicy().Inspect(context.Background(), input)
						} else {
							_, err = NewRootConfigPolicy().Build(context.Background(), input)
						}
						if tc.valid {
							if err != nil {
								t.Fatalf("valid active/inactive outbound resolver rejected: %v", err)
							}
						} else {
							assertPolicyDataFailure(t, err, "proxies[].dns[].outbound")
						}
					})
				}
			})
		}
	}
}

func TestRootPolicy_DNSSelectorPreservesEncodedNULName(t *testing.T) {
	server := "udp://192.0.2.1#%00"
	input := dnsPolicyInput("proxies: [{name: \"\\0\", type: direct}]\ndns: {nameserver: ['" + server + "']}\n")
	out, err := NewRootConfigPolicy().Build(context.Background(), input)
	if err != nil {
		t.Fatalf("encoded selector for an allowed proxy data name rejected: %v", err)
	}
	assertPolicyProxyLeaf(t, out.YAML, []string{"name"}, `"\0"`)
	assertPolicyRootLeaf(t, out.YAML, []string{"dns", "nameserver"}, "['"+server+"']")
	parsed, err := parsePolicyNameserver(server)
	if err != nil || parsed.proxy != "\x00" {
		t.Fatal("decoded selector identity changed")
	}
	input.YAML = []byte("proxies: [{name: \"\\0\", type: direct}]\ndns: {nameserver: ['udp://192.0.2.1#unregistered=value']}\n")
	_, err = NewRootConfigPolicy().Build(context.Background(), input)
	assertPolicyDataFailure(t, err, "dns.nameserver[]")
}

func TestRootPolicy_DNSResourceReferences(t *testing.T) {
	for _, source := range []string{"ts://peer", "tailscale://peer"} {
		parsed, err := parsePolicyNameserver(source)
		if err != nil || parsed.host != "peer" {
			t.Fatalf("valid reference grammar rejected: %v", err)
		}
	}
	for _, tc := range []struct {
		name, source string
		valid        bool
	}{
		{"interface routing", "dns: {nameserver: ['192.0.2.1#test-interface']}", true},
		{"proxy routing", "dns: {nameserver: ['192.0.2.1#DIRECT']}", true},
		{"rules routing", "dns: {nameserver: ['192.0.2.1#RULES']}", true},
		{"inactive system selector", "dns: {nameserver: ['system://#unregistered']}", true},
		{"missing tailscale outbound", "dns: {nameserver: ['ts://unregistered']}", false},
		{"wrong outbound capability", "proxies: [{name: peer, type: direct}]\ndns: {nameserver: ['tailscale://peer']}", false},
		{"disabled DNS still parsed", "dns: {enable: false, nameserver: ['ts://peer']}", false},
		{"fallback tailscale", "dns: {fallback: ['ts://peer'], fallback-filter: {geoip: false}}", false},
		{"direct tailscale", "dns: {direct-nameserver: ['ts://peer']}", false},
		{"proxy bootstrap tailscale", "dns: {proxy-server-nameserver: ['ts://peer']}", false},
		{"policy tailscale", "dns: {nameserver-policy: {example.test: 'ts://peer'}}", false},
		{"proxy policy tailscale", "dns: {proxy-server-nameserver: ['192.0.2.1'], proxy-server-nameserver-policy: {example.test: ['ts://peer']}}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := rootPolicyInput()
			input.YAML = []byte(tc.source)
			_, buildErr := NewRootConfigPolicy().Build(context.Background(), input)
			_, inspectErr := NewRootConfigPolicy().Inspect(context.Background(), input)
			if (buildErr == nil) != tc.valid || (inspectErr == nil) != tc.valid {
				t.Fatalf("valid=%v build=%v inspect=%v", tc.valid, buildErr, inspectErr)
			}
		})
	}
}
