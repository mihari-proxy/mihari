package subscription

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

func xhttpPolicyInput(t *testing.T, extra string) PolicyInput {
	return baselineProxyInput(t, "vless", "network: xhttp\nalpn: [http/1.1]\nxhttp-opts:\n  "+strings.ReplaceAll(extra, "\n", "\n  "))
}

func TestRootPolicy_XHTTPFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"path", "relative/path?literal", "[]"}, {"host", "example.test:443", "[]"}, {"mode", "stream-up", "AUTO"},
		{"headers", "{User-Agent: fixture, X-Test: value}", "{X-Test: []}"}, {"no-grpc-header", "true", "1"},
		{"x-padding-bytes", "'0'", "'0-9223372036854775807'"}, {"x-padding-obfs-mode", "true", "1"},
		{"x-padding-key", "x_padding", "[]"}, {"x-padding-header", "Referer", "[]"},
		{"x-padding-placement", "queryInHeader", "[]"}, {"x-padding-method", "tokenish", "[]"},
		{"uplink-http-method", "X-UPLOAD", "'bad method'"}, {"session-placement", "cookie", "[]"},
		{"session-key", "session", "[]"}, {"session-table", "hex", "'非ASCII'"},
		{"session-length", "unused-default-table-length", "[]"}, {"seq-placement", "header", "[]"}, {"seq-key", "X-Seq", "[]"},
		{"uplink-data-placement", "header", "[]"}, {"uplink-data-key", "X-Data", "[]"}, {"uplink-chunk-size", "'64-96'", "[]"},
		{"sc-max-each-post-bytes", "'4096'", "'0-1'"}, {"sc-min-posts-interval-ms", "'0-30'", "'9223372036855'"},
		{"reuse-settings", "{}", "[]"}, {"download-settings", "{}", "[]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), xhttpPolicyInput(t, tc.field+": "+tc.good))
			if err != nil {
				t.Fatalf("valid XHTTP field rejected: %v", err)
			}
			assertPolicyProxyLeaf(t, out.YAML, []string{"xhttp-opts", tc.field}, tc.good)
			_, err = NewRootConfigPolicy().Build(context.Background(), xhttpPolicyInput(t, tc.field+": "+tc.bad))
			field := "proxies[].xhttp-opts." + tc.field
			if tc.field == "headers" {
				field += ".[entry]"
			}
			assertPolicyDataFailure(t, err, field)
		})
	}
}

func TestRootPolicy_XHTTPReuseFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"max-concurrency", "'1025'", "'0-9223372036854775807'"}, {"max-connections", "'1-9223372036854775807'", "'2-1'"},
		{"c-max-reuse-times", "'2147483647'", "'2147483648'"}, {"h-max-request-times", "'0-2147483647'", "'2147483648'"},
		{"h-max-reusable-secs", "'9223372036'", "'9223372037'"}, {"h-keep-alive-period", "-9223372036", "-9223372037"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			for _, nested := range []bool{false, true} {
				wrap := func(value string) string {
					if nested {
						return "reuse-settings: {}\ndownload-settings:\n  reuse-settings: {" + tc.field + ": " + value + "}"
					}
					return "reuse-settings: {" + tc.field + ": " + value + "}"
				}
				out, err := NewRootConfigPolicy().Build(context.Background(), xhttpPolicyInput(t, wrap(tc.good)))
				if err != nil {
					t.Fatalf("valid XMUX field lost: %v", err)
				}
				path := []string{"xhttp-opts"}
				if nested {
					path = append(path, "download-settings")
				}
				path = append(path, "reuse-settings", tc.field)
				assertPolicyProxyLeaf(t, out.YAML, path, tc.good)
				_, err = NewRootConfigPolicy().Build(context.Background(), xhttpPolicyInput(t, wrap(tc.bad)))
				assertPolicyDataFailure(t, err, "proxies[]."+strings.Join(path, "."))
			}
		})
	}
}

func TestRootPolicy_XHTTPDownloadFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"path", "''", "[]"}, {"host", "download.test", "[]"}, {"headers", "{}", "{X-Test: []}"},
		{"server", "download.test", "''"}, {"port", "9443", "65536"}, {"tls", "false", "1"}, {"alpn", "[]", "[1]"},
		{"servername", "''", "[]"}, {"skip-cert-verify", "false", "1"}, {"name-cert-verify", "''", "[]"},
		{"fingerprint", "''", "invalid"}, {"certificate", "''", "/tmp/cert"}, {"private-key", "''", "/tmp/key"}, {"client-fingerprint", "''", "[]"},
		{"ech-opts", "{}", "{enable: true, config: invalid}"}, {"shadow-tls-opts", "{}", "{version: 4}"},
		{"restls-opts", "{}", "{version-hint: invalid}"}, {"jls-opts", "{username: '', password: ''}", "{}"},
		{"reality-opts", "{public-key: ''}", "{}"}, {"reuse-settings", "{}", "[]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), xhttpPolicyInput(t, "download-settings: {"+tc.field+": "+tc.good+"}"))
			if err != nil {
				t.Fatalf("valid download override lost: %v", err)
			}
			assertPolicyProxyLeaf(t, out.YAML, []string{"xhttp-opts", "download-settings", tc.field}, tc.good)
			_, err = NewRootConfigPolicy().Build(context.Background(), xhttpPolicyInput(t, "download-settings: {"+tc.field+": "+tc.bad+"}"))
			field := tc.field
			switch tc.field {
			case "headers":
				field += ".[entry]"
			case "alpn":
				field += "[]"
			case "private-key":
				field = "certificate"
			case "ech-opts":
				field += ".config"
			case "shadow-tls-opts":
				field += ".version"
			case "restls-opts":
				field += ".version-hint"
			case "jls-opts":
				field += ".username"
			case "reality-opts":
				field += ".public-key"
			}
			assertPolicyDataFailure(t, err, "proxies[].xhttp-opts.download-settings."+field)
		})
	}
}

