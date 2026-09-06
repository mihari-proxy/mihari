package subscription

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

func TestRootPolicy_MieruNameRequiredByConsumer(t *testing.T) {
	for _, provider := range []bool{false, true} {
		for _, name := range []string{"''", "fixture", `"\0"`} {
			body := "{name: " + name + ", type: mieru, server: 192.0.2.1, port: 443, transport: TCP, username: fixture-user, password: fixture-password}"
			input := "proxies: [" + body + "]\n"
			field := "proxies[].name"
			if provider {
				input = "proxy-providers: {p: {type: inline, payload: [" + body + "]}}\n"
				field = "proxy-providers.[entry].payload[].name"
			}
			t.Run(field+"/"+name, func(t *testing.T) {
				out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(input))
				if name == "''" {
					assertPolicyDataFailure(t, err, field)
				} else {
					if err != nil {
						t.Fatalf("nonempty Mieru data name rejected: %v", err)
					}
					content := out.YAML
					if provider {
						if len(out.Providers) != 1 {
							t.Fatal("Mieru provider resource missing")
						}
						content = out.Providers[0].Inline
					}
					assertPolicyProxyLeaf(t, content, []string{"name"}, name)
				}
			})
		}
	}
}

func TestRootPolicy_MieruProxyFields(t *testing.T) {
	base := "    server: example.test\n    port: 443\n    transport: TCP\n    username: fixture-user\n    password: fixture-password\n"
	for _, tc := range []struct{ field, good, bad string }{
		{"udp", "true", "1"},
		{"multiplexing", "MULTIPLEXING_HIGH", "HIGH"},
		{"handshake-mode", "HANDSHAKE_NO_WAIT", "NO_WAIT"},
		{"traffic-pattern", "CAE=", "////"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("mieru", base+"    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid Mieru field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+": "+tc.good) {
				t.Fatal("Mieru field changed")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("mieru", base+"    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid Mieru field accepted")
			}
		})
	}
	for _, extra := range []string{
		"    multiplexing: MULTIPLEXING_DEFAULT\n    handshake-mode: HANDSHAKE_DEFAULT\n",
		"    multiplexing: MULTIPLEXING_OFF\n    handshake-mode: HANDSHAKE_STANDARD\n",
		"    multiplexing: MULTIPLEXING_LOW\n", "    multiplexing: MULTIPLEXING_MIDDLE\n",
	} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("mieru", base+extra)); err != nil {
			t.Fatalf("exact Mieru enum rejected: %v", err)
		}
	}
	withoutPort := strings.Replace(base, "    port: 443\n", "", 1)
	for _, ports := range []string{"1-65535", "443-443"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("mieru", withoutPort+"    port-range: '"+ports+"'\n")); err != nil {
			t.Fatalf("valid Mieru range rejected: %v", err)
		}
	}
	for _, ports := range []string{"0-65535", "1-65536", "443-1", "443-444 trailing", "1-"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("mieru", withoutPort+"    port-range: '"+ports+"'\n")); err == nil {
			t.Fatal("invalid Mieru range accepted")
		}
	}
	for _, extra := range []string{base + "    port-range: 1-2\n", withoutPort, strings.Replace(base, "TCP", "tcp", 1), strings.Replace(base, "fixture-user", "''", 1), strings.Replace(base, "fixture-password", "''", 1)} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("mieru", extra)); err == nil {
			t.Fatal("invalid Mieru required/cross fields accepted")
		}
	}
	out, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("mieru", base+"    traffic-pattern: EIAA\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.YAML), "traffic-pattern: EAA=") {
		t.Fatal("Mieru typed bytes not regenerated")
	}
}

func TestRootPolicy_MieruPatternTypedPositive(t *testing.T) {
	// Official v3.35.0 TrafficPattern field 1 is optional int32 seed.
	got, err := validateMieruPattern(context.Background(), "CAE=")
	if err != nil {
		t.Fatal(err)
	}
	if got != "CAE=" {
		t.Fatal("valid Mieru pattern lost")
	}
}

