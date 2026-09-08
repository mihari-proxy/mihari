package subscription

import (
	"context"
	"strings"
	"testing"
)

func trojanPolicyInput(extra string) PolicyInput {
	return simpleProxyInput("trojan", "    server: example.test\n    port: 443\n    password: fixture-password\n"+extra)
}

func TestRootPolicy_TrojanFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"alpn", "[h2]", "[1]"}, {"sni", "example.test", "[]"}, {"skip-cert-verify", "true", "1"},
		{"name-cert-verify", "example.test", "[]"}, {"fingerprint", "''", "invalid"}, {"certificate", "''", "/tmp/cert.pem"}, {"private-key", "''", "/tmp/key.pem"},
		{"udp", "true", "1"}, {"network", "grpc", "[]"}, {"client-fingerprint", "chrome", "[]"},
		{"ech-opts", "{enable: true, query-server-name: example.test}", "{enable: true, config: invalid}"},
		{"shadow-tls-opts", "{password: fixture, version: 3}", "{version: 4}"},
		{"restls-opts", "{password: fixture, version-hint: tls13}", "{password: fixture, version-hint: invalid}"},
		{"jls-opts", "{username: fixture, password: fixture}", "{username: fixture, password: ''}"},
		{"reality-opts", "{public-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA, short-id: '0123456789abcdef', support-x25519mlkem768: true}", "{public-key: invalid}"},
		{"ws-opts", "{path: '/example?ed=1024', headers: {X-Fixture: value}, max-early-data: -1, early-data-header-name: X-Data, v2ray-http-upgrade: true, v2ray-http-upgrade-fast-open: true}", "{path: '%invalid'}"},
		{"grpc-opts", "{grpc-service-name: '/example/Tun', grpc-user-agent: fixture, ping-interval: -1, max-connections: -1, min-streams: -1, max-streams: 9223372036854775807}", "{ping-interval: 9223372037}"},
		{"ss-opts", "{enabled: true, method: aes-128-gcm, password: fixture}", "{enabled: true, password: fixture, method: unknown}"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), trojanPolicyInput("    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid Trojan field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("Trojan field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), trojanPolicyInput("    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid Trojan field accepted")
			}
		})
	}
}

func TestRootPolicy_TrojanTransportBoundaries(t *testing.T) {
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"    reality-opts: {public-key: '', short-id: inactive-invalid}\n", true},
		{"    reality-opts: {public-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA, short-id: '0'}\n", false},
		{"    reality-opts: {public-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA, short-id: '0123456789abcdef00'}\n", false},
		{"    reality-opts: {public-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA}\n    shadow-tls-opts: {password: fixture}\n", false},
		{"    ws-opts: {max-early-data: 9223372036854775807}\n", true},
		{"    ws-opts: {path: 'file:///tmp/data?ed=-1'}\n", true}, // only parsed Path/RawQuery over existing carrier
		{"    ws-opts: {early-data-header-name: 'bad header'}\n", false},
		{"    ws-opts: {headers: {X-Example: \"injected\\nline\"}}\n", false},
		{"    grpc-opts: {grpc-user-agent: \"injected\\nline\"}\n", false},
		{"    ss-opts: {enabled: true, password: fixture}\n", true},
		{"    ss-opts: {enabled: true, password: ''}\n", false},
		{"    ss-opts: {enabled: false, method: unknown}\n", true},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), trojanPolicyInput(tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("Trojan boundary mismatch: %v", err)
		}
	}
	for _, method := range []string{"DUMMY", "RC4-MD5", "AES-128-CTR", "AES-192-CTR", "AES-256-CTR", "AES-128-CFB", "AES-192-CFB", "AES-256-CFB", "CHACHA20", "CHACHA20-IETF", "XCHACHA20",
		"AES-128-GCM", "AES-192-GCM", "AES-256-GCM", "CHACHA20-IETF-POLY1305", "XCHACHA20-IETF-POLY1305", "CHACHA8-IETF-POLY1305", "XCHACHA8-IETF-POLY1305", "AES-128-CCM", "AES-192-CCM", "AES-256-CCM",
		"AEAD_AES_128_GCM", "AEAD_AES_192_GCM", "AEAD_AES_256_GCM", "AEAD_CHACHA20_POLY1305", "AEAD_XCHACHA20_POLY1305", "AEAD_CHACHA8_POLY1305", "AEAD_XCHACHA8_POLY1305", "AEAD_AES_128_CCM", "AEAD_AES_192_CCM", "AEAD_AES_256_CCM"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), trojanPolicyInput("    ss-opts: {enabled: true, password: fixture, method: "+strings.ToLower(method)+"}\n")); err != nil {
			t.Fatalf("native Trojan cipher rejected: %v", err)
		}
	}
}
