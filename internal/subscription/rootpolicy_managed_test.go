package subscription

import (
	"context"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_ManagedTUNBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tun    map[string]any
		enable bool
		stack  string
	}{
		{"nil", nil, false, ""}, {"empty", map[string]any{}, false, ""},
		{"missing-enable", map[string]any{"stack": "gVisor"}, false, "gvisor"},
		{"off", map[string]any{"enable": false}, false, ""},
		{"on", map[string]any{"enable": true, "stack": "Mixed"}, true, "mixed"},
		{"system", map[string]any{"enable": true, "stack": "SYSTEM"}, true, "system"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := dnsPolicyInput("tun: {enable: true, stack: System, device: /private/input-device, file-descriptor: 123, auto-route: true}\n")
			input.Settings.Tun = tc.tun
			out, err := NewRootConfigPolicy().Build(context.Background(), input)
			if err != nil {
				t.Fatalf("managed TUN rejected: %v", err)
			}
			var got struct {
				Tun map[string]any `yaml:"tun"`
			}
			if err := yaml.Unmarshal(out.YAML, &got); err != nil {
				t.Fatal(err)
			}
			if got.Tun["enable"] != tc.enable {
				t.Fatal("subscription controlled TUN enable")
			}
			if tc.stack != "" && got.Tun["stack"] != tc.stack {
				t.Fatal("managed stack lost")
			}
			wantFields := 1
			if tc.stack != "" {
				wantFields++
			}
			if len(got.Tun) != wantFields || strings.Contains(string(out.YAML), "input-device") {
				t.Fatal("unmanaged TUN capability emitted")
			}
		})
	}
	for _, tun := range []map[string]any{
		{"enable": "true"}, {"enable": nil}, {"stack": ""}, {"stack": "unknown"}, {"stack": 1},
		{"file-descriptor": 123}, {"private-secret-key": "private-secret-value"},
	} {
		input := rootPolicyInput()
		input.Settings.Tun = tun
		_, err := NewRootConfigPolicy().Build(context.Background(), input)
		if err == nil {
			t.Fatal("unknown or malformed managed TUN field accepted")
		}
		if strings.Contains(err.Error(), "private-secret") {
			t.Fatal("settings TUN error leaked input")
		}
	}
}

func TestRootPolicy_SubscriptionTUNDeclaredFieldsAreDiscarded(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"enable", "true"}, {"device", "/private/input-device"}, {"stack", "gVisor"},
		{"dns-hijack", "['any:53']"}, {"auto-route", "true"}, {"auto-detect-interface", "true"},
		{"mtu", "4294967295"}, {"gso", "true"}, {"gso-max-size", "4294967295"},
		{"inet6-address", "['fd00::1/64']"}, {"iproute2-table-index", "123"}, {"iproute2-rule-index", "456"},
		{"auto-redirect", "true"}, {"auto-redirect-input-mark", "4294967295"}, {"auto-redirect-output-mark", "1"},
		{"auto-redirect-iproute2-fallback-rule-index", "123"}, {"loopback-address", "[192.0.2.1]"}, {"strict-route", "true"},
		{"route-address", "[192.0.2.0/24]"}, {"route-address-set", "[source]"}, {"route-exclude-address", "['2001:db8::/64']"}, {"route-exclude-address-set", "[source]"},
		{"include-interface", "[eth0]"}, {"exclude-interface", "[lo]"}, {"include-uid", "[4294967295]"}, {"include-uid-range", "['1:100']"}, {"exclude-uid", "[0]"}, {"exclude-uid-range", "['100:200']"},
		{"exclude-src-port", "[65535]"}, {"exclude-src-port-range", "['1:65535']"}, {"exclude-dst-port", "[0]"}, {"exclude-dst-port-range", "['1:65535']"},
		{"include-android-user", "[-1]"}, {"include-package", "[test.package]"}, {"exclude-package", "[test.package]"},
		{"include-mac-address", "['00:11:22:33:44:55']"}, {"exclude-mac-address", "['00:11:22:33:44:55']"},
		{"endpoint-independent-nat", "true"}, {"udp-timeout", "9223372036854775807"}, {"icmp-timeout", "-9223372036854775808"},
		{"disable-icmp-forwarding", "true"}, {"file-descriptor", "9223372036854775807"},
		{"inet4-route-address", "[192.0.2.0/24]"}, {"inet6-route-address", "['fd00::/64']"},
		{"inet4-route-exclude-address", "[192.0.2.0/24]"}, {"inet6-route-exclude-address", "['fd00::/64']"},
		{"recvmsgx", "true"}, {"sendmsgx", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("tun: {"+tc.name+": "+tc.value+"}\n"))
			if err != nil {
				t.Fatalf("known discarded TUN field rejected: %v", err)
			}
			var got struct {
				Tun map[string]bool `yaml:"tun"`
			}
			if err := yaml.Unmarshal(out.YAML, &got); err != nil {
				t.Fatal(err)
			}
			enable, exists := got.Tun["enable"]
			if len(got.Tun) != 1 || !exists || enable {
				t.Fatal("subscription TUN field escaped")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("tun: {"+tc.name+": {invalid: value}}\n")); err == nil {
				t.Fatal("malformed ignored field accepted")
			}
		})
	}
}

