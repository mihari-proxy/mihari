package subscription

import (
	"context"
	"fmt"
	"testing"
)

func TestRootPolicy_DNSNameserverGrammar(t *testing.T) {
	for _, server := range []string{
		"192.0.2.1", "2001:db8::1", "resolver.test:0", "udp://[2001:db8::1]:65535", "tcp://resolver.test", "tls://resolver.test", "quic://resolver.test",
		"http://fixture:password@resolver.test/dns-query?ignored=yes", "https://resolver.test/dns-query#h3=true&skip-cert-verify=true&name-cert-verify=certificate.test",
		"system", "system://ignored/path?ignored=yes", "dhcp://system", "rcode://success", "rcode://format_error", "rcode://server_failure", "rcode://name_error", "rcode://not_implemented", "rcode://refused",
		"tls://resolver.test#disable-reuse=true&disable-ipv4=true&disable-ipv6=FALSE&ecs=192.0.2.1&ecs-override=true",
		"udp://resolver.test#RULES", "udp://resolver.test#eth0", "udp://resolver.test#h3&disable-ipv4=true&", "https://resolver.test#h3=TRUE&h3=true",
	} {
		t.Run(server, func(t *testing.T) {
			input := dnsPolicyInput("dns:\n  nameserver: ['" + server + "']\n")
			out, err := NewRootConfigPolicy().Build(context.Background(), input)
			if err != nil {
				t.Fatalf("valid DNS nameserver rejected: %v", err)
			}
			assertPolicyRootLeaf(t, out.YAML, []string{"dns", "nameserver"}, "['"+server+"']")
		})
	}
	for _, server := range []string{"file:///tmp/resolver", "dhcp://eth0", "dhcp://system#disable-ipv4=true", "udp://resolver.test:65536", "udp://resolver.test:service", "udp://", "rcode://unknown", "udp://resolver.test#unregistered=value", "udp://resolver.test#ecs=not-an-address", "udp://resolver.test#disable-qtype-65536=true"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("dns:\n  nameserver: ['"+server+"']\n")); err == nil {
			t.Fatal("invalid or capability-changing nameserver accepted")
		}
	}
}

func TestRootPolicy_DNSFragmentNamedParameters(t *testing.T) {
	for _, tc := range []struct{ parameter, active, inactive string }{
		{"h3", "true", "TRUE"},
		{"skip-cert-verify", "true", "false"},
		{"name-cert-verify", "/private/not-a-file", ""},
		{"disable-reuse", "true", "unknown-inactive"},
		{"disable-ipv4", "true", ""},
		{"disable-ipv6", "true", "FALSE"},
		{"ecs", "192.0.2.1/24", ""},
		{"ecs-override", "true", "TRUE"},
		{"disable-qtype-65", "true", "FALSE"},
	} {
		t.Run(tc.parameter, func(t *testing.T) {
			for _, value := range []string{tc.active, tc.inactive} {
				server := "https://192.0.2.1/dns-query#" + tc.parameter + "=" + value
				out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("dns: {nameserver: ['"+server+"']}\n"))
				if err != nil {
					t.Fatalf("known active/inactive fragment data rejected: %v", err)
				}
				assertPolicyRootLeaf(t, out.YAML, []string{"dns", "nameserver"}, "['"+server+"']")
			}
			// Fragment components remain inside a typed string; they cannot turn
			// the resolver into an unknown YAML object or extend registered keys.
			for _, raw := range []string{
				"{" + tc.parameter + ": true}",
				"'https://192.0.2.1#" + tc.parameter + "-unregistered=true'",
			} {
				_, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("dns: {nameserver: ["+raw+"]}\n"))
				assertPolicyDataFailure(t, err, "dns.nameserver[]")
			}
		})
	}
}

func TestRootPolicy_DNSQuestionTypeRegistry(t *testing.T) {
	for _, code := range []int{1, 10, 12, 21, 23, 33, 35, 37, 39, 41, 53, 55, 65, 99, 102, 104, 109, 128, 249, 250, 255, 258, 260, 32768, 32769, 11, 22, 34, 38, 40, 54, 66, 103, 251, 252, 253, 254, 259, 65535} {
		valid := false
		for _, r := range [][2]int{{1, 10}, {12, 21}, {23, 33}, {35, 37}, {39, 39}, {41, 53}, {55, 65}, {99, 102}, {104, 109}, {128, 128}, {249, 250}, {255, 258}, {260, 260}, {32768, 32769}} {
			valid = valid || (code >= r[0] && code <= r[1])
		}
		input := dnsPolicyInput(fmt.Sprintf("dns:\n  nameserver: ['udp://192.0.2.1#disable-qtype-%d=true']\n", code))
		_, err := NewRootConfigPolicy().Build(context.Background(), input)
		if (err == nil) != valid {
			t.Fatalf("DNS type membership mismatch for numeric type %d: %v", code, err)
		}
	}
}

func dnsPolicyInput(value string) PolicyInput {
	input := rootPolicyInput()
	input.YAML = []byte(value)
	return input
}
