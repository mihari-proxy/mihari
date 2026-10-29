package config

import (
	"bytes"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
	"os"
	"path/filepath"
	"strings"
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
	s.ProxyAuth = &ProxyAuthSettings{Mode: "enabled", Users: []ProxyAuthCredential{{Username: "u", Password: "a"}}, BypassPrefixes: []string{"192.0.2.0/24"}}
	clone := s.Clone()
	clone.ProxyAuth.Users[0].Password = "b"
	clone.ProxyAuth.BypassPrefixes[0] = "198.51.100.0/24"
	if s.ProxyAuth.Users[0].Password != "a" || s.ProxyAuth.BypassPrefixes[0] != "192.0.2.0/24" {
		t.Fatal("auth clone shares mutable data")
	}
}

func TestProxyAuth_DefaultBlockIsOmittedOnSave(t *testing.T) {
	s := Defaults()
	s.ControllerSecret = strings.Repeat("ab", 32)
	s.ProxyAuth = &ProxyAuthSettings{Mode: "follow", BypassMode: "follow"}
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("proxy-auth")) {
		t.Fatal("default auth block prevents older versions reading defaults")
	}
}
func TestProxyAuth_RejectsMappedLoopbackContainingCIDR(t *testing.T) {
	if err := ValidateProxyAuth(protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "custom", BypassPrefixes: []string{"::8000:0:0/81"}}); err == nil {
		t.Fatal("IPv4 mapped loopback range accepted")
	}
}
func TestProxyAuthRecovery_DiscardsAllAuthKeysOnly(t *testing.T) {
	s := Defaults()
	s.ControllerSecret = strings.Repeat("ab", 32)
	s.MixedAddr = "127.0.0.1:19390"
	raw, err := yaml.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("proxy-auth: {mode: broken}\nproxy-auth: {users: invalid}\n")...)
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadForProxyAuthReset(path)
	if err != nil || got.MixedAddr != s.MixedAddr || got.ProxyAuth != nil {
		t.Fatalf("targeted auth repair unavailable: %v", err)
	}
}

func TestProxyAuth_ValidBaselineAndModeLimits(t *testing.T) {
	baseline := protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "custom", Users: []protocol.ProxyAuthUser{{Username: "alice", Password: "fixture"}}, BypassPrefixes: []string{"192.0.2.0/24"}}
	if err := ValidateProxyAuth(baseline); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*protocol.ProxyAuthConfig)
	}{
		{"mode", func(c *protocol.ProxyAuthConfig) { c.Mode = "invalid" }},
		{"bypass mode", func(c *protocol.ProxyAuthConfig) { c.BypassMode = "invalid" }},
		{"duplicate CIDR", func(c *protocol.ProxyAuthConfig) { c.BypassPrefixes = []string{"192.0.2.0/24", "192.0.2.1/24"} }},
		{"user limit", func(c *protocol.ProxyAuthConfig) {
			c.Users = nil
			for i := range 257 {
				c.Users = append(c.Users, protocol.ProxyAuthUser{Username: fmt.Sprint("user-", i), Password: "fixture"})
			}
		}},
		{"prefix limit", func(c *protocol.ProxyAuthConfig) {
			c.BypassPrefixes = nil
			for i := range 257 {
				c.BypassPrefixes = append(c.BypassPrefixes, fmt.Sprintf("192.0.%d.%d/32", i/256, i%256))
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := baseline
			test.change(&candidate)
			if err := ValidateProxyAuth(candidate); err == nil {
				t.Fatal("invalid candidate accepted")
			}
		})
	}
}
