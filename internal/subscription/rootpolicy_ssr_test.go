package subscription

import (
	"context"
	"strings"
	"testing"
)

func ssrPolicyInput(cipher, obfs, protocol, extra string) PolicyInput {
	return simpleProxyInput("ssr", "    server: example.test\n    port: 443\n    password: fixture-password\n    cipher: "+cipher+"\n    obfs: "+obfs+"\n    protocol: "+protocol+"\n"+extra)
}

func TestRootPolicy_SSRFieldsAndVariants(t *testing.T) {
	for _, cipher := range []string{"none", "dummy", "rc4-md5", "aes-128-ctr", "aes-192-ctr", "aes-256-ctr", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "chacha20", "chacha20-ietf", "xchacha20", "AES-128-CFB"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), ssrPolicyInput(cipher, "plain", "origin", "")); err != nil {
			t.Fatalf("valid SSR stream cipher rejected: %v", err)
		}
	}
	for _, obfs := range []string{"plain", "http_simple", "http_post", "random_head", "tls1.2_ticket_auth", "tls1.2_ticket_fastauth"} {
		out, err := NewRootConfigPolicy().Build(context.Background(), ssrPolicyInput("aes-128-cfb", obfs, "origin", "    obfs-param: example.test\n"))
		if err != nil {
			t.Fatalf("valid SSR obfs rejected: %v", err)
		}
		if !strings.Contains(string(out.YAML), "obfs-param: example.test") {
			t.Fatal("SSR obfs data lost")
		}
	}
	for _, protocol := range []string{"origin", "auth_aes128_md5", "auth_aes128_sha1", "auth_sha1_v4", "auth_chain_a", "auth_chain_b"} {
		for _, param := range []string{"4294967295:fixture-key", "4294967296:native-random-fallback", "raw-PSK-fallback"} {
			if _, err := NewRootConfigPolicy().Build(context.Background(), ssrPolicyInput("aes-128-cfb", "plain", protocol, "    protocol-param: "+param+"\n")); err != nil {
				t.Fatalf("valid SSR protocol parameter rejected: %v", err)
			}
		}
	}
	for _, extra := range []string{"    obfs-param: []\n", "    protocol-param: []\n", "    udp: 1\n"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), ssrPolicyInput("aes-128-cfb", "plain", "origin", extra)); err == nil {
			t.Fatal("invalid SSR field type accepted")
		}
	}
	for _, cipher := range []string{"NONE", "DUMMY", "aes-128-gcm", "unknown"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), ssrPolicyInput(cipher, "plain", "origin", "")); err == nil {
			t.Fatal("unsupported SSR cipher accepted")
		}
	}
	if _, err := NewRootConfigPolicy().Build(context.Background(), ssrPolicyInput("aes-128-cfb", "http_simple", "origin", `    obfs-param: "example.test#User-Agent: fixture\nX-Path: /network/path"`+"\n    udp: true\n")); err != nil {
		t.Fatalf("raw HTTP obfs network data rejected: %v", err)
	}
}

func TestRootPolicy_SSRSNILength(t *testing.T) {
	for _, tc := range []struct {
		length int
		tail   string
		valid  bool
	}{
		{64965, "", true}, {64966, "", false}, {64966, "1", true},
	} {
		param := strings.Repeat("a", tc.length) + tc.tail
		_, err := NewRootConfigPolicy().Build(context.Background(), ssrPolicyInput("aes-128-cfb", "tls1.2_ticket_auth", "origin", "    obfs-param: "+param+"\n"))
		if (err == nil) != tc.valid {
			t.Fatalf("SSR maximum record length mismatch: %v", err)
		}
	}
	for _, tc := range []struct {
		param string
		valid bool
	}{
		{strings.Repeat("界", 21655), true}, {strings.Repeat("界", 21656), false},
		{strings.Repeat("a", 64965) + "," + strings.Repeat("b", 64965), true},
		{strings.Repeat("a", 64966) + ",suppressed1", true},
		{strings.Repeat("a", 64966) + "1,selected.example", false},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), ssrPolicyInput("aes-128-cfb", "tls1.2_ticket_fastauth", "origin", "    obfs-param: "+tc.param+"\n"))
		if (err == nil) != tc.valid {
			t.Fatalf("SSR byte length/list/suppression order mismatch: %v", err)
		}
	}
}
