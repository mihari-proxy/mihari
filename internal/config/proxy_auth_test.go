package config

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

func TestProxyAuth_RejectsAmbiguousCredentialsAndLoopbackCIDRs(t *testing.T) {
	for _, test := range []struct {
		name     string
		users    []protocol.ProxyAuthUser
		prefixes []string
	}{
		{name: "duplicate", users: []protocol.ProxyAuthUser{{Username: "u", Password: "a"}, {Username: "u", Password: "b"}}},
		{name: "colon username", users: []protocol.ProxyAuthUser{{Username: "u:v", Password: "a"}}},
		{name: "empty password", users: []protocol.ProxyAuthUser{{Username: "u"}}},
		{name: "terminal control", users: []protocol.ProxyAuthUser{{Username: "u", Password: "a\x1b"}}},
		{name: "ipv4 loopback", prefixes: []string{"127.0.0.0/8"}},
		{name: "ipv6 loopback", prefixes: []string{"::1/128"}},
		{name: "ipv4 broad", prefixes: []string{"0.0.0.0/0"}},
		{name: "ipv6 broad", prefixes: []string{"::/0"}},
		{name: "mapped loopback", prefixes: []string{"::ffff:127.0.0.1/128"}},
		{name: "invalid", prefixes: []string{"not a prefix"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateProxyAuth(protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "custom", Users: test.users, BypassPrefixes: test.prefixes}); err == nil {
				t.Fatal("invalid auth accepted")
			}
		})
	}
}
func TestProxyAuth_CloneDoesNotShareCredentials(t *testing.T) {
	s := Defaults()
	s.ProxyAuth = &protocol.ProxyAuthConfig{Mode: "enabled", Users: []protocol.ProxyAuthUser{{Username: "u", Password: "a"}}, BypassPrefixes: []string{"192.0.2.0/24"}}
	clone := s.Clone()
	clone.ProxyAuth.Users[0].Password = "b"
	clone.ProxyAuth.BypassPrefixes[0] = "198.51.100.0/24"
	if s.ProxyAuth.Users[0].Password != "a" || s.ProxyAuth.BypassPrefixes[0] != "192.0.2.0/24" {
		t.Fatal("auth clone shares mutable data")
	}
}
