package subscription

import (
	"context"
	"strings"
	"testing"
)

func TestRootPolicy_TLSMirrorLeafFixtures(t *testing.T) {
	for _, tc := range []struct{ block, field, good, bad string }{
		{"root", "primary-key", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", "invalid"},
		{"time", "base-nanoseconds", "9223372036854775807", "9223372036854775808"},
		{"time", "uniform-random-multiplier-nanoseconds", "9223372036854775808", "9223372036854775809"},
		{"wait", "base-nanoseconds", "9223372036854775807", "9223372036854775808"},
		{"wait", "uniform-random-multiplier-nanoseconds", "9223372036854775808", "9223372036854775809"},
		{"enrolment", "primary-ingress-outbound", "ingress-metadata", "[]"},
		{"enrolment", "primary-egress-outbound", "egress-metadata", "[]"},
		{"step", "name", "fixture-step", "[]"},
		{"step", "host", "example.test", "\"bad\\nHost: injection\""},
		{"step", "path", "'/network/path?query=1'", "[]"},
		{"step", "method", "CUSTOM!METHOD", "'bad method'"},
		{"step", "connection-ready", "true", "1"},
		{"step", "connection-recall-exit", "true", "1"},
		{"step", "h2-do-not-wait-for-download-finish", "true", "1"},
		{"header", "name", "X-Valid", "'bad name'"},
		{"header", "value", "fixture-value", "\"bad\\nvalue\""},
		{"header", "values", "[first, second]", "[first, \"bad\\rvalue\"]"},
		{"transition", "weight", "2147483647", "2147483648"},
		{"transition", "goto-location", "9223372036854775807", "9223372036854775808"},
	} {
		t.Run(tc.block+"/"+tc.field, func(t *testing.T) {
			for index, value := range []string{tc.good, tc.bad} {
				body := tc.field + ": " + value
				path := []string{"tlsmirror-opts"}
				switch tc.block {
				case "root":
				case "time":
					body = "defer-instance-derived-write-time: {" + body + "}"
					path = append(path, "defer-instance-derived-write-time")
				case "enrolment":
					body = "connection-enrolment: {" + body + "}"
					path = append(path, "connection-enrolment")
				case "step", "wait", "header", "transition":
					path = append(path, "embedded-traffic-generator", "steps", "[]")
					if tc.block == "wait" {
						body = "wait-time: {" + body + "}"
						path = append(path, "wait-time")
					}
					if tc.block == "header" {
						if tc.field != "name" {
							body = "name: X-Valid, " + body
						}
						body = "headers: [{" + body + "}]"
						path = append(path, "headers", "[]")
					}
					if tc.block == "transition" {
						if tc.field != "weight" {
							body = "weight: 1, " + body
						}
						body = "next-step: [{" + body + "}]"
						path = append(path, "next-step", "[]")
					}
					body = "embedded-traffic-generator: {steps: [{" + body + "}]}"
				}
				path = append(path, tc.field)
				if tc.block != "root" {
					body = "primary-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=, " + body
				}
				input := baselineProxyInput(t, "vmess", "tls: true\ntlsmirror-opts: {"+body+"}\n")
				out, err := NewRootConfigPolicy().Build(context.Background(), input)
				if index == 0 {
					if err != nil {
						t.Fatalf("valid active TLSMirror leaf rejected: %v", err)
					}
					assertPolicyProxyLeaf(t, out.YAML, path, tc.good)
				} else {
					field := "proxies[]." + strings.ReplaceAll(strings.Join(path, "."), ".[]", "[]")
					if tc.block == "time" || tc.block == "wait" {
						field = strings.TrimSuffix(field, "."+tc.field)
					}
					if tc.block == "header" && tc.field == "values" {
						field += "[]"
					}
					assertPolicyDataFailure(t, err, field)
				}
			}
		})
	}
}
