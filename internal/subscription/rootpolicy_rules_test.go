package subscription

import (
	"context"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_RuleLeafRegistry(t *testing.T) {
	for _, tc := range []struct{ kind, good, bad string }{
		{"DOMAIN", "Example.Test", ""}, {"DOMAIN-SUFFIX", ".example.test", ""}, {"DOMAIN-KEYWORD", "*network-data*", ""},
		{"DOMAIN-REGEX", "(?<=example)\\.test{1,2}", ""}, {"DOMAIN-WILDCARD", "*?.test", ""},
		{"GEOSITE", "cn@ads", ""}, {"GEOIP", "LAN", ""}, {"SRC-GEOIP", "lan", ""},
		{"IP-ASN", "non-numeric-native-never-match", ""}, {"SRC-IP-ASN", "00123", ""},
		{"IP-CIDR", "192.0.2.9/24", "192.0.2.1"}, {"IP-CIDR6", "192.0.2.1/32", "::1/129"},
		{"SRC-IP-CIDR", "2001:db8::1/64", "secret-value"}, {"IP-SUFFIX", "192.0.2.9/8", "192.0.2.1/33"}, {"SRC-IP-SUFFIX", "2001:db8::1/128", "secret-value"},
		{"SRC-PORT", "[65535]-[0]", "65536"}, {"DST-PORT", "80/443/8000-9000", "*"}, {"IN-PORT", "0", "-1"},
		{"DSCP", "*", "64"}, {"UID", "0-4294967295", "4294967296"},
		{"PROCESS-NAME", "fixture.exe", ""}, {"PROCESS-PATH", "/private/fixture-path", ""},
		{"PROCESS-NAME-REGEX", "(?<=fixture)\\.exe", ""}, {"PROCESS-PATH-REGEX", "(?<=/bin/)fixture", ""},
		{"PROCESS-NAME-WILDCARD", "fixture*", ""}, {"PROCESS-PATH-WILDCARD", "/private/fixture*", ""},
		{"NETWORK", "tcp", "all"}, {"IN-TYPE", "SOCKS / HTTP / TUN", "unknown-type"},
		{"IN-USER", "fixture / other", "fixture//other"}, {"IN-NAME", "fixture/other", "fixture/ "}, {"REMATCH-NAME", "fixture/other", " /fixture"},
		{"RULE-SET", "fixture-provider", ""},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			for i, payload := range []string{tc.good, tc.bad} {
				line := strings.ToLower(tc.kind) + "," + payload + ",DIRECT"
				encoded, err := yaml.Marshal(struct {
					Rules []string `yaml:"rules"`
				}{[]string{line}})
				if err != nil {
					t.Fatal(err)
				}
				input := rootPolicyInput()
				input.YAML = encoded
				switch tc.kind {
				case "GEOSITE":
					addPolicyGeoFixture(t, &input, GeoSiteDAT)
				case "IP-ASN", "SRC-IP-ASN":
					addPolicyGeoFixture(t, &input, GeoASNMMDB)
				case "RULE-SET":
					input.YAML = append(input.YAML, []byte("rule-providers: {fixture-provider: {type: inline, behavior: domain, payload: [example.test]}}\n")...)
				}
				out, err := NewRootConfigPolicy().Build(context.Background(), input)
				if i == 0 {
					if err != nil {
						t.Fatalf("known rule leaf rejected: %v", err)
					}
					var got struct {
						Rules []string `yaml:"rules"`
					}
					if err := yaml.Unmarshal(out.YAML, &got); err != nil {
						t.Fatal(err)
					}
					if len(got.Rules) != 1 || !strings.HasPrefix(got.Rules[0], tc.kind+",") {
						t.Fatal("rule not generated from typed discriminator")
					}
				} else if err == nil {
					t.Fatal("malformed rule leaf accepted")
				}
			}
		})
	}
}

func TestRootPolicy_RuleFreshStructure(t *testing.T) {
	input := dnsPolicyInput("rules:\n - ' match , DIRECT , ignored-extra '\n - ' ip-suffix , 192.0.2.9/8 , DIRECT , src , ignored-param '\n - ' dst-port , [443]-[80]/65535 , DIRECT '\n - ' domain-regex , (?<=fixture)\\.test{1,2} , DIRECT '\n")
	out, err := NewRootConfigPolicy().Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(out.YAML, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"MATCH,DIRECT", "IP-SUFFIX,192.0.2.9/8,DIRECT,src", "DST-PORT,80-443/65535,DIRECT", "DOMAIN-REGEX,(?<=fixture)\\.test{1,2},DIRECT"}
	if len(got.Rules) != len(want) {
		t.Fatal("rule sequence lost")
	}
	for i := range want {
		if got.Rules[i] != want[i] {
			t.Fatalf("typed rule serialization differs at rule %d", i)
		}
	}
	for _, line := range []string{"MATCH", "MATCH,", "UNKNOWN,secret-value,DIRECT", "DOMAIN,secret-value", "SRC-PORT,//,DIRECT", "DSCP,256,DIRECT", "DST-PORT," + strings.Repeat("1/", 28) + "1,DIRECT"} {
		encoded, err := yaml.Marshal(struct {
			Rules []string `yaml:"rules"`
		}{[]string{line}})
		if err != nil {
			t.Fatal(err)
		}
		input.YAML = encoded
		_, err = NewRootConfigPolicy().Build(context.Background(), input)
		if err == nil {
			t.Fatal("malformed rule boundary accepted")
		}
		if strings.Contains(err.Error(), "secret-value") {
			t.Fatal("rule error disclosed data")
		}
	}
}
