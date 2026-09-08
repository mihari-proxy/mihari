package subscription

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// These fixtures prove rejection of the containing capability, not independent
// validation or native usability of the child. No child is passed to the core.
func TestRootPolicy_ForbiddenBlockLeaves(t *testing.T) {
	for _, tc := range []struct{ parent, field, value string }{
		{"external-controller-cors", "allow-origins", "['https://example.test']"},
		{"external-controller-cors", "allow-private-network", "false"},
		{"tuic-server", "enable", "false"},
		{"tuic-server", "listen", "'127.0.0.1:8443'"},
		{"tuic-server", "token", "[fixture-token]"},
		{"tuic-server", "users", "{fixture-user: fixture-password}"},
		{"tuic-server", "certificate", "fixture-certificate"},
		{"tuic-server", "private-key", "fixture-private-key"},
		{"tuic-server", "congestion-controller", "cubic"},
		{"tuic-server", "max-idle-time", "15000"},
		{"tuic-server", "authentication-timeout", "1000"},
		{"tuic-server", "alpn", "[h3]"},
		{"tuic-server", "max-udp-relay-packet-size", "1500"},
		{"tuic-server", "cwnd", "32"},
		{"iptables", "enable", "false"},
		{"iptables", "inbound-interface", "eth0"},
		{"iptables", "bypass", "[192.0.2.1]"},
		{"iptables", "dns-redirect", "false"},
		{"tls", "certificate", "fixture-certificate"},
		{"tls", "private-key", "fixture-private-key"},
		{"tls", "client-auth-type", "no-client-cert"},
		{"tls", "client-auth-cert", "fixture-ca"},
		{"tls", "ech-key", "fixture-key"},
		{"tls", "custom-certifactes", "[fixture-ca]"},
		{"tunnels", "network", "[tcp, udp]"},
		{"tunnels", "address", "'127.0.0.1:8443'"},
		{"tunnels", "target", "'192.0.2.1:443'"},
		{"tunnels", "proxy", "DIRECT"},
	} {
		t.Run(tc.parent+"/"+tc.field, func(t *testing.T) {
			input := rootPolicyInput()
			out, err := NewRootConfigPolicy().Build(context.Background(), input)
			if err != nil {
				t.Fatalf("paired managed control rejected: %v", err)
			}
			assertPolicyRootLeaf(t, out.YAML, []string{"mixed-port"}, "9190")
			assertPolicyRootLeaf(t, out.YAML, []string{"rules"}, "['MATCH,DIRECT']")
			value := "{" + tc.field + ": " + tc.value + "}"
			if tc.parent == "tunnels" {
				value = "[" + value + "]"
			}
			input.YAML = append(input.YAML, []byte(tc.parent+": "+value+"\n")...)
			_, err = NewRootConfigPolicy().Build(context.Background(), input)
			var failure PolicyError
			if !errors.As(err, &failure) || failure.Field != tc.parent || strings.Contains(err.Error(), "fixture-") {
				t.Fatalf("forbidden parent did not contain child at safe boundary: %v", err)
			}
		})
	}
}
