package subscription

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

func ssPolicyInput(cipher, password, extra string) PolicyInput {
	return simpleProxyInput("ss", "    server: example.test\n    port: 443\n    cipher: "+cipher+"\n    password: '"+password+"'\n"+extra)
}

func TestRootPolicy_SSCompleteCipherRegistry(t *testing.T) {
	methods := []string{"none", "aes-128-ctr", "aes-192-ctr", "aes-256-ctr", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "rc4-md5", "chacha20-ietf", "xchacha20", "chacha20",
		"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305", "chacha8-ietf-poly1305", "xchacha8-ietf-poly1305", "rabbit128-poly1305", "aes-128-ccm", "aes-192-ccm", "aes-256-ccm", "aes-128-gcm-siv", "aes-256-gcm-siv", "aegis-128l", "aegis-256", "aez-384", "deoxys-ii-256-128", "lea-128-gcm", "lea-192-gcm", "lea-256-gcm", "ascon128", "ascon128a",
		"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305", "2022-blake3-chacha8-poly1305", "2022-blake3-aes-128-ccm", "2022-blake3-aes-256-ccm"}
	if len(methods) != 39 {
		t.Fatal("SS2 fixture census drift")
	}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			password := "fixture-password"
			size := 32
			if strings.HasPrefix(method, "2022-") {
				if strings.Contains(method, "aes-128") {
					size = 16
				}
				password = base64.StdEncoding.EncodeToString(make([]byte, size))
			}
			if method == "none" {
				password = ""
			}
			out, err := NewRootConfigPolicy().Build(context.Background(), ssPolicyInput(method, password, ""))
			if err != nil {
				t.Fatalf("valid SS2 cipher rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), "cipher: "+method) {
				t.Fatal("SS2 cipher lost")
			}
			if method != "none" {
				if _, err := NewRootConfigPolicy().Build(context.Background(), ssPolicyInput(method, "", "")); err == nil {
					t.Fatal("empty encrypted SS password accepted")
				}
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), ssPolicyInput(strings.ToUpper(method), password, "")); err == nil {
				t.Fatal("SS2 case-sensitive cipher accepted uppercase")
			}
			if strings.HasPrefix(method, "2022-") {
				for _, invalid := range []string{base64.StdEncoding.EncodeToString(make([]byte, size-1)), base64.StdEncoding.EncodeToString(make([]byte, size+1)), ":" + password, password + ":", password + "::" + password, "invalid"} {
					if _, err := NewRootConfigPolicy().Build(context.Background(), ssPolicyInput(method, invalid, "")); err == nil {
						t.Fatal("invalid SS2022 key accepted")
					}
				}
				_, err := NewRootConfigPolicy().Build(context.Background(), ssPolicyInput(method, password+":"+password, ""))
				if (err == nil) != strings.Contains(method, "aes-") {
					t.Fatal("SS2022 identity chain family mismatch")
				}
			}
		})
	}
	for _, tc := range []struct {
		cipher    string
		size, max int
	}{{"2022-blake3-aes-128-gcm", 16, 1972}, {"2022-blake3-aes-256-ccm", 32, 1971}} {
		key := base64.StdEncoding.EncodeToString(make([]byte, tc.size))
		for _, n := range []int{tc.max, tc.max + 1} {
			password := strings.Repeat(key+":", n-1) + key
			_, err := NewRootConfigPolicy().Build(context.Background(), ssPolicyInput(tc.cipher, password, ""))
			if (err == nil) != (n == tc.max) {
				t.Fatal("SS2022 fixed TCP header boundary mismatch")
			}
		}
	}
}

