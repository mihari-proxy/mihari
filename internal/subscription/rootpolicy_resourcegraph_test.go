package subscription

import (
	"context"
	"fmt"
	"testing"
)

func TestRootPolicy_GeoConstructorClosureIncludesDisabledAndUnusedData(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      GeoResourceKind
	}{
		{"rule country", "rules: ['GEOIP,CN,DIRECT']", GeoCountryMMDB},
		{"rule DAT", "geodata-mode: true\nrules: ['SRC-GEOIP,!cn,DIRECT']", GeoIPDAT},
		{"nested ASN", "rules: ['AND,((NETWORK,TCP),(SRC-IP-ASN,example-asn)),DIRECT']", GeoASNMMDB},
		{"unused subrule", "sub-rules: {unused: ['GEOSITE,CN@ads,DIRECT']}", GeoSiteDAT},
		{"disabled DNS policy", "dns: {enable: false, nameserver-policy: {'geosite:CN': '1.1.1.1'}}", GeoSiteDAT},
		{"proxy DNS policy", "dns: {proxy-server-nameserver: [1.1.1.1], proxy-server-nameserver-policy: {'geosite:CN': '1.1.1.1'}}", GeoSiteDAT},
		{"fallback implicit default country", "dns: {enable: false, fallback: [1.1.1.1]}", GeoCountryMMDB},
		{"fallback default DAT", "geodata-mode: true\ndns: {fallback: [1.1.1.1]}", GeoIPDAT},
		{"fallback geosite", "dns: {fallback: [1.1.1.1], fallback-filter: {geoip: false, geosite: ['CN@ads']}}", GeoSiteDAT},
		{"fake filter", "dns: {enhanced-mode: fake-ip, fake-ip-filter: ['geosite:CN']}", GeoSiteDAT},
		{"fake rule", "dns: {enhanced-mode: fake-ip, fake-ip-filter-mode: rule, fake-ip-filter: ['GEOSITE,CN,fake-ip']}", GeoSiteDAT},
		{"disabled sniff domain", "sniffer: {enable: false, skip-domain: ['geosite:CN']}", GeoSiteDAT},
		{"disabled sniff IP", "sniffer: {enable: false, skip-src-address: ['geoip:CN']}", GeoCountryMMDB},
		{"sniff force", "sniffer: {force-domain: ['geosite:CN']}", GeoSiteDAT},
		{"sniff destination", "geodata-mode: true\nsniffer: {skip-dst-address: ['geoip:CN']}", GeoIPDAT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := decodeProviderTestValue(t, tc.raw, rootSchema())
			if err != nil {
				t.Fatal(err)
			}
			graph, err := collectPolicyResourceGraph(context.Background(), root, nil)
			if err != nil || len(graph.geo) != 1 {
				t.Fatalf("constructor closure: %v / %d kinds", err, len(graph.geo))
			}
			if _, exists := graph.geo[tc.want]; !exists {
				t.Fatal("wrong required Geo kind")
			}
		})
	}
	for _, raw := range []string{
		"rules: ['GEOIP,LAN,DIRECT']", "sniffer: {skip-dst-address: ['geoip:lan']}",
		"dns: {fallback-filter: {geosite: [CN], geoip: true}}", "dns: {enhanced-mode: normal, fake-ip-filter: ['geosite:CN']}",
		"hosts: {'geosite.test': lan}",
	} {
		root, err := decodeProviderTestValue(t, raw, rootSchema())
		if err != nil {
			t.Fatal(err)
		}
		graph, err := collectPolicyResourceGraph(context.Background(), root, nil)
		if err != nil || len(graph.geo) != 0 {
			t.Fatalf("inactive/non-Geo data introduced assets: %v", err)
		}
	}
	definition, err := decodeProviderTestValue(t, "type: inline\nbehavior: classical\npayload: ['AND,((NETWORK,TCP),(GEOSITE,CN))']", ruleProviderDefinitionSchema())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := preparePolicyProvider(context.Background(), rootPolicyInput(), "rule", "unused", definition, true)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := collectPolicyResourceGraph(context.Background(), policyValue{kind: policyObject}, []preparedPolicyProvider{provider})
	if err != nil || len(graph.geo[GeoSiteDAT]) != 1 {
		t.Fatalf("unused classical provider Geo omitted: %v", err)
	}
}

