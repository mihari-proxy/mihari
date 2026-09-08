package subscription

import (
	"context"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_ProviderBytesUseSameEffectiveProtocolSchema(t *testing.T) {
	definition, err := decodeProviderTestValue(t, "type: http\nurl: https://example.test/providers\noverride: {routing-mark: 3}\nfilter: unmatched", proxyProviderDefinitionSchema())
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("proxies:\n  - {name: node, type: direct, routing-mark: -1}\n  - {name: node, type: direct}\n")
	got, err := decodeProxyProviderBytes(context.Background(), definition, data, "proxy-providers.[entry].resource")
	if err != nil {
		t.Fatalf("validated bytes: %v", err)
	}
	if len(got.items) != 2 {
		t.Fatal("provider order or duplicates changed")
	}
	for _, item := range got.items {
		mark, _ := item.get("routing-mark")
		if mark.unsigned != 3 {
			t.Fatal("effective overlay not applied to external resource")
		}
	}
	for i := range data {
		data[i] = 'x'
	}
	name, _ := got.items[0].get("name")
	if name.text != "node" {
		t.Fatal("output retained resource bytes")
	}
	for _, raw := range []string{
		"proxies: []", "proxies: null", "{}", "proxies: [{name: node, type: direct, unknown-secret: true}]",
		"proxies: [{name: node, type: direct}]\nunknown-secret: true", "proxies: [{name: node, type: direct}]\nproxies: []",
		"proxies: [{name: node, type: direct}]\n---\nsecret: value", "proxies: [*unknown]", "ss://encoded-subscription",
	} {
		if _, err := decodeProxyProviderBytes(context.Background(), definition, []byte(raw), "resource"); err == nil {
			t.Fatal("unvalidated resource grammar accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := decodeProxyProviderBytes(ctx, definition, []byte("proxies: [{name: node, type: direct}]"), "resource"); err != context.Canceled {
		t.Fatal("cancellation lost")
	}
	if _, err := decodeProxyProviderBytes(context.Background(), definition, []byte(strings.Repeat(" ", maxDocumentBytes+1)), "resource"); err == nil {
		t.Fatal("source byte budget bypassed")
	}
}

func TestRootPolicy_RuleProviderBytesCanonicalPhysicalLines(t *testing.T) {
	definition, err := decodeProviderTestValue(t, "type: http\nbehavior: classical\nurl: https://example.test/rules", ruleProviderDefinitionSchema())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"DOMAIN,example.test", "DOMAIN-REGEX,first\nsecond\"#value", "PROCESS-PATH,/tmp/" + strings.Repeat("long-name", 10000), "DOMAIN-REGEX,first\u0085second\u2028last"}
	input := struct {
		Payload []string `yaml:"payload"`
	}{want}
	data, err := yaml.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := decodeRuleProviderBytes(context.Background(), definition, data, "rule-providers.[entry].resource")
	if err != nil {
		t.Fatalf("full YAML entries: %v", err)
	}
	encoded := encodeRuleProviderYAML(payload)
	lines := strings.Split(strings.TrimSuffix(string(encoded), "\n"), "\n")
	if len(lines) != len(want)+1 || lines[0] != "payload:" {
		t.Fatal("native line-oriented payload framing broken")
	}
	for i, line := range lines[1:] {
		// The fixed consumer reparses the retained header with each one physical
		// line. Assert every generated entry survives that actual decoder boundary.
		var parsed struct {
			Payload []string `yaml:"payload"`
		}
		if err := yaml.Unmarshal([]byte("payload:\n"+line+"\n"), &parsed); err != nil {
			t.Fatal(err)
		}
		if len(parsed.Payload) != 1 || parsed.Payload[0] != want[i] {
			t.Fatalf("physical line %d lost its original matching data", i)
		}
	}
	flow := []byte("payload: ['DOMAIN,one.test', 'DOMAIN,two.test']")
	flowPayload, err := decodeRuleProviderBytes(context.Background(), definition, flow, "resource")
	if err != nil || len(flowPayload.items) != 2 {
		t.Fatalf("flow entries lost: %v", err)
	}
	for _, raw := range []string{"payload: []\nrules: []", "payload: []\nunknown-secret: true", "payload: []\npayload: []", "rules: ['DOMAIN,one.test']\n---\npayload: []", "payload: ['DOMAIN,one.test', 'UNKNOWN,value']"} {
		if _, err := decodeRuleProviderBytes(context.Background(), definition, []byte(raw), "resource"); err == nil {
			t.Fatal("malformed or ambiguous YAML accepted")
		}
	}
	alias, err := decodeRuleProviderBytes(context.Background(), definition, []byte("rules: ['DOMAIN,one.test']"), "resource")
	if err != nil || len(alias.items) != 1 {
		t.Fatalf("registered rules alias rejected: %v", err)
	}
	empty, err := decodeRuleProviderBytes(context.Background(), definition, encodeRuleProviderYAML(policyValue{kind: policyList}), "resource")
	if err != nil || len(empty.items) != 0 {
		t.Fatalf("canonical empty YAML resource does not roundtrip: %v", err)
	}
}

func TestRootPolicy_RuleProviderTextGrammar(t *testing.T) {
	definition, err := decodeProviderTestValue(t, "type: http\nbehavior: domain\nformat: text\nurl: https://example.test/rules", ruleProviderDefinitionSchema())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := decodeRuleProviderBytes(context.Background(), definition, []byte("\ufeff#not-a-bom-comment\n"), "resource")
	// A BOM is not TrimSpace and not a native comment marker. Its literal domain
	// characters remain data if the domain trie accepts them.
	if err != nil || len(payload.items) != 1 {
		t.Fatalf("text prefix semantics: %v", err)
	}
	payload, err = decodeRuleProviderBytes(context.Background(), definition, []byte("\n # comment\r\n// premium\n  +.Example.TEST \r\none.test"), "resource")
	if err != nil || len(payload.items) != 2 || payload.items[0].text != "+.example.test" {
		t.Fatalf("text comments/trimming/order: %v", err)
	}
	if empty, err := decodeRuleProviderBytes(context.Background(), definition, []byte{}, "resource"); err != nil || len(empty.items) != 0 {
		t.Fatalf("valid empty text set: %v", err)
	}
	if _, err := decodeRuleProviderBytes(context.Background(), definition, []byte("valid.test\nbad/path"), "resource"); err == nil {
		t.Fatal("native warning/skip silently retained")
	}
}
