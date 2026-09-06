package subscription

import (
	"context"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_GroupIdentityAndSourceListLeaves(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"name", `"Exact\0组"`, "''"},
		{"type", "select", "relay"},
		{"proxies", "[REJECT, DIRECT, REJECT]", "DIRECT"},
		{"use", "[second, first, second]", "[1]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			for index, value := range []string{tc.good, tc.bad} {
				body := "proxy-providers: {first: {type: inline, payload: [{name: first-node, type: direct}]}, second: {type: inline, payload: [{name: second-node, type: direct}]}}\nproxy-groups:\n  - " + tc.field + ": " + value + "\n"
				if tc.field != "name" {
					body += "    name: fixture\n"
				}
				if tc.field != "type" {
					body += "    type: select\n"
				}
				if tc.field != "proxies" && tc.field != "use" {
					body += "    proxies: [DIRECT]\n"
				}
				out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(body))
				if index == 1 {
					field := "proxy-groups[]." + tc.field
					if tc.field == "use" {
						field += "[]"
					}
					assertPolicyDataFailure(t, err, field)
					continue
				}
				if err != nil {
					t.Fatalf("group identity/source data rejected: %v", err)
				}
				assertPolicyRootLeaf(t, out.YAML, []string{"proxy-groups", "[]", tc.field}, tc.good)
			}
		})
	}
	for _, kind := range []string{"select", "url-test", "fallback", "load-balance"} {
		out, err := NewRootConfigPolicy().Build(context.Background(), groupPolicyInput(kind, ""))
		if err != nil {
			t.Fatal(err)
		}
		assertPolicyRootLeaf(t, out.YAML, []string{"proxy-groups", "[]", "type"}, kind)
	}
	for _, tc := range []struct{ body, field string }{
		{"{type: select, proxies: [DIRECT]}", "proxy-groups[]"},
		{"{name: fixture, proxies: [DIRECT]}", "proxy-groups[]"},
		{"{name: null, type: select, proxies: [DIRECT]}", "proxy-groups[].name"},
		{"{name: fixture, type: false, proxies: [DIRECT]}", "proxy-groups[].type"},
		{"{name: fixture, type: unknown-kind, proxies: [DIRECT]}", "proxy-groups[].type"},
		{"{name: fixture, type: select, proxies: [1]}", "proxy-groups[].proxies[]"},
		{"{name: fixture, type: select, use: first}", "proxy-groups[].use"},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("proxy-groups: ["+tc.body+"]"))
		assertPolicyDataFailure(t, err, tc.field)
	}
}

func TestRootPolicy_GroupInactiveKindContexts(t *testing.T) {
	for _, kind := range []string{"select", "url-test", "fallback", "load-balance"} {
		for _, tc := range []struct{ field, active, value string }{
			{"default-selected", "select", "missing-preference"},
			{"tolerance", "url-test", "65535"},
			{"strategy", "load-balance", "inactive-strategy"},
			{"routing-mark", "", "-1"},
			{"interface-name", "", "inactive-interface"},
			{"dialer-proxy", "", "missing-proxy"},
		} {
			if kind == tc.active {
				continue
			}
			t.Run(kind+"/"+tc.field, func(t *testing.T) {
				out, err := NewRootConfigPolicy().Build(context.Background(), groupPolicyInput(kind, "    "+tc.field+": "+tc.value+"\n"))
				if err != nil {
					t.Fatal(err)
				}
				var decoded struct {
					Groups []map[string]yaml.Node `yaml:"proxy-groups"`
				}
				if err := yaml.Unmarshal(out.YAML, &decoded); err != nil {
					t.Fatal(err)
				}
				if len(decoded.Groups) != 1 {
					t.Fatal("group fixture missing")
				}
				if _, exists := decoded.Groups[0][tc.field]; exists {
					t.Fatal("inactive group setting acquired output meaning")
				}
			})
		}
	}
}

func TestRootPolicy_GroupNumericAndMetadataBoundaries(t *testing.T) {
	for _, tc := range []struct{ kind, field, good, bad string }{
		{"select", "interval", "0", "-1"},
		{"select", "interval", "9223372036", "9223372037"},
		{"select", "timeout", "0", "-1"},
		{"select", "timeout", "9223372036854", "9223372036855"},
		{"url-test", "max-failed-times", "0", "-1"},
		{"url-test", "max-failed-times", "9223372036854775807", "9223372036854775808"},
		{"url-test", "tolerance", "0", "65536"},
		{"select", "expected-status", "'65535'", "'65536'"},
		{"select", "url", "''", "[]"},
		{"select", "empty-fallback", "''", "[]"},
		{"select", "icon", "'/private/network-metadata'", "[]"},
		{"select", "disable-udp", "false", "'false'"},
		{"select", "hidden", "false", "'false'"},
		{"select", "default-selected", "missing-preference", "false"},
	} {
		t.Run(tc.kind+"/"+tc.field+"/"+tc.good, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), groupPolicyInput(tc.kind, "    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatal(err)
			}
			assertPolicyRootLeaf(t, out.YAML, []string{"proxy-groups", "[]", tc.field}, tc.good)
			_, err = NewRootConfigPolicy().Build(context.Background(), groupPolicyInput(tc.kind, "    "+tc.field+": "+tc.bad+"\n"))
			assertPolicyDataFailure(t, err, "proxy-groups[]."+tc.field)
		})
	}
	for _, strategy := range []string{"", "consistent-hashing", "round-robin", "sticky-sessions"} {
		out, err := NewRootConfigPolicy().Build(context.Background(), groupPolicyInput("load-balance", "    strategy: '"+strategy+"'\n"))
		if err != nil {
			t.Fatal(err)
		}
		assertPolicyRootLeaf(t, out.YAML, []string{"proxy-groups", "[]", "strategy"}, "'"+strategy+"'")
	}
}

func TestRootPolicy_GroupIncludeExpansionDoesNotRewriteSourceLists(t *testing.T) {
	input := dnsPolicyInput("proxy-providers: {ready: {type: inline, payload: [{name: node, type: direct}]}}\nproxy-groups: [{name: fixture, type: select, proxies: [DIRECT], use: [missing], include-all: true, include-all-providers: false, include-all-proxies: false}]")
	out, err := NewRootConfigPolicy().Build(context.Background(), input)
	if err != nil {
		t.Fatalf("effective include-all replacement rejected: %v", err)
	}
	assertPolicyRootLeaf(t, out.YAML, []string{"proxy-groups", "[]"}, "{name: fixture, type: select, proxies: [DIRECT], use: [missing], include-all: true, include-all-providers: false, include-all-proxies: false}")
}

func TestRootPolicy_GroupDefaultAndLiteralOutput(t *testing.T) {
	for _, tc := range []struct{ name, extra, want string }{
		{"absent-lazy", "", "{name: choice, type: select, proxies: [DIRECT]}"},
		{"explicit-false-include", "    include-all: false\n", "{name: choice, type: select, proxies: [DIRECT], include-all: false}"},
		{"backtick-filter", "    filter: 'first`second'\n", "{name: choice, type: select, proxies: [DIRECT], filter: 'first`second'}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), groupPolicyInput("select", tc.extra))
			if err != nil {
				t.Fatal(err)
			}
			assertPolicyRootLeaf(t, out.YAML, []string{"proxy-groups", "[]"}, tc.want)
		})
	}
}
