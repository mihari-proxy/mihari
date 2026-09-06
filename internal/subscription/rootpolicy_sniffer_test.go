package subscription

import (
	"context"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_SnifferFields(t *testing.T) {
	for _, tc := range []struct{ name, good, bad string }{
		{"enable", "true", "'true'"}, {"override-destination", "false", "null"},
		{"force-dns-mapping", "true", "0"}, {"parse-pure-ip", "false", "{}"},
		{"sniffing", "[TLS, http, QuIc]", "[ssh]"},
		{"port-whitelist", "['0', '65535', '[443]-[80]', '', '   ']", "['65536']"},
		{"force-domain", "['+.test', 'sub.*.example']", "['bad..test']"},
		{"skip-domain", "['.test', '*.example']", "['bad.test.']"},
		{"skip-src-address", "[192.0.2.1/24]", "[192.0.2.1]"},
		{"skip-dst-address", "['2001:db8::/64']", "[false]"},
		{"sniff", "{TLS: {ports: ['443'], override-destination: null}, http: {}, QUIC: {override-destination: false}}", "{SSH: {}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, value := range []string{tc.good, tc.bad} {
				out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("sniffer:\n  "+tc.name+": "+value+"\n"))
				if i == 0 {
					if err != nil {
						t.Fatalf("valid sniffer field rejected: %v", err)
					}
					expected := tc.good
					if tc.name == "sniff" {
						expected = "{TLS: {ports: ['443'], override-destination: null}, HTTP: {}, QUIC: {override-destination: false}}"
					}
					assertPolicyRootLeaf(t, out.YAML, []string{"sniffer", tc.name}, expected)
				} else {
					field := "sniffer." + tc.name
					switch tc.name {
					case "sniffing", "port-whitelist", "force-domain", "skip-domain", "skip-src-address", "skip-dst-address":
						field += "[]"
					case "sniff":
						field += ".[unknown]"
					}
					assertPolicyDataFailure(t, err, field)
				}
			}
		})
	}
}

func TestRootPolicy_SnifferPortGrammarAndPrecedence(t *testing.T) {
	for _, value := range []string{"''", "'  '", "'0'", "'65535-0'", "'[80] - [443]'", "'443'"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("sniffer: {sniff: {TLS: {ports: ["+value+"]}}}\n")); err != nil {
			t.Fatalf("valid sniff port rejected: %v", err)
		}
	}
	for _, value := range []string{"'65536'", "'1-65536'", "'-1'", "'*'", "'80/443'", "'80,443'", "'1-2-3'", "80", "null", "'18446744073709551616'"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("sniffer: {sniff: {TLS: {ports: ["+value+"]}}}\n")); err == nil {
			t.Fatal("invalid sniff port accepted")
		}
	}
	for _, body := range []string{
		"{enable: true}", "{sniff: {}, sniffing: [TLS], port-whitelist: ['443']}",
		"{sniff: {http: {}}, sniffing: [ignored-future], port-whitelist: ['ignored-range']}",
		"{sniff: {TLS: {ports: [], override-destination: null}}, override-destination: false}",
	} {
		out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("sniffer: "+body+"\n"))
		if err != nil {
			t.Fatalf("valid sniffer default/precedence rejected: %v", err)
		}
		var generated struct {
			Sniffer struct {
				Sniff map[string]struct {
					Override *bool `yaml:"override-destination"`
				} `yaml:"sniff"`
			} `yaml:"sniffer"`
		}
		if err := yaml.Unmarshal(out.YAML, &generated); err != nil {
			t.Fatal(err)
		}
		if body == "{enable: true}" && len(generated.Sniffer.Sniff) != 0 {
			t.Fatal("implicit sniffer protocols added")
		}
	}
	for _, body := range []string{
		"{sniff: {TLS: {}, tls: {}}}", "{sniff: {TLS: {override-destination: 'false'}}}",
		"{sniff: {TLS: {ports: '443'}}}", "{sniff: {TLS: {secret-field: true}}}",
		"{sniff: {}, sniffing: [ignored-future]}",
		"{sniff: {TLS: {}}, sniffing: [false]}",
		"{sniff: {TLS: {}}, port-whitelist: [80]}",
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("sniffer: "+body+"\n"))
		if err == nil {
			t.Fatal("invalid sniff schema accepted")
		}
		if strings.Contains(err.Error(), "secret-field") {
			t.Fatal("unknown field disclosed")
		}
	}
}