func TestRootPolicy_MieruPatternFields(t *testing.T) {
	for _, tc := range []struct {
		name      string
		good, bad []policyWireValue
	}{
		{"seed", []policyWireValue{{number: 1, kind: wireInt32, integer: -2147483648}}, []policyWireValue{{number: 1, kind: wireInt64, integer: 2147483648}}},
		{"unlock-all", []policyWireValue{{number: 2, kind: wireBool, boolean: true}}, []policyWireValue{geoTestUint(2, 2)}},
		{"tcp-enable", []policyWireValue{geoTestMessage(3, policyWireValue{number: 1, kind: wireBool, boolean: true})}, []policyWireValue{geoTestMessage(3, geoTestUint(1, 2))}},
		{"tcp-sleep", []policyWireValue{geoTestMessage(3, geoTestUint(2, 100))}, []policyWireValue{geoTestMessage(3, geoTestUint(2, 101))}},
		{"nonce-type", []policyWireValue{geoTestMessage(4, geoTestUint(1, 3))}, []policyWireValue{geoTestMessage(4, geoTestUint(1, 4))}},
		{"nonce-udp", []policyWireValue{geoTestMessage(4, policyWireValue{number: 2, kind: wireBool, boolean: true})}, []policyWireValue{geoTestMessage(4, geoTestUint(2, 2))}},
		{"nonce-min", []policyWireValue{geoTestMessage(4, geoTestUint(3, 12))}, []policyWireValue{geoTestMessage(4, geoTestUint(3, 13))}},
		{"nonce-max", []policyWireValue{geoTestMessage(4, geoTestUint(4, 12))}, []policyWireValue{geoTestMessage(4, geoTestUint(4, 13))}},
		{"nonce-hex", []policyWireValue{geoTestMessage(4, geoTestText(5, "0123456789abcdef01234567"), geoTestText(5, "ab"))}, []policyWireValue{geoTestMessage(4, geoTestText(5, "0123456789abcdef0123456789"))}},
		{"padding-middle", []policyWireValue{geoTestMessage(5, geoTestUint(1, 255))}, []policyWireValue{geoTestMessage(5, geoTestUint(1, 256))}},
		{"padding-end", []policyWireValue{geoTestMessage(5, geoTestUint(2, 255))}, []policyWireValue{geoTestMessage(5, geoTestUint(2, 256))}},
		{"entropy-mode", []policyWireValue{geoTestMessage(6, geoTestUint(1, 4))}, []policyWireValue{geoTestMessage(6, geoTestUint(1, 5))}},
		{"entropy-rotation", []policyWireValue{geoTestMessage(6, geoTestUint(2, 240))}, []policyWireValue{geoTestMessage(6, geoTestUint(2, 17))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			good := base64.StdEncoding.EncodeToString(encodePolicyWire(tc.good))
			got, err := validateMieruPattern(context.Background(), good)
			if err != nil {
				t.Fatalf("valid pattern field rejected: %v", err)
			}
			if got != good {
				t.Fatal("typed pattern field changed during regeneration")
			}
			bad := base64.StdEncoding.EncodeToString(encodePolicyWire(tc.bad))
			if _, err := validateMieruPattern(context.Background(), bad); err == nil {
				t.Fatal("invalid pattern field accepted")
			}
		})
	}
}

func TestRootPolicy_MieruPatternDefaultsAndCrossFields(t *testing.T) {
	for _, good := range []string{"", "EAA=", "EIAA"} {
		// Empty proto and false unlockAll are valid defaults; overlong false is regenerated.
		if _, err := validateMieruPattern(context.Background(), good); err != nil {
			t.Fatalf("valid default rejected: %v", err)
		}
	}
	for _, body := range [][]policyWireValue{
		{geoTestMessage(4, geoTestUint(3, 12), geoTestUint(4, 1))},
		{geoTestMessage(4, geoTestText(5, "odd"))},
		{geoTestMessage(4, geoTestText(5, "xz"))},
		{geoTestMessage(3, policyWireValue{number: 2, kind: wireInt32, integer: -1})},
		{geoTestUint(7, 0)},
	} {
		if _, err := validateMieruPattern(context.Background(), base64.StdEncoding.EncodeToString(encodePolicyWire(body))); err == nil {
			t.Fatal("invalid cross-field pattern accepted")
		}
	}
	if _, err := validateMieruPattern(context.Background(), "!"); err == nil {
		t.Fatal("invalid base64 accepted")
	}
}
