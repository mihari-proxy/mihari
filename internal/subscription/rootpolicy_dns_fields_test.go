package subscription

import (
	"context"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_DNSFields(t *testing.T) {
	for _, tc := range []struct{ name, good, bad, extra string }{
		{"enable", "true", "1", ""}, {"prefer-h3", "true", "'true'", ""}, {"ipv6", "true", "null", ""},
		{"ipv6-timeout", "9223372036854", "9223372036855", ""},
		{"use-hosts", "false", "[]", ""}, {"use-system-hosts", "true", "system", ""},
		{"respect-rules", "true", "0", "  proxy-server-nameserver: [192.0.2.1]\n"},
		{"nameserver", "[192.0.2.1]", "192.0.2.1", ""},
		{"fallback", "[192.0.2.1]", "[false]", "  fallback-filter: {geoip: false}\n"},
		{"fallback-lazy-query", "true", "'true'", ""},
		{"listen", "'[::1]:5353'", "'0.0.0.0:53'", ""},
		{"listen-routing-mark", "4294967295", "4294967296", ""},
		{"enhanced-mode", "FAKE-IP", "hosts", ""},
		{"fake-ip-range", "198.18.0.1/29", "2001:db8::/64", "  enhanced-mode: fake-ip\n"},
		{"fake-ip-range6", "2001:db8::/125", "192.0.2.1/24", "  enhanced-mode: fake-ip\n"},
		{"fake-ip-filter", "['+.test']", "[false]", "  enhanced-mode: fake-ip\n"},
		{"fake-ip-filter-mode", "WHITELIST", "unknown", ""},
		{"fake-ip-ttl", "4294967295", "4294967296", "  enhanced-mode: fake-ip\n"},
		{"default-nameserver", "['https://192.0.2.1/dns-query', system]", "[resolver.test]", ""},
		{"cache-algorithm", "arc", "true", ""}, {"cache-max-size", "4096", "'4096'", ""},
		{"nameserver-policy", "{'*.test': [192.0.2.1], 'example.test': 'rcode://refused'}", "{'*.test': [false]}", ""},
		{"proxy-server-nameserver", "[192.0.2.1]", "[{}]", ""},
		{"proxy-server-nameserver-policy", "{example.test: []}", "{example.test: null}", "  proxy-server-nameserver: [192.0.2.1]\n"},
		{"direct-nameserver", "['tls://192.0.2.1']", "null", ""},
		{"direct-nameserver-follow-policy", "true", "[]", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, value := range []string{tc.good, tc.bad} {
				out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("dns:\n"+tc.extra+"  "+tc.name+": "+value+"\n"))
				if i == 0 {
					if err != nil {
						t.Fatalf("valid DNS field rejected: %v", err)
					}
					expected := tc.good
					switch tc.name {
					case "enhanced-mode", "fake-ip-filter-mode":
						expected = strings.ToLower(expected)
					case "nameserver-policy":
						expected = "{'*.test': [192.0.2.1], 'example.test': ['rcode://refused']}"
					}
					assertPolicyRootLeaf(t, out.YAML, []string{"dns", tc.name}, expected)
				} else {
					field := "dns." + tc.name
					switch tc.name {
					case "fallback", "fake-ip-filter", "default-nameserver", "proxy-server-nameserver":
						field += "[]"
					case "nameserver-policy":
						field += ".[entry][]"
					case "proxy-server-nameserver-policy":
						field += ".[entry]"
					}
					assertPolicyDataFailure(t, err, field)
				}
			}
		})
	}
	for _, tc := range []struct{ name, good, bad string }{
		{"geoip", "false", "0"}, {"geoip-code", "CN", "[CN]"},
		{"ipcidr", "[192.0.2.1/24, '2001:db8::/64']", "[192.0.2.1]"},
		{"domain", "['+.test', 'sub.*.test']", "['bad..test']"},
		{"geosite", "[CN]", "[false]"},
	} {
		t.Run("fallback-filter/"+tc.name, func(t *testing.T) {
			for i, value := range []string{tc.good, tc.bad} {
				input := dnsPolicyInput("dns:\n  fallback: [192.0.2.1]\n  fallback-filter:\n    " + tc.name + ": " + value + "\n")
				if tc.name != "geoip" {
					addPolicyGeoFixture(t, &input, GeoCountryMMDB)
				}
				if tc.name == "geosite" {
					addPolicyGeoFixture(t, &input, GeoSiteDAT)
				}
				out, err := NewRootConfigPolicy().Build(context.Background(), input)
				if i == 0 {
					if err != nil {
						t.Fatalf("valid fallback filter rejected: %v", err)
					}
					assertPolicyRootLeaf(t, out.YAML, []string{"dns", "fallback-filter", tc.name}, tc.good)
				} else {
					field := "dns.fallback-filter." + tc.name
					if tc.name == "ipcidr" || tc.name == "domain" || tc.name == "geosite" {
						field += "[]"
					}
					assertPolicyDataFailure(t, err, field)
				}
			}
		})
	}
}

