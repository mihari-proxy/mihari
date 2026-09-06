package subscription

import (
	"context"
	"strings"
	"testing"
)

func TestRootPolicy_ProviderSourceAndHealthFields(t *testing.T) {
	for _, tc := range []struct{ name, field, positive, negative string }{
		{"cache suggestion", "path", "cache/proxies.yaml", "../private"},
		{"download URL", "url", "https://example.test/providers", "file:///private"},
		{"manager proxy mode", "proxy", "''", "custom-proxy"},
		{"interval", "interval", "60", "59"},
		{"size limit", "size-limit", "1024", "9223372036854775808"},
		{"headers", "header", "{X-Network: [first, second], Host: [example.test]}", "{X-Network: [true]}"},
		{"age codec", "age-secret-key", "''", "AGE-SECRET-KEY-fixture"},
		{"health enable", "health-check", "{enable: true}", "{enable: 1}"},
		{"health URL", "health-check", "{enable: false, url: 'https://example.test/health'}", "{enable: false, url: 'file:///private'}"},
		{"health interval", "health-check", "{enable: true, url: 'http://example.test/', interval: 9223372036}", "{enable: true, url: 'http://example.test/', interval: 9223372037}"},
		{"health timeout", "health-check", "{enable: false, timeout: 9223372036854}", "{enable: false, timeout: 9223372036855}"},
		{"health lazy", "health-check", "{enable: true, lazy: false}", "{enable: true, lazy: 'false'}"},
		{"health status", "health-check", "{enable: false, expected-status: '200-299/404/65535'}", "{enable: false, expected-status: '65536'}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := "type: http\n"
			if tc.field != "url" {
				base += "url: https://example.test/providers\n"
			}
			got, err := decodeProviderTestValue(t, base+tc.field+": "+tc.positive, proxyProviderDefinitionSchema())
			if err != nil {
				t.Fatalf("valid field: %v", err)
			}
			assertPolicyTypedLeaf(t, got, []string{tc.field}, tc.positive)
			if tc.field == "health-check" || tc.field == "age-secret-key" {
				input, id := providerLeafInput(t, "proxy", base+tc.field+": "+tc.positive, providerLeafResource("proxy"), "")
				out, buildErr := NewRootConfigPolicy().Build(context.Background(), input)
				if buildErr != nil {
					t.Fatalf("managed field rejected: %v", buildErr)
				}
				if tc.field == "health-check" {
					assertPolicyRootLeaf(t, out.YAML, []string{"proxy-providers", "fixture", "health-check"}, tc.positive)
				} else {
					assertManagedProviderDefinition(t, out, "proxy", id, "yaml")
				}
			}
			_, err = decodeProviderTestValue(t, base+tc.field+": "+tc.negative, proxyProviderDefinitionSchema())
			field := "proxy-providers.[entry]." + tc.field
			switch tc.field {
			case "health-check":
				field += "." + map[string]string{"health enable": "enable", "health URL": "url", "health interval": "interval", "health timeout": "timeout", "health lazy": "lazy", "health status": "expected-status"}[tc.name]
			case "header":
				field += ".[entry][]"
			}
			assertPolicyDataFailure(t, err, field)
		})
	}
	for _, raw := range []string{
		"type: http", "type: HTTP\nurl: https://example.test/", "type: http\nurl: https://example.test/\ninterval: 0",
		"type: file\npath: cache/proxies.yaml", "type: inline\npath: C:\\private", "type: inline\npath: /private",
		"type: inline\nhealth-check: {}", "type: inline\nhealth-check: {enable: false, timeout: -9223372036855}",
		"type: inline\nheader: {X-Network: [\"a\\nb\"]}",
	} {
		if _, err := decodeProviderTestValue(t, raw, proxyProviderDefinitionSchema()); err == nil {
			t.Fatal("invalid source boundary accepted")
		}
	}
	for _, raw := range []string{
		"type: file\npath: " + strings.Repeat("a", 64) + "\ninterval: -1\nsize-limit: -1",
		"type: inline\npayload: []\nhealth-check: {enable: true, interval: 0, timeout: 0, expected-status: '*'}",
		"type: inline\npayload: []\nhealth-check: {enable: false, interval: -1}",
		"type: inline\npayload: []\nhealth-check: {enable: true, url: '', interval: -1}",
		"type: inline\npayload: []\nhealth-check: {enable: false, timeout: -1}",
		"type: inline\npayload: []\nhealth-check: {enable: true, url: 'http://example.test/', timeout: -9223372036854}",
	} {
		if _, err := decodeProviderTestValue(t, raw, proxyProviderDefinitionSchema()); err != nil {
			t.Fatalf("source/default/dormant positive: %v", err)
		}
	}
}

func TestRootPolicy_ProviderInlinePayloadAndOverrides(t *testing.T) {
	raw := "type: inline\nfilter: '(?<=node)1`node'\nexclude-filter: 'blocked'\nexclude-type: 'DIRECT|unknown-native-no-match'\ndialer-proxy: parent\noverride:\n  routing-mark: 3\n  dialer-proxy: DIRECT\n  additional-prefix: ../\n  proxy-name: [{pattern: node, target: renamed}]\npayload:\n  - {name: node1, type: direct, routing-mark: -1}\n  - {name: node1, type: direct}\n"
	got, err := decodeProviderTestValue(t, raw, proxyProviderDefinitionSchema())
	if err != nil {
		t.Fatalf("inline definition: %v", err)
	}
	payload, _ := got.get("payload")
	if len(payload.items) != 2 {
		t.Fatal("native duplicate/filter input removed")
	}
	for _, proxy := range payload.items {
		name, _ := proxy.get("name")
		mark, _ := proxy.get("routing-mark")
		dialer, _ := proxy.get("dialer-proxy")
		if name.text != "node1" || mark.unsigned != 3 || dialer.text != "DIRECT" {
			t.Fatal("effective capability overlay or original name changed")
		}
	}
	override, _ := got.get("override")
	if _, exists := override.get("proxy-name"); !exists {
		t.Fatal("name-only transformation lost")
	}
	for _, key := range []string{"routing-mark", "dialer-proxy"} {
		if _, exists := override.get(key); exists {
			t.Fatal("simple override retained for a second application")
		}
	}
	if _, exists := got.get("dialer-proxy"); exists {
		t.Fatal("provider dialer overlay retained")
	}
}

func TestRootPolicy_ProviderFilteredOutNodesStillValidated(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"excluded type", "type: inline\nexclude-type: direct\npayload: [{name: node, type: direct, routing-mark: -1}]"},
		{"unmatched name", "type: inline\nfilter: other\npayload: [{name: node, type: direct, unknown-secret: true}]"},
		{"invalid override", "type: inline\nfilter: other\noverride: {routing-mark: -1}\npayload: [{name: node, type: direct}]"},
		{"duplicate override", "type: inline\noverride: {udp: true}\noverride: {udp: false}\npayload: []"},
		{"duplicate dialer", "type: inline\ndialer-proxy: DIRECT\ndialer-proxy: other\npayload: []"},
		{"name expression capability", "type: inline\noverride: {proxy-name: [{pattern: node, target: other, routing-mark: 1}]}\npayload: []"},
		{"general expression", "type: inline\noverride: {override-expr: []}\npayload: []"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeProviderTestValue(t, tc.raw, proxyProviderDefinitionSchema()); err == nil {
				t.Fatal("unsafe provider accepted")
			}
		})
	}
}
