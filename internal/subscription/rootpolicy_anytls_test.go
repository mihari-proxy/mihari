package subscription

import (
	"context"
	"strings"
	"testing"
)

func anyTLSPolicyInput(extra string) PolicyInput {
	return simpleProxyInput("anytls", "    server: example.test\n    port: 443\n    password: fixture-password\n"+extra)
}

func TestRootPolicy_AnyTLSFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"alpn", "[h2, http/1.1]", "[1]"}, {"sni", "example.test", "[]"},
		{"client-fingerprint", "chrome", "[]"}, {"skip-cert-verify", "true", "1"},
		{"name-cert-verify", "example.test", "[]"}, {"fingerprint", "''", "invalid"},
		{"certificate", "''", "/tmp/client.crt"}, {"private-key", "''", "/tmp/client.key"},
		{"udp", "true", "1"}, {"client-metadata", "'fixture /network/path'", `"fixture\nv=1"`},
		{"idle-session-check-interval", "9223372036", "9223372037"},
		{"idle-session-timeout", "-9223372036", "-9223372037"},
		{"min-idle-session", "-1", "9223372036854775808"}, {"disable-reuse", "true", "1"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), anyTLSPolicyInput("    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid AnyTLS field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("AnyTLS field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), anyTLSPolicyInput("    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid AnyTLS field accepted")
			}
		})
	}
	for _, size := range []int{65479, 65480} {
		_, err := NewRootConfigPolicy().Build(context.Background(), anyTLSPolicyInput("    client-metadata: "+strings.Repeat("a", size)+"\n"))
		if (err == nil) != (size == 65479) {
			t.Fatal("AnyTLS settings frame length boundary incorrect")
		}
	}
}

func TestRootPolicy_AnyTLSSecurityModes(t *testing.T) {
	for _, extra := range []string{
		"    ech-opts: {enable: true, query-server-name: public.example.test}\n",
		"    shadow-tls-opts: {password: fixture-password, version: 0}\n",
		"    shadow-tls-opts: {password: fixture-password, version: 1}\n",
		"    shadow-tls-opts: {password: fixture-password, version: 3}\n",
		"    restls-opts: {version-hint: TLS13, password: fixture-password}\n",
		"    restls-opts: {version-hint: tls12, restls-script: '250?100<1,350~100<1,600~100,300~200,300~100'}\n",
		"    jls-opts: {username: fixture-user, password: fixture-password}\n",
		"    shadow-tls-opts: {}\n    restls-opts: {}\n    jls-opts: {username: '', password: ''}\n",
	} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), anyTLSPolicyInput(extra)); err != nil {
			t.Fatalf("valid TLS network mode rejected: %v", err)
		}
	}
	for _, extra := range []string{
		"    ech-opts: {enable: 1}\n", "    ech-opts: {enable: true, config: invalid}\n",
		"    ech-opts: {query-server-name: []}\n", "    shadow-tls-opts: {version: 4}\n",
		"    shadow-tls-opts: {password: []}\n", "    restls-opts: {password: fixture}\n",
		"    restls-opts: {version-hint: tls14}\n", "    restls-opts: {restls-script: []}\n",
		"    jls-opts: {username: fixture, password: ''}\n", "    jls-opts: {username: [], password: fixture}\n",
		"    shadow-tls-opts: {version: 3}\n    jls-opts: {username: fixture, password: fixture}\n",
	} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), anyTLSPolicyInput(extra)); err == nil {
			t.Fatal("invalid TLS mode accepted")
		}
	}
}

func TestRootPolicy_RestlsScriptBounds(t *testing.T) {
	for _, tc := range []struct {
		version, script string
		valid           bool
	}{
		{"tls12", "16364", true}, {"tls12", "16365", false}, {"tls13", "16372", true}, {"tls13", "16373", false},
		{"tls12", "16364~1", true}, {"tls12", "16364?2", false}, {"tls13", "16372?1<254", true},
		{"tls13", "16372~2", false}, {"tls13", "1<255", false}, {"tls13", "32767", false},
		{"tls12", " 250 ? 100 < 1 ,, 0~0<0 ", true}, {"tls13", "1~", true},
		{"tls13", "<1", true}, {"tls13", ",, ,", true}, {"tls13", "9999999999999999999999", false},
		{"tls13", "1\t", false}, {"tls13", "/bin/script", false},
	} {
		t.Run(tc.version+"/"+tc.script, func(t *testing.T) {
			extra := "    restls-opts:\n      version-hint: " + tc.version + "\n      restls-script: '" + tc.script + "'\n"
			_, err := NewRootConfigPolicy().Build(context.Background(), anyTLSPolicyInput(extra))
			if (err == nil) != tc.valid {
				t.Fatalf("Restls boundary acceptance mismatch: %v", err)
			}
		})
	}
}
