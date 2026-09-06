package subscription

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func providerLeafInput(t *testing.T, kind, body string, resource []byte, sourceID string) (PolicyInput, string) {
	t.Helper()
	input := rootPolicyInput()
	id, err := ProviderResourceID(input.SubscriptionID, input.Generation, kind, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	input.YAML = []byte(kind + "-providers:\n  fixture:\n    " + strings.ReplaceAll(body, "\n", "\n    ") + "\n")
	if resource != nil {
		if sourceID == "" {
			sourceID = id
		}
		input.Resources = map[string][]byte{sourceID: resource}
	}
	return input, id
}

func providerLeafResource(kind string) []byte {
	if kind == "proxy" {
		return []byte("proxies: [{name: node, type: direct}]")
	}
	return []byte("payload: ['+.Example.TEST']")
}

func assertManagedProviderDefinition(t *testing.T, out PolicyOutput, kind, id, format string) {
	t.Helper()
	extension := "yaml"
	if format == "text" {
		extension = "txt"
	}
	want := "{type: file, path: 'providers/" + id + "." + extension + "'"
	if kind == "rule" {
		want += ", behavior: domain, format: " + format
	}
	want += "}"
	// Exact object comparison proves supplied source-only fields were removed.
	assertPolicyRootLeaf(t, out.YAML, []string{kind + "-providers", "fixture"}, want)
}

func TestRootPolicy_ProviderSourceLeavesReachManagedOutput(t *testing.T) {
	for _, kind := range []string{"proxy", "rule"} {
		for _, tc := range []struct{ field, good, bad string }{
			{"type", "http", "unknown"},
			{"path", "cache/suggestion.yaml", "../private"},
			{"url", "'https://example.test/provider?fixture-token=one'", "'file:///private'"},
			{"proxy", "''", "custom"},
			{"interval", "60", "59"},
			{"size-limit", "1024", "9223372036854775808"},
			{"header", "{x-network: [first, second], Host: [example.test]}", "{X-Network: [true]}"},
		} {
			t.Run(kind+"/"+tc.field, func(t *testing.T) {
				for index, value := range []string{tc.good, tc.bad} {
					body := tc.field + ": " + value
					if tc.field != "type" {
						body += "\ntype: http"
					}
					if tc.field != "url" {
						body += "\nurl: https://example.test/default"
					}
					if kind == "rule" {
						body += "\nbehavior: domain"
					}
					input, id := providerLeafInput(t, kind, body, providerLeafResource(kind), "")
					out, err := NewRootConfigPolicy().Build(context.Background(), input)
					if index == 1 {
						field := kind + "-providers.[entry]." + tc.field
						if tc.field == "header" {
							field += ".[entry][]"
						}
						assertPolicyDataFailure(t, err, field)
						continue
					}
					if err != nil {
						t.Fatalf("valid source field rejected: %v", err)
					}
					if len(out.Providers) != 1 {
						t.Fatal("provider metadata missing")
					}
					spec := out.Providers[0]
					wantURL, wantInterval, wantMax := "https://example.test/default", time.Hour, int64(maxDocumentBytes)
					var wantHeader map[string][]string
					switch tc.field {
					case "url":
						wantURL = "https://example.test/provider?fixture-token=one"
					case "interval":
						wantInterval = time.Minute
					case "size-limit":
						wantMax = 1024
					case "header":
						wantHeader = map[string][]string{"X-Network": {"first", "second"}, "Host": {"example.test"}}
					}
					if spec.URL != wantURL || spec.Interval != wantInterval || spec.MaxBytes != wantMax || !reflect.DeepEqual(spec.Header, wantHeader) || spec.SourceResourceID != "" || spec.ResourceID != id || spec.Kind != kind {
						t.Fatal("source field lost or acquired different manager meaning")
					}
					assertManagedProviderDefinition(t, out, kind, id, "yaml")
				}
			})
		}
	}
}

func TestRootPolicy_ProviderSourceLimitsAndInactiveMetadata(t *testing.T) {
	for _, kind := range []string{"proxy", "rule"} {
		for _, limit := range []int64{0, -1, 1024, maxDocumentBytes + 1} {
			t.Run(fmt.Sprintf("%s/limit/%d", kind, limit), func(t *testing.T) {
				body := fmt.Sprintf("type: http\nurl: https://example.test/\nsize-limit: %d", limit)
				if kind == "rule" {
					body += "\nbehavior: domain"
				}
				input, id := providerLeafInput(t, kind, body, providerLeafResource(kind), "")
				out, err := NewRootConfigPolicy().Build(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				want := int64(maxDocumentBytes)
				if limit > 0 && limit < want {
					want = limit
				}
				if len(out.Providers) != 1 || out.Providers[0].MaxBytes != want {
					t.Fatal("manager size policy changed")
				}
				assertManagedProviderDefinition(t, out, kind, id, "yaml")
			})
		}
		for _, source := range []string{"file", "inline"} {
			t.Run(kind+"/inactive/"+source, func(t *testing.T) {
				oldID := strings.Repeat("b", 64)
				body := "type: " + source + "\ninterval: -1\nsize-limit: 1\nurl: 'file:///ignored-data'\nproxy: ignored-selector\nheader: {X-Ignored: [first, second]}"
				var resource []byte
				if source == "file" {
					body += "\npath: " + oldID
					resource = providerLeafResource(kind)
				} else if kind == "proxy" {
					body += "\npayload: [{name: node, type: direct}]"
				} else {
					body += "\npayload: ['+.Example.TEST']"
				}
				if kind == "rule" {
					body += "\nbehavior: domain"
					if source == "inline" {
						body += "\npath-in-bundle: ../ignored-bundle-data\nformat: text"
					}
				} else {
					body += "\nage-secret-key: ''"
				}
				input, id := providerLeafInput(t, kind, body, resource, oldID)
				out, err := NewRootConfigPolicy().Build(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				if len(out.Providers) != 1 {
					t.Fatal("provider metadata missing")
				}
				spec := out.Providers[0]
				if spec.URL != "" || spec.Interval != 0 || spec.Header != nil || spec.MaxBytes != maxDocumentBytes || spec.Format != "yaml" {
					t.Fatal("inactive fields acquired HTTP meaning")
				}
				wantSource := ""
				if source == "file" {
					wantSource = oldID
				}
				if spec.SourceResourceID != wantSource || spec.ResourceID != id || id == oldID {
					t.Fatal("source/current resource identities confused")
				}
				assertManagedProviderDefinition(t, out, kind, id, "yaml")
			})
		}
	}
}

func TestRootPolicy_ProviderOverlayMaterializedAndNameOnlyRetained(t *testing.T) {
	const names = "{additional-prefix: '../network-name/', additional-suffix: '-suffix', proxy-name: [{pattern: '(?<=node)1', target: '$0-new'}]}"
	const simple = "tfo: true, mptcp: false, udp: false, udp-over-tcp: true, up: '10 Mbps', down: '20 Mbps', dialer-proxy: DIRECT, skip-cert-verify: true, name-cert-verify: fixture.test, interface-name: fixture-interface, routing-mark: 3, ip-version: ipv6-prefer"
	body := "type: inline\ndialer-proxy: overwritten-parent\noverride: {" + simple + ", " + strings.TrimSuffix(strings.TrimPrefix(names, "{"), "}") + "}\npayload:\n" +
		"- {name: node1, type: ss, server: 192.0.2.1, port: 443, cipher: aes-128-gcm, password: fixture-password, routing-mark: -1}\n" +
		"- {name: node1, type: hysteria2, server: 192.0.2.1, port: 443, password: fixture-password, up: invalid-zero-rate}\n"
	input, id := providerLeafInput(t, "proxy", body, nil, "")
	out, err := NewRootConfigPolicy().Build(context.Background(), input)
	if err != nil {
		t.Fatalf("valid effective provider overlay rejected: %v", err)
	}
	assertPolicyRootLeaf(t, out.YAML, []string{"proxy-providers", "fixture"}, "{type: file, path: 'providers/"+id+".yaml', override: "+names+"}")
	if len(out.Providers) != 1 {
		t.Fatal("provider bytes missing")
	}
	var document struct {
		Proxies []yaml.Node `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(out.Providers[0].Inline, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Proxies) != 2 {
		t.Fatal("original duplicate names/order not retained")
	}
	for index, node := range document.Proxies {
		encoded, err := yaml.Marshal(struct {
			Proxies []yaml.Node `yaml:"proxies"`
		}{[]yaml.Node{node}})
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ field, want string }{
			{"name", "node1"}, {"tfo", "true"}, {"mptcp", "false"}, {"dialer-proxy", "DIRECT"},
			{"interface-name", "fixture-interface"}, {"routing-mark", "3"}, {"ip-version", "ipv6-prefer"},
		} {
			assertPolicyProxyLeaf(t, encoded, []string{tc.field}, tc.want)
		}
		fields := []struct{ field, want string }{{"type", "ss"}, {"udp", "false"}, {"udp-over-tcp", "true"}}
		absent := []string{"up", "down", "skip-cert-verify", "name-cert-verify"}
		if index == 1 {
			fields = []struct{ field, want string }{{"type", "hysteria2"}, {"up", "'10 Mbps'"}, {"down", "'20 Mbps'"}, {"skip-cert-verify", "true"}, {"name-cert-verify", "fixture.test"}}
			absent = []string{"udp", "udp-over-tcp"}
		}
		for _, tc := range fields {
			assertPolicyProxyLeaf(t, encoded, []string{tc.field}, tc.want)
		}
		var values map[string]yaml.Node
		if err := node.Decode(&values); err != nil {
			t.Fatal(err)
		}
		for _, field := range absent {
			if _, exists := values[field]; exists {
				t.Fatal("overlay broadened an undeclared protocol field")
			}
		}
	}
}

func TestRootPolicy_ProviderNameOnlyChildBoundaries(t *testing.T) {
	for _, field := range []string{"pattern", "target"} {
		for _, bad := range []string{"1", "null"} {
			var name string
			if field == "pattern" {
				name = "{pattern: " + bad + ", target: '$0-new'}"
			} else {
				name = "{pattern: '(?<=node)1', target: " + bad + "}"
			}
			input, _ := providerLeafInput(t, "proxy", "type: inline\noverride: {proxy-name: ["+name+"]}\npayload: [{name: node1, type: direct}]", nil, "")
			_, err := NewRootConfigPolicy().Build(context.Background(), input)
			assertPolicyDataFailure(t, err, "proxy-providers.[entry].override.proxy-name[]."+field)
		}
	}
	for _, expr := range []string{"[]", "['routing-mark = 1']"} {
		input, _ := providerLeafInput(t, "proxy", "type: inline\noverride: {override-expr: "+expr+"}\npayload: [{name: node, type: direct}]", nil, "")
		_, err := NewRootConfigPolicy().Build(context.Background(), input)
		assertPolicyDataFailure(t, err, "proxy-providers.[entry].override.override-expr")
	}
}

func TestRootPolicy_ProviderRetainedAndPayloadLeafOutput(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"filter", "'(?<=node)1'", "[]"},
		{"exclude-filter", "'(?<=blocked)1'", "[]"},
		{"exclude-type", "'socks5|direct'", "[]"},
		{"health-check", "null", "false"},
		{"override", "null", "false"},
		{"dialer-proxy", "DIRECT", "[]"},
		{"payload", "[{name: node, type: direct}, {name: node, type: direct}]", "{}"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			for index, value := range []string{tc.good, tc.bad} {
				body := "type: inline\n" + tc.field + ": " + value
				if tc.field != "payload" {
					body += "\npayload: [{name: node1, type: http, server: 192.0.2.1, port: 443}]"
				}
				input, id := providerLeafInput(t, "proxy", body, nil, "")
				out, err := NewRootConfigPolicy().Build(context.Background(), input)
				if index == 1 {
					assertPolicyDataFailure(t, err, "proxy-providers.[entry]."+tc.field)
					continue
				}
				if err != nil || len(out.Providers) != 1 {
					t.Fatalf("valid provider leaf rejected: %v", err)
				}
				switch tc.field {
				case "payload":
					assertManagedProviderDefinition(t, out, "proxy", id, "yaml")
					assertPolicyRootLeaf(t, out.Providers[0].Inline, []string{"proxies"}, tc.good)
				case "dialer-proxy":
					assertManagedProviderDefinition(t, out, "proxy", id, "yaml")
					assertPolicyProxyLeaf(t, out.Providers[0].Inline, []string{"dialer-proxy"}, tc.good)
				default:
					assertPolicyRootLeaf(t, out.YAML, []string{"proxy-providers", "fixture", tc.field}, tc.good)
				}
			}
		})
	}
}

func TestRootPolicy_RuleProviderBehaviorAndFormatOutput(t *testing.T) {
	for _, tc := range []struct{ behavior, payload, want string }{
		{"domain", "['+.Example.TEST', '', 'one.*.test']", "['+.example.test', 'one.*.test']"},
		{"ipcidr", "['192.0.2.7/24', '2001:db8::1/32']", "['192.0.2.7/24', '2001:db8::1/32']"},
		{"classical", "['DOMAIN,Example.TEST', 'NETWORK,tcp']", "['DOMAIN,example.test', 'NETWORK,TCP']"},
	} {
		t.Run(tc.behavior, func(t *testing.T) {
			body := "type: inline\nbehavior: " + tc.behavior + "\npayload: " + tc.payload
			input, _ := providerLeafInput(t, "rule", body, nil, "")
			out, err := NewRootConfigPolicy().Build(context.Background(), input)
			if err != nil || len(out.Providers) != 1 {
				t.Fatalf("valid rule behavior rejected: %v", err)
			}
			if out.Providers[0].Behavior != tc.behavior || out.Providers[0].Format != "yaml" {
				t.Fatal("rule behavior metadata changed")
			}
			assertPolicyRootLeaf(t, out.YAML, []string{"rule-providers", "fixture", "behavior"}, tc.behavior)
			assertPolicyRootLeaf(t, out.Providers[0].Inline, []string{"payload"}, tc.want)
		})
	}
	for _, tc := range []struct{ field, good, bad string }{
		{"behavior", "domain", "Domain"}, {"format", "text", "mrs"},
		{"payload", "['+.Example.TEST']", "[42]"}, {"path-in-bundle", "''", "rules.yaml"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			for index, value := range []string{tc.good, tc.bad} {
				body := "type: http\nurl: https://example.test/rules\n" + tc.field + ": " + value
				if tc.field != "behavior" {
					body += "\nbehavior: domain"
				}
				resource := providerLeafResource("rule")
				format := "yaml"
				if tc.field == "format" && index == 0 {
					resource, format = []byte("+.Example.TEST\n"), "text"
				}
				input, id := providerLeafInput(t, "rule", body, resource, "")
				out, err := NewRootConfigPolicy().Build(context.Background(), input)
				if index == 1 {
					field := "rule-providers.[entry]." + tc.field
					if tc.field == "payload" {
						field += "[]"
					}
					assertPolicyDataFailure(t, err, field)
					continue
				}
				if err != nil || len(out.Providers) != 1 {
					t.Fatalf("valid rule field rejected: %v", err)
				}
				if out.Providers[0].Format != format || out.Providers[0].Behavior != "domain" {
					t.Fatal("rule format/behavior metadata changed")
				}
				assertManagedProviderDefinition(t, out, "rule", id, format)
				if format == "text" {
					if string(out.Providers[0].Inline) != "+.example.test\n" {
						t.Fatal("canonical text resource changed")
					}
				} else {
					assertPolicyRootLeaf(t, out.Providers[0].Inline, []string{"payload"}, "['+.example.test']")
				}
			}
		})
	}
}

func TestRootPolicy_ProviderSourceSizeRejectsExcessValidBytes(t *testing.T) {
	for _, kind := range []string{"proxy", "rule"} {
		resource := providerLeafResource(kind)
		body := fmt.Sprintf("type: http\nurl: https://example.test/\nsize-limit: %d", len(resource))
		if kind == "rule" {
			body += "\nbehavior: domain"
		}
		for _, excess := range []bool{false, true} {
			data := append([]byte(nil), resource...)
			if excess {
				data = append(data, ' ')
			}
			input, _ := providerLeafInput(t, kind, body, data, "")
			out, err := NewRootConfigPolicy().Build(context.Background(), input)
			if excess {
				assertPolicyDataFailure(t, err, "providers.[entry].resource")
			} else if err != nil || len(out.Providers) != 1 || out.Providers[0].MaxBytes != int64(len(resource)) {
				t.Fatalf("exact source byte boundary rejected: %v", err)
			}
		}
	}
}

func TestRootPolicy_ProviderSourceBoundaryErrorsAreExact(t *testing.T) {
	for _, kind := range []string{"proxy", "rule"} {
		for _, tc := range []struct{ name, body, field string }{
			{"missing URL", "type: http", "url"},
			{"zero interval", "type: http\nurl: https://example.test/\ninterval: 0", "interval"},
			{"negative interval", "type: http\nurl: https://example.test/\ninterval: -1", "interval"},
			{"interval overflow", "type: http\nurl: https://example.test/\ninterval: 9223372037", "interval"},
			{"file path is not object ID", "type: file\npath: cache/file.yaml", "path"},
			{"header name", "type: http\nurl: https://example.test/\nheader: {'bad name': [value]}", "header"},
			{"header CRLF", "type: http\nurl: https://example.test/\nheader: {X-Data: [\"bad\\r\\nvalue\"]}", "header.[entry][]"},
			{"unknown timeout", "type: http\nurl: https://example.test/\ntimeout: 1", "[unknown]"},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				body := tc.body
				if kind == "rule" {
					body += "\nbehavior: domain"
				}
				input, _ := providerLeafInput(t, kind, body, nil, "")
				_, err := NewRootConfigPolicy().Build(context.Background(), input)
				assertPolicyDataFailure(t, err, kind+"-providers.[entry]."+tc.field)
			})
		}
		body := "type: http\nurl: https://example.test/\ninterval: 9223372036\nheader: {X-Data: [\"one\\tdata\", '中文']}"
		if kind == "rule" {
			body += "\nbehavior: domain"
		}
		input, _ := providerLeafInput(t, kind, body, providerLeafResource(kind), "")
		out, err := NewRootConfigPolicy().Build(context.Background(), input)
		if err != nil || len(out.Providers) != 1 {
			t.Fatalf("maximum source interval/header data rejected: %v", err)
		}
		if out.Providers[0].Interval != 9223372036*time.Second || !reflect.DeepEqual(out.Providers[0].Header, map[string][]string{"X-Data": {"one\tdata", "中文"}}) {
			t.Fatal("source interval/header boundary changed")
		}
	}
}

func TestRootPolicy_ProviderHealthAndEmptyPayloadBoundaries(t *testing.T) {
	for _, tc := range []struct{ value, field string }{
		{"{}", "enable"},
		{"{enable: true, url: 'https://example.test/', interval: -1}", "interval"},
		{"{enable: false, timeout: -9223372036855}", "timeout"},
	} {
		input, _ := providerLeafInput(t, "proxy", "type: inline\nhealth-check: "+tc.value+"\npayload: [{name: node, type: direct}]", nil, "")
		_, err := NewRootConfigPolicy().Build(context.Background(), input)
		assertPolicyDataFailure(t, err, "proxy-providers.[entry].health-check."+tc.field)
	}
	for _, value := range []string{
		"{enable: true, url: 'https://example.test/', interval: 0, timeout: 0, lazy: false, expected-status: '*'}",
		"{enable: true, url: '', interval: -1, timeout: -9223372036854}",
		"{enable: false, interval: -1, timeout: -1}",
	} {
		input, _ := providerLeafInput(t, "proxy", "type: inline\nhealth-check: "+value+"\npayload: [{name: node, type: direct}]", nil, "")
		out, err := NewRootConfigPolicy().Build(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		assertPolicyRootLeaf(t, out.YAML, []string{"proxy-providers", "fixture", "health-check"}, value)
	}
	input, _ := providerLeafInput(t, "proxy", "type: inline\npayload: []", nil, "")
	_, err := NewRootConfigPolicy().Build(context.Background(), input)
	assertPolicyDataFailure(t, err, "providers.[entry].payload")
	input, id := providerLeafInput(t, "rule", "type: inline\nbehavior: domain\npayload: []", nil, "")
	out, err := NewRootConfigPolicy().Build(context.Background(), input)
	if err != nil || len(out.Providers) != 1 {
		t.Fatalf("empty rule set rejected: %v", err)
	}
	assertManagedProviderDefinition(t, out, "rule", id, "yaml")
	assertPolicyRootLeaf(t, out.Providers[0].Inline, []string{"payload"}, "[]")
}

func TestRootPolicy_RuleFileBundleCannotAcquireArchiveFallback(t *testing.T) {
	for _, bundle := range []string{"''", "rules.yaml"} {
		oldID := strings.Repeat("c", 64)
		body := "type: file\nbehavior: domain\npath: " + oldID + "\npath-in-bundle: " + bundle
		input, id := providerLeafInput(t, "rule", body, providerLeafResource("rule"), oldID)
		out, err := NewRootConfigPolicy().Build(context.Background(), input)
		if bundle != "''" {
			assertPolicyDataFailure(t, err, "rule-providers.[entry].path-in-bundle")
			continue
		}
		if err != nil || len(out.Providers) != 1 || out.Providers[0].SourceResourceID != oldID {
			t.Fatalf("exact empty bundle changed file-source meaning: %v", err)
		}
		assertManagedProviderDefinition(t, out, "rule", id, "yaml")
	}
}