func TestRootPolicy_DiscardedTUNRepresentations(t *testing.T) {
	type domain struct {
		fields, good, bad string
	}
	for _, tc := range []domain{
		{"mtu gso-max-size auto-redirect-input-mark auto-redirect-output-mark", "4294967295", "4294967296"},
		{"mtu gso-max-size auto-redirect-input-mark auto-redirect-output-mark", "0", "-1"},
		{"iproute2-table-index iproute2-rule-index auto-redirect-iproute2-fallback-rule-index udp-timeout icmp-timeout file-descriptor", "9223372036854775807", "9223372036854775808"},
		{"iproute2-table-index iproute2-rule-index auto-redirect-iproute2-fallback-rule-index udp-timeout icmp-timeout file-descriptor", "-9223372036854775808", "-9223372036854775809"},
		{"include-uid exclude-uid", "[4294967295]", "[4294967296]"},
		{"include-uid exclude-uid", "[0]", "[-1]"},
		{"exclude-src-port exclude-dst-port", "[65535]", "[65536]"},
		{"exclude-src-port exclude-dst-port", "[0]", "[-1]"},
		{"include-android-user", "[9223372036854775807]", "[9223372036854775808]"},
		{"include-android-user", "[-9223372036854775808]", "[-9223372036854775809]"},
		{"inet6-address route-address route-exclude-address inet4-route-address inet6-route-address inet4-route-exclude-address inet6-route-exclude-address", "['2001:db8::1/128']", "['2001:db8::1/129']"},
		{"inet6-address route-address route-exclude-address inet4-route-address inet6-route-address inet4-route-exclude-address inet6-route-exclude-address", "['192.0.2.1/32']", "['']"},
		{"loopback-address", "['::1']", "['::1/128']"},
		{"loopback-address", "['192.0.2.1']", "['']"},
		{"dns-hijack route-address-set route-exclude-address-set include-interface exclude-interface include-uid-range exclude-uid-range exclude-src-port-range exclude-dst-port-range include-package exclude-package include-mac-address exclude-mac-address", "['']", "[1]"},
		{"device", "''", "1"},
		{"stack", "MiXeD", "''"},
		{"stack", "System", "unregistered-stack"},
		{"enable auto-route auto-detect-interface gso auto-redirect strict-route endpoint-independent-nat disable-icmp-forwarding recvmsgx sendmsgx", "false", "'false'"},
	} {
		for _, field := range strings.Fields(tc.fields) {
			t.Run(field+"/"+tc.good, func(t *testing.T) {
				out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("tun: {"+field+": "+tc.good+"}\n"))
				if err != nil {
					t.Fatalf("valid discarded representation rejected: %v", err)
				}
				var got struct {
					Tun map[string]bool `yaml:"tun"`
				}
				if err := yaml.Unmarshal(out.YAML, &got); err != nil {
					t.Fatal(err)
				}
				enable, exists := got.Tun["enable"]
				if len(got.Tun) != 1 || !exists || enable {
					t.Fatal("discarded representation affected managed output")
				}
				_, err = NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("tun: {"+field+": "+tc.bad+"}\n"))
				if err == nil || !strings.Contains(err.Error(), "tun."+field) {
					t.Fatalf("invalid representation did not identify its safe field: %v", err)
				}
			})
		}
	}
}
