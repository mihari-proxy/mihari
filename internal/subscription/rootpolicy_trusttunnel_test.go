package subscription

import (
	"context"
	"strings"
	"testing"
)

func trustTunnelPolicyInput(extra string) PolicyInput {
	return simpleProxyInput("trusttunnel", "    server: example.test\n    port: 443\n    username: fixture-user\n    password: fixture-password\n"+extra)
}

func TestRootPolicy_TrustTunnelFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"alpn", "[h2, h3]", "[http/1.1]"}, {"sni", "example.test", "[]"},
		{"client-fingerprint", "chrome", "[]"}, {"skip-cert-verify", "true", "1"},
		{"name-cert-verify", "example.test", "[]"}, {"fingerprint", "''", "invalid"},
		{"certificate", "''", "/tmp/client.crt"}, {"private-key", "''", "/tmp/client.key"},
		{"udp", "true", "1"}, {"health-check", "true", "1"}, {"quic", "true", "1"},
		{"congestion-controller", "bbr", "[]"}, {"cwnd", "1", "9223372036854775808"},
		{"bbr-profile", "conservative", "[]"}, {"max-connections", "-1", "[]"},
		{"min-streams", "-1", "[]"}, {"max-streams", "9223372036854775807", "[]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), trustTunnelPolicyInput("    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid TrustTunnel field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("TrustTunnel field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), trustTunnelPolicyInput("    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid TrustTunnel field accepted")
			}
		})
	}
	for _, cc := range []string{"", "cubic", "new_reno", "bbr_meta_v1", "bbr_meta_v2", "bbr", "unknown-native-noop"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), trustTunnelPolicyInput("    quic: true\n    congestion-controller: '"+cc+"'\n    cwnd: 1\n")); err != nil {
			t.Fatalf("valid QUIC controller rejected: %v", err)
		}
	}
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"    quic: true\n    alpn: [h3]\n", true}, {"    quic: true\n    alpn: [h2]\n", false},
		{"    quic: true\n    congestion-controller: bbr\n    cwnd: 7205759403792793\n", true},
		{"    quic: true\n    congestion-controller: bbr\n    cwnd: 7205759403792794\n", false},
		{"    quic: false\n    congestion-controller: bbr\n    cwnd: 9223372036854775807\n", true},
		{"    quic: true\n    congestion-controller: cubic\n    cwnd: 9223372036854775807\n", true},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), trustTunnelPolicyInput(tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("QUIC branch boundary mismatch: %v", err)
		}
	}
}
