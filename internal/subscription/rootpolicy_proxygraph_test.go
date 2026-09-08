package subscription

import (
	"context"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_ProxyNamesAreExactData(t *testing.T) {
	for _, name := range []string{"", "name\x00suffix", "MiXeD名"} {
		t.Run(name, func(t *testing.T) {
			input := rootPolicyInput()
			data, err := yaml.Marshal(struct {
				Proxies []struct {
					Name string `yaml:"name"`
					Type string `yaml:"type"`
				} `yaml:"proxies"`
				Groups []struct {
					Name    string   `yaml:"name"`
					Type    string   `yaml:"type"`
					Proxies []string `yaml:"proxies"`
				} `yaml:"proxy-groups"`
			}{
				Proxies: []struct {
					Name string `yaml:"name"`
					Type string `yaml:"type"`
				}{{name, "direct"}},
				Groups: []struct {
					Name    string   `yaml:"name"`
					Type    string   `yaml:"type"`
					Proxies []string `yaml:"proxies"`
				}{{"group\x00name", "select", []string{name}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			input.YAML = data
			out, err := NewRootConfigPolicy().Build(context.Background(), input)
			if err != nil {
				t.Fatalf("exact name rejected: %v", err)
			}
			var result struct {
				Proxies []struct {
					Name string `yaml:"name"`
				} `yaml:"proxies"`
				Groups []struct {
					Name    string   `yaml:"name"`
					Proxies []string `yaml:"proxies"`
				} `yaml:"proxy-groups"`
			}
			if err := yaml.Unmarshal(out.YAML, &result); err != nil {
				t.Fatal(err)
			}
			if result.Proxies[0].Name != name || result.Groups[0].Name != "group\x00name" || result.Groups[0].Proxies[0] != name {
				t.Fatal("name changed during fresh encoding")
			}
		})
	}
	for _, source := range []string{
		"proxies: [{type: direct}]",
		"proxies: [{type: direct, name: null}]",
		"proxies: [{type: direct, name: ''}, {type: direct, name: ''}]",
		"proxy-groups: [{name: '', type: select, proxies: [DIRECT]}]",
	} {
		input := rootPolicyInput()
		input.YAML = []byte(source)
		if _, err := NewRootConfigPolicy().Build(context.Background(), input); err == nil {
			t.Fatal("missing, null, duplicate or empty group name accepted")
		}
	}
}

func TestRootPolicy_ProxyNamespaceAndRawGroupReferences(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		valid        bool
	}{
		{"built-in collision", "proxies: [{name: DIRECT, type: direct}]", false},
		{"root group collision", "proxies: [{name: p, type: direct}]\nproxy-groups: [{name: p, type: select, proxies: [DIRECT]}]", false},
		{"duplicate group", "proxy-groups: [{name: g, type: select, proxies: [DIRECT]}, {name: g, type: select, proxies: [DIRECT]}]", false},
		{"forward group", "proxy-groups: [{name: g, type: select, proxies: [h]}, {name: h, type: select, proxies: [DIRECT]}]", true},
		{"raw cycle despite exclude", "proxy-groups: [{name: g, type: select, proxies: [h], exclude-type: Selector}, {name: h, type: select, proxies: [g]}]", false},
		{"missing member despite exclude", "proxy-groups: [{name: g, type: select, proxies: [missing], exclude-type: Http}]", false},
		{"empty group", "proxy-groups: [{name: g, type: select}]", false},
		{"include proxies empty fallback", "proxy-groups: [{name: g, type: select, include-all-proxies: true}]", true},
		{"include providers empty", "proxy-groups: [{name: g, type: select, include-all-providers: true}]", false},
		{"include providers replaces use", "proxy-groups: [{name: g, type: select, proxies: [DIRECT], use: [missing], include-all-providers: true}]", true},
		{"include proxies retains missing", "proxy-groups: [{name: g, type: select, proxies: [missing], include-all-proxies: true}]", false},
		{"missing fallback always checked", "proxy-groups: [{name: g, type: select, proxies: [DIRECT], empty-fallback: missing}]", false},
		{"group fallback always forbidden", "proxy-groups: [{name: h, type: select, proxies: [DIRECT]}, {name: g, type: select, proxies: [DIRECT], empty-fallback: h}]", false},
		{"unbound selector preference", "proxy-groups: [{name: g, type: select, proxies: [DIRECT], default-selected: missing}]", true},
		{"auto global not yet a group member", "proxy-groups: [{name: g, type: select, proxies: [GLOBAL]}]", false},
		{"explicit global group", "proxy-groups: [{name: GLOBAL, type: select, proxies: [DIRECT]}]", true},
		{"root global captured", "proxies: [{name: GLOBAL, type: direct}]\nproxy-groups: [{name: g, type: select, proxies: [GLOBAL]}]", true},
		{"provider default reserved", "proxy-providers: {default: {type: inline, payload: [{name: p, type: direct}]}}", false},
		{"use missing", "proxy-groups: [{name: g, type: select, use: [missing]}]", false},
		{"provider member not root name", "proxy-providers: {pool: {type: inline, payload: [{name: p, type: direct}]}}\nproxy-groups: [{name: g, type: select, proxies: [p]}]", false},
		{"provider shares use-only group", "proxy-providers: {g: {type: inline, payload: [{name: p, type: direct}]}}\nproxy-groups: [{name: g, type: select, use: [g]}]", true},
		{"provider conflicts synthetic", "proxy-providers: {g: {type: inline, payload: [{name: p, type: direct}]}}\nproxy-groups: [{name: g, type: select, proxies: [DIRECT]}]", false},
		{"group provider is not use source", "proxy-groups: [{name: g, type: select, proxies: [DIRECT]}, {name: h, type: select, proxies: [g], use: [g]}]", false},
		{"group default allowed", "proxy-groups: [{name: default, type: select, proxies: [DIRECT]}]", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := rootPolicyInput()
			input.YAML = []byte(tc.source)
			_, err := NewRootConfigPolicy().Build(context.Background(), input)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestRootPolicy_EffectiveDialerAndMixedCycles(t *testing.T) {
	const http = "{name: p, type: http, server: example.test, port: 443, dialer-proxy: g}"
	for _, tc := range []struct {
		name, source string
		valid        bool
	}{
		{"inactive direct dialer", "proxies: [{name: p, type: direct, dialer-proxy: missing}]", true},
		{"inactive dns dialer", "proxies: [{name: p, type: dns, dialer-proxy: p}]", true},
		{"inactive reject dialer", "proxies: [{name: p, type: reject, dialer-proxy: missing}]", true},
		{"rematch label and missing fallback", "proxies: [{name: p, type: rematch, target-rematch-name: '', target-sub-rule: missing, dialer-proxy: missing}]", true},
		{"missing active dialer", "proxies: [" + http + "]", false},
		{"forward active dialer", "proxies: [" + http + ", {name: g, type: direct}]", true},
		{"pure dialer cycle", "proxies: [" + http + ", {name: g, type: http, server: example.test, port: 443, dialer-proxy: p}]", false},
		{"mixed sole member", "proxies: [" + http + "]\nproxy-groups: [{name: g, type: select, proxies: [p]}]", false},
		{"local multi-filter is inactive", "proxies: [" + http + "]\nproxy-groups: [{name: g, type: select, proxies: [p], filter: 'x`y'}]", false},
		{"mixed selectable member", "proxies: [" + http + "]\nproxy-groups: [{name: g, type: select, proxies: [DIRECT,p], default-selected: DIRECT}]", false},
		{"mixed type excluded", "proxies: [" + http + "]\nproxy-groups: [{name: g, type: select, proxies: [p], exclude-type: hTtP}]", true},
		{"mixed fallback reinserted", "proxies: [" + http + "]\nproxy-groups: [{name: g, type: select, proxies: [p], exclude-type: Http, empty-fallback: p}]", false},
		{"group chain acyclic", "proxies: [" + http + "]\nproxy-groups: [{name: g, type: select, proxies: [h]}, {name: h, type: select, proxies: [DIRECT]}]", true},
		{"automatic GLOBAL cycle", "proxies: [{name: p, type: http, server: example.test, port: 443, dialer-proxy: GLOBAL}]", false},
		{"old GLOBAL safe", "proxies: [{name: GLOBAL, type: http, server: example.test, port: 443, dialer-proxy: DIRECT}]", true},
		{"old GLOBAL hidden missing", "proxies: [{name: GLOBAL, type: http, server: example.test, port: 443, dialer-proxy: missing}]", false},
		{"old GLOBAL cycle", "proxies: [{name: GLOBAL, type: http, server: example.test, port: 443, dialer-proxy: GLOBAL}]", false},
		{"provider mixed cycle", "proxy-providers: {pool: {type: inline, payload: [" + http + "]}}\nproxy-groups: [{name: g, type: select, use: [pool]}]", false},
		{"provider member excluded by type", "proxy-providers: {pool: {type: inline, payload: [" + http + "]}}\nproxy-groups: [{name: g, type: select, use: [pool], exclude-type: Http}]", true},
		{"filtered-out provider missing dialer", "proxy-providers: {pool: {type: inline, exclude-type: http, payload: [" + http + ", {name: safe, type: direct}]}}", false},
		{"provider namespace no sibling dialer", "proxy-providers: {pool: {type: inline, payload: [" + http + ", {name: g, type: direct}]}}", false},
		{"provider prefix preserves cycle", "proxy-providers: {pool: {type: inline, override: {additional-prefix: prefix}, payload: [" + http + "]}}\nproxy-groups: [{name: g, type: select, use: [pool]}]", false},
		{"unknown regexp is not a proven cycle", "proxies: [" + http + "]\nproxy-groups: [{name: g, type: select, proxies: [p], exclude-filter: '(?<=p)$'}]", true},
		{"known literal exclusion", "proxies: [" + http + "]\nproxy-groups: [{name: g, type: select, proxies: [p], exclude-filter: p}]", true},
		{"ordinary rule target missing", "rules: ['MATCH,missing']", false},
		{"unused subrule target missing", "sub-rules: {unused: ['MATCH,missing']}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := rootPolicyInput()
			input.YAML = []byte(tc.source)
			_, err := NewRootConfigPolicy().Build(context.Background(), input)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestRootPolicy_ProviderMembershipUsesNativeNameOrder(t *testing.T) {
	const safe = "{name: same, type: direct}"
	const recursive = "{name: same, type: http, server: example.test, port: 443, dialer-proxy: g}"
	for _, tc := range []struct {
		name, providers, group string
		valid                  bool
	}{
		{"first duplicate wins safe", "pool: {type: inline, payload: [" + safe + "," + recursive + "]}", "use: [pool]", true},
		{"first duplicate wins recursive", "pool: {type: inline, payload: [" + recursive + "," + safe + "]}", "use: [pool]", false},
		{"provider excludes before duplicate", "pool: {type: inline, exclude-type: http, payload: [" + recursive + "," + safe + "]}", "use: [pool]", true},
		{"provider source type discriminator", "pool: {type: inline, exclude-type: Shadowsocks, payload: [" + safe + "]}", "use: [pool]", true},
		{"selector first provider wins", "safe: {type: inline, payload: [" + safe + "]}, loop: {type: inline, payload: [" + recursive + "]}", "use: [safe,loop]", true},
		{"selector reversed provider wins", "safe: {type: inline, payload: [" + safe + "]}, loop: {type: inline, payload: [" + recursive + "]}", "use: [loop,safe]", false},
		{"type exclusion precedes selector lookup", "safe: {type: inline, payload: [" + safe + "]}, loop: {type: inline, payload: [" + recursive + "]}", "use: [safe,loop], exclude-type: Direct", false},
		{"group filter cannot remove local", "pool: {type: inline, payload: [" + recursive + "]}", "proxies: [DIRECT], use: [pool], filter: absent", true},
		{"provider prefix participates in filter", "pool: {type: inline, override: {additional-prefix: pre-}, payload: [" + recursive + "]}", "use: [pool], filter: pre-", false},
		{"unknown renamed filter stays unknown", "pool: {type: inline, override: {proxy-name: [{pattern: '(?<=s)ame', target: changed}]}, payload: [" + recursive + "]}", "use: [pool], filter: same", true},
		{"unknown name still sole recursive member", "pool: {type: inline, override: {proxy-name: [{pattern: '(?<=s)ame', target: changed}]}, payload: [" + recursive + "]}", "use: [pool]", false},
		{"unknown preceding match can shadow later cycle", "safe: {type: inline, override: {proxy-name: [{pattern: x, target: same}]}, payload: [{name: x, type: direct}]}, loop: {type: inline, payload: [" + recursive + "]}", "use: [safe,loop], filter: same", true},
		{"known cycle precedes unknown matching member", "safe: {type: inline, override: {proxy-name: [{pattern: x, target: same}]}, payload: [{name: x, type: direct}]}, loop: {type: inline, payload: [" + recursive + "]}", "use: [loop,safe], filter: same", false},
		{"unknown preceding exclusion can shadow later cycle", "safe: {type: inline, override: {proxy-name: [{pattern: x, target: same}]}, payload: [{name: x, type: direct}]}, loop: {type: inline, payload: [" + recursive + "]}", "use: [safe,loop], exclude-filter: absent", true},
		{"known cycle precedes unknown excluded member", "safe: {type: inline, override: {proxy-name: [{pattern: x, target: same}]}, payload: [{name: x, type: direct}]}, loop: {type: inline, payload: [" + recursive + "]}", "use: [loop,safe], exclude-filter: absent", false},
		{"multi-provider multi-filter remains uncertain", "safe: {type: inline, override: {proxy-name: [{pattern: x, target: same}]}, payload: [{name: x, type: direct}]}, loop: {type: inline, payload: [" + recursive + "]}", "use: [safe,loop], filter: 'same`(?<=same)$'", true},
		{"all provider members excluded", "pool: {type: inline, filter: absent, payload: [" + safe + "]}", "use: [pool]", false},
		{"multi-pass rename kept for core", "pool: {type: inline, filter: 'same`pre-', override: {additional-prefix: pre-}, payload: [" + recursive + "]}", "use: [pool]", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := rootPolicyInput()
			input.YAML = []byte("proxy-providers: {" + tc.providers + "}\nproxy-groups: [{name: g, type: select, " + tc.group + "}]")
			_, err := NewRootConfigPolicy().Build(context.Background(), input)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
