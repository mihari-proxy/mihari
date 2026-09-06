package subscription

import (
	"context"
	"strings"
	"testing"
)

func hysteriaPolicyInput(extra string) PolicyInput {
	return simpleProxyInput("hysteria", "    server: example.test\n    port: 443\n    up: '10 Mbps'\n    down: '20 Mbps'\n"+extra)
}

func TestRootPolicy_HysteriaFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"ports", "'443,9000-8000,0,65535'", "'65536'"}, {"protocol", "wechat-video", "faketcp"},
		{"obfs-protocol", "udp", "faketcp"}, {"up-speed", "73786976294838", "73786976294839"},
		{"down-speed", "1", "[]"}, {"auth", "Zml4dHVyZQ==", "invalid"}, {"auth-str", "fixture-auth", "[]"},
		{"obfs", "fixture-obfs", "[]"}, {"sni", "example.test", "[]"}, {"alpn", "[hysteria]", "[1]"},
		{"skip-cert-verify", "true", "1"}, {"name-cert-verify", "example.test", "[]"}, {"fingerprint", "''", "invalid"},
		{"certificate", "''", "/tmp/cert.pem"}, {"private-key", "''", "/tmp/key.pem"},
		{"recv-window-conn", "4611686018427387903", "[]"}, {"recv-window", "4611686018427387903", "4611686018427387904"},
		{"disable-mtu-discovery", "true", "1"}, {"fast-open", "true", "1"}, {"hop-interval", "9223372036", "9223372037"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), hysteriaPolicyInput("    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid Hysteria field rejected: %v", err)
			}
			if tc.field != "obfs-protocol" && !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("Hysteria field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), hysteriaPolicyInput("    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid Hysteria field accepted")
			}
		})
	}
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"    protocol: faketcp\n    obfs-protocol: udp\n", true},
		{"    protocol: udp\n    obfs_protocol: faketcp\n", false},
		{"    protocol: wechat-video\n    ports: inactive-invalid\n    hop-interval: -1\n", true},
		{"    ports: 443-444\n    hop-interval: -1\n", false},
		{"    recv-window: 0\n    recv-window-conn: -1\n", true},
		{"    recv-window: 1\n    recv-window-conn: -1\n", false},
	} {
		out, err := NewRootConfigPolicy().Build(context.Background(), hysteriaPolicyInput(tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("Hysteria effective-field boundary mismatch: %v", err)
		}
		if err == nil && (strings.Contains(string(out.YAML), "faketcp") || strings.Contains(string(out.YAML), "obfs-protocol")) {
			t.Fatal("unsafe or precedence-altering protocol alias remained")
		}
	}
	for _, size := range []int{65535, 65536} {
		_, err := NewRootConfigPolicy().Build(context.Background(), hysteriaPolicyInput("    auth-str: "+strings.Repeat("a", size)+"\n"))
		if (err == nil) != (size == 65535) {
			t.Fatal("Hysteria auth wire length boundary wrong")
		}
	}
	input := hysteriaPolicyInput("    up-speed: 10\n")
	input.YAML = []byte(strings.Replace(string(input.YAML), "up: '10 Mbps'", "up: invalid", 1))
	if _, err := NewRootConfigPolicy().Build(context.Background(), input); err == nil {
		t.Fatal("alias bypassed required native initial speed validation")
	}
}
