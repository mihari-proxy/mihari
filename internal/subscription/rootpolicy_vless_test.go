package subscription

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

func TestRootPolicy_VLESSFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"uuid", "''", "[]"}, {"flow", "xtls-rprx-vision-udp443", "unsupported-flow-value"},
		{"tls", "true", "1"}, {"alpn", "[h2]", "[1]"}, {"udp", "true", "1"},
		{"packet-addr", "true", "1"}, {"xudp", "true", "1"}, {"packet-encoding", "packet", "[]"},
		{"encryption", "none", "unknown"}, {"network", "native-tcp-fallback", "[]"},
		{"ech-opts", "{enable: true, query-server-name: example.test}", "{config: []}"},
		{"shadow-tls-opts", "{version: 0}", "{version: 4}"}, {"restls-opts", "{version-hint: tls13}", "{version-hint: invalid}"},
		{"jls-opts", "{username: '', password: ''}", "{username: fixture, password: ''}"}, {"reality-opts", "{public-key: ''}", "{public-key: invalid}"},
		{"http-opts", "{method: CUSTOM, path: ['/x'], headers: {X-Test: [a,b]}}", "{method: 'bad method'}"},
		{"h2-opts", "{host: [example.test], path: /x}", "{host: [1]}"},
		{"grpc-opts", "{grpc-service-name: /custom/path}", "{grpc-user-agent: \"bad\\nvalue\"}"},
		{"ws-opts", "{path: /x}", "{path: 1}"}, {"ws-headers", "{X-Test: unused-legacy-header}", "{X-Test: []}"},
		{"skip-cert-verify", "true", "1"}, {"name-cert-verify", "example.test", "[]"}, {"fingerprint", "''", "invalid"},
		{"certificate", "''", "/tmp/cert"}, {"private-key", "''", "/tmp/key"}, {"servername", "example.test", "[]"}, {"client-fingerprint", "chrome", "[]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			prefix := ""
			if tc.field == "restls-opts" {
				prefix = "tls: true\n"
			}
			out, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "vless", prefix+tc.field+": "+tc.good))
			if err != nil {
				t.Fatalf("valid VLESS field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("VLESS field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "vless", tc.field+": "+tc.bad)); err == nil {
				t.Fatal("invalid VLESS field accepted")
			}
		})
	}
}

func TestRootPolicy_VLESSEncryption(t *testing.T) {
	p := base64.RawURLEncoding.EncodeToString(append([]byte{9}, make([]byte, 31)...))
	z := base64.RawURLEncoding.EncodeToString(make([]byte, 1184))
	build := func(value string) error {
		_, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "vless", "encryption: "+strconv.Quote(value)))
		return err
	}
	for _, mode := range []string{"native", "xorpub", "random"} {
		for _, rtt := range []string{"1rtt", "0rtt"} {
			if err := build("mlkem768x25519plus." + mode + "." + rtt + "." + p + "." + z); err != nil {
				t.Fatalf("mixed encryption chain rejected: %v", err)
			}
		}
	}
	for _, tc := range []struct {
		suffix string
		valid  bool
	}{
		{p, true}, {p + ".", true}, {p + "..", false},
		{"100-35-35." + p, true}, {p + ".100-65553-65553", true}, {p + ".100-65554-65554", false},
		{"100-100-35." + p + ".0-0-0." + z + ".0-0-0", true},
		{"+100-035-035." + p, true}, {"100-35-35-ignore." + p, true},
		{"99-35-35." + p, false}, {"100-34-35." + p, false},
		{p + ".100-65553-65553.0-0-0.0-0-1", false},
		{p + ".100-35-35.0-0-9223372036854", true}, {p + ".100-35-35.0-0-9223372036855", false},
		{p + ".100-35-35.101-0-0", true},
		{"100-35-35", false}, {p + "=", false},
		{p[:10] + "\r\n" + p[10:], true}, {p[:10] + " " + p[10:], false},
		{base64.RawURLEncoding.EncodeToString(make([]byte, 32)), true},
		{base64.RawURLEncoding.EncodeToString(make([]byte, 31)), false},
	} {
		err := build("mlkem768x25519plus.native.1rtt." + tc.suffix)
		if (err == nil) != tc.valid {
			t.Fatalf("encryption grammar boundary mismatch: %v", err)
		}
	}
	for _, coefficient := range []int{3328, 3329} {
		bytes := make([]byte, 1184)
		bytes[0], bytes[1] = byte(coefficient), byte(coefficient>>8)
		err := build("mlkem768x25519plus.native.1rtt." + base64.RawURLEncoding.EncodeToString(bytes))
		if (err == nil) != (coefficient == 3328) {
			t.Fatal("MLKEM canonical coefficient check failed")
		}
	}
	for _, value := range []string{"NONE", "mlkem768x25519plus.NATIVE.1rtt." + p, "mlkem768x25519plus.native.600s." + p} {
		if err := build(value); err == nil || strings.Contains(err.Error(), p) {
			t.Fatal("invalid encryption accepted or disclosed")
		}
	}
}

func TestRootPolicy_VLESSNativeTCPFallback(t *testing.T) {
	for _, network := range []string{"kcp", "mkcp", "mekya", "future"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "vless", "network: "+network+"\ntls: true\nshadow-tls-opts: {password: fixture, version: 3}")); err != nil {
			t.Fatalf("VLESS native TCP fallback rejected: %v", err)
		}
	}
}
