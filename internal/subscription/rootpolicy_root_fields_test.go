package subscription

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
)

func assertPolicyDataFailure(t *testing.T, err error, field string) {
	t.Helper()
	var failure PolicyError
	if !errors.As(err, &failure) || failure.Code != protocol.CodeDataFailure || failure.Field != field {
		t.Fatalf("expected typed data failure at %s", field)
	}
	if err.Error() != "root configuration policy: "+field {
		t.Fatal("policy error contained text outside the safe field contract")
	}
}

func assertPolicyRootLeaf(t *testing.T, output []byte, path []string, expected string) {
	t.Helper()
	var document yaml.Node
	if err := yaml.Unmarshal(output, &document); err != nil {
		t.Fatal(err)
	}
	node := document.Content[0]
	for _, key := range path {
		if key == "[]" {
			if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
				t.Fatal("fresh root sequence missing")
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
			t.Fatalf("fresh root field missing: %s", strings.Join(path, "."))
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
		t.Fatalf("fresh root value changed: %s", strings.Join(path, "."))
	}
}

func TestRootPolicy_RemainingRootDataFields(t *testing.T) {
	for _, tc := range []struct{ name, good, bad string }{
		{"authentication", "['fixture-user:fixture-password', ignored-without-colon]", "[false]"},
		{"skip-auth-prefixes", "[127.0.0.1/32]", "[127.0.0.1]"},
		{"lan-allowed-ips", "['::/0']", "['bad/prefix']"},
		{"lan-disallowed-ips", "[192.0.2.1/24]", "[123]"},
		{"geodata-mode", "true", "'true'"}, {"geodata-loader", "standard", "false"}, {"geosite-matcher", "hybrid", "[]"},
		{"experimental", "{quic-go-disable-gso: true, quic-go-disable-ecn: false, dialer-ip4p-convert: true}", "{quic-go-disable-gso: 'true'}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, raw := range []string{tc.good, tc.bad} {
				out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(tc.name+": "+raw+"\n"))
				if i == 0 {
					if err != nil {
						t.Fatalf("known root data rejected: %v", err)
					}
					assertPolicyRootLeaf(t, out.YAML, []string{tc.name}, tc.good)
				} else {
					field := tc.name
					switch tc.name {
					case "authentication", "skip-auth-prefixes", "lan-allowed-ips", "lan-disallowed-ips":
						field += "[]"
					case "experimental":
						field += ".quic-go-disable-gso"
					}
					assertPolicyDataFailure(t, err, field)
				}
			}
		})
	}
}

func TestRootPolicy_KnownNoOpsAndOwnedWriters(t *testing.T) {
	input := dnsPolicyInput("global-client-fingerprint: fixture-value\nclash-for-android: {append-system-dns: true, ui-subtitle-pattern: fixture-value}\nexperimental: {fingerprints: [fixture-value]}\ngeo-auto-update: true\ngeo-update-interval: 9223372036854775807\ngeox-url: {geoip: 'file:///private/fixture-value', mmdb: fixture-value, asn: fixture-value, geosite: fixture-value}\nprofile: {store-selected: true, store-fake-ip: true}\n")
	out, err := NewRootConfigPolicy().Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, ignored := range []string{"fixture-value", "global-client-fingerprint", "clash-for-android", "fingerprints", "geo-update-interval"} {
		if strings.Contains(string(out.YAML), ignored) {
			t.Fatal("ignored/owned input emitted")
		}
	}
	var got struct {
		Auto    bool              `yaml:"geo-auto-update"`
		Profile map[string]bool   `yaml:"profile"`
		Geo     map[string]string `yaml:"geox-url"`
	}
	if err := yaml.Unmarshal(out.YAML, &got); err != nil {
		t.Fatal(err)
	}
	if got.Auto || len(got.Profile) != 2 || got.Profile["store-selected"] || got.Profile["store-fake-ip"] || len(got.Geo) != 4 {
		t.Fatal("native writers retained")
	}
	for _, url := range got.Geo {
		if url != "" {
			t.Fatal("native download URL retained")
		}
	}
	for _, raw := range []string{
		"global-client-fingerprint: []", "clash-for-android: {append-system-dns: 1}", "clash-for-android: {ui-subtitle-pattern: false}",
		"experimental: {fingerprints: [1]}", "experimental: {quic-go-disable-ecn: []}", "experimental: {dialer-ip4p-convert: 'true'}",
		"geo-auto-update: 1", "geo-update-interval: '24'", "geox-url: {geoip: []}", "geox-url: {mmdb: false}", "geox-url: {asn: 1}", "geox-url: {geosite: null}",
		"profile: {store-selected: 'true'}", "profile: {store-fake-ip: 0}",
	} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(raw+"\n")); err == nil {
			t.Fatal("wrong type accepted in ignored known field")
		}
	}
}

