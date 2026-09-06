package subscription

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func assertPolicyProxyLeaf(t *testing.T, output []byte, path []string, expected string) {
	t.Helper()
	var document struct {
		Proxies []yaml.Node `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(output, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Proxies) != 1 {
		t.Fatal("fixture proxy missing")
	}
	node := &document.Proxies[0]
	for _, key := range path {
		if key == "[]" {
			if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
				t.Fatal("fixture sequence missing")
			}
			node = node.Content[0]
			continue
		}
		var next *yaml.Node
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				next = node.Content[i+1]
				break
			}
		}
		if next == nil {
			t.Fatalf("fresh field missing: %s", strings.Join(path, "."))
		}
		node = next
	}
	var got, want any
	if err := node.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh typed field changed: %s", strings.Join(path, "."))
	}
}

func TestRootPolicy_SharedCarrierLeafFixtures(t *testing.T) {
	for _, tc := range []struct{ block, network, field, good, bad string }{
		{"ws-opts", "ws", "path", "'/carrier?ed=128'", "'%invalid'"},
		{"ws-opts", "ws", "headers", "{X-Fixture: value}", "{X-Fixture: \"bad\\nvalue\"}"},
		{"ws-opts", "ws", "max-early-data", "9223372036854775807", "9223372036854775808"},
		{"ws-opts", "ws", "early-data-header-name", "X-Early", "'bad header'"},
		{"ws-opts", "ws", "v2ray-http-upgrade", "true", "1"},
		{"ws-opts", "ws", "v2ray-http-upgrade-fast-open", "true", "1"},
		{"grpc-opts", "grpc", "grpc-service-name", "'/service/Tun'", "[]"},
		{"grpc-opts", "grpc", "grpc-user-agent", "fixture-agent", "\"bad\\nagent\""},
		{"grpc-opts", "grpc", "ping-interval", "9223372036", "9223372037"},
		{"grpc-opts", "grpc", "max-connections", "-1", "9223372036854775808"},
		{"grpc-opts", "grpc", "min-streams", "-1", "9223372036854775808"},
		{"grpc-opts", "grpc", "max-streams", "9223372036854775807", "9223372036854775808"},
	} {
		for _, kind := range []string{"trojan", "vmess", "vless"} {
			t.Run(kind+"/"+tc.block+"/"+tc.field, func(t *testing.T) {
				prefix := "network: " + tc.network + "\n"
				if kind != "trojan" {
					prefix += "tls: true\n"
				}
				for index, value := range []string{tc.good, tc.bad} {
					options := tc.field + ": " + value
					if tc.field == "early-data-header-name" {
						options += ", max-early-data: 1"
					}
					if tc.field == "v2ray-http-upgrade-fast-open" {
						options += ", v2ray-http-upgrade: true"
					}
					input := baselineProxyInput(t, kind, prefix+tc.block+": {"+options+"}\n")
					out, err := NewRootConfigPolicy().Build(context.Background(), input)
					if index == 0 {
						if err != nil {
							t.Fatalf("valid active carrier leaf rejected: %v", err)
						}
						assertPolicyProxyLeaf(t, out.YAML, []string{tc.block, tc.field}, tc.good)
					} else {
						field := "proxies[]." + tc.block + "." + tc.field
						if tc.field == "headers" {
							field += ".[entry]"
						}
						assertPolicyDataFailure(t, err, field)
					}
				}
			})
		}
	}
}

func TestRootPolicy_MKCPLeafFixtures(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"mtu", "4294967295", "4294967296"}, {"tti", "1000", "1001"},
		{"uplink-capacity", "4095", "4096"}, {"downlink-capacity", "4095", "4096"},
		{"congestion", "true", "1"}, {"write-buffer", "4294967295", "4294967296"},
		{"read-buffer", "4294967295", "4294967296"}, {"seed", "fixture-seed", "[]"},
		{"header", "wireguard", "[]"},
	} {
		for _, block := range []string{"mkcp-opts", "mekya-opts"} {
			t.Run(block+"/"+tc.field, func(t *testing.T) {
				for index, value := range []string{tc.good, tc.bad} {
					path := []string{block, tc.field}
					extra := "network: mkcp\nmkcp-opts: {" + tc.field + ": " + value + "}\n"
					if block == "mekya-opts" {
						extra = "network: mekya\nmekya-opts: {url: 'https://example.test/carrier', kcp: {" + tc.field + ": " + value + "}}\n"
						path = []string{block, "kcp", tc.field}
					}
					out, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "vmess", extra))
					if index == 0 {
						if err != nil {
							t.Fatalf("valid MKCP leaf rejected: %v", err)
						}
						assertPolicyProxyLeaf(t, out.YAML, path, tc.good)
					} else {
						var failure PolicyError
						if !errors.As(err, &failure) || failure.Field != "proxies[]."+strings.Join(path, ".") {
							t.Fatalf("wrong MKCP leaf failure: %v", err)
						}
					}
				}
			})
		}
	}
}

func TestRootPolicy_MekyaLeafFixtures(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"url", "'https://example.test/carrier'", "'file:///private/secret-value'"},
		{"h2-pool-size", "3", "576460752303423488"},
		{"max-write-delay", "-9223372036854", "-9223372036855"},
		{"polling-interval-initial", "9223372036854", "9223372036855"},
		{"max-request-size", "1048576", "9223372036854775808"},
		{"max-write-size", "-1", "9223372036854775808"},
		{"max-write-duration-ms", "-1", "9223372036854775808"},
		{"max-simultaneous-write-connection", "-1", "9223372036854775808"},
		{"packet-writing-buffer", "-1", "9223372036854775808"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			for index, value := range []string{tc.good, tc.bad} {
				extra := "network: mekya\nmekya-opts: {" + tc.field + ": " + value + "}\n"
				out, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "vmess", extra))
				if index == 0 {
					if err != nil {
						t.Fatalf("valid Mekya leaf rejected: %v", err)
					}
					assertPolicyProxyLeaf(t, out.YAML, []string{"mekya-opts", tc.field}, tc.good)
				} else {
					var failure PolicyError
					if !errors.As(err, &failure) || failure.Field != "proxies[].mekya-opts."+tc.field {
						t.Fatalf("wrong Mekya leaf failure: %v", err)
					}
					if strings.Contains(err.Error(), "secret-value") {
						t.Fatal("transport URL leaked")
					}
				}
			}
		})
	}
}
