package subscription

import (
	"context"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_RootStructuredContainers(t *testing.T) {
	for _, tc := range []struct{ field, good, expected string }{
		{"proxy-providers", "{}", "{}"}, {"rule-providers", "{}", "{}"},
		{"proxies", "[]", "[]"}, {"proxy-groups", "[]", "[]"}, {"rules", "[]", "[]"},
		{"sub-rules", "{}", "{}"}, {"hosts", "{}", "{}"}, {"dns", "{}", "{}"},
		{"ntp", "{}", "{}"}, {"tun", "{}", "{enable: false}"},
		{"experimental", "{}", "{}"}, {"profile", "{}", "{store-selected: false, store-fake-ip: false}"},
		{"geox-url", "{}", "{geoip: '', geosite: '', mmdb: '', asn: ''}"},
		{"sniffer", "{}", "{}"}, {"clash-for-android", "{}", ""},
		{"dns.fallback-filter", "{}", "{}"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			path := strings.Split(tc.field, ".")
			wrap := func(value string) string {
				for i := len(path) - 1; i >= 0; i-- {
					value = "{" + path[i] + ": " + value + "}"
				}
				return value + "\n"
			}
			out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(wrap(tc.good)))
			if err != nil {
				t.Fatalf("valid structured container rejected: %v", err)
			}
			if tc.expected != "" {
				assertPolicyRootLeaf(t, out.YAML, path, tc.expected)
			} else {
				var document map[string]yaml.Node
				if err := yaml.Unmarshal(out.YAML, &document); err != nil {
					t.Fatal(err)
				}
				if _, exists := document[tc.field]; exists {
					t.Fatal("known no-op container emitted")
				}
			}
			_, err = NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(wrap("false")))
			assertPolicyDataFailure(t, err, tc.field)
		})
	}
}

func TestRootPolicy_RootDurationRepresentationEndpoints(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"keep-alive-idle", "9223372036", "9223372037"},
		{"keep-alive-idle", "-9223372036", "-9223372037"},
		{"keep-alive-interval", "9223372036", "9223372037"},
		{"keep-alive-interval", "-9223372036", "-9223372037"},
		{"ntp.interval", "153722867", "153722868"},
	} {
		t.Run(tc.field+"/"+tc.good, func(t *testing.T) {
			wrap := func(value string) string { return tc.field + ": " + value + "\n" }
			path := []string{tc.field}
			if tc.field == "ntp.interval" {
				wrap = func(value string) string { return "ntp: {enable: true, interval: " + value + "}\n" }
				path = []string{"ntp", "interval"}
			}
			out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(wrap(tc.good)))
			if err != nil {
				t.Fatalf("representable duration endpoint rejected: %v", err)
			}
			assertPolicyRootLeaf(t, out.YAML, path, tc.good)
			_, err = NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(wrap(tc.bad)))
			assertPolicyDataFailure(t, err, tc.field)
		})
	}
}

func TestRootPolicy_EachSnifferProtocolLeaf(t *testing.T) {
	for _, protocol := range []string{"TLS", "HTTP", "QUIC"} {
		for _, tc := range []struct{ field, good, bad string }{
			{"ports", "['65535-0']", "['65536']"},
			{"override-destination", "false", "'false'"},
			{"override-destination", "true", "[]"},
			{"override-destination", "null", "1"},
		} {
			t.Run(protocol+"/"+tc.field+"/"+tc.good, func(t *testing.T) {
				wrap := func(value string) string {
					return "sniffer: {enable: true, sniff: {" + protocol + ": {" + tc.field + ": " + value + "}}}\n"
				}
				out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(wrap(tc.good)))
				if err != nil {
					t.Fatalf("valid protocol leaf rejected: %v", err)
				}
				assertPolicyRootLeaf(t, out.YAML, []string{"sniffer", "sniff", protocol, tc.field}, tc.good)
				_, err = NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(wrap(tc.bad)))
				assertPolicyDataFailure(t, err, "sniffer.sniff."+protocol+"."+tc.field)
			})
		}
	}
}
