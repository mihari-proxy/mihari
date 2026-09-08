package subscription

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRootPolicy_ProviderPreparationOwnsSourcesAndFreshPaths(t *testing.T) {
	input := rootPolicyInput()
	for _, tc := range []struct {
		name, kind, source string
		resource           []byte
	}{
		{"inline proxy", "proxy", "type: inline\npayload: [{name: node, type: direct}]\nsize-limit: 1\nheader: {X-Ignored: [data]}", nil},
		{"http proxy", "proxy", "type: http\nurl: https://example.test/provider?private-token=fixture\npath: ignored/cache.yaml\nheader: {X-Network: [first, second]}\nsize-limit: 1024", []byte("proxies: [{name: node, type: direct}]")},
		{"file rule", "rule", "type: file\nbehavior: domain\npath: " + strings.Repeat("b", 64) + "\nformat: text\nsize-limit: 1", []byte("+.Example.TEST\n")},
		{"inline rule", "rule", "type: inline\nbehavior: classical\nformat: text\npayload: ['DOMAIN-REGEX,first\nsecond']", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := proxyProviderDefinitionSchema()
			if tc.kind == "rule" {
				schema = ruleProviderDefinitionSchema()
			}
			definition, err := decodeProviderTestValue(t, tc.source, schema)
			if err != nil {
				t.Fatal(err)
			}
			id, err := ProviderResourceID(input.SubscriptionID, input.Generation, tc.kind, "../provider-name")
			if err != nil {
				t.Fatal(err)
			}
			input.Resources = map[string][]byte{}
			if tc.resource != nil {
				key := id
				if tc.name == "file rule" {
					key = strings.Repeat("b", 64)
				}
				input.Resources[key] = tc.resource
			}
			got, err := preparePolicyProvider(context.Background(), input, tc.kind, "../provider-name", definition, true)
			if err != nil {
				t.Fatalf("prepared source: %v", err)
			}
			if !got.ready || got.spec.ResourceID != id || got.spec.Name != "../provider-name" || got.spec.Generation != input.Generation {
				t.Fatal("complete provider identity lost")
			}
			typ, _ := got.definition.get("type")
			path, _ := got.definition.get("path")
			if typ.text != "file" || path.text != "providers/"+id+"."+map[bool]string{true: "txt", false: "yaml"}[tc.name == "file rule"] {
				t.Fatal("managed path depends on source or provider name")
			}
			for _, field := range []string{"url", "interval", "header", "size-limit", "proxy", "payload", "path-in-bundle", "age-secret-key"} {
				if _, found := got.definition.get(field); found {
					t.Fatal("native source capability retained")
				}
			}
			if tc.name == "http proxy" {
				if got.spec.Interval != time.Hour || got.spec.MaxBytes != 1024 || !reflect.DeepEqual(got.spec.Header, map[string][]string{"X-Network": {"first", "second"}}) || got.spec.URL != "https://example.test/provider?private-token=fixture" {
					t.Fatal("manager download contract lost")
				}
				got.spec.Header["X-Network"][0] = "changed"
				headers, _ := definition.get("header")
				first, _ := headers.get("X-Network")
				if first.items[0].text != "first" {
					t.Fatal("header output alias")
				}
			} else if got.spec.Interval != 0 || got.spec.URL != "" || got.spec.MaxBytes != maxDocumentBytes || got.spec.Header != nil {
				t.Fatal("inactive native download fields became active")
			}
			if tc.name == "file rule" && got.spec.SourceResourceID != strings.Repeat("b", 64) {
				t.Fatal("cross-generation source object lost")
			}
		})
	}
}

func TestRootPolicy_ProviderBudgetBoundariesWithoutLargeAllocation(t *testing.T) {
	var count policyProviderBudget
	for i := 0; i < 256; i++ {
		if err := count.source(0); err != nil {
			t.Fatal("256 providers rejected")
		}
	}
	if err := count.source(0); err == nil {
		t.Fatal("257th provider accepted")
	}
	for _, generated := range []bool{false, true} {
		var budget policyProviderBudget
		add := budget.source
		if generated {
			add = budget.output
		}
		for i := 0; i < 16; i++ {
			if err := add(16 << 20); err != nil {
				t.Fatal("256MiB aggregate rejected")
			}
		}
		if err := add(1); err == nil {
			t.Fatal("aggregate overflow accepted")
		}
		var oversized, negative policyProviderBudget
		add = oversized.source
		if generated {
			add = oversized.output
		}
		if err := add((16 << 20) + 1); err == nil {
			t.Fatal("per-source overflow accepted")
		}
		add = negative.source
		if generated {
			add = negative.output
		}
		if err := add(-1); err == nil {
			t.Fatal("negative byte accounting accepted")
		}
	}
}

func TestRootPolicy_ProviderSetIncludesUnusedDefinitionsAndBudgets(t *testing.T) {
	input := rootPolicyInput()
	for _, count := range []int{256, 257} {
		var raw strings.Builder
		raw.WriteString("rule-providers:\n")
		for i := 0; i < count; i++ {
			fmt.Fprintf(&raw, "  unused-%d: {type: inline, behavior: domain, payload: [example.test]}\n", i)
		}
		root, err := decodeProviderTestValue(t, raw.String(), objectSchema(map[string]*policySchema{"rule-providers": providerMapSchema("rule")}))
		if err != nil {
			t.Fatal(err)
		}
		got, err := preparePolicyProviderSet(context.Background(), input, root, true)
		if count == 256 {
			if err != nil || len(got) != 256 {
				t.Fatalf("full unused set: %v", err)
			}
		} else if err == nil {
			t.Fatal("provider count limit bypassed")
		}
	}
}

func TestRootPolicy_ProviderPreparationDiscoveryNeverIgnoresBadBytes(t *testing.T) {
	input := rootPolicyInput()
	definition, err := decodeProviderTestValue(t, "type: http\nurl: https://example.test/provider\nsize-limit: 64", proxyProviderDefinitionSchema())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := ProviderResourceID(input.SubscriptionID, input.Generation, "proxy", "provider")
	got, err := preparePolicyProvider(context.Background(), input, "proxy", "provider", definition, false)
	if err != nil || got.ready || got.spec.ResourceID != id || got.spec.Inline != nil {
		t.Fatalf("discovery without bytes: %v", err)
	}
	if _, err := preparePolicyProvider(context.Background(), input, "proxy", "provider", definition, true); err == nil {
		t.Fatal("complete build accepted missing source")
	}
	for _, data := range [][]byte{[]byte("proxies: [{name: node, type: direct, secret-field: true}]"), []byte(strings.Repeat(" ", 65))} {
		input.Resources = map[string][]byte{id: data}
		if _, err := preparePolicyProvider(context.Background(), input, "proxy", "provider", definition, false); err == nil {
			t.Fatal("discovery ignored supplied invalid or oversized bytes")
		}
	}
}