func TestRootPolicy_XHTTPBranches(t *testing.T) {
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"mode: stream-one", true}, {"mode: stream-one\ndownload-settings: {}", false}, {"mode: stream-one\ndownload-settings: null", true},
		{"session-table: '01'\nsession-length: '31'", true}, {"session-table: '01'\nsession-length: '30'", false},
		{"session-table: '00'\nsession-length: '31'", true},
		{"session-table: 'a'\nsession-length: '1-2147483648'", true},
		{"session-table: 'a'\nsession-length: '1-2147483647'", false},
		{"session-table: '01'\nsession-length: '1-9223372036854775807'", false},
		{"session-table: hex\nsession-length: '1025'", true},
		{"session-table: uuid\nsession-length: 'malformed'", true},
		{"mode: stream-one\nsession-table: '01'\nsession-length: '0-32'", false},
		{"session-placement: header\nsession-table: \"a\\nb\"\nsession-length: '31'", false},
		{"session-placement: path\nsession-table: \"a\\nb\"\nsession-length: '31'", true},
		{"session-placement: cookie\nsession-table: 'a;b'\nsession-length: '31'", false},
		{"session-placement: header\nsession-key: 'bad key'", false},
		{"mode: stream-one\nsession-placement: header\nsession-key: 'bad key'", true},
		{"seq-placement: header\nseq-key: 'bad key'", false}, {"mode: stream-up\nseq-placement: header\nseq-key: 'bad key'", true},
		{"x-padding-obfs-mode: true\nx-padding-placement: header\nx-padding-header: 'bad header'", false},
		{"x-padding-obfs-mode: false\nx-padding-header: 'bad header'", true},
		{"x-padding-obfs-mode: true\nx-padding-placement: unknown", true},
		{"x-padding-obfs-mode: true\nx-padding-method: tokenish\nx-padding-bytes: '9223372036854775807'", false},
		{"uplink-data-placement: header\nuplink-data-key: 'bad key'", false},
		{"uplink-data-placement: header\nuplink-data-key: ''\nuplink-chunk-size: '0-1'", true},
		{"uplink-data-placement: cookie\nuplink-chunk-size: '1'", true},
		{"uplink-data-placement: body\nuplink-chunk-size: 'ignored'", true},
		{"mode: stream-up\nsc-max-each-post-bytes: '0-1'", true},
		{"sc-max-each-post-bytes: '0'", false}, {"sc-min-posts-interval-ms: '0'", false},
		{"download-settings: {reuse-settings: {max-connections: malformed}}", true},
		{"reuse-settings: {}\ndownload-settings: {reuse-settings: {max-connections: malformed}}", false},
		{"download-settings: {reuse-settings: {h-keep-alive-period: 9223372037}}", false},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), xhttpPolicyInput(t, tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("XHTTP branch mismatch: %v", err)
		}
	}
	cert, key := policyTLSFixture(t)
	otherCert, _ := policyTLSFixture(t, 1)
	public := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"tls: true\nalpn: [h3]\nxhttp-opts: {}", true}, {"tls: false\nalpn: [h3]\nxhttp-opts: {}", false},
		{"tls: true\nreality-opts: {public-key: '" + public + "'}\nxhttp-opts: {download-settings: {tls: false, reality-opts: {public-key: ''}}}", true},
		{"tls: true\nreality-opts: {public-key: '" + public + "'}\nxhttp-opts: {download-settings: {tls: false, reality-opts: null}}", false},
		{"tls: true\nreality-opts: {public-key: '" + public + "'}\nxhttp-opts: {download-settings: {alpn: [h3]}}", false},
		{"certificate: " + strconv.Quote(cert) + "\nprivate-key: " + strconv.Quote(key) + "\nxhttp-opts: {download-settings: {certificate: null}}", true},
		{"certificate: " + strconv.Quote(cert) + "\nprivate-key: " + strconv.Quote(key) + "\nxhttp-opts: {download-settings: {certificate: ''}}", false},
		{"certificate: " + strconv.Quote(cert) + "\nprivate-key: " + strconv.Quote(key) + "\nxhttp-opts: {download-settings: {certificate: " + strconv.Quote(otherCert) + "}}", false},
		{"certificate: " + strconv.Quote(cert) + "\nprivate-key: " + strconv.Quote(key) + "\nxhttp-opts: {download-settings: {certificate: '', private-key: ''}}", true},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "vless", "network: xhttp\n"+tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("XHTTP effective TLS boundary mismatch: %v", err)
		}
	}
}
