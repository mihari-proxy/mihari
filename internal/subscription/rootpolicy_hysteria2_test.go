package subscription

import (
	"context"
	"strings"
	"testing"
)

func hysteria2PolicyInput(extra string) PolicyInput {
	return simpleProxyInput("hysteria2", "    server: example.test\n    port: 443\n"+extra)
}

func TestRootPolicy_Hysteria2ObfsUsesExactEnum(t *testing.T) {
	for _, value := range []string{"gecko", "salamander", "GECKO", "SALAMANDER"} {
		t.Run(value, func(t *testing.T) {
			_, err := NewRootConfigPolicy().Build(context.Background(), hysteria2PolicyInput("    obfs: "+value+"\n    obfs-password: fixture-password\n"))
			if (err == nil) != (value == strings.ToLower(value)) {
				t.Fatalf("exact enum consumer mismatch: %v", err)
			}
		})
	}
}

func TestRootPolicy_Hysteria2Fields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"ports", "'[65535 - 0]/443,444'", "'65536'"}, {"hop-interval", "'5-10'", "[]"},
		{"up", "'1 Mbps'", "[]"}, {"down", "'1 Gbps'", "[]"}, {"password", "fixture-password", "[]"},
		{"obfs", "''", "unknown"}, {"obfs-password", "fixture-obfs", "[]"},
		{"obfs-min-packet-size", "-1", "[]"}, {"obfs-max-packet-size", "9223372036854775807", "[]"},
		{"sni", "example.test", "[]"}, {"ech-opts", "{enable: true, query-server-name: example.test}", "[]"},
		{"skip-cert-verify", "true", "1"}, {"name-cert-verify", "example.test", "[]"}, {"fingerprint", "''", "invalid"},
		{"certificate", "''", "/tmp/cert.pem"}, {"private-key", "''", "/tmp/key.pem"}, {"alpn", "[h3]", "[1]"},
		{"cwnd", "7205759403792793", "7205759403792794"}, {"bbr-profile", "aggressive", "[]"},
		{"udp-mtu", "1197", "[]"}, {"handshake-timeout", "-9223372036", "9223372037"},
		{"initial-stream-receive-window", "4611686018427387903", "4611686018427387904"},
		{"initial-connection-receive-window", "1", "-1"},
		{"max-stream-receive-window", "18446744073709551615", "18446744073709551616"},
		{"max-connection-receive-window", "0", "-1"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), hysteria2PolicyInput("    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid Hysteria2 field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("Hysteria2 field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), hysteria2PolicyInput("    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid Hysteria2 field accepted")
			}
		})
	}
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"    obfs: salamander\n    obfs-password: fixture\n", true},
		{"    obfs: gecko\n    obfs-password: fixture\n", true},
		{"    obfs: gecko\n", false},
		{"    obfs: gecko\n    obfs-password: fixture\n    obfs-min-packet-size: 1\n    obfs-max-packet-size: 1\n", true},
		{"    obfs: gecko\n    obfs-password: fixture\n    obfs-min-packet-size: 2048\n    obfs-max-packet-size: 2048\n", true},
		{"    obfs: gecko\n    obfs-password: fixture\n    obfs-min-packet-size: 0\n    obfs-max-packet-size: 511\n", false},
		{"    obfs: gecko\n    obfs-password: fixture\n    obfs-max-packet-size: 2049\n", false},
		{"    ports: '443'\n    hop-interval: '[9223372036-5]'\n", true},
		{"    ports: '443'\n    hop-interval: '9223372037'\n", false},
		{"    ports: '*'\n    hop-interval: inactive-invalid\n", true},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), hysteria2PolicyInput(tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("Hysteria2 effective field mismatch: %v", err)
		}
	}
}

func TestRootPolicy_Hysteria2RealmFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"enable", "true", "1"}, {"token", "fixture-token", "\"line\\ninjection\""},
		{"realm-id", "'fixture/realm'", "[]"}, {"stun-servers", "['192.0.2.1:0', '2001:db8::1', '[2001:db8::1]:65535']", "['192.0.2.1:65536']"},
		{"sni", "example.test", "[]"}, {"skip-cert-verify", "true", "1"}, {"name-cert-verify", "example.test", "[]"},
		{"fingerprint", "''", "invalid"}, {"certificate", "''", "/tmp/cert.pem"}, {"private-key", "''", "/tmp/key.pem"}, {"alpn", "[h2]", "[1]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			for _, valid := range []bool{true, false} {
				value := tc.bad
				if valid {
					value = tc.good
				}
				out, err := NewRootConfigPolicy().Build(context.Background(), hysteria2PolicyInput("    realm-opts:\n      server-url: https://control.invalid/base/\n      "+tc.field+": "+value+"\n"))
				if (err == nil) != valid {
					t.Fatalf("realm field mismatch: %v", err)
				}
				if valid && !strings.Contains(string(out.YAML), tc.field+":") {
					t.Fatal("realm field lost")
				}
			}
		})
	}
	for _, extra := range []string{
		"    realm-opts: {enable: true, server-url: 'file:///tmp/control'}\n",
		"    realm-opts: {enable: true}\n",
		"    ports: '443'\n    realm-opts: {enable: true, server-url: https://control.invalid}\n",
	} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), hysteria2PolicyInput(extra)); err == nil {
			t.Fatal("invalid realm combination accepted")
		}
	}
}

func TestRootPolicy_Hysteria2MTUSanity(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"0", true}, {"13", true}, {"17", true}, {"22", true}, {"1197", true}, {"9223372036854775807", true},
		{"-1", false}, {"12", false}, {"1", false},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), hysteria2PolicyInput("    udp-mtu: "+tc.value+"\n"))
		if (err == nil) != tc.valid {
			t.Fatal("Hysteria2 weak MTU sanity boundary mismatch")
		}
	}
}