func TestRootPolicy_DNSDefaultsAndActiveBoundaries(t *testing.T) {
	for _, body := range []string{
		"{}", "{enable: true}", "{enable: false, nameserver: []}",
		"{listen: ''}", "{listen: '127.0.0.2:0'}", "{listen: 'localhost:53'}",
		"{enhanced-mode: fake-ip, fake-ip-range: '198.18.0.1/29'}",
		"{enhanced-mode: redir-host, fake-ip-range: '198.18.0.1/32'}",
		"{fake-ip-ttl: -9223372036854775808}",
		"{cache-algorithm: arc, cache-max-size: 4611686018427387903}",
		"{cache-algorithm: arc, cache-max-size: 0}", "{cache-algorithm: arc, cache-max-size: -9223372036854775808}",
		"{cache-algorithm: ARC, cache-max-size: 9223372036854775807}",
		"{cache-algorithm: future-lru, cache-max-size: -1}",
		"{nameserver-policy: null, proxy-server-nameserver-policy: {}}",
		"{fallback-filter: {ipcidr: [not-a-prefix], domain: ['bad..test']}}",
	} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("dns: "+body+"\n")); err != nil {
			t.Fatalf("DNS native default/dormant branch rejected: %v", err)
		}
	}
	for _, body := range []string{
		"{enable: true, nameserver: []}", "{respect-rules: true}", "{default-nameserver: []}",
		"{proxy-server-nameserver-policy: {example.test: []}}",
		"{listen: 'resolver.test:53'}", "{listen: ':53'}", "{listen: '[::]:53'}", "{listen: '[::1%fixture]:53'}", "{listen: '127.0.0.1:65536'}",
		"{enhanced-mode: fake-ip, fake-ip-range: '', fake-ip-range6: ''}",
		"{enhanced-mode: fake-ip, fake-ip-range: '198.18.0.1/30'}",
		"{enhanced-mode: fake-ip, fake-ip-range6: '2001:db8::/126'}",
		"{cache-algorithm: arc, cache-max-size: 4611686018427387904}",
	} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("dns: "+body+"\n")); err == nil {
			t.Fatal("DNS effective-boundary violation accepted")
		}
	}
	for _, enabled := range []string{"true", "false"} {
		for _, prefix := range []string{"2001:db8::/64", "192.0.2.1/24"} {
			_, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("ipv6: "+enabled+"\ndns: {fake-ip-range6: '"+prefix+"'}\n"))
			if (err == nil) != strings.Contains(prefix, "2001:") {
				t.Fatal("host-dependent IPv6 input validation")
			}
		}
	}
}

