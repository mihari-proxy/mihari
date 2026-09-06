package subscription

import (
	"context"
	"encoding/pem"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_OpenVPNFields(t *testing.T) {
	static := strconv.Quote("# fixture key\n-----BEGIN OpenVPN Static key V1-----\n" + strings.Repeat("00", 256) + "\n-----END OpenVPN Static key V1-----")
	v2 := strconv.Quote(string(pem.EncodeToMemory(&pem.Block{Type: "OpenVPN tls-crypt-v2 client key", Bytes: make([]byte, 257)})))
	for _, tc := range []struct{ field, good, bad string }{
		{"proto", "' TCP4-CLIENT '", "udp6"}, {"dev", "' TUN '", "/dev/tun"},
		{"cipher", "aes-cbc", "none"}, {"data-ciphers", "[AES-256-GCM, CHACHA20-POLY1305]", "[1]"},
		{"data-ciphers-fallback", "AES-192-CBC", "[]"}, {"auth", "sha-1", "unknown"},
		{"comp-lzo", "adaptive", "true"}, {"cert", "''", "/tmp/cert"}, {"key", "''", "/tmp/key"},
		{"tls-auth", static, "invalid"}, {"key-direction", "'1'", "1"}, {"tls-crypt", static, "invalid"}, {"tls-crypt-v2", v2, "invalid"},
		{"username", "fixture-user", "[]"}, {"password", "fixture-password", "[]"},
		{"peer-info", "{IV_VER: fixture, UV_DEVICE: arbitrary-network-data}", "{UV_DEVICE: []}"},
		{"ping", "9223372036", "9223372037"}, {"ping-restart", "0", "-1"}, {"tran-window", "0", "-1"},
		{"handshake-timeout", "0", "-1"}, {"mtu", "4294967295", "4294967296"}, {"udp", "false", "1"},
		{"ip-stack", "{mode: mips, congestion-controller: reno}", "{mode: os}"},
		{"remote-dns-resolve", "true", "1"}, {"dns", "[https://example.test/dns-query]", "[dhcp://eth0]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "openvpn", tc.field+": "+tc.good))
			if err != nil {
				t.Fatalf("valid OpenVPN field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("OpenVPN field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "openvpn", tc.field+": "+tc.bad)); err == nil {
				t.Fatal("invalid OpenVPN field accepted")
			}
		})
	}
	cert, key := policyTLSFixture(t)
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"ca: /tmp/ca.pem", false}, {"ca: ''", false},
		{"cert: " + strconv.Quote(cert) + "\nkey: " + strconv.Quote(key) + "\nusername: ''", true},
		{"username: ''", false}, {"tran-window: null", true},
		{"tls-auth: " + static + "\ntls-crypt: " + static, false},
		{"tls-crypt: " + static + "\ntls-crypt-v2: " + v2, false},
		{"tls-auth: '" + strings.Repeat("00", 255) + "'", false},
		{"tls-auth: '" + strings.Repeat("00", 257) + "'", false},
		{"key-direction: '2'", false}, {"dev: ' '", false},
		{"ip-stack: {mode: mips}\nmtu: 68", true}, {"ip-stack: {mode: mips}\nmtu: 67", false},
		{"ip-stack: {mode: mips}\nmtu: 65536", false},
		{"data-ciphers: ['future-network-cipher']\ndata-ciphers-fallback: future-network-cipher", true},
		{"comp-lzo: future-disabled-value", true},
		{"username: " + strconv.Quote(strings.Repeat("u", 65535)), true},
		{"peer-info: {IV_PROTO: ignored-override, IV_CIPHERS: ignored-override, UV_NOTE: \"one\\ntwo\"}", true},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "openvpn", tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("OpenVPN branch mismatch: %v", err)
		}
	}
	for _, cipher := range []string{"AES-128-GCM", "AES-192-GCM", "AES-256-GCM", "AES-128-CBC", "AES-192-CBC", "AES-256-CBC", "CHACHA20-POLY1305", "AES-CBC", ""} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "openvpn", "cipher: "+strconv.Quote(cipher))); err != nil {
			t.Fatalf("supported cipher rejected: %v", err)
		}
	}
}

func TestRootPolicy_OpenVPNPeerInfo(t *testing.T) {
	for _, compression := range []string{"yes", "no"} {
		out, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "openvpn", "comp-lzo: '"+compression+"'\npeer-info: {IV_VER: custom-version, IV_PROTO: ignored, IV_CIPHERS: ignored, IV_LZO: conditional, UV_NOTE: \"one\\ntwo\\0three\"}"))
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Proxies []struct {
				PeerInfo map[string]string `yaml:"peer-info"`
			} `yaml:"proxies"`
		}
		if err := yaml.Unmarshal(out.YAML, &decoded); err != nil {
			t.Fatal(err)
		}
		info := decoded.Proxies[0].PeerInfo
		if info["IV_VER"] != "custom-version" || info["IV_PROTO"] != "ignored" || info["IV_CIPHERS"] != "ignored" || info["IV_LZO"] != "conditional" || info["UV_NOTE"] != "one\ntwo\x00three" {
			t.Fatal("native peer metadata input changed")
		}
	}
	for _, bad := range []string{"{secret-key: 1}", "{secret-key: {nested: value}}", "{secret-key: a, secret-key: b}", "{1: value}"} {
		_, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "openvpn", "peer-info: "+bad))
		if err == nil || strings.Contains(err.Error(), "secret-key") {
			t.Fatal("peer metadata rejection or error redaction failed")
		}
	}
}