func TestRootPolicy_RegisteredForbiddenRootCapabilities(t *testing.T) {
	for _, tc := range []struct{ field, value string }{
		{"port", "0"}, {"socks-port", "0"}, {"redir-port", "0"}, {"tproxy-port", "0"},
		{"ss-config", "''"}, {"vmess-config", "''"},
		{"external-controller-unix", "''"}, {"external-controller-pipe", "''"}, {"external-controller-tls", "''"},
		{"external-controller-routing-mark", "0"}, {"external-controller-cors", "{}"}, {"external-doh-server", "''"},
		{"external-ui", "''"}, {"external-ui-name", "''"}, {"external-ui-url", "''"},
		{"listeners", "[]"}, {"tunnels", "[]"}, {"tuic-server", "{}"}, {"iptables", "{}"}, {"tls", "{}"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			_, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(tc.field+": "+tc.value+"\n"))
			assertPolicyDataFailure(t, err, tc.field)
		})
	}
}

func TestRootPolicy_NTPZeroPortIsTypedNetworkData(t *testing.T) {
	if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("ntp: {port: 0, interval: -9223372036854775808}\n")); err != nil {
		t.Fatalf("native NTP zero port/nonpositive stopped interval rejected: %v", err)
	}
}

func TestRootPolicy_OwnedAndNoOpLeafFixtures(t *testing.T) {
	for _, tc := range []struct{ field, good, bad, expected string }{
		{"mixed-port", "65535", "65536", "9190"},
		{"bind-address", "0.0.0.0", "[0.0.0.0]", "127.0.0.1"},
		{"allow-lan", "true", "'true'", "false"},
		{"external-controller", "'0.0.0.0:9999'", "9999", "'127.0.0.1:9090'"},
		{"secret", "subscription-secret-must-disappear", "false", ""},
		{"global-client-fingerprint", "fixture-fingerprint", "[]", ""},
		{"geo-auto-update", "true", "1", "false"},
		{"geo-update-interval", "-9223372036854775808", "9223372036854775808", ""},
		{"profile.store-selected", "true", "'true'", "false"},
		{"profile.store-fake-ip", "true", "0", "false"},
		{"geox-url.geoip", "'file:///private/fixture-resource'", "[]", "''"},
		{"geox-url.geosite", "'https://fixture.invalid/asset'", "null", "''"},
		{"geox-url.mmdb", "fixture-resource", "1", "''"},
		{"geox-url.asn", "fixture-resource", "false", "''"},
		{"clash-for-android.append-system-dns", "true", "1", ""},
		{"clash-for-android.ui-subtitle-pattern", "fixture-pattern", "false", ""},
		{"experimental.fingerprints", "[fixture-fingerprint]", "[1]", ""},
		{"experimental.quic-go-disable-gso", "true", "'true'", "true"},
		{"experimental.quic-go-disable-ecn", "false", "[]", "false"},
		{"experimental.dialer-ip4p-convert", "true", "1", "true"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			path := strings.Split(tc.field, ".")
			wrap := func(raw string) string {
				for i := len(path) - 1; i >= 0; i-- {
					raw = "{" + path[i] + ": " + raw + "}"
				}
				return raw + "\n"
			}
			input := rootPolicyInput()
			input.YAML = []byte(wrap(tc.good))
			out, err := NewRootConfigPolicy().Build(context.Background(), input)
			if err != nil {
				t.Fatalf("known owned/no-op field rejected: %v", err)
			}
			if tc.field == "secret" {
				assertPolicyRootLeaf(t, out.YAML, path, "'"+input.Settings.ControllerSecret+"'")
			} else if tc.expected != "" {
				assertPolicyRootLeaf(t, out.YAML, path, tc.expected)
			} else {
				var document map[string]yaml.Node
				if err := yaml.Unmarshal(out.YAML, &document); err != nil {
					t.Fatal(err)
				}
				if len(path) == 1 || path[0] == "clash-for-android" {
					if _, exists := document[path[0]]; exists {
						t.Fatal("no-op parent emitted")
					}
				} else {
					parent := document[path[0]]
					for i := 0; i+1 < len(parent.Content); i += 2 {
						if parent.Content[i].Value == path[1] {
							t.Fatal("no-op child emitted")
						}
					}
				}
			}
			input.YAML = []byte(wrap(tc.bad))
			_, err = NewRootConfigPolicy().Build(context.Background(), input)
			field := tc.field
			if field == "experimental.fingerprints" {
				field += "[]"
			}
			assertPolicyDataFailure(t, err, field)
		})
	}
}

func TestRootPolicy_RootPrefixListsRejectNativeEmptyZero(t *testing.T) {
	for _, field := range []string{"skip-auth-prefixes", "lan-allowed-ips", "lan-disallowed-ips"} {
		t.Run(field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(field+": ['192.0.2.1/32', '2001:db8::1/128']\n"))
			if err != nil {
				t.Fatalf("valid IPv4/IPv6 prefix list rejected: %v", err)
			}
			assertPolicyRootLeaf(t, out.YAML, []string{field}, "['192.0.2.1/32', '2001:db8::1/128']")
			_, err = NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(field+": ['']\n"))
			assertPolicyDataFailure(t, err, field+"[]")
		})
	}
}
