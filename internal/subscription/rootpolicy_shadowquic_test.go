package subscription

import (
	"context"
	"strings"
	"testing"
)

func shadowQUICPolicyInput(extra string) PolicyInput {
	return simpleProxyInput("shadowquic", "    server: example.test\n    port: 443\n    username: fixture-user\n    password: fixture-password\n"+extra)
}

func TestRootPolicy_ShadowQUICFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"sni", "example.test", "[]"}, {"alpn", "[h3]", "[1]"}, {"quic-versions", "[v2, RFC-9000]", "[v3]"},
		{"udp-over-stream", "true", "1"}, {"zero-rtt", "true", "1"}, {"keep-alive-interval", "9223372036854", "9223372036855"},
		{"congestion-controller", "bbr", "[]"}, {"up", "'10 Mbps'", "[]"}, {"down", "'20 Mbps'", "[]"},
		{"cwnd", "1", "9223372036854775808"}, {"bbr-profile", "aggressive", "[]"},
		{"recv-window-conn", "4611686018427387903", "4611686018427387904"},
		{"recv-window", "1", "-1"}, {"disable-mtu-discovery", "true", "1"},
		{"max-datagram-frame-size", "1401", "-2"}, {"max-open-streams", "-1", "[]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), shadowQUICPolicyInput("    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid ShadowQUIC field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("ShadowQUIC field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), shadowQUICPolicyInput("    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid ShadowQUIC field accepted")
			}
		})
	}
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"    max-datagram-frame-size: -1\n", true}, {"    max-datagram-frame-size: 1\n", true},
		{"    max-datagram-frame-size: 4611686018427387903\n", true}, {"    max-datagram-frame-size: 4611686018427387904\n", false},
		{"    max-open-streams: 9223372036854775807\n    keep-alive-interval: -9223372036854775808\n", true},
		{"    congestion-controller: cubic\n    cwnd: 9223372036854775807\n", true},
		{"    congestion-controller: cubic\n    down: '1 Mbps'\n    cwnd: 7205759403792794\n", false},
		{"    congestion-controller: cubic\n    down: '1 Mbps'\n    cwnd: 7205759403792793\n", true},
		{"    up: '9223372036854775807 Bps'\n", true}, {"    up: '9223372036854775808 Bps'\n", false},
		{"    down: '18446744073709551615 Bps'\n", true},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), shadowQUICPolicyInput(tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("ShadowQUIC branch boundary mismatch: %v", err)
		}
	}
}
