package subscription

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestRootPolicy_SimpleProxyCredentialFraming(t *testing.T) {
	for _, kind := range []string{"socks5", "gost-relay", "http"} {
		for _, provider := range []bool{false, true} {
			for _, tc := range []struct {
				name, username, password, badField string
			}{
				{"byte-boundary", strings.Repeat("u", 255), strings.Repeat("p", 255), ""},
				{"username-over", strings.Repeat("u", 256), "p", "username"},
				{"password-over", "u", strings.Repeat("p", 256), "password"},
				{"multibyte-boundary", strings.Repeat("界", 85), strings.Repeat("界", 85), ""},
				{"multibyte-username-over", strings.Repeat("界", 85) + "u", "", "username"},
				{"multibyte-password-over", "u", strings.Repeat("界", 85) + "p", "password"},
				{"empty-user-long-password", "", strings.Repeat("p", 256), "password"},
				{"empty-pair", "", "", ""},
				{"control-data", "u\x00\n", "p\r\n", ""},
			} {
				t.Run(kind+"/provider="+strconv.FormatBool(provider)+"/"+tc.name, func(t *testing.T) {
					body := "{name: fixture, type: " + kind + ", server: 192.0.2.1, port: 443, username: " + strconv.Quote(tc.username) + ", password: " + strconv.Quote(tc.password) + "}"
					input := "proxies: [" + body + "]\n"
					field := "proxies[]."
					if provider {
						input = "proxy-providers: {p: {type: inline, override: {udp: false}, payload: [" + body + "]}}\n"
						field = "proxy-providers.[entry].payload[]."
					}
					out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput(input))
					bad := tc.badField != "" && kind != "http" && !(kind == "socks5" && tc.username == "")
					if bad {
						assertPolicyDataFailure(t, err, field+tc.badField)
						return
					}
					if err != nil {
						t.Fatalf("representable or inactive credentials rejected: %v", err)
					}
					content := out.YAML
					if provider {
						if len(out.Providers) != 1 {
							t.Fatal("provider resource missing")
						}
						content = out.Providers[0].Inline
					}
					assertPolicyProxyLeaf(t, content, []string{"username"}, strconv.Quote(tc.username))
					assertPolicyProxyLeaf(t, content, []string{"password"}, strconv.Quote(tc.password))
				})
			}
		}
	}
}

func TestRootPolicy_GostForwardMuxFields(t *testing.T) {
	for _, field := range []string{"forward", "mux"} {
		t.Run(field, func(t *testing.T) {
			for _, value := range []string{"true", "false"} {
				t.Run(value, func(t *testing.T) {
					out, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "gost-relay", field+": "+value+"\n"))
					if err != nil {
						t.Fatal(err)
					}
					assertPolicyProxyLeaf(t, out.YAML, []string{field}, value)
				})
			}
			_, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "gost-relay", field+": 'true'\n"))
			assertPolicyDataFailure(t, err, "proxies[]."+field)
		})
	}
}
