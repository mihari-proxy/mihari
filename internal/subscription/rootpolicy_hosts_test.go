package subscription

import (
	"context"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_HostsTypedValues(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"ipv4", "192.0.2.1"}, {"ipv6", "'2001:db8::1'"}, {"mapped", "'::ffff:192.0.2.1'"},
		{"weighted-list", "[192.0.2.1, 192.0.2.1, '2001:db8::1']"},
		{"alias", "...TARGET.test..."}, {"lan", "lan"}, {"lan-list", "[lan]"},
		// The native alias consumer only trims boundary dots and requires two
		// pieces. An unmatched non-trie alias is data, not a filesystem path.
		{"unmatched-alias", "'a..test'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("hosts:\n  Fixture.test: "+tc.value+"\n"))
			if err != nil {
				t.Fatalf("valid typed host rejected: %v", err)
			}
			var got struct {
				Hosts map[string][]string `yaml:"hosts"`
			}
			if err := yaml.Unmarshal(out.YAML, &got); err != nil {
				t.Fatal(err)
			}
			values := got.Hosts["fixture.test"]
			if len(values) == 0 {
				t.Fatal("host data lost")
			}
			if tc.name == "weighted-list" && (len(values) != 3 || values[0] != values[1]) {
				t.Fatal("address weighting changed")
			}
			if strings.HasPrefix(tc.name, "lan") && values[0] != "lan" {
				t.Fatal("LAN sentinel evaluated against host")
			}
			expected := tc.value
			if !strings.HasPrefix(expected, "[") {
				expected = "[" + expected + "]"
			}
			assertPolicyRootLeaf(t, out.YAML, []string{"hosts", "fixture.test"}, expected)
		})
	}
	for _, raw := range []string{"null", "42", "true", "[]", "[null]", "[42]", "[192.0.2.1, alias.test]", "{path: secret-value}", "localhost", "[lan, 192.0.2.1]"} {
		_, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("hosts:\n  private-name.test: "+raw+"\n"))
		if err == nil {
			t.Fatal("invalid typed host accepted")
		}
		if strings.Contains(err.Error(), "private-name") || strings.Contains(err.Error(), "secret-value") {
			t.Fatal("host error disclosed input")
		}
	}
}

func TestRootPolicy_HostPatternAndAliasGraph(t *testing.T) {
	for _, body := range []string{
		"  '*.test': 192.0.2.1\n  exact.test: 192.0.2.2\n",
		"  '+.test': 192.0.2.1\n  '*.test': 192.0.2.2\n",
		"  sub.*.test: 192.0.2.1\n  '.example.test': 192.0.2.2\n",
		"  source.test: target.test\n  target.test: final.test\n  final.test: 192.0.2.1\n",
		// Exact matches outrank wildcard aliases, preventing this apparent cycle.
		"  '*.test': exact.test\n  exact.test: 192.0.2.1\n",
		"  localhost: '::1'\n",
	} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("hosts:\n"+body)); err != nil {
			t.Fatalf("valid host graph rejected: %v", err)
		}
	}
	for _, body := range []string{
		"  'a.test.': 192.0.2.1\n", "  ' a.test': 192.0.2.1\n", "  a..test: 192.0.2.1\n", "  'a*b.test': 192.0.2.1\n", "  'a.+.test': 192.0.2.1\n",
		"  A.test: 192.0.2.1\n  a.test: 192.0.2.2\n",
		"  a.test: 192.0.2.1\n  a.test: 192.0.2.2\n",
		"  '+.a.test': 192.0.2.1\n  a.test: 192.0.2.2\n",
		"  '+.a.test': 192.0.2.1\n  '.a.test': 192.0.2.2\n",
		"  source.test: SOURCE.test\n",
		"  source.test: target.test\n  target.test: source.test\n",
		"  '*.test': source.test\n",
		"  '+.test': deep.source.test\n",
		"  a.test: z.other\n  '*.other': b.test\n  b.test: a.test\n",
	} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("hosts:\n"+body)); err == nil {
			t.Fatal("invalid or cyclic host graph accepted")
		}
	}
}