func TestRootPolicy_RuleProviderGraphReferencesBehaviorAndCycles(t *testing.T) {
	for _, tc := range []struct {
		name, raw, provider string
		valid               bool
	}{
		{"missing ordinary", "rules: ['RULE-SET,missing,DIRECT']", "", false},
		{"domain DNS", "dns: {nameserver-policy: {'rule-set:data': '1.1.1.1'}}", "domain", true},
		{"IP DNS rejected", "dns: {nameserver-policy: {'rule-set:data': '1.1.1.1'}}", "ipcidr", false},
		{"classical DNS", "dns: {nameserver-policy: {'rule-set:data': '1.1.1.1'}}", "classical", true},
		{"IP sniff", "sniffer: {skip-src-address: ['rule-set:data']}", "ipcidr", true},
		{"domain IP sniff rejected", "sniffer: {skip-src-address: ['rule-set:data']}", "domain", false},
		{"classical IP sniff", "sniffer: {skip-src-address: ['rule-set:data']}", "classical", true},
		{"fake rule", "dns: {enhanced-mode: fake-ip, fake-ip-filter-mode: rule, fake-ip-filter: ['RULE-SET,data,fake-ip']}", "classical", true},
		{"fake rule wrong behavior", "dns: {enhanced-mode: fake-ip, fake-ip-filter-mode: rule, fake-ip-filter: ['RULE-SET,data,fake-ip']}", "ipcidr", false},
		{"ordinary accepts IP", "rules: ['RULE-SET,data,DIRECT']", "ipcidr", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := decodeProviderTestValue(t, tc.raw, rootSchema())
			if err != nil {
				t.Fatal(err)
			}
			var providers []preparedPolicyProvider
			if tc.provider != "" {
				definition, err := decodeProviderTestValue(t, fmt.Sprintf("type: inline\nbehavior: %s\npayload: []", tc.provider), ruleProviderDefinitionSchema())
				if err != nil {
					t.Fatal(err)
				}
				provider, err := preparePolicyProvider(context.Background(), rootPolicyInput(), "rule", "data", definition, true)
				if err != nil {
					t.Fatal(err)
				}
				providers = append(providers, provider)
			}
			graph, err := collectPolicyResourceGraph(context.Background(), root, providers)
			if err != nil {
				t.Fatal(err)
			}
			err = validateRuleProviderReferences(context.Background(), graph, providers)
			if (err == nil) != tc.valid {
				t.Fatalf("provider reference validity: %v", err)
			}
		})
	}
	for _, cycle := range []bool{false, true} {
		var providers []preparedPolicyProvider
		for i, name := range []string{"first", "second"} {
			payload := "['AND,((RULE-SET,second))']"
			if i == 1 {
				payload = "['DOMAIN,example.test']"
				if cycle {
					payload = "['OR,((RULE-SET,first))']"
				}
			}
			definition, err := decodeProviderTestValue(t, "type: inline\nbehavior: classical\npayload: "+payload, ruleProviderDefinitionSchema())
			if err != nil {
				t.Fatal(err)
			}
			provider, err := preparePolicyProvider(context.Background(), rootPolicyInput(), "rule", name, definition, true)
			if err != nil {
				t.Fatal(err)
			}
			providers = append(providers, provider)
		}
		graph, err := collectPolicyResourceGraph(context.Background(), policyValue{kind: policyObject}, providers)
		if err != nil {
			t.Fatal(err)
		}
		err = validateRuleProviderReferences(context.Background(), graph, providers)
		if (err != nil) != cycle {
			t.Fatalf("unused recursive provider cycle=%v: %v", cycle, err)
		}
	}
}
