package subscription

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func snellPolicyInput(extra string) PolicyInput {
	return simpleProxyInput("snell", "    server: example.test\n    port: 443\n    psk: fixture-psk\n"+extra)
}

func TestRootPolicy_SnellFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"version", "5", "6"}, {"reuse", "true", "1"}, {"udp", "false", "1"}, {"client-fingerprint", "chrome", "[]"},
		{"obfs-opts", "{mode: http, host: example.test}", "{mode: unknown}"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), snellPolicyInput("    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid Snell field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("Snell field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), snellPolicyInput("    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid Snell field accepted")
			}
		})
	}
	for version := 0; version <= 5; version++ {
		for _, udp := range []bool{true, false} {
			_, err := NewRootConfigPolicy().Build(context.Background(), snellPolicyInput("    version: "+strconv.Itoa(version)+"\n    udp: "+strconv.FormatBool(udp)+"\n"))
			if (err == nil) != (!udp || version >= 3) {
				t.Fatal("Snell version UDP compatibility mismatch")
			}
		}
	}
}

func TestRootPolicy_SnellActualEnumConsumers(t *testing.T) {
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"    obfs-opts: {mode: restls, host: example.test, password: fixture-password, version-hint: TLS12, restls-script: '16364'}\n", true},
		{"    obfs-opts: {mode: restls, host: example.test, password: fixture-password, version-hint: TLS12, restls-script: '16365'}\n", false},
		{"    obfs-opts: {mode: restls, host: example.test, password: fixture-password, version-hint: TLS13, restls-script: '16372'}\n", true},
		{"    obfs-opts: {mode: HTTP, host: example.test}\n", false},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), snellPolicyInput(tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("native enum behavior valid=%v error=%v", tc.valid, err)
		}
	}
}

func TestRootPolicy_SnellObfuscationFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"mode", "http", "unknown"}, {"host", "example.test", "[]"}, {"password", "fixture-password", "[]"},
		{"username", "fixture-user", "[]"}, {"version", "3", "[]"}, {"version-hint", "tls13", "[]"},
		{"restls-script", "'250?100<1,350~100'", "[]"}, {"force-tls12", "true", "1"},
		{"skip-cert-verify", "true", "1"}, {"name-cert-verify", "example.test", "[]"}, {"fingerprint", "''", "invalid"},
		{"certificate", "''", "/tmp/cert.pem"}, {"private-key", "''", "/tmp/key.pem"}, {"alpn", "[h2]", "[1]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			for _, valid := range []bool{true, false} {
				value := tc.bad
				if valid {
					value = tc.good
				}
				out, err := NewRootConfigPolicy().Build(context.Background(), snellPolicyInput("    obfs-opts:\n      "+tc.field+": "+value+"\n"))
				if (err == nil) != valid {
					t.Fatalf("Snell obfuscation field mismatch: %v", err)
				}
				if valid && !strings.Contains(string(out.YAML), tc.field+":") {
					t.Fatal("Snell obfuscation field lost")
				}
			}
		})
	}
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"{mode: tls}", true}, {"{mode: shadow-tls, host: example.test}", true},
		{"{mode: shadow-tls, host: example.test, version: 0}", false}, {"{mode: shadow-tls}", false},
		{"{mode: restls, host: example.test, password: fixture, version-hint: tls13}", true},
		{"{mode: restls, host: example.test, password: fixture, version-hint: tls12, restls-script: '16364'}", true},
		{"{mode: restls, host: example.test, password: fixture, version-hint: tls12, restls-script: '16365'}", false},
		{"{mode: jls, host: example.test, username: fixture, password: fixture}", true},
		{"{mode: jls, host: example.test, username: '', password: fixture}", false},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), snellPolicyInput("    obfs-opts: "+tc.value+"\n"))
		if (err == nil) != tc.valid {
			t.Fatalf("Snell active obfuscation mismatch: %v", err)
		}
	}
	for _, size := range []int{48939, 48940} {
		_, err := NewRootConfigPolicy().Build(context.Background(), snellPolicyInput("    obfs-opts: {mode: tls, host: "+strings.Repeat("a", size)+"}\n"))
		if (err == nil) != (size == 48939) {
			t.Fatal("simple TLS-obfs record boundary mismatch")
		}
	}
	for _, size := range []int{16313, 16314} {
		_, err := NewRootConfigPolicy().Build(context.Background(), snellPolicyInput("    obfs-opts: {mode: tls, host: "+strings.Repeat("界", size)+"}\n"))
		if (err == nil) != (size == 16313) {
			t.Fatal("simple TLS-obfs byte boundary mismatch")
		}
	}
}
