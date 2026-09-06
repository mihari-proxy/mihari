package subscription

import (
	"context"
	"strings"
	"testing"
)

func TestRootPolicy_UniversalMuxFields(t *testing.T) {
	// The parser applies this same wrapper after every outbound constructor.
	// Exercise each compiled binding without creating native mux sessions.
	for _, kind := range []string{"ss", "ssr", "socks5", "http", "vmess", "vless", "snell", "trojan", "hysteria", "hysteria2", "tuic", "shadowquic", "gost-relay", "direct", "dns", "reject", "rematch", "ssh", "mieru", "anytls", "sudoku", "masque", "trusttunnel", "openvpn", "wireguard"} {
		for _, tc := range []struct{ field, good, bad string }{
			{"enabled", "true", "1"}, {"protocol", "yamux", "SMUX"},
			{"max-connections", "-9223372036854775808", "-9223372036854775809"},
			{"min-streams", "-1", "9223372036854775808"},
			{"max-streams", "9223372036854775807", "9223372036854775808"},
			{"padding", "true", "1"}, {"statistic", "true", "1"}, {"only-tcp", "false", "'false'"},
			{"brutal-opts", "{}", "false"},
			{"brutal-opts.enabled", "true", "1"},
			{"brutal-opts.up", "'18446744073709551615 Bps'", "'18446744073709551616 Bps'"},
			{"brutal-opts.down", "'18446744 TBps'", "'18446745 TBps'"},
		} {
			t.Run(kind+"/"+tc.field, func(t *testing.T) {
				for index, value := range []string{tc.good, tc.bad} {
					path := strings.Split(tc.field, ".")
					member := tc.field + ": " + value
					if len(path) == 2 {
						member = path[0] + ": {" + path[1] + ": " + value + "}"
					}
					if tc.field != "enabled" {
						member = "enabled: true, " + member
					}
					out, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, kind, "smux: {"+member+"}\n"))
					if index == 0 {
						if err != nil {
							t.Fatalf("valid mux field rejected: %v", err)
						}
						assertPolicyProxyLeaf(t, out.YAML, append([]string{"smux"}, path...), tc.good)
					} else {
						assertPolicyDataFailure(t, err, "proxies[].smux."+tc.field)
					}
				}
			})
		}
	}
	for _, protocol := range []string{"", "h2mux", "smux", "yamux"} {
		out, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("direct", "    smux: {enabled: true, protocol: '"+protocol+"'}\n"))
		if err != nil {
			t.Fatalf("native mux protocol rejected: %v", err)
		}
		assertPolicyProxyLeaf(t, out.YAML, []string{"smux", "protocol"}, "'"+protocol+"'")
	}
	out, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("direct", "    smux: {enabled: false, protocol: inactive-unknown}\n"))
	if err != nil {
		t.Fatalf("inactive mux data rejected: %v", err)
	}
	assertPolicyProxyLeaf(t, out.YAML, []string{"smux", "protocol"}, "inactive-unknown")
}

func TestRootPolicy_RateStringBounds(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"10", true}, {"10 Mbps", true}, {"10 MBps", true}, {"10 Kbps", true}, {"10 Gbps", true}, {"10 TBps", true},
		{"0", true}, {"", true}, {"-1", true}, {"native-invalid-noop", true}, {"+10", true},
		{"18446744 TBps", true}, {"18446745 TBps", false}, {"18446744073709551615 Bps", true},
		{"18446744073709551616 Bps", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			extra := "    smux: {enabled: true, brutal-opts: {enabled: true, up: '" + tc.value + "', down: '1 Mbps'}}\n"
			_, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("direct", extra))
			if (err == nil) != tc.valid {
				t.Fatalf("rate overflow boundary mismatch: %v", err)
			}
		})
	}
}
