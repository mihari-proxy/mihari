package subscription

import (
	"context"
	"strings"
	"testing"
)

func vmessPolicyInput(extra string) PolicyInput {
	return simpleProxyInput("vmess", "    server: example.test\n    port: 443\n    uuid: arbitrary-identity\n    alterId: 0\n    cipher: auto\n"+extra)
}

func TestRootPolicy_VMessFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"udp", "true", "1"}, {"network", "http", "[]"}, {"tls", "true", "1"}, {"alpn", "[h2]", "[1]"},
		{"skip-cert-verify", "true", "1"}, {"name-cert-verify", "example.test", "[]"}, {"fingerprint", "''", "invalid"}, {"certificate", "''", "/tmp/cert.pem"}, {"private-key", "''", "/tmp/key.pem"},
		{"servername", "example.test", "[]"}, {"ech-opts", "{enable: true, query-server-name: example.test}", "[]"},
		{"http-opts", "{method: CUSTOM!METHOD, path: ['/a?literal'], headers: {Host: [example.test], X-Example: [one, two]}}", "{method: 'bad method'}"},
		{"h2-opts", "{host: [example.test], path: /example}", "{host: [1]}"},
		{"grpc-opts", "{grpc-service-name: example}", "{max-streams: []}"}, {"ws-opts", "{path: '/example?ed=1024'}", "{headers: {X-Example: []}}"},
		{"mkcp-opts", "{mtu: 1, tti: 1000, uplink-capacity: 4095, downlink-capacity: 4095, congestion: true, write-buffer: 4294967295, read-buffer: 4294967295, seed: fixture, header: wechat-video}", "{tti: 1001}"},
		{"mekya-opts", "{url: 'https://example.test/carrier', h2-pool-size: 2, max-write-delay: -1, max-request-size: 1048576, polling-interval-initial: 1, max-write-size: -1, max-write-duration-ms: -1, max-simultaneous-write-connection: -1, packet-writing-buffer: -1, kcp: {tti: 50}}", "{kcp: {uplink-capacity: 4096}}"},
		{"packet-addr", "true", "1"}, {"xudp", "true", "1"}, {"packet-encoding", "packet", "[]"}, {"global-padding", "true", "1"}, {"authenticated-length", "true", "1"}, {"client-fingerprint", "chrome", "[]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), vmessPolicyInput("    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid VMess field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("VMess field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), vmessPolicyInput("    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid VMess field accepted")
			}
		})
	}
	for _, cipher := range []string{"auto", "NONE", "zero", "aes-128-cfb", "AES-128-GCM", "chacha20-poly1305"} {
		in := vmessPolicyInput("")
		in.YAML = []byte(strings.Replace(string(in.YAML), "cipher: auto", "cipher: "+cipher, 1))
		if _, err := NewRootConfigPolicy().Build(context.Background(), in); err != nil {
			t.Fatalf("native VMess cipher rejected: %v", err)
		}
	}
	for _, alter := range []string{"-9223372036854775808", "9223372036854775807"} {
		in := vmessPolicyInput("")
		in.YAML = []byte(strings.Replace(string(in.YAML), "alterId: 0", "alterId: "+alter, 1))
		if _, err := NewRootConfigPolicy().Build(context.Background(), in); err != nil {
			t.Fatalf("VMess signed alterId rejected: %v", err)
		}
	}
}

func TestRootPolicy_VMessTLSMirror(t *testing.T) {
	const base = "    tls: true\n    tlsmirror-opts:\n      primary-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"
	for _, tc := range []struct{ field, good, bad string }{
		{"explicit-nonce-ciphersuites", "[0, 65535]", "[65536]"},
		{"defer-instance-derived-write-time", "{base-nanoseconds: 1, uniform-random-multiplier-nanoseconds: 9223372036854775807}", "{base-nanoseconds: 2, uniform-random-multiplier-nanoseconds: 9223372036854775807}"},
		{"transport-layer-padding", "{enabled: true}", "{enabled: 1}"},
		{"connection-enrolment", "{primary-ingress-outbound: direct, primary-egress-outbound: egress}", "{primary-ingress-outbound: []}"},
		{"sequence-watermarking-enabled", "true", "1"},
		{"embedded-traffic-generator", "{steps: [{name: fixture, host: example.test, path: '/literal?path', method: CUSTOM!METHOD, headers: [{name: X-Fixture, value: one, values: [two, three]}], next-step: [{weight: 1, goto-location: 0}], connection-ready: true, connection-recall-exit: true, wait-time: {base-nanoseconds: 1}, h2-do-not-wait-for-download-finish: true}]}", "{steps: [{method: 'bad method'}]}"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), vmessPolicyInput(base+"      "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid TLSMirror field rejected: %v", err)
			}
			assertPolicyProxyLeaf(t, out.YAML, []string{"tlsmirror-opts", tc.field}, tc.good)
			_, err = NewRootConfigPolicy().Build(context.Background(), vmessPolicyInput(base+"      "+tc.field+": "+tc.bad+"\n"))
			failureField := "proxies[].tlsmirror-opts." + tc.field
			switch tc.field {
			case "explicit-nonce-ciphersuites":
				failureField += "[]"
			case "transport-layer-padding":
				failureField += ".enabled"
			case "connection-enrolment":
				failureField += ".primary-ingress-outbound"
			case "embedded-traffic-generator":
				failureField += ".steps[].method"
			}
			assertPolicyDataFailure(t, err, failureField)
		})
	}
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"[{weight: -1, goto-location: -1}, {weight: 2, goto-location: 9000}]", true},
		{"[{weight: 2147483647, goto-location: 0}]", true},
		{"[{weight: 2147483647}, {weight: 1}]", false},
		{"[{weight: 0}]", false},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), vmessPolicyInput(base+"      embedded-traffic-generator: {steps: [{next-step: "+tc.value+"}]}\n"))
		if (err == nil) != tc.valid {
			t.Fatalf("TLSMirror transition arithmetic mismatch: %v", err)
		}
	}
}

func TestRootPolicy_VMessSecurityCombinations(t *testing.T) {
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"    tls: true\n    shadow-tls-opts: {password: fixture}\n", true},
		{"    tls: true\n    restls-opts: {password: fixture, version-hint: TLS13}\n", true},
		{"    tls: true\n    jls-opts: {username: fixture, password: fixture}\n", true},
		{"    tls: true\n    reality-opts: {public-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA}\n", true},
		{"    shadow-tls-opts: {password: fixture}\n", false},
		{"    network: mkcp\n    tls: true\n    shadow-tls-opts: {password: fixture}\n", false},
		{"    network: kcp\n    tls: true\n    reality-opts: {public-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA}\n", true},
		{"    network: fallback-tcp\n", true},
		{"    network: mekya\n    mekya-opts: {url: '//example.test/carrier'}\n", true},
		{"    network: mekya\n    mekya-opts: {url: 'file:///tmp/carrier'}\n", false},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), vmessPolicyInput(tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("VMess security combination mismatch: %v", err)
		}
	}
}