func TestRootPolicy_DNSOrderedPolicy(t *testing.T) {
	input := dnsPolicyInput("dns:\n  nameserver-policy:\n    '*.test': [192.0.2.1]\n    'a.test,b.test': []\n    'sub.*.test': 'rcode://refused'\n")
	out, err := NewRootConfigPolicy().Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(out.YAML, &doc); err != nil {
		t.Fatal(err)
	}
	var policy *yaml.Node
	for i := 0; i < len(doc.Content[0].Content); i += 2 {
		if doc.Content[0].Content[i].Value != "dns" {
			continue
		}
		dns := doc.Content[0].Content[i+1]
		for j := 0; j < len(dns.Content); j += 2 {
			if dns.Content[j].Value == "nameserver-policy" {
				policy = dns.Content[j+1]
			}
		}
	}
	if policy == nil || len(policy.Content) != 6 || policy.Content[0].Value != "*.test" || policy.Content[2].Value != "a.test,b.test" || policy.Content[4].Value != "sub.*.test" || len(policy.Content[3].Content) != 0 {
		t.Fatal("ordered policy/empty-list shadowing changed")
	}
	for _, body := range []string{
		"{A.test: 192.0.2.1, a.test: 192.0.2.2}",
		"{'a.test,b.test': 192.0.2.1, b.test: 192.0.2.2}",
		"{'+.test': 192.0.2.1, '.test': 192.0.2.2}",
		"{'bad..test': 192.0.2.1}", "{private-secret.test: {path: private-secret}}",
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("dns: {nameserver-policy: "+body+"}\n"))
		if err == nil {
			t.Fatal("ambiguous/invalid DNS policy accepted")
		}
		if strings.Contains(err.Error(), "private-secret") {
			t.Fatal("DNS policy disclosed data")
		}
	}
}

func TestRootPolicy_DNSFakeIPRuleMode(t *testing.T) {
	for _, rule := range []string{
		"DOMAIN,example.test,FAKE-IP", "DOMAIN-SUFFIX,test,real-ip", "DOMAIN-KEYWORD,example,fake-ip", "DOMAIN-WILDCARD,*?.test,real-ip",
		"DOMAIN-REGEX,(?<=example)\\.test{1,2},real-ip", "GEOSITE,cn,fake-ip", "RULE-SET,fixture-provider,real-ip", "MATCH,fake-ip",
	} {
		input := dnsPolicyInput("dns: {enhanced-mode: fake-ip, fake-ip-filter-mode: rule, fake-ip-filter: ['" + rule + "']}\n")
		if strings.HasPrefix(rule, "GEOSITE,") {
			addPolicyGeoFixture(t, &input, GeoSiteDAT)
		}
		if strings.HasPrefix(rule, "RULE-SET,") {
			input.YAML = append(input.YAML, []byte("rule-providers: {fixture-provider: {type: inline, behavior: domain, payload: [example.test]}}\n")...)
		}
		out, err := NewRootConfigPolicy().Build(context.Background(), input)
		if err != nil {
			t.Fatalf("domain-only fake-IP rule rejected: %v", err)
		}
		var got struct {
			DNS struct {
				Filter []string `yaml:"fake-ip-filter"`
			} `yaml:"dns"`
		}
		if err := yaml.Unmarshal(out.YAML, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.DNS.Filter) != 1 || strings.Contains(got.DNS.Filter[0], "FAKE-IP") {
			t.Fatal("fake-IP action not canonicalized")
		}
	}
	for _, body := range []string{
		"{enhanced-mode: fake-ip, fake-ip-filter-mode: rule}",
		"{enhanced-mode: fake-ip, fake-ip-filter-mode: rule, fake-ip-filter: ['MATCH,DIRECT']}",
		"{enhanced-mode: fake-ip, fake-ip-filter-mode: rule, fake-ip-filter: ['IP-CIDR,192.0.2.0/24,fake-ip']}",
		"{enhanced-mode: fake-ip, fake-ip-filter-mode: rule, fake-ip-filter: ['PROCESS-PATH,/private/fixture,real-ip']}",
		"{enhanced-mode: fake-ip, fake-ip-filter-mode: rule, fake-ip-filter: ['UNKNOWN,fixture,real-ip']}",
	} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("dns: "+body+"\n")); err == nil {
			t.Fatal("invalid/non-domain fake-IP rule accepted")
		}
	}
	for _, body := range []string{"{enhanced-mode: fake-ip, fake-ip-filter-mode: rule, fake-ip-filter: []}", "{enhanced-mode: normal, fake-ip-filter-mode: rule, fake-ip-filter: [ignored-inactive]}"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("dns: "+body+"\n")); err != nil {
			t.Fatalf("empty/dormant fake-IP filter rejected: %v", err)
		}
	}
}
