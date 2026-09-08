package subscription

import "testing"

func TestRootPolicy_RuleProviderTypedPayload(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"domain", "type: inline\nbehavior: domain\npayload: ['+.Example.TEST', '', 'one.*.test']", 2},
		{"ipcidr", "type: inline\nbehavior: ipcidr\npayload: ['192.0.2.7/24', '2001:db8::1/32']", 2},
		{"classical", "type: inline\nbehavior: classical\npayload: ['DOMAIN,Example.TEST', 'AND,((NETWORK,tcp),(GEOIP,CN))', 'OR,((RULE-SET,inner),(PROCESS-PATH,/tmp/network-name))']", 3},
		{"file", "type: file\nbehavior: domain\npath: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nformat: text\ninterval: -1", 0},
		{"http", "type: http\nbehavior: classical\nurl: https://example.test/rules\nformat: yaml\ninterval: 60\nsize-limit: 0\nheader: {X-Network: [first, second]}\nproxy: ''\npath: cache/rules.yaml\npath-in-bundle: ''", 0},
		{"inline bundle dormant", "type: inline\nbehavior: domain\npath-in-bundle: ../bundle-data\npayload: []", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeProviderTestValue(t, tc.body, ruleProviderDefinitionSchema())
			if err != nil {
				t.Fatalf("positive: %v", err)
			}
			payload, _ := got.get("payload")
			if len(payload.items) != tc.want {
				t.Fatal("payload order/count lost")
			}
			if tc.name == "classical" && payload.items[1].rule == nil {
				t.Fatal("recursive typed rule metadata missing")
			}
		})
	}
}

func TestRootPolicy_RuleProviderRejectsMalformedAndUnmanagedCapabilities(t *testing.T) {
	for _, raw := range []string{
		"type: inline", "type: inline\nbehavior: Domain", "type: inline\nbehavior: domain\nformat: mrs", "type: inline\nbehavior: domain\nformat: unknown",
		"type: inline\nbehavior: domain\npayload: ['bad/path']", "type: inline\nbehavior: domain\npayload: ['a..b']", "type: inline\nbehavior: domain\npayload: [1]",
		"type: inline\nbehavior: ipcidr\npayload: ['192.0.2.1']", "type: inline\nbehavior: ipcidr\npayload: ['192.0.2.1/33']",
		"type: inline\nbehavior: classical\npayload: ['MATCH']", "type: inline\nbehavior: classical\npayload: ['RULE-SET,other']", "type: inline\nbehavior: classical\npayload: ['SUB-RULE,(NETWORK,TCP),other']",
		"type: http\nbehavior: domain\nurl: https://example.test/\npath-in-bundle: rules.yaml", "type: http\nbehavior: domain\nurl: https://example.test/\nproxy: custom",
		"type: inline\nbehavior: domain\nunknown-secret: true", "type: inline\nbehavior: domain\nbehavior: classical",
	} {
		if _, err := decodeProviderTestValue(t, raw, ruleProviderDefinitionSchema()); err == nil {
			t.Fatal("invalid rule provider accepted")
		}
	}
}
