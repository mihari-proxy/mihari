package subscription

import (
	"context"
	"errors"
	"testing"
)

func TestRootPolicy_NTPDialerUsesActiveFinalNamespace(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		valid        bool
	}{
		{"active missing", "ntp: {enable: true, dialer-proxy: missing}", false},
		{"empty server remains joined address", "ntp: {enable: true, server: '', dialer-proxy: missing}", false},
		{"no interface fallback", "ntp: {enable: true, dialer-proxy: eth0}", false},
		{"no rules sentinel", "ntp: {enable: true, dialer-proxy: RULES}", false},
		{"builtin", "ntp: {enable: true, dialer-proxy: DIRECT}", true},
		{"automatic global", "ntp: {enable: true, dialer-proxy: GLOBAL}", true},
		{"group", "proxy-groups: [{name: g, type: select, proxies: [DIRECT]}]\nntp: {enable: true, dialer-proxy: g}", true},
		{"ordinary outbound", "proxies: [{name: p, type: direct}]\nntp: {enable: true, dialer-proxy: p}", true},
		{"empty direct dialer", "ntp: {enable: true, dialer-proxy: ''}", true},
		{"disabled default", "ntp: {dialer-proxy: missing}", true},
		{"disabled explicit", "ntp: {enable: false, dialer-proxy: missing}", true},
		{"zero interval stopped", "ntp: {enable: true, interval: 0, dialer-proxy: missing}", true},
		{"negative interval stopped", "ntp: {enable: true, interval: -1, dialer-proxy: missing}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := dnsPolicyInput(tc.config + "\n")
			for _, inspect := range []bool{false, true} {
				var err error
				if inspect {
					_, err = NewRootConfigPolicy().Inspect(context.Background(), input)
				} else {
					_, err = NewRootConfigPolicy().Build(context.Background(), input)
				}
				if tc.valid {
					if err != nil {
						t.Fatalf("valid active/inactive NTP reference rejected: %v", err)
					}
				} else {
					var failure PolicyError
					if !errors.As(err, &failure) || failure.Field != "ntp.dialer-proxy" {
						t.Fatalf("missing active NTP reference not rejected at safe field: %v", err)
					}
				}
			}
		})
	}
}
