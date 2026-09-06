package subscription

import (
	"context"
	"strings"
	"testing"
)

func tuicPolicyInput(extra string) PolicyInput {
	return simpleProxyInput("tuic", "    server: example.test\n    port: 443\n"+extra)
}

func TestRootPolicy_TUICFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"token", "fixture-token", "[]"}, {"uuid", "arbitrary-uuid-nil-fallback", "[]"}, {"password", "fixture-password", "[]"}, {"ip", "'192.0.2.1'", "[]"},
		{"heartbeat-interval", "-9223372036854775808", "9223372036855"}, {"alpn", "[h3]", "[1]"}, {"reduce-rtt", "true", "1"}, {"request-timeout", "-9223372036854", "9223372036855"},
		{"udp-relay-mode", "quic", "[]"}, {"congestion-controller", "bbr", "[]"}, {"disable-sni", "true", "1"}, {"max-udp-relay-packet-size", "9223372036854775780", "9223372036854775781"},
		{"fast-open", "true", "1"}, {"max-open-streams", "8000000000000000000", "9223372036854775807"}, {"cwnd", "32", "[]"}, {"bbr-profile", "conservative", "[]"},
		{"skip-cert-verify", "true", "1"}, {"name-cert-verify", "example.test", "[]"}, {"fingerprint", "''", "invalid"}, {"certificate", "''", "/tmp/cert.pem"}, {"private-key", "''", "/tmp/key.pem"},
		{"recv-window-conn", "4611686018427387903", "4611686018427387904"}, {"recv-window", "0", "-1"}, {"disable-mtu-discovery", "true", "1"},
		{"max-datagram-frame-size", "1401", "27"}, {"sni", "example.test", "[]"}, {"ech-opts", "{enable: true, query-server-name: example.test}", "[]"},
		{"udp-over-stream", "true", "1"}, {"udp-over-stream-version", "2", "3"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), tuicPolicyInput("    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid TUIC field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("TUIC field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), tuicPolicyInput("    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid TUIC field accepted")
			}
		})
	}
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"    max-datagram-frame-size: 28\n", true},
		{"    max-datagram-frame-size: 27\n    udp-relay-mode: quic\n", true},
		{"    max-datagram-frame-size: 27\n    token: fixture\n", true},
		{"    max-datagram-frame-size: -1\n    udp-over-stream: true\n", true},
		{"    max-datagram-frame-size: -2\n", false},
		{"    max-datagram-frame-size: 28\n    max-udp-relay-packet-size: 9223372036854775807\n", true},
		{"    max-udp-relay-packet-size: 1\n", true},
		{"    max-open-streams: -8000000000000000000\n", true},
		{"    max-open-streams: -9223372036854775808\n", false},
		{"    congestion-controller: cubic\n    cwnd: 9223372036854775807\n", true},
		{"    congestion-controller: bbr\n    cwnd: 7205759403792794\n", false},
		{"    udp-relay-mode: NATIVE-fallback\n", true},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), tuicPolicyInput(tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("TUIC effective boundary mismatch: %v", err)
		}
	}
}