func TestRootPolicy_SSPlugins(t *testing.T) {
	for _, tc := range []struct{ plugin, options string }{
		{"obfs", "{mode: tls, host: example.test}"}, {"obfs", "{mode: http}"},
		{"v2ray-plugin", "{mode: websocket, host: example.test, path: '/example', tls: true, mux: false, headers: {X-Fixture: value}, v2ray-http-upgrade: true, v2ray-http-upgrade-fast-open: true, ech-opts: {enable: true, query-server-name: example.test}}"},
		{"gost-plugin", "{mode: websocket, host: example.test, path: /example, tls: true, mux: true}"},
		{"shadow-tls", "{host: example.test, version: 3, password: fixture, alpn: [h2]}"},
		{"restls", "{host: example.test, password: fixture, version-hint: tls13, restls-script: '250?100<1', force-tls12: true}"},
		{"jls", "{host: example.test, username: fixture, password: fixture}"}, {"kcptun", "{}"},
	} {
		t.Run(tc.plugin, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), ssPolicyInput("aes-128-gcm", "fixture", "    plugin: "+tc.plugin+"\n    plugin-opts: "+tc.options+"\n"))
			if err != nil {
				t.Fatalf("valid built-in SS plugin rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), "plugin: "+tc.plugin) {
				t.Fatal("SS plugin lost")
			}
		})
	}
	for _, extra := range []string{"    plugin: external-plugin\n", "    plugin: obfs\n    plugin-opts: {}\n", "    plugin: v2ray-plugin\n    plugin-opts: {mode: unknown}\n", "    plugin-opts: {certificate: /tmp/cert}\n", "    udp-over-tcp-version: 3\n"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), ssPolicyInput("aes-128-gcm", "fixture", extra)); err == nil {
			t.Fatal("invalid SS plugin/UOT accepted")
		}
	}
}

func TestRootPolicy_SSKCPFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"key", "fixture", "[]"}, {"crypt", "aes-128-gcm", "[]"}, {"mode", "manual", "noncanonical"}, {"conn", "65535", "65536"},
		{"autoexpire", "-9223372036", "9223372037"}, {"scavengettl", "600", "9223372037"}, {"mtu", "1501", "[]"}, {"ratelimit", "4294967295", "4294967296"},
		{"sndwnd", "-1", "4294967296"}, {"rcvwnd", "4294967295", "4294967296"}, {"datashard", "253", "254"}, {"parityshard", "246", "247"},
		{"dscp", "63", "[]"}, {"nocomp", "true", "1"}, {"acknodelay", "true", "1"}, {"nodelay", "4294967295", "[]"}, {"interval", "5001", "[]"},
		{"resend", "2147483647", "[]"}, {"nc", "2147483647", "[]"}, {"sockbuf", "4194304", "[]"}, {"smuxver", "3", "-1"},
		{"smuxbuf", "2147483647", "-1"}, {"framesize", "65535", "65536"}, {"streambuf", "4194304", "4194305"}, {"keepalive", "3074457345", "3074457346"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			for _, valid := range []bool{true, false} {
				value := tc.bad
				if valid {
					value = tc.good
				}
				out, err := NewRootConfigPolicy().Build(context.Background(), ssPolicyInput("aes-128-gcm", "fixture", "    plugin: kcptun\n    plugin-opts: {"+tc.field+": "+value+"}\n"))
				if (err == nil) != valid {
					t.Fatalf("KCP field boundary mismatch: %v", err)
				}
				if valid && !strings.Contains(string(out.YAML), tc.field+":") {
					t.Fatal("KCP field lost")
				}
			}
		})
	}
	for _, tc := range []struct {
		opts  string
		valid bool
	}{
		{"{datashard: 128, parityshard: 128}", true}, {"{datashard: 128, parityshard: 129}", false},
		{"{datashard: -1, parityshard: 9223372036854775807}", true},
		{"{mode: manual, nodelay: 4294967296}", false}, {"{mode: fast, nodelay: 9223372036854775807}", true},
		{"{mode: manual, interval: -1, resend: -1, nc: -1}", true}, {"{keepalive: -1}", false},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), ssPolicyInput("aes-128-gcm", "fixture", "    plugin: kcptun\n    plugin-opts: "+tc.opts+"\n"))
		if (err == nil) != tc.valid {
			t.Fatalf("KCP effective defaults mismatch: %v", err)
		}
	}
}
